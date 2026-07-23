package db233

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var sessionSnapshotSerializeCalls atomic.Int32

type sessionSnapshotTestEntity struct {
	PlayerID   string           `db:"playerId" primary_key:"true"`
	Level      int              `db:"level"`
	Values     map[string][]int `db:"values"`
	AliasShort []int            `db:"aliasShort"`
	AliasLong  []int            `db:"aliasLong"`
	Runtime    map[string]any   `db:"-"`
	Self       any              `db:"-"`
	PanicHook  bool             `db:"-"`
	Prepared   int              `db:"prepared"`
}

func (entity *sessionSnapshotTestEntity) TableName() string { return "session_snapshot_test" }

func (entity *sessionSnapshotTestEntity) SerializeBeforeSaveDb() {
	sessionSnapshotSerializeCalls.Add(1)
	entity.Prepared++
	if entity.PanicHook {
		panic("snapshot hook panic")
	}
}

func (*sessionSnapshotTestEntity) DeserializeAfterLoadDb() {}

type sessionSnapshotMutexEntity struct {
	PlayerID string     `db:"playerId" primary_key:"true"`
	Guard    sync.Mutex `db:"-"`
}

func (*sessionSnapshotMutexEntity) TableName() string       { return "session_snapshot_mutex" }
func (*sessionSnapshotMutexEntity) SerializeBeforeSaveDb()  {}
func (*sessionSnapshotMutexEntity) DeserializeAfterLoadDb() {}

type sessionSnapshotPrivateRuntimeEntity struct {
	PlayerID string         `db:"playerId" primary_key:"true"`
	runtime  map[string]any `db:"-"`
}

func (*sessionSnapshotPrivateRuntimeEntity) TableName() string {
	return "session_snapshot_private_runtime"
}
func (*sessionSnapshotPrivateRuntimeEntity) SerializeBeforeSaveDb()  {}
func (*sessionSnapshotPrivateRuntimeEntity) DeserializeAfterLoadDb() {}

type sessionSnapshotSameRootEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
}

type panicSnapshotEntity struct {
	PlayerID string `db:"playerId" primary_key:"true"`
}

func (*panicSnapshotEntity) TableName() string       { return "panic_snapshot_entity" }
func (*panicSnapshotEntity) SerializeBeforeSaveDb()  {}
func (*panicSnapshotEntity) DeserializeAfterLoadDb() {}
func (*panicSnapshotEntity) SnapshotForDb233() (IDbEntity, error) {
	panic("private snapshot payload")
}

type countedSnapshotEntity struct {
	PlayerID string        `db:"playerId" primary_key:"true"`
	Level    int           `db:"level"`
	Calls    *atomic.Int32 `db:"-"`
}

func (*countedSnapshotEntity) TableName() string       { return "counted_snapshot_entity" }
func (*countedSnapshotEntity) SerializeBeforeSaveDb()  {}
func (*countedSnapshotEntity) DeserializeAfterLoadDb() {}
func (entity *countedSnapshotEntity) SnapshotForDb233() (IDbEntity, error) {
	entity.Calls.Add(1)
	clone := *entity
	return &clone, nil
}

func (*sessionSnapshotSameRootEntity) TableName() string       { return "session_snapshot_same_root" }
func (*sessionSnapshotSameRootEntity) SerializeBeforeSaveDb()  {}
func (*sessionSnapshotSameRootEntity) DeserializeAfterLoadDb() {}
func (entity *sessionSnapshotSameRootEntity) SnapshotForDb233() (IDbEntity, error) {
	return entity, nil
}

func enableSessionSnapshotTests(t *testing.T) {
	t.Helper()
	settings := preserveEntityCacheSettings(t)
	current := DefaultEntityCacheSettings()
	current.Enabled = true
	current.SessionFlushIntervalMs = 0
	settings.ApplyFull(current)
	GetCacheableEntityRegistry().Register(CacheableEntitySpec{Prototype: &sessionSnapshotTestEntity{}})
}

func TestSnapshotEntityDeepClonePreservesCyclesAndShapes(t *testing.T) {
	backing := []int{1, 2, 3}
	shared := []int{7, 8}
	entity := &sessionSnapshotTestEntity{
		PlayerID:   "snapshot",
		Values:     map[string][]int{"first": shared, "second": shared},
		AliasShort: backing[:2:3],
		AliasLong:  backing[:3:3],
		PanicHook:  true,
	}
	entity.Self = entity
	entity.Runtime = map[string]any{}
	entity.Runtime["self"] = entity.Runtime
	entity.Runtime["root"] = entity

	snapshotValue, err := SnapshotEntity(entity)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotValue.(*sessionSnapshotTestEntity)
	if snapshot == entity {
		t.Fatal("深快照复用了根指针")
	}
	if snapshot.Self != snapshot || snapshot.Runtime["root"] != snapshot {
		t.Fatal("根循环引用未重定向到克隆根")
	}
	selfMap, ok := snapshot.Runtime["self"].(map[string]any)
	if !ok {
		t.Fatal("map 自循环类型未保留")
	}
	selfMap["cycle-probe"] = true
	if snapshot.Runtime["cycle-probe"] != true {
		t.Fatal("map 自循环未保留")
	}
	delete(snapshot.Runtime, "cycle-probe")
	if len(snapshot.AliasShort) != 2 || cap(snapshot.AliasShort) != 2 || len(snapshot.AliasLong) != 3 {
		t.Fatalf(
			"切片 shape 错误: short=(%d,%d), long=(%d,%d)",
			len(snapshot.AliasShort), cap(snapshot.AliasShort),
			len(snapshot.AliasLong), cap(snapshot.AliasLong),
		)
	}
	backing[0] = 99
	shared[0] = 66
	entity.Runtime["new"] = true
	if snapshot.AliasShort[0] != 1 || snapshot.Values["first"][0] != 7 {
		t.Fatal("业务对象修改穿透到持久化快照")
	}
	if _, exists := snapshot.Runtime["new"]; exists {
		t.Fatal("db:\"-\" 运行态 map 未深拷贝")
	}
	snapshot.Values["first"][0] = 42
	if snapshot.Values["second"][0] != 42 {
		t.Fatal("相同 slice 别名关系未保留")
	}
	if !snapshot.PanicHook {
		t.Fatal("db:\"-\" 标量字段未保留")
	}
}

func TestSnapshotEntitySkipsPrivateRuntimeField(t *testing.T) {
	entity := &sessionSnapshotPrivateRuntimeEntity{
		PlayerID: "private-runtime",
		runtime:  map[string]any{"cache": make(chan struct{})},
	}
	snapshotValue, err := SnapshotEntity(entity)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotValue.(*sessionSnapshotPrivateRuntimeEntity)
	if snapshot.runtime != nil {
		t.Fatal("db:- private runtime field must not be copied into a write snapshot")
	}
}

func TestSnapshotEntityCustomHookPanicIsRedacted(t *testing.T) {
	_, err := SnapshotEntity(&panicSnapshotEntity{PlayerID: "private-player"})
	if err == nil {
		t.Fatal("自定义快照 panic 未传播")
	}
	if strings.Contains(err.Error(), "private snapshot payload") || strings.Contains(err.Error(), "private-player") {
		t.Fatalf("自定义快照 panic 泄露业务数据: %v", err)
	}
}

func TestSnapshotEntityLargeCapacityCopiesOnlyLogicalLength(t *testing.T) {
	backing := make([]int, 1<<16)
	backing[0] = 7
	backing[1] = 99
	entity := &sessionSnapshotTestEntity{
		PlayerID:   "large-cap",
		AliasShort: backing[:1],
	}
	snapshotValue, err := SnapshotEntity(entity)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotValue.(*sessionSnapshotTestEntity)
	if len(snapshot.AliasShort) != 1 || cap(snapshot.AliasShort) != 1 {
		t.Fatalf("large-cap shape=(%d,%d)", len(snapshot.AliasShort), cap(snapshot.AliasShort))
	}
	if snapshot.AliasShort[0] != 7 {
		t.Fatalf("logical slice 内容错误: %v", snapshot.AliasShort)
	}
}

func TestSnapshotEntityZerosNonPersistentRuntimeSynchronization(t *testing.T) {
	entity := &sessionSnapshotMutexEntity{PlayerID: "mutex"}
	entity.Guard.Lock()
	snapshotValue, err := SnapshotEntity(entity)
	entity.Guard.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotValue.(*sessionSnapshotMutexEntity)
	// 若复制了 locked Mutex，这里会永久阻塞；快照必须获得独立零值锁。
	snapshot.Guard.Lock()
	playerID := snapshot.PlayerID
	snapshot.Guard.Unlock()
	if playerID != entity.PlayerID {
		t.Fatalf("快照内容错误: playerID=%q", playerID)
	}
}

func TestSnapshotEntityRejectsCustomSameRoot(t *testing.T) {
	if _, err := SnapshotEntity(&sessionSnapshotSameRootEntity{PlayerID: "same"}); err == nil {
		t.Fatal("自定义 Snapshotter 返回原根指针应失败")
	}
}

func TestPlayerSessionDirtySnapshotIsImmutableAndHookRunsOnceAcrossRetry(t *testing.T) {
	enableSessionSnapshotTests(t)
	sessionSnapshotSerializeCalls.Store(0)
	repo := setupFlushTestRepo(t)
	session := newPlayerSession("immutable", repo, nil)
	live := &sessionSnapshotTestEntity{
		PlayerID: "immutable",
		Level:    1,
		Values:   map[string][]int{"levels": {1, 2}},
	}

	writeFailure := errors.New("snapshot write failed")
	var hookMu sync.Mutex
	shouldFail := true
	observed := make([]int, 0, 2)
	repo.SetTestUpsertHook(func(entities []IDbEntity) error {
		snapshot := entities[0].(*sessionSnapshotTestEntity)
		hookMu.Lock()
		observed = append(observed, snapshot.Values["levels"][0])
		fail := shouldFail
		hookMu.Unlock()
		if fail {
			return writeFailure
		}
		return nil
	})

	if err := session.Put(live); err != nil {
		t.Fatal(err)
	}
	live.Level = 9
	live.Values["levels"][0] = 9
	if err := session.FlushDirtyOnly(); !errors.Is(err, writeFailure) {
		t.Fatalf("首次失败原因丢失: %v", err)
	}
	if calls := sessionSnapshotSerializeCalls.Load(); calls != 1 {
		t.Fatalf("首次 Serialize calls=%d want=1", calls)
	}
	hookMu.Lock()
	shouldFail = false
	hookMu.Unlock()
	if err := session.FlushDirtyOnly(); err != nil {
		t.Fatal(err)
	}
	if calls := sessionSnapshotSerializeCalls.Load(); calls != 1 {
		t.Fatalf("SQL 重试重复执行 Serialize: calls=%d", calls)
	}
	if session.DirtyCount() != 0 {
		t.Fatalf("成功重试后 dirty=%d", session.DirtyCount())
	}
	hookMu.Lock()
	defer hookMu.Unlock()
	if len(observed) != 2 || observed[0] != 1 || observed[1] != 1 {
		t.Fatalf("刷写快照被业务修改撕裂: %v", observed)
	}
}

func TestPlayerSessionHookPanicIsStickyUntilNewPut(t *testing.T) {
	enableSessionSnapshotTests(t)
	sessionSnapshotSerializeCalls.Store(0)
	repo := setupFlushTestRepo(t)
	repo.SetTestUpsertHook(func([]IDbEntity) error { return nil })
	session := newPlayerSession("panic", repo, nil)
	entity := &sessionSnapshotTestEntity{PlayerID: "panic", PanicHook: true}

	if err := session.Put(entity); err != nil {
		t.Fatal(err)
	}
	if err := session.FlushDirtyOnly(); err == nil {
		t.Fatal("Serialize panic 未传播")
	}
	if err := session.FlushDirtyOnly(); err == nil {
		t.Fatal("准备失败状态未保留")
	}
	if calls := sessionSnapshotSerializeCalls.Load(); calls != 1 {
		t.Fatalf("失败快照重复执行 hook: calls=%d", calls)
	}
	entity.PanicHook = false
	if err := session.Put(entity); err != nil {
		t.Fatal(err)
	}
	if err := session.FlushDirtyOnly(); err != nil {
		t.Fatal(err)
	}
	if calls := sessionSnapshotSerializeCalls.Load(); calls != 2 {
		t.Fatalf("新 Put 未获得新的准备机会: calls=%d", calls)
	}
}

func TestPlayerSessionFailedFlushDoesNotOverwriteConcurrentNewSnapshot(t *testing.T) {
	enableSessionSnapshotTests(t)
	sessionSnapshotSerializeCalls.Store(0)
	repo := setupFlushTestRepo(t)
	session := newPlayerSession("newer", repo, nil)
	live := &sessionSnapshotTestEntity{PlayerID: "newer", Level: 1}
	if err := session.Put(live); err != nil {
		t.Fatal(err)
	}

	writeFailure := errors.New("blocked old write failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	repo.SetTestUpsertHook(func([]IDbEntity) error {
		once.Do(func() { close(entered) })
		<-release
		return writeFailure
	})
	flushDone := make(chan error, 1)
	go func() { flushDone <- session.FlushDirtyOnly() }()
	<-entered

	live.Level = 2
	if err := session.Put(live); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-flushDone; !errors.Is(err, writeFailure) {
		t.Fatalf("旧写失败原因丢失: %v", err)
	}

	var persistedLevel int
	repo.SetTestUpsertHook(func(entities []IDbEntity) error {
		persistedLevel = entities[0].(*sessionSnapshotTestEntity).Level
		return nil
	})
	if err := session.FlushDirtyOnly(); err != nil {
		t.Fatal(err)
	}
	if persistedLevel != 2 {
		t.Fatalf("旧失败快照覆盖新版本: persisted=%d", persistedLevel)
	}
	if calls := sessionSnapshotSerializeCalls.Load(); calls != 2 {
		t.Fatalf("每个逻辑快照 hook 次数错误: %d", calls)
	}
}

func TestWriteBufferEnqueueSnapshotsCallerAndKeepsLatestReplacement(t *testing.T) {
	repo := setupFlushTestRepo(t)
	GetCrudManagerInstance().AutoInitEntity(&sessionSnapshotTestEntity{})
	buffer := newWriteBufferForGeneration(repo, "")
	live := &sessionSnapshotTestEntity{
		PlayerID: "buffer-latest",
		Level:    1,
		Values:   map[string][]int{"items": {1}},
	}
	if queued, err := buffer.Enqueue(live); err != nil || !queued {
		t.Fatalf("首次 Enqueue queued=%v err=%v", queued, err)
	}
	live.Level = 2
	live.Values["items"][0] = 2

	buffer.mu.Lock()
	first := buffer.pending[live.TableName()]["buffer-latest"].(*sessionSnapshotTestEntity)
	buffer.mu.Unlock()
	if first.Level != 1 || first.Values["items"][0] != 1 {
		t.Fatalf("caller 修改穿透写缓冲: level=%d values=%v", first.Level, first.Values)
	}
	if queued, err := buffer.Enqueue(live); err != nil || !queued {
		t.Fatalf("替换 Enqueue queued=%v err=%v", queued, err)
	}
	buffer.mu.Lock()
	latest := buffer.pending[live.TableName()]["buffer-latest"].(*sessionSnapshotTestEntity)
	size := buffer.size
	buffer.mu.Unlock()
	if size != 1 || latest.Level != 2 || latest.Values["items"][0] != 2 {
		t.Fatalf("latest replacement 错误: size=%d level=%d values=%v", size, latest.Level, latest.Values)
	}
}

func TestWriteBufferFailedOldSnapshotDoesNotOverwriteConcurrentLatest(t *testing.T) {
	sessionSnapshotSerializeCalls.Store(0)
	repo := setupFlushTestRepo(t)
	GetCrudManagerInstance().AutoInitEntity(&sessionSnapshotTestEntity{})
	buffer := newWriteBufferForGeneration(repo, "")
	live := &sessionSnapshotTestEntity{PlayerID: "buffer-race", Level: 1}
	if queued, err := buffer.Enqueue(live); err != nil || !queued {
		t.Fatalf("Enqueue old queued=%v err=%v", queued, err)
	}

	writeFailure := errors.New("old buffer snapshot failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	repo.SetTestUpsertHook(func([]IDbEntity) error {
		once.Do(func() { close(entered) })
		<-release
		return writeFailure
	})
	flushDone := make(chan error, 1)
	go func() { flushDone <- buffer.Flush() }()
	<-entered
	live.Level = 2
	if queued, err := buffer.Enqueue(live); err != nil || !queued {
		close(release)
		t.Fatalf("Enqueue latest queued=%v err=%v", queued, err)
	}
	close(release)
	if err := <-flushDone; !errors.Is(err, writeFailure) {
		t.Fatalf("旧 flush error=%v", err)
	}

	buffer.mu.Lock()
	pending := buffer.pending[live.TableName()]["buffer-race"].(*sessionSnapshotTestEntity)
	buffer.mu.Unlock()
	if pending.Level != 2 {
		t.Fatalf("旧失败快照覆盖 latest: %d", pending.Level)
	}
	var persistedLevel int
	repo.SetTestUpsertHook(func(entities []IDbEntity) error {
		persistedLevel = entities[0].(*sessionSnapshotTestEntity).Level
		return nil
	})
	if err := buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	if persistedLevel != 2 {
		t.Fatalf("最终写入 level=%d", persistedLevel)
	}
}

func TestPlayerSessionPutQueuedWriteBufferTakesOneDeepSnapshot(t *testing.T) {
	performance := preservePerformanceSettingsUnit(t)
	performanceSettings := performance.Snapshot()
	performanceSettings.WriteBufferEnabled = true
	performance.ApplyFull(performanceSettings)
	cache := preserveEntityCacheSettings(t)
	cacheSettings := cache.Snapshot()
	cacheSettings.Enabled = false
	cacheSettings.SessionFlushIntervalMs = 0
	cache.ApplyFull(cacheSettings)
	registry := preserveCacheableRegistry(t)
	registry.Register(CacheableEntitySpec{Prototype: &countedSnapshotEntity{}})
	GetCrudManagerInstance().AutoInitEntity(&countedSnapshotEntity{})

	db := newStrictTestDb(t, newScriptedDBState())
	repo := NewBaseCrudRepository(db)
	buffer := newWriteBufferForGeneration(repo, "")
	repo.wbMu.Lock()
	repo.writeBuffer = buffer
	repo.wbMu.Unlock()
	sessions := NewSessionRepository(repo)
	defer sessions.Stop()
	session := newPlayerSessionForGeneration("one-copy", repo, sessions, "")
	var calls atomic.Int32
	entity := &countedSnapshotEntity{PlayerID: session.PlayerID, Level: 1, Calls: &calls}
	if err := session.Put(entity); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("queued Put deep snapshot calls=%d want=1", got)
	}
	if session.DirtyCount() != 0 || buffer.queueSize() != 1 {
		t.Fatalf("ownership transfer dirty=%d queued=%d", session.DirtyCount(), buffer.queueSize())
	}
}

func BenchmarkSnapshotEntityPlayerShape(b *testing.B) {
	backing := []int{1, 2, 3, 4}
	entity := &sessionSnapshotTestEntity{
		PlayerID: "benchmark",
		Level:    42,
		Values: map[string][]int{
			"items": backing,
			"alias": backing,
		},
		Runtime: map[string]any{"enabled": true},
	}
	entity.Self = entity
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := SnapshotEntity(entity); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriteBufferEnqueueSnapshot(b *testing.B) {
	repo := NewBaseCrudRepository(nil)
	GetCrudManagerInstance().AutoInitEntity(&sessionSnapshotTestEntity{})
	buffer := newWriteBufferForGeneration(repo, "")
	entity := &sessionSnapshotTestEntity{
		PlayerID: "benchmark-buffer",
		Level:    42,
		Values:   map[string][]int{"items": {1, 2, 3, 4}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		entity.Level = index
		if _, err := buffer.Enqueue(entity); err != nil {
			b.Fatal(err)
		}
	}
}
