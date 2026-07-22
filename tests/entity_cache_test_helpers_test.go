package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// SaveCacheableEntityRegistry 保存全局缓存白名单并在测试结束时恢复。
func SaveCacheableEntityRegistry(t *testing.T) db233.CacheableEntityRegistrySnapshot {
	t.Helper()
	registry := db233.GetCacheableEntityRegistry()
	saved := registry.Snapshot()
	t.Cleanup(func() {
		registry.Restore(saved)
	})
	return saved
}

// SaveCrudPerformanceSettings 保存性能配置并在测试结束时恢复。
func SaveCrudPerformanceSettings(t *testing.T) db233.CrudPerformanceSettings {
	t.Helper()
	manager := db233.GetCrudPerformanceSettings()
	saved := manager.Snapshot()
	t.Cleanup(func() {
		manager.ApplyFull(saved)
	})
	return saved
}

// NewEmptyBatchFindSessionRepository 创建带真实本地 MySQL 空记录的 SessionRepository。
// 严格错误传播下，Session 登录不再允许用 nil 数据源伪装“无记录”。
func NewEmptyBatchFindSessionRepository(t *testing.T, playerIDs ...string) *db233.SessionRepository {
	t.Helper()
	db := CreateTestDb(t)
	if db == nil {
		return nil
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setupBatchFindTable(db); err != nil {
		t.Fatalf("创建 Session 测试表失败: %v", err)
	}
	t.Cleanup(func() { _, _ = db.DataSource.Exec("DROP TABLE IF EXISTS test_batch_find") })
	for _, playerID := range playerIDs {
		if _, err := db.DataSource.Exec("DELETE FROM test_batch_find WHERE playerId = ?", playerID); err != nil {
			t.Fatalf("清理 Session 测试玩家 %s 失败: %v", playerID, err)
		}
	}
	sessionRepo := db233.NewSessionRepository(db233.NewBaseCrudRepository(db))
	t.Cleanup(sessionRepo.Stop)
	return sessionRepo
}
