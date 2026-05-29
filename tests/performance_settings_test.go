package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestCrudPerformanceSettings_LoadFromJSON(t *testing.T) {
	mgr := db233.GetCrudPerformanceSettings()
	mgr.ApplyFull(db233.DefaultCrudPerformanceSettings())

	jsonData := []byte(`{
		"performance": {
			"findByIdsChunkSize": 300,
			"concurrentMaxWorkers": 20,
			"writeBufferEnabled": true,
			"writeBufferFlushIntervalMs": 50
		}
	}`)
	if err := mgr.LoadFromJSON(jsonData); err != nil {
		t.Fatalf("LoadFromJSON 失败: %v", err)
	}

	s := mgr.Snapshot()
	if s.FindByIdsChunkSize != 300 {
		t.Errorf("findByIdsChunkSize 期望 300，得到 %d", s.FindByIdsChunkSize)
	}
	if s.ConcurrentMaxWorkers != 20 {
		t.Errorf("concurrentMaxWorkers 期望 20，得到 %d", s.ConcurrentMaxWorkers)
	}
	if !s.WriteBufferEnabled {
		t.Error("writeBufferEnabled 应为 true")
	}
	if s.WriteBufferFlushIntervalMs != 50 {
		t.Errorf("writeBufferFlushIntervalMs 期望 50，得到 %d", s.WriteBufferFlushIntervalMs)
	}
}

func TestCrudPerformanceSettings_DynamicSet(t *testing.T) {
	mgr := db233.GetCrudPerformanceSettings()
	mgr.ApplyFull(db233.DefaultCrudPerformanceSettings())

	if err := mgr.Set("concurrentMaxWorkers", 32); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	if mgr.Snapshot().ConcurrentMaxWorkers != 32 {
		t.Errorf("动态修改 concurrentMaxWorkers 失败")
	}

	if err := mgr.Set("enableConcurrentFind", false); err != nil {
		t.Fatalf("Set bool 失败: %v", err)
	}
	if mgr.Snapshot().EnableConcurrentFind {
		t.Error("enableConcurrentFind 应为 false")
	}
}

func TestCrudPerformanceSettings_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "performance.json")
	content := `{"findByIdsChunkSize": 128, "batchUpsertChunkSize": 64}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	mgr := db233.GetCrudPerformanceSettings()
	if err := mgr.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile 失败: %v", err)
	}
	s := mgr.Snapshot()
	if s.FindByIdsChunkSize != 128 || s.BatchUpsertChunkSize != 64 {
		t.Errorf("LoadFromFile 配置不正确: %+v", s)
	}
}

func TestCrudPerformanceSettings_LoadFromConfigManager(t *testing.T) {
	cm := db233.GetConfigManager()
	cm.Set("performance.concurrentMaxWorkers", 24)
	cm.Set("performance.writeBufferEnabled", true)

	mgr := db233.GetCrudPerformanceSettings()
	mgr.LoadFromConfigManager("performance")

	s := mgr.Snapshot()
	if s.ConcurrentMaxWorkers != 24 {
		t.Errorf("从 ConfigManager 加载 concurrentMaxWorkers 失败: %d", s.ConcurrentMaxWorkers)
	}
	if !s.WriteBufferEnabled {
		t.Error("从 ConfigManager 加载 writeBufferEnabled 失败")
	}
}

func TestCrudPerformanceSettings_ToConcurrentCrudConfig(t *testing.T) {
	s := db233.CrudPerformanceSettings{
		ConcurrentMaxWorkers: 16,
		EnableConcurrentFind: true,
	}
	cfg := s.ToConcurrentCrudConfig()
	if cfg.MaxConcurrency != 16 || !cfg.EnableConcurrent {
		t.Errorf("ToConcurrentCrudConfig 不正确: %+v", cfg)
	}
}

func TestSqlTemplateCache(t *testing.T) {
	cache := db233.GetSqlTemplateCache()
	cache.Clear()

	entity := &TestBatchFindEntity{}
	sql1 := cache.GetFindByIdSQL(entity, "test_batch_find", "playerId")
	sql2 := cache.GetFindByIdSQL(entity, "test_batch_find", "playerId")
	if sql1 != sql2 {
		t.Error("SQL 模板应被缓存")
	}
	if sql1 != "SELECT * FROM test_batch_find WHERE playerId = ?" {
		t.Errorf("SQL 模板不正确: %s", sql1)
	}
}
