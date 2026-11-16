package db233

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
)

func TestLocalWriteJournalReloadsPersistedRetry(t *testing.T) {
	replayErr := errors.New("temporary database failure")
	state := newScriptedDBState(scriptedStep{kind: "exec", execErr: replayErr})
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	dir := t.TempDir()
	journal := NewLocalWriteJournal(dir, NewBaseCrudRepository(db))
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{&flushTestEntity{PlayerID: "retry", Name: "value"}}); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := journal.ReplayAllStrict(); failed != 1 || !errors.Is(err, replayErr) {
		t.Fatalf("first replay failed=%d err=%v", failed, err)
	}
	_ = journal.StopStrict()
	reopened := NewLocalWriteJournal(dir, NewBaseCrudRepository(db))
	if err := reopened.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatalf("reload of retry record failed: %v", err)
	}
	t.Cleanup(func() { _ = reopened.StopStrict() })
	if count, err := reopened.PendingCount(); err != nil || count != 1 {
		t.Fatalf("pending after reopen=%d err=%v", count, err)
	}
}

func TestJournalRetryUpdateRejectsChangedPayloadOrRegression(t *testing.T) {
	previous := &JournalEntry{ID: "one", Operation: "Save", TableName: "test", PrimaryKey: "1", EntityJSON: []byte(`{"value":1}`), RetryCount: 1}
	current := *previous
	current.EntityJSON = bytes.Clone(previous.EntityJSON)
	current.RetryCount = 2
	if !isJournalRetryUpdate(previous, &current) {
		t.Fatal("valid retry update rejected")
	}
	current.EntityJSON = []byte(`{"value":2}`)
	if isJournalRetryUpdate(previous, &current) {
		t.Fatal("changed payload accepted as retry")
	}
	current.EntityJSON = bytes.Clone(previous.EntityJSON)
	current.RetryCount = 1
	if isJournalRetryUpdate(previous, &current) {
		t.Fatal("non-increasing retry accepted")
	}
}

func TestLocalWriteJournalIsolatesMySQLValueError(t *testing.T) {
	valueErr := &mysql.MySQLError{Number: 1406, Message: "value too long"}
	state := newScriptedDBState(
		scriptedStep{kind: "exec", execErr: valueErr},
		scriptedStep{kind: "exec"},
		scriptedStep{kind: "exec", execErr: valueErr},
	)
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	journal := NewLocalWriteJournal(t.TempDir(), NewBaseCrudRepository(db))
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "a-good", Name: "good"},
		&flushTestEntity{PlayerID: "b-bad", Name: "bad"},
	}); err != nil {
		t.Fatal(err)
	}
	succeeded, failed, err := journal.ReplayAllStrict()
	if succeeded != 1 || failed != 1 || !errors.Is(err, valueErr) {
		t.Fatalf("replay succeeded=%d failed=%d error=%v", succeeded, failed, err)
	}
	if count, err := journal.PendingCount(); err != nil || count != 1 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
}

func TestLocalWriteJournalDoesNotSplitAmbiguousDatabaseFailure(t *testing.T) {
	replayErr := errors.New("connection lost")
	state := newScriptedDBState(scriptedStep{kind: "exec", execErr: replayErr})
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	journal := NewLocalWriteJournal(t.TempDir(), NewBaseCrudRepository(db))
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "a", Name: "first"},
		&flushTestEntity{PlayerID: "b", Name: "second"},
	}); err != nil {
		t.Fatal(err)
	}
	succeeded, failed, err := journal.ReplayAllStrict()
	if succeeded != 0 || failed != 2 || !errors.Is(err, replayErr) {
		t.Fatalf("replay succeeded=%d failed=%d error=%v", succeeded, failed, err)
	}
	if count, err := journal.PendingCount(); err != nil || count != 2 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	execCount := 0
	for _, call := range state.calls {
		if call.kind == "exec" {
			execCount++
		}
	}
	if execCount != 1 {
		t.Fatalf("ambiguous error retried in %d database writes", execCount)
	}
}

func TestLocalWriteJournalMovesTerminalFailuresToDeadLetter(t *testing.T) {
	replayErr := errors.New("persistent replay failure")
	state := newScriptedDBState(
		scriptedStep{kind: "exec", execErr: replayErr},
		scriptedStep{kind: "exec", execErr: replayErr},
	)
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	dir := t.TempDir()
	journal := NewLocalWriteJournal(dir, repo)
	journal.SetRecoveryPolicy(2)
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "player-terminal", Name: "pending"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, failed, err := journal.ReplayAllStrict(); failed != 1 || !errors.Is(err, replayErr) {
		t.Fatalf("first replay failed=%d err=%v", failed, err)
	}
	if _, failed, err := journal.ReplayAllStrict(); failed != 1 || err != nil {
		t.Fatalf("terminal replay failed=%d err=%v", failed, err)
	}
	if count, err := journal.PendingCount(); err != nil || count != 0 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "dead-letter"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("dead letters=%d, want 1", len(files))
	}
}

func TestLocalWriteJournalRejectsDifferentEntitySchemaVersion(t *testing.T) {
	db := NewDb(nil, 1, nil)
	db.installEntitySchemaVersions([]EntitySchemaVersionRecord{{
		TableName: "VersionedEntity",
		Version:   2,
	}})
	journal := NewLocalWriteJournal(t.TempDir(), NewBaseCrudRepository(db))
	entry := &JournalEntry{
		TableName:           "VersionedEntity",
		EntityTypeName:      "VersionedEntity",
		EntityJSON:          []byte(`{"id":"1"}`),
		EntitySchemaVersion: 1,
	}
	if err := journal.validateReplaySchemaVersion(entry); err == nil {
		t.Fatal("different per-entity schema version must be rejected")
	}
	entry.EntitySchemaVersion = 0
	if err := journal.validateReplaySchemaVersion(entry); err == nil {
		t.Fatal("legacy unversioned payload must be rejected after table version binding")
	}
}
