package db233

import (
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"
)

func preserveEntityCacheSettings(t *testing.T) *EntityCacheSettingsManager {
	t.Helper()
	manager := GetEntityCacheSettings()
	saved := manager.Snapshot()
	t.Cleanup(func() { manager.ApplyFull(saved) })
	return manager
}

func preserveCacheableRegistry(t *testing.T) *CacheableEntityRegistry {
	t.Helper()
	registry := GetCacheableEntityRegistry()
	saved := registry.Snapshot()
	t.Cleanup(func() { registry.Restore(saved) })
	return registry
}

func entityCacheCallbackCount(manager *EntityCacheSettingsManager) int {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return len(manager.onChange)
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestSessionRepository_StopIdempotentConcurrentAndUnsubscribes(t *testing.T) {
	manager := preserveEntityCacheSettings(t)
	settings := DefaultEntityCacheSettings()
	settings.SessionFlushIntervalMs = int(time.Hour / time.Millisecond)
	manager.ApplyFull(settings)

	before := entityCacheCallbackCount(manager)
	repository := NewSessionRepository(NewBaseCrudRepository(nil))
	if got := entityCacheCallbackCount(manager); got != before+1 {
		t.Fatalf("listener count=%d want=%d", got, before+1)
	}

	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			repository.Stop()
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
		t.Fatal("concurrent Stop blocked")
	}
	if got := entityCacheCallbackCount(manager); got != before {
		t.Fatalf("Stop 未取消配置订阅: count=%d want=%d", got, before)
	}
}

func TestSessionRepository_FlushIntervalCanDisableAndReenable(t *testing.T) {
	manager := preserveEntityCacheSettings(t)
	settings := DefaultEntityCacheSettings()
	settings.Enabled = true
	settings.SessionFlushIntervalMs = 0
	settings.SessionFlushMergeByTable = true
	manager.ApplyFull(settings)

	repo := NewBaseCrudRepository(nil)
	recorder := newUpsertRecorder()
	repo.SetTestUpsertHook(recorder.hook)
	sessions := NewSessionRepository(repo)
	defer sessions.Stop()

	session := newPlayerSession("dynamic", repo, sessions)
	session.loaded = true
	sessions.sessions.Store(session.PlayerID, session)
	markDirty := func(level int) {
		session.mu.Lock()
		session.dirty["flush_test_entity"] = &flushTestEntity{PlayerID: session.PlayerID, Level: level}
		session.mu.Unlock()
	}

	markDirty(1)
	if err := manager.Set("sessionFlushIntervalMs", 10); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, time.Second, func() bool { return recorder.batchCount() == 1 })

	if err := manager.Set("sessionFlushIntervalMs", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	baseline := recorder.batchCount()
	markDirty(2)
	time.Sleep(50 * time.Millisecond)
	if got := recorder.batchCount(); got != baseline {
		t.Fatalf("interval=0 仍触发刷写: got=%d baseline=%d", got, baseline)
	}

	if err := manager.Set("sessionFlushIntervalMs", 10); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, time.Second, func() bool { return recorder.batchCount() == baseline+1 })
}

func TestSessionRepository_ConcurrentOpenCoalescesByPlayer(t *testing.T) {
	manager := preserveEntityCacheSettings(t)
	settings := DefaultEntityCacheSettings()
	settings.SessionFlushIntervalMs = 0
	manager.ApplyFull(settings)
	registry := preserveCacheableRegistry(t)
	registry.Register(CacheableEntitySpec{Prototype: &flushTestEntity{}})

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	state := newScriptedDBState(scriptedStep{
		kind:          "query",
		queryContains: "flush_test_entity",
		columns:       []string{"playerId", "name", "level"},
		rows:          [][]driver.Value{{"same-player", "hero", int64(7)}},
		driverEntered: entered,
		driverRelease: release,
	})
	datasource := openScriptedDB(t, state)
	repo := NewBaseCrudRepository(NewDb(datasource, 0, nil))
	sessions := NewSessionRepository(repo)
	defer sessions.Stop()

	const workers = 32
	results := make(chan *PlayerSession, workers)
	errorsCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			session, err := sessions.OpenSession("same-player", []IDbEntity{&flushTestEntity{}})
			results <- session
			errorsCh <- err
		}()
	}
	<-entered
	close(release)

	var first *PlayerSession
	for i := 0; i < workers; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		result := <-results
		if first == nil {
			first = result
		} else if result != first {
			t.Fatal("同一玩家并发登录返回了不同 Session")
		}
	}
	if got := state.countCalls("query"); got != 1 {
		t.Fatalf("同一玩家并发登录查询次数=%d want=1", got)
	}
}

func TestPlayerSession_ConcurrentPutTracksEntityOnce(t *testing.T) {
	registry := preserveCacheableRegistry(t)
	registry.Register(CacheableEntitySpec{Prototype: &flushTestEntity{}, MaxInstances: 1000})
	repository := &SessionRepository{entityCounts: make(map[string]int)}
	session := newPlayerSession("put-race", NewBaseCrudRepository(nil), repository)

	const workers = 64
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(level int) {
			defer wg.Done()
			errorsCh <- session.putCacheOnly(&flushTestEntity{PlayerID: session.PlayerID, Level: level})
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := repository.EntityCacheCount("flushTestEntity"); got != 1 {
		t.Fatalf("同表并发 Put 计数=%d want=1", got)
	}
	session.releaseEntityCounts()
	if got := repository.EntityCacheCount("flushTestEntity"); got != 0 {
		t.Fatalf("释放后计数=%d want=0", got)
	}
}

func TestSessionLRU_ShrinkAndAdmissionTrimToCapacity(t *testing.T) {
	lru := newSessionLRU(3)
	if evicted := lru.Add("a"); len(evicted) != 0 {
		t.Fatalf("unexpected eviction: %v", evicted)
	}
	lru.Add("b")
	lru.Add("c")
	evicted := lru.SetMaxSize(1)
	if len(evicted) != 2 || evicted[0] != "a" || evicted[1] != "b" {
		t.Fatalf("shrink eviction=%v want=[a b]", evicted)
	}
	if got := lru.Len(); got != 1 {
		t.Fatalf("len=%d want=1", got)
	}
	evicted = lru.Add("d")
	if len(evicted) != 1 || evicted[0] != "c" || lru.Len() != 1 {
		t.Fatalf("admission eviction=%v len=%d", evicted, lru.Len())
	}
}

func TestSessionLRUEvictionFlushFailureRollsBackAdmission(t *testing.T) {
	settings := preserveEntityCacheSettings(t)
	configured := DefaultEntityCacheSettings()
	configured.Enabled = true
	configured.MaxSessions = 1
	configured.FlushOnEvict = true
	configured.SessionFlushIntervalMs = 0
	settings.ApplyFull(configured)
	registry := preserveCacheableRegistry(t)
	registry.Register(CacheableEntitySpec{Prototype: &flushTestEntity{}})

	repo := setupFlushTestRepo(t)
	repo.SetTestUpsertHook(func([]IDbEntity) error { return errors.New("database unavailable") })
	sessions := NewSessionRepository(repo)
	defer func() {
		sessions.CloseAdmissionAndWait()
		sessions.Stop()
	}()
	oldSession, err := sessions.OpenSession("old", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldSession.Put(&flushTestEntity{PlayerID: "old", Level: 1}); err != nil {
		t.Fatal(err)
	}
	if opened, err := sessions.OpenSession("new", nil); err == nil || opened != nil {
		t.Fatalf("LRU flush failure admitted new session: opened=%v err=%v", opened, err)
	}
	if sessions.OnlineCount() != 1 || sessions.GetSession("old") != oldSession || sessions.GetSession("new") != nil {
		t.Fatalf("failed admission changed online set: count=%d", sessions.OnlineCount())
	}
	if oldSession.DirtyCount() != 1 || sessions.lru.Len() != 1 {
		t.Fatalf("old dirty/LRU not restored: dirty=%d lru=%d", oldSession.DirtyCount(), sessions.lru.Len())
	}
}
