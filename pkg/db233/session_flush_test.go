package db233

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type flushTestEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Name     string `db:"name"`
	Level    int    `db:"level"`
}

func (e *flushTestEntity) TableName() string       { return "flush_test_entity" }
func (e *flushTestEntity) SerializeBeforeSaveDb()  {}
func (e *flushTestEntity) DeserializeAfterLoadDb() {}

type flushTestEntityB struct {
	PlayerID string `db:"playerId" primary_key:"true"`
	Gold     int    `db:"gold"`
}

func (e *flushTestEntityB) TableName() string       { return "flush_test_entity_b" }
func (e *flushTestEntityB) SerializeBeforeSaveDb()  {}
func (e *flushTestEntityB) DeserializeAfterLoadDb() {}

type upsertRecorder struct {
	mu          sync.Mutex
	batches     [][]IDbEntity
	inFlight    int
	maxInFlight int
	delay       time.Duration
	failOnCall  int
	calls       int
}

func newUpsertRecorder() *upsertRecorder {
	return &upsertRecorder{}
}

func (rec *upsertRecorder) hook(entities []IDbEntity) error {
	rec.mu.Lock()
	rec.calls++
	rec.inFlight++
	if rec.inFlight > rec.maxInFlight {
		rec.maxInFlight = rec.inFlight
	}
	delay := rec.delay
	failOn := rec.failOnCall
	callN := rec.calls
	rec.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	batch := make([]IDbEntity, len(entities))
	copy(batch, entities)

	rec.mu.Lock()
	rec.batches = append(rec.batches, batch)
	rec.inFlight--
	rec.mu.Unlock()

	if failOn > 0 && callN == failOn {
		return fmt.Errorf("injected upsert error on call %d", callN)
	}
	return nil
}

func (rec *upsertRecorder) batchCount() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.batches)
}

func (rec *upsertRecorder) totalEntities() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := 0
	for _, b := range rec.batches {
		n += len(b)
	}
	return n
}

func (rec *upsertRecorder) maxConcurrent() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.maxInFlight
}

func setupFlushTestRepo(t *testing.T) *BaseCrudRepository {
	t.Helper()
	repo := NewBaseCrudRepository(nil)
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntity{})
	GetCrudManagerInstance().AutoInitEntity(&flushTestEntityB{})
	return repo
}

func applyFlushTestCacheSettings(t *testing.T, patch EntityCacheSettings) {
	t.Helper()
	base := DefaultEntityCacheSettings()
	base.Enabled = true
	base.SessionFlushIntervalMs = 0
	if patch.SessionFlushMaxWorkers > 0 {
		base.SessionFlushMaxWorkers = patch.SessionFlushMaxWorkers
	}
	if patch.ShutdownFlushMaxWorkers > 0 {
		base.ShutdownFlushMaxWorkers = patch.ShutdownFlushMaxWorkers
	}
	if patch.ShutdownFlushWaveIntervalMs >= 0 {
		base.ShutdownFlushWaveIntervalMs = patch.ShutdownFlushWaveIntervalMs
	}
	base.SessionFlushMergeByTable = patch.SessionFlushMergeByTable
	GetEntityCacheSettings().ApplyFull(base)
	t.Cleanup(func() {
		GetEntityCacheSettings().ApplyFull(DefaultEntityCacheSettings())
	})
}

func TestJitterDuration_InRange(t *testing.T) {
	base := 60 * time.Second
	jitterPct := 10
	for i := 0; i < 50; i++ {
		got := jitterDuration(base, jitterPct)
		min := base - base*time.Duration(jitterPct)/100
		max := base + base*time.Duration(jitterPct)/100
		if got < min || got > max {
			t.Fatalf("jitter out of range: got=%v want [%v,%v]", got, min, max)
		}
	}
}

func TestJitterDuration_EdgeCases(t *testing.T) {
	if got := jitterDuration(0, 10); got != 0 {
		t.Fatalf("zero base: %v", got)
	}
	if got := jitterDuration(time.Second, 0); got != time.Second {
		t.Fatalf("zero jitter: %v", got)
	}
	got := jitterDuration(time.Millisecond, 150)
	if got < time.Millisecond {
		t.Fatalf("jitter floor: %v", got)
	}
}

func TestBuildFlushBatchTasks_ChunksByTable(t *testing.T) {
	entities := make([]IDbEntity, 0, 5)
	for i := 0; i < 5; i++ {
		entities = append(entities, &flushTestEntity{PlayerID: fmt.Sprintf("p%d", i), Name: "n"})
	}
	tasks := buildFlushBatchTasks(entities, 2, func(e IDbEntity) string {
		return e.(*flushTestEntity).TableName()
	})
	if len(tasks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(tasks))
	}
}

func TestBuildFlushBatchTasks_GroupsMultipleTables(t *testing.T) {
	entities := []IDbEntity{
		&flushTestEntity{PlayerID: "a", Name: "n"},
		&flushTestEntityB{PlayerID: "b", Gold: 1},
		&flushTestEntity{PlayerID: "c", Name: "n"},
	}
	tasks := buildFlushBatchTasks(entities, 10, func(e IDbEntity) string {
		switch v := e.(type) {
		case *flushTestEntity:
			return v.TableName()
		case *flushTestEntityB:
			return v.TableName()
		default:
			return ""
		}
	})
	if len(tasks) != 2 {
		t.Fatalf("expected 2 table batches, got %d", len(tasks))
	}
}

func TestRunFlushBatchWave_RespectsMaxWorkers(t *testing.T) {
	repo := setupFlushTestRepo(t)
	sr := &SessionRepository{repo: repo}
	rec := newUpsertRecorder()
	rec.delay = 30 * time.Millisecond
	repo.SetTestUpsertHook(rec.hook)

	tasks := make([][]IDbEntity, 12)
	for i := range tasks {
		tasks[i] = []IDbEntity{&flushTestEntity{PlayerID: fmt.Sprintf("w%d", i), Name: "x"}}
	}
	if err := sr.runFlushBatchWave(tasks, 4); err != nil {
		t.Fatal(err)
	}
	if rec.batchCount() != 12 {
		t.Fatalf("batch count=%d", rec.batchCount())
	}
	if rec.maxConcurrent() > 4 {
		t.Fatalf("max concurrent=%d want <=4", rec.maxConcurrent())
	}
}

func TestFlushAllDirtyMerged_CrossSessionFewerBatches(t *testing.T) {
	repo := setupFlushTestRepo(t)
	sr := &SessionRepository{repo: repo}
	rec := newUpsertRecorder()
	repo.SetTestUpsertHook(rec.hook)
	applyFlushTestCacheSettings(t, EntityCacheSettings{SessionFlushMergeByTable: true, SessionFlushMaxWorkers: 4})

	GetCacheableEntityRegistry().Register(CacheableEntitySpec{Prototype: &flushTestEntity{}})
	for i := 0; i < 20; i++ {
		s := &PlayerSession{
			PlayerID: fmt.Sprintf("p%d", i),
			repo:     repo,
			owner:    sr,
			entities: make(map[string]IDbEntity),
			dirty:    make(map[string]IDbEntity),
			loaded:   true,
		}
		e := &flushTestEntity{PlayerID: fmt.Sprintf("p%d", i), Name: "n", Level: i}
		s.dirty[s.repo.getTableName(e)] = e
		sr.sessions.Store(s.PlayerID, s)
	}

	settings := GetEntityCacheSettings().Snapshot()
	if err := sr.flushAllDirtyMerged(settings); err != nil {
		t.Fatal(err)
	}
	if rec.batchCount() != 1 {
		t.Fatalf("merged flush want 1 batch, got %d", rec.batchCount())
	}
	if rec.totalEntities() != 20 {
		t.Fatalf("entities=%d", rec.totalEntities())
	}
}

func TestFlushAllDirtyMerged_RestoresOnError(t *testing.T) {
	repo := setupFlushTestRepo(t)
	sr := &SessionRepository{repo: repo}
	rec := newUpsertRecorder()
	rec.failOnCall = 1
	repo.SetTestUpsertHook(rec.hook)
	applyFlushTestCacheSettings(t, EntityCacheSettings{SessionFlushMergeByTable: true})

	s := &PlayerSession{
		PlayerID: "err1",
		repo:     repo,
		owner:    sr,
		entities: make(map[string]IDbEntity),
		dirty: map[string]IDbEntity{
			repo.getTableName(&flushTestEntity{PlayerID: "err1"}): &flushTestEntity{PlayerID: "err1", Name: "x"},
		},
		loaded: true,
	}
	sr.sessions.Store("err1", s)

	if err := sr.flushAllDirtyMerged(GetEntityCacheSettings().Snapshot()); err == nil {
		t.Fatal("expected error")
	}
	if s.DirtyCount() != 1 {
		t.Fatalf("dirty should restore, got %d", s.DirtyCount())
	}
}

func TestFlushAllShutdown_WavesAndWriteBuffer(t *testing.T) {
	repo := setupFlushTestRepo(t)
	sr := &SessionRepository{repo: repo}
	rec := newUpsertRecorder()
	rec.delay = 5 * time.Millisecond
	repo.SetTestUpsertHook(rec.hook)

	applyFlushTestCacheSettings(t, EntityCacheSettings{
		SessionFlushMergeByTable:    true,
		ShutdownFlushMaxWorkers:     2,
		ShutdownFlushWaveIntervalMs: 15,
	})
	mgr := GetCrudPerformanceSettings()
	savedPerf := mgr.Snapshot()
	t.Cleanup(func() { mgr.ApplyFull(savedPerf) })
	mgr.ApplyFull(CrudPerformanceSettings{BatchUpsertChunkSize: 1})

	for i := 0; i < 6; i++ {
		s := &PlayerSession{
			PlayerID: fmt.Sprintf("sd%d", i),
			repo:     repo,
			owner:    sr,
			entities: make(map[string]IDbEntity),
			dirty: map[string]IDbEntity{
				repo.getTableName(&flushTestEntity{}): &flushTestEntity{PlayerID: fmt.Sprintf("sd%d", i), Name: "s"},
			},
			loaded: true,
		}
		sr.sessions.Store(s.PlayerID, s)
	}

	start := time.Now()
	if err := sr.flushAllShutdown(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("expected wave spacing, elapsed=%v", elapsed)
	}
	if rec.totalEntities() != 6 {
		t.Fatalf("entities=%d", rec.totalEntities())
	}
}

func TestAcquireFlushSlot_LimitsCloseSessionConcurrency(t *testing.T) {
	repo := setupFlushTestRepo(t)
	sr := &SessionRepository{repo: repo}
	rec := newUpsertRecorder()
	rec.delay = 40 * time.Millisecond
	repo.SetTestUpsertHook(rec.hook)
	applyFlushTestCacheSettings(t, EntityCacheSettings{SessionFlushMaxWorkers: 3})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		pid := fmt.Sprintf("c%d", i)
		s := &PlayerSession{
			PlayerID: pid,
			repo:     repo,
			owner:    sr,
			entities: make(map[string]IDbEntity),
			dirty: map[string]IDbEntity{
				repo.getTableName(&flushTestEntity{}): &flushTestEntity{PlayerID: pid, Name: "c"},
			},
			loaded: true,
		}
		sr.sessions.Store(pid, s)
		wg.Add(1)
		go func(sess *PlayerSession) {
			defer wg.Done()
			_ = sess.Flush()
		}(s)
	}
	wg.Wait()
	if rec.maxConcurrent() > 3 {
		t.Fatalf("CloseSession concurrency=%d want <=3", rec.maxConcurrent())
	}
}

func TestFlushAllDirty_SkipsOverlappingTick(t *testing.T) {
	repo := setupFlushTestRepo(t)
	sr := &SessionRepository{repo: repo}
	rec := newUpsertRecorder()
	rec.delay = 50 * time.Millisecond
	repo.SetTestUpsertHook(rec.hook)
	applyFlushTestCacheSettings(t, EntityCacheSettings{SessionFlushMergeByTable: true})

	s := &PlayerSession{
		PlayerID: "ov1",
		repo:     repo,
		owner:    sr,
		entities: make(map[string]IDbEntity),
		dirty: map[string]IDbEntity{
			repo.getTableName(&flushTestEntity{}): &flushTestEntity{PlayerID: "ov1", Name: "o"},
		},
		loaded: true,
	}
	sr.sessions.Store("ov1", s)

	if !sr.tryBeginPeriodicFlush() {
		t.Fatal("first tick should acquire")
	}
	go func() {
		time.Sleep(5 * time.Millisecond)
		sr.endPeriodicFlush()
	}()

	_ = sr.FlushAllDirty() // overlapping — should skip
	time.Sleep(60 * time.Millisecond)
	if rec.batchCount() != 0 {
		t.Fatalf("overlapping tick should skip flush, batches=%d", rec.batchCount())
	}
}

func TestEntityCacheSettings_LoadFromJSON_MergesDefaults(t *testing.T) {
	mgr := GetEntityCacheSettings()
	mgr.ApplyFull(DefaultEntityCacheSettings())

	data := []byte(`{"entityCache":{"maxSessions":123}}`)
	if err := mgr.LoadFromJSON(data); err != nil {
		t.Fatal(err)
	}
	s := mgr.Snapshot()
	if s.MaxSessions != 123 {
		t.Fatalf("maxSessions=%d", s.MaxSessions)
	}
	if !s.SessionFlushMergeByTable {
		t.Error("partial JSON should keep default sessionFlushMergeByTable=true")
	}
}
