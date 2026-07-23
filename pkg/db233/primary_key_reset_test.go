package db233

import (
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
		TableName:  "flush_entities",
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

	barrier, err := db.BeginPrimaryKeyReset("42")
	if err != nil {
		t.Fatal(err)
	}
	if sessions.GetSession("42") != nil {
		t.Fatal("target session was not discarded")
	}
	journal.journalMu.Lock()
	count := len(journal.stateLocked().pendingCache)
	journal.journalMu.Unlock()
	if count != 1 {
		t.Fatalf("WAL pending=%d, want only non-target", count)
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
