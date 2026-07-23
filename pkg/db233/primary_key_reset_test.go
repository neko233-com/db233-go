package db233

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPrimaryKeyResetBarrierDiscardsAllManagedRecoveryState(t *testing.T) {
	db := NewDb(nil, 1, nil)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntityB{})
	GetEntityTypeRegistry().Register(&flushTestEntityB{})

	journal := NewLocalWriteJournal(filepath.Join(t.TempDir(), "wal"), repo)
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	db.WriteJournal = journal
	repo.SetWriteJournal(journal)
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "42", Name: "wal"},
		&flushTestEntity{PlayerID: "43", Name: "keep"},
		&flushTestEntity{PlayerID: "account-42", Name: "account"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntityB{PlayerID: "42", Gold: 42},
	}); err != nil {
		t.Fatal(err)
	}

	writeBuffer := newWriteBufferForGeneration(repo, "epoch")
	repo.writeBuffer = writeBuffer
	if !db.registerBufferedRepository(repo) {
		t.Fatal("register write buffer failed")
	}
	if _, err := writeBuffer.Enqueue(&flushTestEntity{PlayerID: "42", Name: "buffer"}); err != nil {
		t.Fatal(err)
	}

	manager := NewFaultTolerantManager(db, nil)
	if err := manager.SetPersistPathStrict(filepath.Join(t.TempDir(), "failed")); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.StopStrict() })
	if err := manager.RecordFailedOperationStrict(&FailedOperation{
		Operation:  "Save",
		TableName:  "flush_test_entity",
		PrimaryKey: "42",
	}); err != nil {
		t.Fatal(err)
	}
	db.FaultTolerantMgr = manager

	sessions := NewSessionRepository(repo)
	t.Cleanup(sessions.Stop)
	session := newPlayerSessionForGeneration("42", repo, sessions, "epoch")
	sessions.sessions.Store("42", session)
	db.SessionRepo = sessions

	playerTarget, err := NewPrimaryKeyResetTarget("flush_test_entity", "42")
	if err != nil {
		t.Fatal(err)
	}
	accountTarget, err := NewPrimaryKeyResetTarget("flush_test_entity", "account-42")
	if err != nil {
		t.Fatal(err)
	}
	barrier, err := db.BeginPrimaryKeyTargetsReset(
		[]any{"42"},
		[]PrimaryKeyResetTarget{playerTarget, accountTarget, playerTarget},
	)
	if err != nil {
		t.Fatal(err)
	}
	if sessions.GetSession("42") != nil {
		t.Fatal("target session was not discarded")
	}
	journal.journalMu.Lock()
	count := len(journal.stateLocked().pendingCache)
	journal.journalMu.Unlock()
	if count != 2 {
		t.Fatalf("WAL pending=%d, want non-target rows including same key in another table", count)
	}
	writeBuffer.mu.Lock()
	bufferSize := writeBuffer.size
	writeBuffer.mu.Unlock()
	if bufferSize != 0 {
		t.Fatalf("write buffer size=%d, want 0", bufferSize)
	}
	if manager.GetFailedOperationCount() != 0 {
		t.Fatalf("failed operations=%d, want 0", manager.GetFailedOperationCount())
	}
	if err := barrier.Commit(); err != nil {
		t.Fatal(err)
	}
	if db.isDatabaseGenerationUnavailable() {
		t.Fatal("managed writes remained blocked after barrier commit")
	}
}

func TestPrimaryKeyResetBarrierFailClosedBlocksManagedWrites(t *testing.T) {
	db := NewDb(nil, 1, nil)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	barrier, err := db.BeginPrimaryKeyReset("42")
	if err != nil {
		t.Fatal(err)
	}
	commitErr := errors.New("commit result unknown")
	blockedErr := barrier.FailClosed(commitErr)
	if !errors.Is(blockedErr, ErrDatabaseGenerationBlocked) || !errors.Is(blockedErr, commitErr) {
		t.Fatalf("FailClosed error=%v", blockedErr)
	}
	if !db.isDatabaseGenerationUnavailable() {
		t.Fatal("managed writes were not blocked")
	}
	repo := NewBaseCrudRepository(db)
	writeBuffer := newWriteBufferForGeneration(repo, "epoch")
	if _, err := writeBuffer.Enqueue(&flushTestEntity{PlayerID: "42", Name: "blocked"}); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("FailClosed 后 Enqueue error=%v", err)
	}
}
