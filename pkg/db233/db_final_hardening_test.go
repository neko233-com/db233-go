package db233

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type gameInitRollbackEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*gameInitRollbackEntity) TableName() string       { return "game_init_rollback_entity" }
func (*gameInitRollbackEntity) SerializeBeforeSaveDb()  {}
func (*gameInitRollbackEntity) DeserializeAfterLoadDb() {}

type warmupFailureEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*warmupFailureEntity) TableName() string       { return "warmup_failure_entity" }
func (*warmupFailureEntity) SerializeBeforeSaveDb()  {}
func (*warmupFailureEntity) DeserializeAfterLoadDb() {}

func requireComparable[T comparable]() {}

func TestDbRemainsComparable(t *testing.T) {
	requireComparable[Db]()
	requireComparable[LocalWriteJournal]()
}

func TestLegacyFindMethodsPropagateDriverErrors(t *testing.T) {
	finders := []struct {
		name string
		call func(*BaseCrudRepository) error
	}{
		{name: "id", call: func(repo *BaseCrudRepository) error {
			_, err := repo.FindById("p1", &strictContractEntity{})
			return err
		}},
		{name: "ids", call: func(repo *BaseCrudRepository) error {
			_, err := repo.FindByIds([]any{"p1"}, &strictContractEntity{})
			return err
		}},
		{name: "all", call: func(repo *BaseCrudRepository) error {
			_, err := repo.FindAll(&strictContractEntity{})
			return err
		}},
		{name: "condition", call: func(repo *BaseCrudRepository) error {
			_, err := repo.FindByCondition("id = ?", []any{"p1"}, &strictContractEntity{})
			return err
		}},
	}
	for _, finder := range finders {
		t.Run(finder.name, func(t *testing.T) {
			driverErr := errors.New("strict driver failure")
			state := newScriptedDBState(scriptedStep{kind: "query", queryErr: driverErr})
			err := finder.call(NewBaseCrudRepository(newStrictTestDb(t, state)))
			if !errors.Is(err, driverErr) {
				t.Fatalf("driver error lost: %v", err)
			}
		})
	}
}

func TestPlayerSessionRestoreDirtyDoesNotOverwriteNewerPut(t *testing.T) {
	repo := setupFlushTestRepo(t)
	session := newPlayerSession("restore", repo, nil)
	oldEntity := &flushTestEntity{PlayerID: "restore", Level: 1}
	newEntity := &flushTestEntity{PlayerID: "restore", Level: 2}
	session.dirty[oldEntity.TableName()] = oldEntity
	drained := session.takeDirty()
	session.dirty[newEntity.TableName()] = newEntity
	session.restoreDirty(drained)
	if got := session.dirty[newEntity.TableName()]; got != newEntity {
		t.Fatalf("failed flush snapshot overwrote newer entity: got=%p want=%p", got, newEntity)
	}
}

func TestExecuteUpdateMultiRowsStrictUsesSingleLeaseAndFailsFast(t *testing.T) {
	manager := preservePerformanceSettingsUnit(t)
	settings := manager.Snapshot()
	settings.EnablePreparedStmtCache = false
	manager.ApplyFull(settings)

	rowFailure := errors.New("strict row failed")
	state := newScriptedDBState(
		scriptedStep{kind: "exec", result: driver.RowsAffected(2)},
		scriptedStep{kind: "exec", execErr: rowFailure},
		scriptedStep{kind: "exec", result: driver.RowsAffected(9)},
	)
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("strict-batch"); err != nil {
		t.Fatal(err)
	}
	affected, err := db.ExecuteUpdateMultiRowsStrict(
		"UPDATE strict_batch SET value = ?",
		[][]any{{1}, {2}, {3}},
	)
	if affected != 2 || !errors.Is(err, rowFailure) {
		t.Fatalf("affected=%d err=%v", affected, err)
	}
	if calls := state.countCalls("exec"); calls != 2 {
		t.Fatalf("fail-fast exec calls=%d want=2", calls)
	}
}

func TestExecuteUpdateMultiRowsStrictLeaseCoversWholeBatch(t *testing.T) {
	manager := preservePerformanceSettingsUnit(t)
	settings := manager.Snapshot()
	settings.EnablePreparedStmtCache = false
	manager.ApplyFull(settings)

	firstEntered := make(chan struct{}, 1)
	firstRelease := make(chan struct{})
	state := newScriptedDBState(
		scriptedStep{
			kind:          "exec",
			driverEntered: firstEntered,
			driverRelease: firstRelease,
			result:        driver.RowsAffected(1),
		},
		scriptedStep{kind: "exec", result: driver.RowsAffected(1)},
	)
	db := newStrictTestDb(t, state)
	if err := db.configureDatabaseGeneration("old"); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := db.ExecuteUpdateMultiRowsStrict("UPDATE strict_batch SET value = ?", [][]any{{1}, {2}})
		writeDone <- err
	}()
	<-firstEntered

	type transitionResult struct {
		transition *DatabaseGenerationTransition
		err        error
	}
	transitionDone := make(chan transitionResult, 1)
	go func() {
		transition, err := db.BeginDatabaseGenerationTransition("new")
		transitionDone <- transitionResult{transition: transition, err: err}
	}()
	waitForDatabaseGenerationUnavailable(t, db)
	select {
	case result := <-transitionDone:
		if result.transition != nil {
			_ = result.transition.Abort()
		}
		t.Fatalf("transition crossed strict batch: %v", result.err)
	default:
	}
	close(firstRelease)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	result := <-transitionDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := result.transition.Abort(); err != nil {
		t.Fatal(err)
	}
	if calls := state.countCalls("exec"); calls != 2 {
		t.Fatalf("batch exec calls=%d want=2", calls)
	}
}

func TestSessionFlushDoesNotReenterDatabaseGenerationReadLock(t *testing.T) {
	db := NewDb(nil, 0, nil)
	if err := db.configureDatabaseGeneration("stable"); err != nil {
		t.Fatal(err)
	}
	repo := NewBaseCrudRepository(db)
	repo.SetTestUpsertHook(func([]IDbEntity) error { return nil })
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	sessions := NewSessionRepository(repo)
	defer sessions.Stop()
	session := newPlayerSessionForGeneration("nested-lock", repo, sessions, "stable")
	sessions.sessions.Store(session.PlayerID, session)

	wb := newWriteBufferForGeneration(repo, "stable")
	repo.wbMu.Lock()
	repo.writeBuffer = wb
	repo.wbMu.Unlock()
	if queued, err := wb.Enqueue(&flushTestEntity{PlayerID: session.PlayerID, Level: 1}); err != nil || !queued {
		t.Fatalf("Enqueue queued=%v err=%v", queued, err)
	}

	// 把 Flush 卡在已获取 Session+Db RLock 之后，再排队一个
	// Db writer。旧路径随后调 wb.Flush 重入 RLock，会与 writer
	// 形成自锁；under-lease 路径应直接完成。
	wb.flushMu.Lock()
	flushDone := make(chan error, 1)
	go func() { flushDone <- session.Flush() }()
	waitUntil(t, time.Second, func() bool {
		if db.generationMu.TryLock() {
			db.generationMu.Unlock()
			return false
		}
		return true
	})
	writerDone := make(chan struct{})
	go func() {
		db.generationMu.Lock()
		close(writerDone)
		db.generationMu.Unlock()
	}()
	// 读租约正在持有，writer 不可能提前完成；给调度器一个
	// 明确窗口让 RWMutex 发布 writer-pending 门。
	time.Sleep(20 * time.Millisecond)
	probeDone := make(chan struct{})
	go func() {
		db.generationMu.RLock()
		close(probeDone)
		db.generationMu.RUnlock()
	}()
	select {
	case <-probeDone:
		t.Fatal("generation writer was not queued before releasing flush")
	case <-time.After(20 * time.Millisecond):
	}
	wb.flushMu.Unlock()
	select {
	case err := <-flushDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Session Flush deadlocked on nested generation RLock")
	}
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("generation writer remained blocked")
	}
	<-probeDone
}

func TestSessionRepositoryAdmissionAndSessionCloseDrainInflight(t *testing.T) {
	settings := preserveEntityCacheSettings(t)
	current := DefaultEntityCacheSettings()
	current.SessionFlushIntervalMs = 0
	settings.ApplyFull(current)
	repo := setupFlushTestRepo(t)
	sessions := NewSessionRepository(repo)
	defer sessions.Stop()

	releaseRepository, err := sessions.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		sessions.CloseAdmissionAndWait()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("repository close did not wait for admitted operation")
	case <-time.After(20 * time.Millisecond):
	}
	releaseRepository()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("repository close remained blocked")
	}
	if _, err := sessions.OpenSession("after-close", nil); !errors.Is(err, ErrSessionRepositoryClosed) {
		t.Fatalf("new operation after close err=%v", err)
	}

	owner := &SessionRepository{repo: repo, entityCounts: make(map[string]int), sessionOps: make(map[string]*sessionOperation)}
	session := newPlayerSession("session-close", repo, owner)
	owner.sessions.Store(session.PlayerID, session)
	releaseSession, err := session.beginLocalOperation()
	if err != nil {
		t.Fatal(err)
	}
	removed := make(chan error, 1)
	go func() { removed <- owner.removeSession(session.PlayerID, false) }()
	waitUntil(t, time.Second, func() bool {
		session.lifecycleMu.Lock()
		defer session.lifecycleMu.Unlock()
		return session.closing
	})
	if err := session.Put(&flushTestEntity{PlayerID: session.PlayerID}); !errors.Is(err, ErrSessionRepositoryClosed) {
		t.Fatalf("Put through closing session err=%v", err)
	}
	releaseSession()
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
}

func TestFlushSemaphoreReleaseBindsAcquiredChannel(t *testing.T) {
	settings := preserveEntityCacheSettings(t)
	current := DefaultEntityCacheSettings()
	current.SessionFlushMaxWorkers = 1
	settings.ApplyFull(current)
	sessions := &SessionRepository{}
	releaseOld := sessions.acquireFlushSlot()
	if err := settings.Set("sessionFlushMaxWorkers", 2); err != nil {
		t.Fatal(err)
	}
	releaseNew := sessions.acquireFlushSlot()
	releaseNew()
	done := make(chan struct{})
	go func() {
		releaseOld()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("release read from replacement semaphore")
	}
}

func TestTrackingDMLAndDDLHoldGenerationLease(t *testing.T) {
	t.Run("insert", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		state := newScriptedDBState(scriptedStep{kind: "exec", queryContains: "INSERT", driverEntered: entered, driverRelease: release})
		db := newStrictTestDb(t, state)
		if err := db.configureDatabaseGeneration("old"); err != nil {
			t.Fatal(err)
		}
		table := &TrackingTable{Name: "tracking_generation", Columns: []TrackingColumn{{Name: "id", Type: "int64", Required: true}}}
		operationDone := make(chan error, 1)
		go func() {
			_, err := InsertTrackingPayload(db, table, map[string]any{"id": int64(1)})
			operationDone <- err
		}()
		<-entered
		assertGenerationTransitionWaits(t, db, release, operationDone)
	})

	t.Run("ddl", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		state := newScriptedDBState(
			scriptedStep{kind: "query", queryContains: "information_schema.tables", columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
			scriptedStep{kind: "exec", queryContains: "CREATE TABLE", driverEntered: entered, driverRelease: release},
		)
		db := newStrictTestDb(t, state)
		if err := db.configureDatabaseGeneration("old"); err != nil {
			t.Fatal(err)
		}
		schema := &TrackingSchema{Version: "1", Tables: []TrackingTable{{
			Name:    "tracking_generation",
			Columns: []TrackingColumn{{Name: "id", Type: "int64", PrimaryKey: true}},
		}}}
		operationDone := make(chan error, 1)
		go func() {
			_, err := ApplyTrackingSchema(db, schema, nil)
			operationDone <- err
		}()
		<-entered
		assertGenerationTransitionWaits(t, db, release, operationDone)
	})
}

func assertGenerationTransitionWaits(t *testing.T, db *Db, release chan struct{}, operationDone <-chan error) {
	t.Helper()
	type transitionResult struct {
		transition *DatabaseGenerationTransition
		err        error
	}
	transitionDone := make(chan transitionResult, 1)
	go func() {
		transition, err := db.BeginDatabaseGenerationTransition("new")
		transitionDone <- transitionResult{transition: transition, err: err}
	}()
	select {
	case result := <-transitionDone:
		if result.transition != nil {
			_ = result.transition.Abort()
		}
		t.Fatalf("generation transition crossed active operation: %v", result.err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	result := <-transitionDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := result.transition.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestPlayerSessionPlayerIDIsRedactedFromLogsAndErrors(t *testing.T) {
	settings := preserveEntityCacheSettings(t)
	current := DefaultEntityCacheSettings()
	current.SessionFlushIntervalMs = 0
	settings.ApplyFull(current)
	registry := preserveCacheableRegistry(t)
	registry.Register(CacheableEntitySpec{Prototype: &flushTestEntity{}})
	previousLogger := defaultLogger
	var output bytes.Buffer
	defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
	t.Cleanup(func() { defaultLogger = previousLogger })

	repo := setupFlushTestRepo(t)
	repo.SetTestUpsertHook(func([]IDbEntity) error { return errors.New("flush failed") })
	sessions := NewSessionRepository(repo)
	defer func() {
		sessions.CloseAdmissionAndWait()
		sessions.Stop()
	}()
	canary := "player-secret\nFORGED-SESSION-LOG"
	session, err := sessions.OpenSession(canary, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Put(&flushTestEntity{PlayerID: canary, Level: 1}); err != nil {
		t.Fatal(err)
	}
	err = sessions.CloseSession(canary)
	if err == nil {
		t.Fatal("expected flush failure")
	}
	for label, text := range map[string]string{"log": output.String(), "error": err.Error()} {
		if strings.Contains(text, canary) || strings.Contains(text, "FORGED-SESSION-LOG") {
			t.Fatalf("%s leaked playerID: %q", label, text)
		}
	}
}

func TestEntityCacheDurationBounds(t *testing.T) {
	settings := preserveEntityCacheSettings(t)
	before := settings.Snapshot()
	if err := settings.Set("sessionFlushIntervalMs", -1); err == nil {
		t.Fatal("negative session flush interval accepted")
	}
	if got := settings.Snapshot().SessionFlushIntervalMs; got != before.SessionFlushIntervalMs {
		t.Fatalf("failed Set mutated interval: got=%d want=%d", got, before.SessionFlushIntervalMs)
	}
	if err := settings.Set("shutdownFlushWaveIntervalMs", -1); err == nil {
		t.Fatal("negative shutdown wave interval accepted")
	}
	maxInt := int(^uint(0) >> 1)
	if err := settings.Set("sessionFlushIntervalMs", maxInt); err != nil {
		t.Fatal(err)
	}
	got := settings.Snapshot().SessionFlushIntervalMs
	if saturatedMilliseconds(got) <= 0 {
		t.Fatalf("extreme interval overflowed: value=%d duration=%v", got, saturatedMilliseconds(got))
	}
	for i := 0; i < 100; i++ {
		if jitterDuration(time.Duration(1<<63-1), 100) <= 0 {
			t.Fatal("extreme jitter overflowed")
		}
	}
}

func TestEntityTypeRegistryStrictCollision(t *testing.T) {
	registry := GetEntityTypeRegistry()
	name := EntityTypeName(&gameInitRollbackEntity{})
	registry.mu.Lock()
	oldFactory, oldFactoryExists := registry.factories[name]
	oldType, oldTypeExists := registry.types[name]
	registry.factories[name] = func() IDbEntity { return &flushTestEntity{} }
	registry.types[name] = reflect.TypeOf(&flushTestEntity{})
	registry.mu.Unlock()
	t.Cleanup(func() {
		registry.mu.Lock()
		if oldFactoryExists {
			registry.factories[name] = oldFactory
		} else {
			delete(registry.factories, name)
		}
		if oldTypeExists {
			registry.types[name] = oldType
		} else {
			delete(registry.types, name)
		}
		registry.mu.Unlock()
	})
	if err := registry.RegisterStrict(&gameInitRollbackEntity{}); err == nil {
		t.Fatal("same short name from different type was accepted")
	}
}

func TestInitGameDbWarmupFailureIsStrictUnlessExplicitlyAllowed(t *testing.T) {
	performance := preservePerformanceSettingsUnit(t)
	settings := performance.Snapshot()
	settings.EnableColdStartWarmup = true
	settings.EnablePreparedStmtCache = false
	settings.PoolWarmupRounds = 1
	performance.ApplyFull(settings)
	warmErr := errors.New("warmup metadata unavailable")
	newOptions := func() GameDbOptions {
		return GameDbOptions{
			DatabaseGeneration: "warmup-epoch",
			EntityTypes:        []IDbEntity{&warmupFailureEntity{}},
		}
	}

	t.Run("strict rollback", func(t *testing.T) {
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{kind: "query", queryErr: warmErr}))
		sessions, err := InitGameDb(db, nil, newOptions())
		if sessions != nil || !errors.Is(err, warmErr) {
			t.Fatalf("InitGameDb sessions=%v err=%v", sessions, err)
		}
		if db.SessionRepo != nil || db.WriteJournal != nil || db.FaultTolerantMgr != nil {
			t.Fatal("warmup failure leaked partially initialized resources")
		}
	})

	t.Run("explicit best effort", func(t *testing.T) {
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{kind: "query", queryErr: warmErr}))
		options := newOptions()
		options.AllowWarmupFailure = true
		sessions, err := InitGameDb(db, nil, options)
		if err != nil || sessions == nil {
			t.Fatalf("InitGameDb sessions=%v err=%v", sessions, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestInitGameDbSerializesGlobalMutationAndRollsBackRegistration(t *testing.T) {
	t.Run("serializes", func(t *testing.T) {
		previousPerformance := GetCrudPerformanceSettings().Snapshot()
		previousCache := GetEntityCacheSettings().Snapshot()
		previousRegistry := GetCacheableEntityRegistry().Snapshot()
		t.Cleanup(func() {
			GetCrudPerformanceSettings().ApplyFull(previousPerformance)
			GetEntityCacheSettings().ApplyFull(previousCache)
			GetCacheableEntityRegistry().Restore(previousRegistry)
		})
		db := NewDb(openScriptedDB(t, newScriptedDBState()), 0, nil)
		result := make(chan error, 1)
		gameDbInitMu.Lock()
		locked := true
		defer func() {
			if locked {
				gameDbInitMu.Unlock()
			}
		}()
		go func() {
			_, err := InitGameDb(db, nil, GameDbOptions{EnableEntityCache: false})
			result <- err
		}()
		select {
		case err := <-result:
			t.Fatalf("InitGameDb bypassed global mutex: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
		gameDbInitMu.Unlock()
		locked = false
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rollback", func(t *testing.T) {
		opts := GameDbOptions{
			EnableLocalJournal: true,
			EnableWriteBuffer:  false,
			EnableEntityCache:  false,
			LocalJournalPath:   t.TempDir(),
			EntityTypes:        []IDbEntity{&gameInitRollbackEntity{}},
		}
		registrationBefore := snapshotGameDbRegistrations(opts)
		cacheBefore := GetCacheableEntityRegistry().Snapshot()
		performanceBefore := GetCrudPerformanceSettings().Snapshot()
		entitySettingsBefore := GetEntityCacheSettings().Snapshot()
		t.Cleanup(func() {
			registrationBefore.restore()
			GetCacheableEntityRegistry().Restore(cacheBefore)
			GetCrudPerformanceSettings().ApplyFull(performanceBefore)
			GetEntityCacheSettings().ApplyFull(entitySettingsBefore)
		})
		payload, err := json.Marshal(&gameInitRollbackEntity{ID: "1"})
		if err != nil {
			t.Fatal(err)
		}
		entry := JournalEntry{
			ID:             "rollback-entry",
			Operation:      "SaveBatchUpsert",
			TableName:      "game_init_rollback_entity",
			PrimaryKey:     "1",
			EntityTypeName: EntityTypeName(&gameInitRollbackEntity{}),
			EntityJSON:     payload,
			CreatedAt:      time.Now(),
		}
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		line = append(line, '\n')
		if err := os.WriteFile(filepath.Join(opts.LocalJournalPath, "pending.ndjson"), line, 0o600); err != nil {
			t.Fatal(err)
		}
		driverErr := errors.New("replay failed")
		db := newStrictTestDb(t, newScriptedDBState(scriptedStep{kind: "exec", execErr: driverErr}))
		_, err = InitGameDb(db, nil, opts)
		if !errors.Is(err, driverErr) {
			t.Fatalf("expected replay failure, got %v", err)
		}
		if _, err := GetEntityTypeRegistry().Create(EntityTypeName(&gameInitRollbackEntity{})); err == nil {
			t.Fatal("failed InitGameDb left EntityTypeRegistry entry")
		}
		if GetCrudManagerInstance().IsContainsEntity(&gameInitRollbackEntity{}) {
			t.Fatal("failed InitGameDb left CrudManager metadata")
		}
	})
}

func TestFaultToleranceStrictLifecycleWithClose(t *testing.T) {
	t.Chdir(t.TempDir())
	config := &DbConnectionConfig{}
	first := NewDb(openScriptedDB(t, newScriptedDBState()), 0, nil)
	if err := first.EnableFaultToleranceStrict(config); err != nil {
		t.Fatal(err)
	}
	second := NewDb(openScriptedDB(t, newScriptedDBState()), 0, nil)
	if err := second.EnableFaultToleranceStrict(config); err == nil {
		t.Fatal("StartStrict path ownership error was swallowed")
	}
	second.resourceMu.Lock()
	if second.FaultTolerantMgr != nil {
		second.resourceMu.Unlock()
		t.Fatal("failed enable published manager")
	}
	second.resourceMu.Unlock()

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = first.DisableFaultToleranceStrict()
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = first.Close()
	}()
	close(start)
	wg.Wait()
	first.resourceMu.Lock()
	manager := first.FaultTolerantMgr
	first.resourceMu.Unlock()
	if manager != nil {
		t.Fatal("Close/Disable race left manager attached")
	}
}
