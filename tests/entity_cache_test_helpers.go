package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

// SaveEntityCacheSettings 保存当前 entityCache 配置并在测试结束时还原（不污染其他用例）。
func SaveEntityCacheSettings(t *testing.T) db233.EntityCacheSettings {
	t.Helper()
	saved := db233.GetEntityCacheSettings().Snapshot()
	t.Cleanup(func() {
		db233.GetEntityCacheSettings().ApplyFull(saved)
	})
	return saved
}

// SetEntityCacheKey 动态改单项配置，测试结束自动还原（压测不 ApplyFull 覆盖）。
func SetEntityCacheKey(t *testing.T, key string, value any) {
	t.Helper()
	saved := db233.GetEntityCacheSettings().Snapshot()
	var old any
	switch key {
	case "enabled":
		old = saved.Enabled
	case "sessionFlushIntervalMs":
		old = saved.SessionFlushIntervalMs
	case "flushOnEvict":
		old = saved.FlushOnEvict
	case "negativeCacheEnabled":
		old = saved.NegativeCacheEnabled
	case "maxSessions":
		old = saved.MaxSessions
	default:
		t.Fatalf("SetEntityCacheKey 未支持的 key: %s", key)
	}
	if err := db233.GetEntityCacheSettings().Set(key, value); err != nil {
		t.Fatalf("Set(%s) 失败: %v", key, err)
	}
	t.Cleanup(func() {
		_ = db233.GetEntityCacheSettings().Set(key, old)
	})
}

// EnableSessionNegativeCache 仅对当前 Session 开负缓存（不改全局配置）。
func EnableSessionNegativeCache(session *db233.PlayerSession) {
	session.SetNegativeCacheEnabled(true)
}
