package db233

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func preservePerformanceSettingsUnit(t *testing.T) *CrudPerformanceSettingsManager {
	t.Helper()
	manager := GetCrudPerformanceSettings()
	saved := manager.Snapshot()
	t.Cleanup(func() { manager.ApplyFull(saved) })
	return manager
}

func TestLocalWriteJournal_StartStopIdempotent(t *testing.T) {
	journal := NewLocalWriteJournal(t.TempDir(), NewBaseCrudRepository(nil))
	journal.SetRetryInterval(time.Hour)
	journal.Start()
	journal.Start()

	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			journal.Stop()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("journal Stop blocked")
	}
}

func TestLocalWriteJournal_StopBeforeStart(t *testing.T) {
	journal := NewLocalWriteJournal(t.TempDir(), NewBaseCrudRepository(nil))
	journal.Stop()
	journal.Start()
	journal.Stop()
}

func TestFaultTolerantManager_StopBroadcastsToAllLoops(t *testing.T) {
	manager := NewFaultTolerantManager(nil, nil)
	manager.SetPersistPath(t.TempDir())
	manager.healthCheckInterval = time.Hour
	manager.retryInterval = time.Hour
	manager.Start()
	manager.Start()

	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.Stop()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("FaultTolerantManager Stop blocked or leaked a loop")
	}
}

func TestDbCloseIdempotentUnregistersPoolAndPreparedStatements(t *testing.T) {
	state := newScriptedDBState()
	datasource := openScriptedDB(t, state)
	db := NewDb(datasource, 0, nil)
	RegisterDbForConnectionPool(db)
	t.Cleanup(func() { _ = db.Close() })

	cache := GetPreparedStmtCache()
	cache.Clear()
	t.Cleanup(cache.Clear)
	key := preparedStmtKey(datasource, "SELECT 1")
	entry := &preparedStmtEntry{key: key, db: datasource, created: time.Now()}
	cache.mu.Lock()
	element := cache.lru.PushBack(entry)
	cache.entries[key] = entry
	cache.lruIndex[key] = element
	cache.mu.Unlock()
	if cache.Len() != 1 {
		t.Fatalf("prepared cache len=%d want=1", cache.Len())
	}

	const workers = 32
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsCh <- db.Close()
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, registered := registeredPoolDbs.Load(db); registered {
		t.Fatal("closed Db remains in connection-pool registry")
	}
	if cache.Len() != 0 {
		t.Fatalf("Db.Close 未清理 prepared statements: len=%d", cache.Len())
	}

	RegisterDbForConnectionPool(db)
	if _, registered := registeredPoolDbs.Load(db); registered {
		t.Fatal("closed Db was registered again")
	}
}

func TestDbCloseStopsAndFlushesTrackedWriteBuffers(t *testing.T) {
	manager := preservePerformanceSettingsUnit(t)
	settings := DefaultCrudPerformanceSettings()
	settings.WriteBufferEnabled = true
	settings.WriteBufferFlushIntervalMs = int(time.Hour / time.Millisecond)
	manager.ApplyFull(settings)

	datasource := openScriptedDB(t, newScriptedDBState())
	db := NewDb(datasource, 0, nil)
	repo := NewBaseCrudRepository(db)
	recorder := newUpsertRecorder()
	repo.SetTestUpsertHook(recorder.hook)
	if err := repo.SaveBuffered(&flushTestEntity{PlayerID: "db-close", Name: "latest"}); err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if recorder.batchCount() != 1 {
		t.Fatalf("Db.Close flush batches=%d want=1", recorder.batchCount())
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ensureWriteBuffer(settings); err != ErrCrudRepositoryClosed {
		t.Fatalf("closed repository ensure error=%v", err)
	}
}

func TestDbCloseDrainsAdmittedWritesBeforeStoppingRecovery(t *testing.T) {
	db := NewDb(openScriptedDB(t, newScriptedDBState()), 0, nil)
	if err := db.configureDatabaseGeneration("old"); err != nil {
		t.Fatalf("configure generation: %v", err)
	}
	callbackEntered := make(chan struct{}, 1)
	callbackRelease := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(callbackRelease)
		}
	}()
	callbackDone := make(chan error, 1)
	go func() {
		callbackDone <- db.ExecuteWithConnection(func(*sql.Conn) error {
			callbackEntered <- struct{}{}
			<-callbackRelease
			return nil
		})
	}()
	select {
	case <-callbackEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("connection callback did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- db.Close() }()
	waitForDatabaseGenerationUnavailable(t, db)
	if _, err := db.ExecuteUpdate("UPDATE must_not_start_during_close SET value = 1"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("write admitted during close: %v", err)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Db.Close crossed admitted callback: %v", err)
	default:
	}

	close(callbackRelease)
	released = true
	if err := <-callbackDone; err != nil {
		t.Fatalf("connection callback: %v", err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Db.Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Db.Close did not finish after admitted callback")
	}
}

func TestDbCloseDatabaseDownKeepsDurableWALForRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})

	downDB := NewDb(nil, 0, nil)
	if err := downDB.configureDatabaseGeneration("production-epoch"); err != nil {
		t.Fatal(err)
	}
	downRepo := NewBaseCrudRepository(downDB)
	journal := NewLocalWriteJournal(dir, downRepo)
	if err := journal.ConfigureDatabaseGeneration("production-epoch"); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
		&flushTestEntity{PlayerID: "durable-player", Name: "must-survive"},
	}); err != nil {
		t.Fatal(err)
	}
	downDB.WriteJournal = journal
	if err := downDB.Close(); err == nil {
		t.Fatal("Db.Close reported success while durable WAL could not reach the database")
	}
	if _, err := os.Stat(filepath.Join(dir, "pending.ndjson")); err != nil {
		t.Fatalf("Db.Close removed durable WAL after failed drain: %v", err)
	}

	state := newScriptedDBState(scriptedStep{kind: "exec", result: driver.RowsAffected(1)})
	recoveredDB := newStrictTestDb(t, state)
	if err := recoveredDB.configureDatabaseGeneration("production-epoch"); err != nil {
		t.Fatal(err)
	}
	recoveredRepo := NewBaseCrudRepository(recoveredDB)
	recoveredJournal := NewLocalWriteJournal(dir, recoveredRepo)
	t.Cleanup(func() { _ = recoveredJournal.StopStrict() })
	if err := recoveredJournal.ConfigureDatabaseGeneration("production-epoch"); err != nil {
		t.Fatal(err)
	}
	success, failed, err := recoveredJournal.ReplayAllStrict()
	if err != nil || success != 1 || failed != 0 {
		t.Fatalf("restart replay success=%d failed=%d err=%v", success, failed, err)
	}
	if pending, err := recoveredJournal.PendingCount(); err != nil || pending != 0 {
		t.Fatalf("restart WAL pending=%d err=%v", pending, err)
	}
}
