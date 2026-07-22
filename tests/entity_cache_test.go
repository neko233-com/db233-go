package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestEntityCacheSettings_LoadFromJSON(t *testing.T) {
	SaveEntityCacheSettings(t)
	mgr := db233.GetEntityCacheSettings()
	jsonData := []byte(`{
		"entityCache": {
			"enabled": true,
			"evictionPolicy": "lru",
			"maxSessions": 5000,
			"sessionFlushIntervalMs": 60000,
			"flushOnEvict": true,
			"negativeCacheEnabled": false,
			"entityTypeLimits": {
				"TestBatchFindEntity": 5000,
				"TestPlayerBagEntity": 3000
			}
		}
	}`)
	if err := mgr.LoadFromJSON(jsonData); err != nil {
		t.Fatalf("LoadFromJSON 失败: %v", err)
	}
	s := mgr.Snapshot()
	if !s.Enabled || s.MaxSessions != 5000 || s.SessionFlushIntervalMs != 60000 {
		t.Fatalf("配置不正确: %+v", s)
	}
	if s.EntityTypeLimits["TestPlayerBagEntity"] != 3000 {
		t.Errorf("entityTypeLimits 未加载")
	}
	if s.NegativeCacheEnabled {
		t.Error("JSON 未指定时 negativeCacheEnabled 应为 false")
	}
}

func TestCacheableEntityRegistry(t *testing.T) {
	SaveCacheableEntityRegistry(t)
	reg := db233.GetCacheableEntityRegistry()
	reg.Clear()
	reg.Register(db233.CacheableEntitySpec{
		Prototype:    &TestBatchFindEntity{},
		MaxInstances: 100,
	})
	if !reg.IsCacheable(&TestBatchFindEntity{}) {
		t.Error("应可缓存")
	}
	if reg.IsCacheable(&TestPlayerBagEntity{}) {
		t.Error("未注册应不可缓存")
	}
	if reg.MaxInstances("TestBatchFindEntity") != 100 {
		t.Errorf("MaxInstances 期望 100")
	}
}

func TestSessionLRU_Eviction(t *testing.T) {
	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	sr := NewEmptyBatchFindSessionRepository(t, "p1", "p2", "p3")

	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                true,
		EvictionPolicy:         db233.EntityCacheEvictionLRU,
		MaxSessions:            2,
		SessionFlushIntervalMs: 0,
		FlushOnEvict:           false,
	})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	_, _ = sr.OpenSession("p1", []db233.IDbEntity{&TestBatchFindEntity{}})
	_, _ = sr.OpenSession("p2", []db233.IDbEntity{&TestBatchFindEntity{}})
	_, _ = sr.OpenSession("p3", []db233.IDbEntity{&TestBatchFindEntity{}})

	if sr.OnlineCount() != 2 {
		t.Fatalf("LRU 应淘汰 1 个 Session，在线数期望 2，得到 %d", sr.OnlineCount())
	}
	if sr.GetSession("p1") != nil {
		t.Error("最久未访问的 p1 应被淘汰")
	}
	if sr.GetSession("p3") == nil {
		t.Error("p3 应在线")
	}
}

func TestPlayerSession_DeferredWrite(t *testing.T) {
	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                true,
		SessionFlushIntervalMs: 0,
	})
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})

	sr := NewEmptyBatchFindSessionRepository(t, "defer1")

	session, err := sr.OpenSession("defer1", []db233.IDbEntity{&TestBatchFindEntity{}})
	if err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}

	entity := &TestBatchFindEntity{PlayerID: "defer1", Name: "test", Level: 5}
	if err := session.Put(entity); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if session.DirtyCount() != 1 {
		t.Error("应标记 dirty")
	}
}

func TestPlayerSession_NonCacheableRejected(t *testing.T) {
	SaveCacheableEntityRegistry(t)
	db233.GetCacheableEntityRegistry().Clear()
	db233.GetCacheableEntityRegistry().Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}})
	sr := NewEmptyBatchFindSessionRepository(t, "x")

	session, openErr := sr.OpenSession("x", []db233.IDbEntity{&TestBatchFindEntity{}})
	if openErr != nil {
		t.Fatalf("OpenSession 失败: %v", openErr)
	}
	err := session.Put(&TestPlayerBagEntity{PlayerID: "x", Gold: 1})
	if err == nil {
		t.Error("未注册可缓存类型应拒绝 Put")
	}
}

func TestEntityCacheCount(t *testing.T) {
	SaveCacheableEntityRegistry(t)
	reg := db233.GetCacheableEntityRegistry()
	reg.Register(db233.CacheableEntitySpec{Prototype: &TestBatchFindEntity{}, MaxInstances: 10})

	sr := NewEmptyBatchFindSessionRepository(t, "c1")

	if _, err := sr.OpenSession("c1", []db233.IDbEntity{&TestBatchFindEntity{}}); err != nil {
		t.Fatalf("OpenSession 失败: %v", err)
	}
	if sr.EntityCacheCount("TestBatchFindEntity") != 0 {
		t.Fatalf("Load 无 DB 无数据时缓存计数必须为 0，实际为 %d", sr.EntityCacheCount("TestBatchFindEntity"))
	}
}

func TestEntityCacheConfigFile(t *testing.T) {
	SaveEntityCacheSettings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "perf.json")
	content := `{
		"concurrentMaxWorkers": 8,
		"entityCache": {
			"enabled": true,
			"maxSessions": 100,
			"sessionFlushIntervalMs": 30000
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if err := db233.GetEntityCacheSettings().LoadFromJSON(data); err != nil {
		t.Fatal(err)
	}
	if db233.GetEntityCacheSettings().Snapshot().SessionFlushIntervalMs != 30000 {
		t.Error("应从同文件加载 entityCache")
	}
}

func TestSessionFlushIntervalDynamic(t *testing.T) {
	SaveEntityCacheSettings(t)
	db233.GetEntityCacheSettings().ApplyFull(db233.EntityCacheSettings{
		Enabled:                true,
		SessionFlushIntervalMs: int(time.Minute / time.Millisecond),
	})
	sr := db233.NewSessionRepository(db233.NewBaseCrudRepository(nil))
	defer sr.Stop()
	sr.SetFlushInterval(0)
	if db233.GetEntityCacheSettings().Snapshot().SessionFlushIntervalMs != 0 {
		t.Error("动态设置刷写间隔失败")
	}
}

func TestInitGameDb_ReturnsSessionRepo(t *testing.T) {
	SaveEntityCacheSettings(t)
	SaveCacheableEntityRegistry(t)
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()
	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("创建 InitGameDb 测试表失败: %v", err)
	}
	defer func() {
		if _, err := db.DataSource.Exec("DROP TABLE IF EXISTS test_batch_find"); err != nil {
			t.Errorf("清理 InitGameDb 测试表失败: %v", err)
		}
	}()

	opts := db233.DefaultGameDbOptions()
	opts.EnableLocalJournal = false
	opts.EnableWriteBuffer = false
	opts.EntityTypes = []db233.IDbEntity{&TestBatchFindEntity{}}
	opts.CacheableEntities = []db233.CacheableEntitySpec{
		{Prototype: &TestBatchFindEntity{}, MaxInstances: 1000},
	}

	sr, err := db233.InitGameDb(db, nil, opts)
	if err != nil {
		t.Fatalf("InitGameDb 失败: %v", err)
	}
	if sr == nil || db.SessionRepo == nil {
		t.Fatal("应返回 SessionRepository")
	}
	sr.Stop()
}

func TestEntityCacheSettings_SnapshotIsIndependent(t *testing.T) {
	SaveEntityCacheSettings(t)
	manager := db233.GetEntityCacheSettings()
	inputLimits := map[string]int{"HeroEntity": 10}
	settings := db233.DefaultEntityCacheSettings()
	settings.EntityTypeLimits = inputLimits
	manager.ApplyFull(settings)

	inputLimits["HeroEntity"] = 99
	snapshot := manager.Snapshot()
	snapshot.EntityTypeLimits["HeroEntity"] = 77
	if got := manager.Snapshot().EntityTypeLimits["HeroEntity"]; got != 10 {
		t.Fatalf("配置 map 被外部别名修改: got=%d want=10", got)
	}

	beforeSet := manager.Snapshot()
	if err := manager.Set("entityTypeLimits.HeroEntity", 20); err != nil {
		t.Fatal(err)
	}
	if got := beforeSet.EntityTypeLimits["HeroEntity"]; got != 10 {
		t.Fatalf("Set 原地修改旧快照: got=%d want=10", got)
	}
}
