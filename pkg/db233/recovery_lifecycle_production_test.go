package db233

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	recoveryPingDriverOnce sync.Once
	recoveryPingSequence   atomic.Uint64
	recoveryPingStates     sync.Map
)

type recoveryPingState struct {
	pingErr    error
	pingCalls  atomic.Int64
	closeCalls atomic.Int64
	pingSignal chan struct{}
}

type recoveryPingDriver struct{}

func (recoveryPingDriver) Open(name string) (driver.Conn, error) {
	value, ok := recoveryPingStates.Load(name)
	if !ok {
		return nil, fmt.Errorf("unknown recovery ping database %q", name)
	}
	return &recoveryPingConn{state: value.(*recoveryPingState)}, nil
}

type recoveryPingConn struct{ state *recoveryPingState }

func (*recoveryPingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (*recoveryPingConn) Begin() (driver.Tx, error) { return nil, errors.New("tx unsupported") }
func (c *recoveryPingConn) Close() error {
	c.state.closeCalls.Add(1)
	return nil
}
func (c *recoveryPingConn) Ping(ctx context.Context) error {
	c.state.pingCalls.Add(1)
	select {
	case c.state.pingSignal <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return c.state.pingErr
	}
}

func newRecoveryPingDB(t *testing.T, state *recoveryPingState) *sql.DB {
	t.Helper()
	recoveryPingDriverOnce.Do(func() { sql.Register("db233-recovery-ping", recoveryPingDriver{}) })
	dsn := fmt.Sprintf("recovery-%d", recoveryPingSequence.Add(1))
	recoveryPingStates.Store(dsn, state)
	db, err := sql.Open("db233-recovery-ping", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		recoveryPingStates.Delete(dsn)
	})
	return db
}

func waitForPingCalls(t *testing.T, state *recoveryPingState, expected int64) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for state.pingCalls.Load() < expected {
		select {
		case <-state.pingSignal:
		case <-deadline.C:
			t.Fatalf("ping calls=%d, want >=%d", state.pingCalls.Load(), expected)
		}
	}
}

func TestFaultTolerantManager_CancelableRecoveryNeverReplacesOrClosesDataSource(t *testing.T) {
	state := &recoveryPingState{
		pingErr:    errors.New("ping unavailable"),
		pingSignal: make(chan struct{}, 16),
	}
	sqlDB := newRecoveryPingDB(t, state)
	db := NewDb(sqlDB, 1, nil)
	manager := NewFaultTolerantManager(db, nil)
	if err := manager.SetPersistPathStrict(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	manager.maxReconnectAttempts = 100
	manager.reconnectInterval = time.Hour
	manager.healthCheckTimeout = time.Second

	original := db.DataSource
	started := time.Now()
	manager.CheckAndReconnect() // legacy API 必须异步且合并。
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("legacy CheckAndReconnect blocked for %v", elapsed)
	}
	waitForPingCalls(t, state, 2) // 初检 + 第一轮恢复探测，随后进入可取消 backoff。
	if err := manager.StopStrict(); err != nil {
		t.Fatalf("StopStrict: %v", err)
	}
	if db.DataSource != original {
		t.Fatal("容错管理器替换了 Db.DataSource")
	}
	if got := state.closeCalls.Load(); got != 0 {
		t.Fatalf("容错管理器关闭了共享 DataSource 连接: %d", got)
	}
	stableCalls := state.pingCalls.Load()
	time.Sleep(20 * time.Millisecond)
	if got := state.pingCalls.Load(); got != stableCalls {
		t.Fatalf("StopStrict 返回后仍访问 DB: before=%d after=%d", stableCalls, got)
	}
	if err := manager.CheckAndReconnectStrict(); !errors.Is(err, ErrFaultTolerantManagerStopped) {
		t.Fatalf("停止后 strict check error=%v", err)
	}
}

func TestFaultTolerantManager_RecordClonesCallerOwnedData(t *testing.T) {
	manager := NewFaultTolerantManager(nil, nil)
	if err := manager.SetPersistPathStrict(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.StopStrict() })

	paramBytes := []byte("param-original")
	entityBytes := []byte("entity-original")
	op := &FailedOperation{
		Operation:  "ExecuteUpdate",
		SQL:        "UPDATE t SET value = ?",
		Params:     []any{paramBytes},
		EntityData: map[string]any{"nested": entityBytes},
		EntityJSON: []byte(`{"value":"original"}`),
		TableName:  "t",
	}
	if err := manager.RecordFailedOperationStrict(op); err != nil {
		t.Fatal(err)
	}
	paramBytes[0] = 'X'
	entityBytes[0] = 'X'
	op.EntityJSON[0] = 'X'
	op.SQL = "mutated"
	if op.ID != "" || !op.Timestamp.IsZero() {
		t.Fatal("RecordFailedOperationStrict mutated caller-owned metadata")
	}

	manager.failedOpsMutex.RLock()
	stored := manager.failedOps[0]
	manager.failedOpsMutex.RUnlock()
	if string(stored.Params[0].([]byte)) != "param-original" {
		t.Fatalf("Params was aliased: %q", stored.Params[0])
	}
	if string(stored.EntityData["nested"].([]byte)) != "entity-original" {
		t.Fatalf("EntityData was aliased: %q", stored.EntityData["nested"])
	}
	if string(stored.EntityJSON) != `{"value":"original"}` || stored.SQL != "UPDATE t SET value = ?" {
		t.Fatalf("stored operation was mutated: %#v", stored)
	}
}

func TestRecoveryGeneration_UnverifiableMetadataQuarantinesAndStaysBlocked(t *testing.T) {
	t.Run("missing manifest with data", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pending.ndjson"), []byte("legacy-data\n"), recoveryFileMode); err != nil {
			t.Fatal(err)
		}
		journal := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
		t.Cleanup(func() { _ = journal.StopStrict() })
		if err := journal.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
			t.Fatalf("Configure error=%v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "pending.ndjson")); !os.IsNotExist(err) {
			t.Fatalf("unverifiable data remained active: %v", err)
		}
		if err := journal.RotateDatabaseGeneration("epoch"); err != nil {
			t.Fatalf("explicit recovery after quarantine: %v", err)
		}
	})

	t.Run("corrupt or unsupported manifest", func(t *testing.T) {
		for name, content := range map[string][]byte{
			"corrupt":     []byte("{broken"),
			"unsupported": []byte(`{"formatVersion":99,"databaseGeneration":"old"}`),
		} {
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "wal-generation.json"), content, recoveryFileMode); err != nil {
					t.Fatal(err)
				}
				journal := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
				t.Cleanup(func() { _ = journal.StopStrict() })
				if err := journal.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrDatabaseGenerationBlocked) {
					t.Fatalf("Configure error=%v", err)
				}
				entries, err := os.ReadDir(filepath.Join(dir, "quarantine"))
				if err != nil || len(entries) != 1 {
					t.Fatalf("quarantine entries=%d err=%v", len(entries), err)
				}
				if err := journal.RotateDatabaseGeneration("epoch"); err != nil {
					t.Fatalf("explicit recovery after quarantine: %v", err)
				}
			})
		}
	})
}

func TestRecoveryPersistence_PrivatePermissionsAndPathOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recovery")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := writeJSONAtomic(manifestPath, map[string]string{"secret": "value"}, 0o666); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != recoveryDirectoryMode {
			t.Fatalf("directory mode=%v", info.Mode().Perm())
		}
		info, err = os.Stat(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != recoveryFileMode {
			t.Fatalf("file mode=%v", info.Mode().Perm())
		}
	}

	walDir := filepath.Join(t.TempDir(), "wal")
	first := NewLocalWriteJournal(walDir, NewBaseCrudRepository(nil))
	if err := first.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	second := NewLocalWriteJournal(walDir, NewBaseCrudRepository(nil))
	if err := second.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrLocalWriteJournalPathInUse) || !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("second owner error=%v", err)
	}
	if err := first.StopStrict(); err != nil {
		t.Fatal(err)
	}
	if err := second.RotateDatabaseGeneration("epoch"); err != nil {
		t.Fatalf("path was not reusable after clean stop: %v", err)
	}
	if err := second.StopStrict(); err != nil {
		t.Fatal(err)
	}

	ftmDir := filepath.Join(t.TempDir(), "failed-ops")
	firstManager := NewFaultTolerantManager(nil, nil)
	if err := firstManager.SetPersistPathStrict(ftmDir); err != nil {
		t.Fatal(err)
	}
	if err := firstManager.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	if err := firstManager.RecordFailedOperationStrict(&FailedOperation{
		Operation: "ExecuteUpdate",
		SQL:       "UPDATE path_lock_test SET value=?",
		Params:    []any{"preserve"},
	}); err != nil {
		t.Fatal(err)
	}
	failedOpsPath := filepath.Join(ftmDir, "failed_operations.json")
	beforeConflict, err := os.ReadFile(failedOpsPath)
	if err != nil {
		t.Fatal(err)
	}
	secondManager := NewFaultTolerantManager(nil, nil)
	if err := secondManager.SetPersistPathStrict(ftmDir); err != nil {
		t.Fatal(err)
	}
	if err := secondManager.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrFaultTolerantManagerPathInUse) || !errors.Is(err, ErrDatabaseGenerationBlocked) {
		t.Fatalf("second FTM owner error=%v", err)
	}
	if err := secondManager.StopStrict(); !errors.Is(err, ErrFaultTolerantManagerPathInUse) {
		t.Fatalf("conflicting FTM StopStrict error=%v", err)
	}
	afterConflict, err := os.ReadFile(failedOpsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeConflict, afterConflict) {
		t.Fatal("conflicting FTM overwrote the active owner's failed queue")
	}
	if err := firstManager.StopStrict(); err != nil {
		t.Fatal(err)
	}
	thirdManager := NewFaultTolerantManager(nil, nil)
	if err := thirdManager.SetPersistPathStrict(ftmDir); err != nil {
		t.Fatal(err)
	}
	if err := thirdManager.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatalf("FTM path was not reusable after clean stop: %v", err)
	}
	if got := thirdManager.GetFailedOperationCount(); got != 1 {
		t.Fatalf("recovered failed operation count=%d", got)
	}
	if err := thirdManager.StopStrict(); err != nil {
		t.Fatal(err)
	}
	ftmLockInfo, err := os.Stat(filepath.Join(ftmDir, "failed_operations.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && ftmLockInfo.Mode().Perm() != recoveryFileMode {
		t.Fatalf("FTM advisory lock mode=%v", ftmLockInfo.Mode().Perm())
	}
}

func TestFaultTolerantManager_DefaultPersistPathIsExclusive(t *testing.T) {
	t.Chdir(t.TempDir())
	first := NewFaultTolerantManager(nil, nil)
	if err := first.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	second := NewFaultTolerantManager(nil, nil)
	if err := second.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrFaultTolerantManagerPathInUse) {
		t.Fatalf("default path allowed two managers: %v", err)
	}
	if err := second.StopStrict(); !errors.Is(err, ErrFaultTolerantManagerPathInUse) {
		t.Fatalf("second StopStrict error=%v", err)
	}
	if err := first.StopStrict(); err != nil {
		t.Fatal(err)
	}
}

func TestInitGameDb_RejectsDuplicateAndCleansPartialInitialization(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		db := newStrictTestDb(t, newScriptedDBState())
		opts := DefaultGameDbOptions()
		opts.DatabaseGeneration = "epoch"
		opts.EnableLocalJournal = false
		opts.EnableWriteBuffer = false
		opts.EnableEntityCache = false
		if _, err := InitGameDb(db, nil, opts); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := InitGameDb(db, nil, opts); err == nil {
			t.Fatal("duplicate InitGameDb unexpectedly succeeded")
		}
	})

	t.Run("generation failure", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pending.ndjson"), []byte("orphan\n"), recoveryFileMode); err != nil {
			t.Fatal(err)
		}
		db := NewDb(nil, 2, nil)
		opts := DefaultGameDbOptions()
		opts.DatabaseGeneration = "epoch"
		opts.LocalJournalPath = dir
		opts.EnableLocalJournal = true
		opts.EnableWriteBuffer = false
		opts.EnableEntityCache = false
		if _, err := InitGameDb(db, nil, opts); !errors.Is(err, ErrDatabaseGenerationBlocked) {
			t.Fatalf("InitGameDb error=%v", err)
		}
		if db.FaultTolerantMgr != nil || db.WriteJournal != nil || db.SessionRepo != nil {
			t.Fatal("partial initialization leaked attached resources")
		}
		if db.DatabaseGeneration() != "" {
			t.Fatalf("generation was not restored: %q", db.DatabaseGeneration())
		}
		if _, registered := registeredPoolDbs.Load(db); registered {
			t.Fatal("partial initialization leaked connection-pool registration")
		}
		lockPath := filepath.Join(dir, "pending.ndjson.lock")
		lockInfo, err := os.Stat(lockPath)
		if err != nil {
			t.Fatalf("advisory lock file missing after cleanup: %v", err)
		}
		if runtime.GOOS != "windows" && lockInfo.Mode().Perm() != recoveryFileMode {
			t.Fatalf("advisory lock mode=%v", lockInfo.Mode().Perm())
		}
		probe := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
		if err := probe.RotateDatabaseGeneration("epoch-recovered"); err != nil {
			t.Fatalf("partial initialization retained WAL ownership: %v", err)
		}
		if err := probe.StopStrict(); err != nil {
			t.Fatal(err)
		}
	})
}

type nonIdempotentBufferEntity struct {
	ID        string `db:"id" primary_key:"true" json:"id"`
	Payload   string `db:"payload" json:"payload"`
	HookCalls int    `db:"-" json:"-"`
	PanicHook bool   `db:"-" json:"-"`
}

func (*nonIdempotentBufferEntity) TableName() string { return "non_idempotent_buffer" }
func (e *nonIdempotentBufferEntity) SerializeBeforeSaveDb() {
	e.HookCalls++
	e.Payload += "!"
	if e.PanicHook {
		panic("injected serialize panic")
	}
}
func (*nonIdempotentBufferEntity) DeserializeAfterLoadDb() {}

func TestWriteBuffer_SerializesOnceAcrossRetryAndPropagatesBackgroundError(t *testing.T) {
	t.Run("SQL retry", func(t *testing.T) {
		writeErr := errors.New("injected write failure")
		state := newScriptedDBState(
			scriptedStep{kind: "exec", execErr: writeErr},
			scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 1}},
		)
		db := newStrictTestDb(t, state)
		repo := NewBaseCrudRepository(db)
		GetCrudManagerInstance().AutoInitEntity(&nonIdempotentBufferEntity{})
		wb := newWriteBuffer(repo)
		entity := &nonIdempotentBufferEntity{ID: "player", Payload: "value"}
		if queued, err := wb.Enqueue(entity); err != nil || !queued {
			t.Fatalf("Enqueue queued=%v err=%v", queued, err)
		}
		if err := wb.Flush(); !errors.Is(err, writeErr) {
			t.Fatalf("first Flush error=%v", err)
		}
		if err := wb.Flush(); err != nil {
			t.Fatalf("second Flush: %v", err)
		}
		if entity.HookCalls != 0 || entity.Payload != "value" {
			t.Fatalf("caller entity was mutated by background ownership: hook calls=%d payload=%q", entity.HookCalls, entity.Payload)
		}
		calls := state.snapshotCalls()
		execCalls := make([]scriptedCall, 0, 2)
		for _, call := range calls {
			if call.kind == "exec" {
				execCalls = append(execCalls, call)
			}
		}
		if len(execCalls) != 2 || len(execCalls[0].args) < 2 || execCalls[0].args[1].Value != "value!" || execCalls[1].args[1].Value != "value!" {
			t.Fatalf("serialized snapshot was not reused across retry: calls=%+v", calls)
		}
	})

	t.Run("background and close", func(t *testing.T) {
		backgroundErr := errors.New("background flush failure")
		repo := NewBaseCrudRepository(nil)
		var observedSerialized atomic.Bool
		repo.SetTestUpsertHook(func(entities []IDbEntity) error {
			if len(entities) == 1 {
				if snapshot, ok := entities[0].(*nonIdempotentBufferEntity); ok && snapshot.HookCalls == 1 && snapshot.Payload == "value!" {
					observedSerialized.Store(true)
				}
			}
			return backgroundErr
		})
		wb := newWriteBuffer(repo)
		settings := DefaultCrudPerformanceSettings()
		settings.WriteBufferFlushIntervalMs = 2
		wb.Start(settings)
		entity := &nonIdempotentBufferEntity{ID: "background", Payload: "value"}
		if queued, err := wb.Enqueue(entity); err != nil || !queued {
			t.Fatalf("Enqueue queued=%v err=%v", queued, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			if err, failures := wb.LastBackgroundFlushError(); errors.Is(err, backgroundErr) && failures > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("background flush error was not observable")
			}
			time.Sleep(time.Millisecond)
		}
		if err := wb.Stop(); !errors.Is(err, backgroundErr) {
			t.Fatalf("Stop did not propagate background error: %v", err)
		}
		if !observedSerialized.Load() || entity.HookCalls != 0 || entity.Payload != "value" {
			t.Fatalf("background snapshot ownership mismatch: observed=%v callerHook=%d callerPayload=%q", observedSerialized.Load(), entity.HookCalls, entity.Payload)
		}
	})

	t.Run("serialize panic stays blocked", func(t *testing.T) {
		repo := NewBaseCrudRepository(nil)
		var writes atomic.Int64
		repo.SetTestUpsertHook(func([]IDbEntity) error {
			writes.Add(1)
			return nil
		})
		wb := newWriteBuffer(repo)
		entity := &nonIdempotentBufferEntity{ID: "panic", Payload: "value", PanicHook: true}
		if queued, err := wb.Enqueue(entity); err != nil || !queued {
			t.Fatalf("Enqueue queued=%v err=%v", queued, err)
		}
		if err := wb.Flush(); err == nil {
			t.Fatal("serialize panic was not returned")
		}
		if err := wb.Flush(); err == nil {
			t.Fatal("failed serialization was not sticky")
		}
		wb.mu.Lock()
		pending := wb.pending[entity.TableName()][entity.ID]
		wb.mu.Unlock()
		snapshot, _ := pending.(*nonIdempotentBufferEntity)
		if snapshot == nil || snapshot.HookCalls != 1 || entity.HookCalls != 0 || writes.Load() != 0 {
			t.Fatalf("panic snapshot=%+v callerHook=%d writes=%d", snapshot, entity.HookCalls, writes.Load())
		}
	})
}

func TestWriteBufferSuccessfulRecoveryClearsCurrentBackgroundError(t *testing.T) {
	recoveryErr := errors.New("temporary write failure")
	var calls atomic.Int32
	repo := NewBaseCrudRepository(nil)
	repo.SetTestUpsertHook(func([]IDbEntity) error {
		if calls.Add(1) == 1 {
			return recoveryErr
		}
		return nil
	})
	GetCrudManagerInstance().AutoInitEntity(&nonIdempotentBufferEntity{})
	buffer := newWriteBuffer(repo)
	if queued, err := buffer.Enqueue(&nonIdempotentBufferEntity{ID: "recover", Payload: "value"}); err != nil || !queued {
		t.Fatalf("Enqueue queued=%v err=%v", queued, err)
	}
	if err := buffer.Flush(); !errors.Is(err, recoveryErr) {
		t.Fatalf("first Flush=%v", err)
	}
	buffer.recordBackgroundFlushError(recoveryErr)
	if err := buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	if current, failures := buffer.LastBackgroundFlushError(); current != nil || failures != 1 {
		t.Fatalf("recovered background state current=%v failures=%d", current, failures)
	}
	if err := buffer.Stop(); err != nil {
		t.Fatalf("Stop returned stale background failure: %v", err)
	}
}

func TestFaultTolerantClearRollbackKeepsMemoryWhenPersistenceFails(t *testing.T) {
	dir := t.TempDir()
	manager := NewFaultTolerantManager(nil, nil)
	if err := manager.SetPersistPathStrict(dir); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureDatabaseGeneration("clear-rollback"); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordFailedOperationStrict(&FailedOperation{
		Operation: "ExecuteUpdate",
		SQL:       "UPDATE durable SET value = ?",
		Params:    []any{"secret"},
	}); err != nil {
		t.Fatal(err)
	}
	failedFile := filepath.Join(dir, "failed_operations.json")
	if err := os.Remove(failedFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(failedFile, recoveryDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := manager.ClearFailedOperationsStrict(); err == nil {
		t.Fatal("Clear succeeded despite an unwritable recovery target")
	}
	if got := manager.GetFailedOperationCount(); got != 1 {
		t.Fatalf("failed Clear lost in-memory recovery operation: %d", got)
	}
	if err := os.Remove(failedFile); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopStrict(); err != nil {
		t.Fatal(err)
	}
}

func TestValidGenerationRotationDoesNotArchiveManifestForever(t *testing.T) {
	dir := t.TempDir()
	GetCrudManagerInstance().AutoInitEntity(&nonIdempotentBufferEntity{})
	old := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
	if err := old.ConfigureDatabaseGeneration("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := old.AppendEntities("SaveBatchUpsert", []IDbEntity{&nonIdempotentBufferEntity{ID: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := old.StopStrict(); err != nil {
		t.Fatal(err)
	}
	current := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
	t.Cleanup(func() { _ = current.StopStrict() })
	if err := current.ConfigureDatabaseGeneration("new"); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "wal-generation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest recoveryGenerationManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil || manifest.DatabaseGeneration != "new" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) == ".json" {
		t.Fatalf("normal rotation should archive only recovery data: %v", entries)
	}
}

func TestLocalWriteJournal_StopDuringReplayStillCleansSuccessfulEntries(t *testing.T) {
	rowsEntered := make(chan struct{}, 1)
	releaseRows := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRows) })

	state := newScriptedDBState(scriptedStep{
		kind: "exec",
		result: repositoryBlockingRowsAffectedResult{
			entered: rowsEntered,
			release: releaseRows,
		},
	})
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&nonIdempotentBufferEntity{})
	GetEntityTypeRegistry().Register(&nonIdempotentBufferEntity{})
	dir := t.TempDir()
	journal := NewLocalWriteJournal(dir, repo)
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	if _, err := journal.AppendEntities(
		"SaveBatchUpsert",
		[]IDbEntity{&nonIdempotentBufferEntity{ID: "stop-race", Payload: "value"}},
	); err != nil {
		t.Fatal(err)
	}

	type replayResult struct {
		success int
		failed  int
		err     error
	}
	replayDone := make(chan replayResult, 1)
	go func() {
		success, failed, err := journal.ReplayAllStrict()
		replayDone <- replayResult{success: success, failed: failed, err: err}
	}()
	select {
	case <-rowsEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not reach RowsAffected")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- journal.StopStrict() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		journal.lifecycleMu.Lock()
		stopped := journal.stopped
		journal.lifecycleMu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("StopStrict did not enter stopped state")
		}
		runtime.Gosched()
	}
	select {
	case err := <-stopDone:
		t.Fatalf("StopStrict returned before in-flight replay completed: %v", err)
	default:
	}

	releaseOnce.Do(func() { close(releaseRows) })
	select {
	case result := <-replayDone:
		if result.success != 1 || result.failed != 0 || result.err != nil {
			t.Fatalf("replay result=%+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not finish")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopStrict: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StopStrict did not finish")
	}
	if _, err := os.Stat(filepath.Join(dir, "pending.ndjson")); !os.IsNotExist(err) {
		t.Fatalf("successful replay entry was not cleaned: %v", err)
	}
}

func TestLocalWriteJournal_ReplayDoesNotRemoveConcurrentNewerPayload(t *testing.T) {
	rowsEntered := make(chan struct{}, 1)
	releaseRows := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRows) })
	state := newScriptedDBState(scriptedStep{
		kind: "exec",
		result: repositoryBlockingRowsAffectedResult{
			entered: rowsEntered,
			release: releaseRows,
		},
	})
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&nonIdempotentBufferEntity{})
	GetEntityTypeRegistry().Register(&nonIdempotentBufferEntity{})
	journal := NewLocalWriteJournal(t.TempDir(), repo)
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	if _, err := journal.AppendEntities(
		"SaveBatchUpsert",
		[]IDbEntity{&nonIdempotentBufferEntity{ID: "same-key", Payload: "old"}},
	); err != nil {
		t.Fatal(err)
	}

	replayDone := make(chan error, 1)
	go func() {
		_, _, err := journal.ReplayAllStrict()
		replayDone <- err
	}()
	select {
	case <-rowsEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not reach RowsAffected")
	}
	if _, err := journal.AppendEntities(
		"SaveBatchUpsert",
		[]IDbEntity{&nonIdempotentBufferEntity{ID: "same-key", Payload: "new"}},
	); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(releaseRows) })
	select {
	case err := <-replayDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not finish")
	}

	journal.journalMu.Lock()
	entries, err := journal.readAllEntriesLocked()
	journal.journalMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("newer payload entries=%d", len(entries))
	}
	var persisted nonIdempotentBufferEntity
	if err := json.Unmarshal(entries[0].EntityJSON, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Payload != "new!" {
		t.Fatalf("newer payload was removed or replaced: %q", persisted.Payload)
	}
}

func TestRecoveryPrivacy_WALAndFailedOperationsDoNotExposeCanaries(t *testing.T) {
	t.Run("fault tolerant manager", func(t *testing.T) {
		previousLogger := defaultLogger
		var output bytes.Buffer
		defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
		t.Cleanup(func() { defaultLogger = previousLogger })

		canary := "ftm-private-canary\nFORGED-FTM-RECORD"
		driverErr := errors.New("driver rejected private value " + canary)
		state := newScriptedDBState(scriptedStep{kind: "exec", execErr: driverErr})
		db := newStrictTestDb(t, state)
		if err := db.configureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		manager := NewFaultTolerantManager(db, nil)
		if err := manager.SetPersistPathStrict(dir); err != nil {
			t.Fatal(err)
		}
		if err := manager.ConfigureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []string{"ExecuteUpdate", "invalid\n" + canary} {
			if err := manager.RecordFailedOperationStrict(&FailedOperation{
				Operation:  operation,
				SQL:        "UPDATE recovery_privacy SET value=?",
				Params:     []any{canary},
				TableName:  canary,
				PrimaryKey: canary,
				LastError:  canary,
			}); err != nil {
				t.Fatal(err)
			}
		}

		assertPersistedLastErrorsAreSafe := func(stage string) {
			t.Helper()
			data, err := os.ReadFile(filepath.Join(dir, "failed_operations.json"))
			if err != nil {
				t.Fatal(err)
			}
			var operations []*FailedOperation
			if err := json.Unmarshal(data, &operations); err != nil {
				t.Fatal(err)
			}
			if len(operations) != 2 {
				t.Fatalf("%s persisted operations=%d", stage, len(operations))
			}
			for _, operation := range operations {
				assertTextDoesNotContainSecrets(t, stage+" LastError", operation.LastError, canary, "FORGED-FTM-RECORD")
				if !strings.Contains(operation.LastError, "ErrorClass=") || strings.Contains(operation.LastError, "ErrorHash=") {
					t.Fatalf("%s LastError lacks safe summary: %q", stage, operation.LastError)
				}
			}
		}
		assertPersistedLastErrorsAreSafe("record")

		retryErr := manager.RetryFailedOperationsNowStrict()
		if retryErr == nil {
			t.Fatal("expected retry failure")
		}
		if !errors.Is(retryErr, driverErr) {
			t.Fatalf("redaction lost original driver cause: %v", retryErr)
		}
		assertTextDoesNotContainSecrets(t, "FTM returned error", retryErr.Error(), canary, "FORGED-FTM-RECORD")
		assertPersistedLastErrorsAreSafe("retry")
		if err := manager.StopStrict(); err != nil {
			t.Fatal(err)
		}
		assertTextDoesNotContainSecrets(t, "FTM logs", output.String(), canary, "FORGED-FTM-RECORD")
	})

	t.Run("WAL", func(t *testing.T) {
		previousLogger := defaultLogger
		var output bytes.Buffer
		defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
		t.Cleanup(func() { defaultLogger = previousLogger })

		canary := "wal-private-canary\nFORGED-WAL-RECORD"
		driverErr := errors.New("driver rejected private value " + canary)
		state := newScriptedDBState(scriptedStep{kind: "exec", execErr: driverErr})
		db := newStrictTestDb(t, state)
		if err := db.configureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		repo := NewBaseCrudRepository(db)
		GetCrudManagerInstance().AutoInitEntity(&nonIdempotentBufferEntity{})
		GetEntityTypeRegistry().Register(&nonIdempotentBufferEntity{})
		journal := NewLocalWriteJournal(t.TempDir(), repo)
		if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = journal.StopStrict() })
		if _, err := journal.AppendEntities("SaveBatchUpsert", []IDbEntity{
			&nonIdempotentBufferEntity{ID: canary, Payload: "bad"},
			&nonIdempotentBufferEntity{ID: "valid", Payload: "driver-error"},
		}); err != nil {
			t.Fatal(err)
		}
		journal.journalMu.Lock()
		entries, err := journal.readAllEntriesLocked()
		if err == nil {
			for _, entry := range entries {
				if entry.PrimaryKey == canary {
					entry.ID = "entry-" + canary
					entry.EntityTypeName = "missing-type-" + canary
				}
			}
			err = journal.rewriteEntriesLocked(entries)
		}
		journal.journalMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}

		success, failed, replayErr := journal.ReplayAllStrict()
		if success != 0 || failed != 2 || replayErr == nil {
			t.Fatalf("replay success=%d failed=%d err=%v", success, failed, replayErr)
		}
		if !errors.Is(replayErr, driverErr) {
			t.Fatalf("redaction lost original WAL driver cause: %v", replayErr)
		}
		assertTextDoesNotContainSecrets(t, "WAL returned error", replayErr.Error(), canary, "FORGED-WAL-RECORD")
		assertTextDoesNotContainSecrets(t, "WAL logs", output.String(), canary, "FORGED-WAL-RECORD")
		stopErr := journal.StopStrict()
		if !errors.Is(stopErr, driverErr) {
			t.Fatalf("StopStrict lost replay cause: %v", stopErr)
		}
		assertTextDoesNotContainSecrets(t, "WAL StopStrict error", stopErr.Error(), canary, "FORGED-WAL-RECORD")
	})
}

func TestLocalWriteJournal_ReplayChunksAndCleansOnlySuccessfulChunks(t *testing.T) {
	settingsManager := GetCrudPerformanceSettings()
	previousSettings := settingsManager.Snapshot()
	currentSettings := previousSettings
	currentSettings.BatchUpsertChunkSize = 2
	settingsManager.ApplyFull(currentSettings)
	t.Cleanup(func() { settingsManager.ApplyFull(previousSettings) })

	chunkErr := errors.New("middle WAL chunk failed")
	state := newScriptedDBState(
		scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 2}},
		scriptedStep{kind: "exec", execErr: chunkErr},
		scriptedStep{kind: "exec", result: scriptedResult{rowsAffected: 1}},
	)
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	journal := NewLocalWriteJournal(t.TempDir(), repo)
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	entities := make([]IDbEntity, 5)
	for index := range entities {
		entities[index] = &flushTestEntity{PlayerID: fmt.Sprintf("player-%d", index), Name: "pending"}
	}
	if _, err := journal.AppendEntities("SaveBatchUpsert", entities); err != nil {
		t.Fatal(err)
	}

	success, failed, replayErr := journal.ReplayAllStrict()
	if success != 3 || failed != 2 || !errors.Is(replayErr, chunkErr) {
		t.Fatalf("replay success=%d failed=%d err=%v", success, failed, replayErr)
	}
	if calls := state.countCalls("exec"); calls != 3 {
		t.Fatalf("replay SQL chunks=%d, want 3", calls)
	}
	if count, err := journal.PendingCount(); err != nil || count != 2 {
		t.Fatalf("pending after partial replay=%d err=%v", count, err)
	}
	journal.journalMu.Lock()
	remaining, err := journal.readAllEntriesLocked()
	journal.journalMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	remainingKeys := make(map[string]struct{}, len(remaining))
	for _, entry := range remaining {
		remainingKeys[entry.PrimaryKey] = struct{}{}
	}
	if _, ok := remainingKeys["player-2"]; !ok {
		t.Fatalf("failed chunk entry player-2 was removed: %#v", remainingKeys)
	}
	if _, ok := remainingKeys["player-3"]; !ok {
		t.Fatalf("failed chunk entry player-3 was removed: %#v", remainingKeys)
	}
	if got := journal.rewriteCount.Load(); got != 2 {
		t.Fatalf("WAL mutations=%d, want append once + cleanup once", got)
	}
}

func TestLocalWriteJournal_TenThousandRepeatedFailuresDoNotRewriteWAL(t *testing.T) {
	settingsManager := GetCrudPerformanceSettings()
	previousSettings := settingsManager.Snapshot()
	currentSettings := previousSettings
	currentSettings.BatchUpsertChunkSize = 200
	settingsManager.ApplyFull(currentSettings)
	t.Cleanup(func() { settingsManager.ApplyFull(previousSettings) })

	const entityCount = 10_000
	const attempts = 2
	const chunksPerAttempt = entityCount / 200
	outageErr := errors.New("bounded WAL outage")
	steps := make([]scriptedStep, 0, attempts*chunksPerAttempt)
	for index := 0; index < attempts*chunksPerAttempt; index++ {
		steps = append(steps, scriptedStep{kind: "exec", execErr: outageErr})
	}
	state := newScriptedDBState(steps...)
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetEntityTypeRegistry().Register(&flushTestEntity{})
	journal := NewLocalWriteJournal(t.TempDir(), repo)
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.StopStrict() })
	repo.SetWriteJournal(journal)

	entities := make([]IDbEntity, entityCount)
	for index := range entities {
		entities[index] = &flushTestEntity{
			PlayerID: fmt.Sprintf("player-%05d", index),
			Name:     "same-payload",
			Level:    1,
		}
	}
	started := time.Now()
	for attempt := 0; attempt < attempts; attempt++ {
		err := repo.SaveBatchUpsert(entities)
		if !errors.Is(err, outageErr) {
			t.Fatalf("attempt %d lost outage cause: %v", attempt, err)
		}
		if len(err.Error()) > 32*1024 {
			t.Fatalf("attempt %d returned unbounded error text: %d bytes", attempt, len(err.Error()))
		}
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("10k repeated outage path took %v", elapsed)
	}
	if calls := state.countCalls("exec"); calls != attempts*chunksPerAttempt {
		t.Fatalf("SQL chunk calls=%d, want %d", calls, attempts*chunksPerAttempt)
	}
	if count, err := journal.PendingCount(); err != nil || count != entityCount {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	if got := journal.rewriteCount.Load(); got != 1 {
		t.Fatalf("repeated identical outage rewrote WAL %d times, want 1", got)
	}
}

const localWriteJournalLockHelperEnv = "DB233_WAL_LOCK_HELPER"

func TestLocalWriteJournal_AdvisoryLockProcessHelper(t *testing.T) {
	if os.Getenv(localWriteJournalLockHelperEnv) != "1" {
		return
	}
	dir := os.Getenv("DB233_WAL_LOCK_DIR")
	readyPath := os.Getenv("DB233_WAL_LOCK_READY")
	journal := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
	if err := journal.ConfigureDatabaseGeneration("epoch"); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "configure helper WAL: %v\n", err)
		os.Exit(2)
	}
	if err := os.WriteFile(readyPath, []byte("ready"), recoveryFileMode); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write helper ready marker: %v\n", err)
		os.Exit(3)
	}
	var release [1]byte
	_, _ = os.Stdin.Read(release[:])
	runtime.KeepAlive(journal)
	// 模拟崩溃：不调用 StopStrict/Unlock，由操作系统在进程退出时释放 advisory lock。
	os.Exit(0)
}

func TestLocalWriteJournal_AdvisoryLockRejectsConcurrentProcessAndRecoversAfterCrash(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "helper.ready")
	command := exec.Command(os.Args[0], "-test.run=^TestLocalWriteJournal_AdvisoryLockProcessHelper$")
	command.Env = append(
		os.Environ(),
		localWriteJournalLockHelperEnv+"=1",
		"DB233_WAL_LOCK_DIR="+dir,
		"DB233_WAL_LOCK_READY="+readyPath,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var childOutput bytes.Buffer
	command.Stdout = &childOutput
	command.Stderr = &childOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = stdin.Close()
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock helper did not become ready: %s", childOutput.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	contender := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
	if err := contender.ConfigureDatabaseGeneration("epoch"); !errors.Is(err, ErrLocalWriteJournalPathInUse) {
		t.Fatalf("concurrent process was not rejected: %v", err)
	}
	if err := contender.StopStrict(); err != nil {
		t.Fatal(err)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("lock helper failed: %v, output=%s", err, childOutput.String())
	}
	lockPath := filepath.Join(dir, "pending.ndjson.lock")
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("advisory lock file should remain stable: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != recoveryFileMode {
		t.Fatalf("advisory lock mode=%v", info.Mode().Perm())
	}

	recovered := NewLocalWriteJournal(dir, NewBaseCrudRepository(nil))
	if err := recovered.ConfigureDatabaseGeneration("epoch"); err != nil {
		t.Fatalf("crash-residual lock file blocked restart: %v", err)
	}
	if err := recovered.StopStrict(); err != nil {
		t.Fatal(err)
	}
}
