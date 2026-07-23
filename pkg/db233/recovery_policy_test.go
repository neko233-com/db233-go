package db233

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
	journal := NewLocalWriteJournal(t.TempDir(), nil)
	entry := &JournalEntry{
		TableName:           "VersionedEntity",
		EntityTypeName:      "VersionedEntity",
		EntityJSON:          []byte(`{"id":"1"}`),
		EntitySchemaVersion: 1,
	}
	if err := journal.validateReplaySchemaVersion(entry); err == nil {
		t.Fatal("different per-entity schema version must be rejected")
	}
}
