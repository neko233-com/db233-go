package db233

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// EntityCacheEvictionLRU 默认 LRU 淘汰策略。
const EntityCacheEvictionLRU = "lru"

// EntityCacheSettings 有状态游戏服 Session 实体缓存配置。
type EntityCacheSettings struct {
	// Enabled 是否启用 Session 实体缓存（读走内存，写延迟落库）。
	Enabled bool `json:"enabled"`

	// EvictionPolicy 淘汰策略，目前仅支持 lru。
	EvictionPolicy string `json:"evictionPolicy"`

	// MaxSessions 全局最大 Session（玩家）数量，超出 LRU 淘汰。
	MaxSessions int `json:"maxSessions"`

	// SessionFlushIntervalMs 定时刷写 dirty 到 DB 的间隔（毫秒）。
	// 默认 60000（1 分钟）；0 表示关闭定时刷写，仅退出/FlushAll 时落库。
	SessionFlushIntervalMs int `json:"sessionFlushIntervalMs"`

	// SessionFlushIntervalJitterPct 定时刷写间隔抖动百分比（0–100），避免整点齐刷。默认 10。
	SessionFlushIntervalJitterPct int `json:"sessionFlushIntervalJitterPct"`

	// SessionFlushMaxWorkers 刷盘最大并发写库数（CloseSession / 定时刷写 / 合并刷）。默认 8。
	SessionFlushMaxWorkers int `json:"sessionFlushMaxWorkers"`

	// SessionFlushMergeByTable 定时刷写是否跨 Session 按表合并 UPSERT。默认 true。
	SessionFlushMergeByTable bool `json:"sessionFlushMergeByTable"`

	// ShutdownFlushMaxWorkers 关服 FlushAll 最大并发写库数。默认 8；可略调高以加快关服。
	ShutdownFlushMaxWorkers int `json:"shutdownFlushMaxWorkers"`

	// ShutdownFlushWaveIntervalMs 关服刷盘波次间隔（毫秒），波间 sleep 削峰 DB。默认 20；0 表示无间隔。
	ShutdownFlushWaveIntervalMs int `json:"shutdownFlushWaveIntervalMs"`

	// FlushOnEvict LRU 淘汰 Session 前是否先刷写 dirty 到 DB。
	FlushOnEvict bool `json:"flushOnEvict"`

	// EntityTypeLimits 各实体类型在缓存中的最大实例数（跨 Session 统计）。
	// key 为 struct 名，如 PlayerBagEntity。
	EntityTypeLimits map[string]int `json:"entityTypeLimits"`

	// NegativeCacheEnabled 负缓存：登录/GetOrLoad 确认「无记录」后不再 SELECT。
	// 默认 false；可通过 Set("negativeCacheEnabled", true) 或 Session 级动态开关启用。
	NegativeCacheEnabled bool `json:"negativeCacheEnabled"`
}

// DefaultEntityCacheSettings 默认实体缓存配置。
func DefaultEntityCacheSettings() EntityCacheSettings {
	return EntityCacheSettings{
		Enabled:                     true,
		EvictionPolicy:              EntityCacheEvictionLRU,
		MaxSessions:                 10000,
		SessionFlushIntervalMs:      60000,
		SessionFlushIntervalJitterPct: 10,
		SessionFlushMaxWorkers:        8,
		SessionFlushMergeByTable:      true,
		ShutdownFlushMaxWorkers:       8,
		ShutdownFlushWaveIntervalMs:   20,
		FlushOnEvict:                true,
		EntityTypeLimits:            make(map[string]int),
		NegativeCacheEnabled:        false,
	}
}

// EntityCacheSettingsManager 实体缓存配置管理器（支持热更新 SessionFlushIntervalMs 等）。
type EntityCacheSettingsManager struct {
	mu       sync.RWMutex
	settings EntityCacheSettings
	onChange []func(EntityCacheSettings)
	cache    atomic.Value // EntityCacheSettings — 无锁热路径读
}

var (
	entityCacheSettingsInstance *EntityCacheSettingsManager
	entityCacheSettingsOnce     sync.Once
)

// GetEntityCacheSettings 获取实体缓存配置单例。
func GetEntityCacheSettings() *EntityCacheSettingsManager {
	entityCacheSettingsOnce.Do(func() {
		mgr := &EntityCacheSettingsManager{}
		mgr.settings = DefaultEntityCacheSettings()
		mgr.publishCache()
		entityCacheSettingsInstance = mgr
	})
	return entityCacheSettingsInstance
}

// Snapshot 获取配置快照（无锁读，适合 Session 热路径）。
func (m *EntityCacheSettingsManager) Snapshot() EntityCacheSettings {
	if v := m.cache.Load(); v != nil {
		return v.(EntityCacheSettings)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

func (m *EntityCacheSettingsManager) publishCache() {
	m.cache.Store(m.settings)
}

// ApplyFull 全量覆盖配置。
func (m *EntityCacheSettingsManager) ApplyFull(settings EntityCacheSettings) {
	m.mu.Lock()
	m.settings = normalizeEntityCacheSettings(settings)
	m.publishCache()
	snapshot := m.settings
	callbacks := append([]func(EntityCacheSettings){}, m.onChange...)
	m.mu.Unlock()
	for _, cb := range callbacks {
		cb(snapshot)
	}
}

// Set 动态修改单项配置。
func (m *EntityCacheSettingsManager) Set(key string, value any) error {
	m.mu.Lock()
	if err := applyEntityCacheKeyValue(&m.settings, key, value); err != nil {
		m.mu.Unlock()
		return err
	}
	m.settings = normalizeEntityCacheSettings(m.settings)
	m.publishCache()
	snapshot := m.settings
	callbacks := append([]func(EntityCacheSettings){}, m.onChange...)
	m.mu.Unlock()
	for _, cb := range callbacks {
		cb(snapshot)
	}
	return nil
}

// OnChange 注册配置变更回调（如调整定时刷写间隔）。
func (m *EntityCacheSettingsManager) OnChange(fn func(EntityCacheSettings)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = append(m.onChange, fn)
}

// LoadFromJSON 从 JSON 加载 entityCache 节点（可与 performance 配置同文件）。
func (m *EntityCacheSettingsManager) LoadFromJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("解析 entityCache JSON 失败: %w", err)
	}
	payload, ok := raw["entityCache"]
	if !ok {
		return nil
	}
	var settings EntityCacheSettings
	settings = DefaultEntityCacheSettings()
	if err := json.Unmarshal(payload, &settings); err != nil {
		return fmt.Errorf("解析 entityCache 节点失败: %w", err)
	}
	m.ApplyFull(settings)
	return nil
}

// IsDeferredWrite 是否延迟写库（缓存开启且不在 Put 时立即刷写）。
func (s EntityCacheSettings) IsDeferredWrite() bool {
	return s.Enabled
}

// IsNegativeCacheEnabled 是否启用负缓存（全局配置）。
func (s EntityCacheSettings) IsNegativeCacheEnabled() bool {
	return s.NegativeCacheEnabled
}

func normalizeEntityCacheSettings(s EntityCacheSettings) EntityCacheSettings {
	def := DefaultEntityCacheSettings()
	if s.EvictionPolicy == "" {
		s.EvictionPolicy = def.EvictionPolicy
	}
	if s.MaxSessions <= 0 {
		s.MaxSessions = def.MaxSessions
	}
	if s.EntityTypeLimits == nil {
		s.EntityTypeLimits = make(map[string]int)
	}
	if s.SessionFlushIntervalJitterPct < 0 {
		s.SessionFlushIntervalJitterPct = def.SessionFlushIntervalJitterPct
	} else if s.SessionFlushIntervalJitterPct == 0 && s.SessionFlushIntervalMs > 0 {
		s.SessionFlushIntervalJitterPct = def.SessionFlushIntervalJitterPct
	}
	if s.SessionFlushMaxWorkers <= 0 {
		s.SessionFlushMaxWorkers = def.SessionFlushMaxWorkers
	}
	if s.ShutdownFlushMaxWorkers <= 0 {
		s.ShutdownFlushMaxWorkers = def.ShutdownFlushMaxWorkers
	}
	if s.ShutdownFlushWaveIntervalMs < 0 {
		s.ShutdownFlushWaveIntervalMs = def.ShutdownFlushWaveIntervalMs
	}
	return s
}

func applyEntityCacheKeyValue(settings *EntityCacheSettings, key string, value any) error {
	switch key {
	case "enabled":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		settings.Enabled = v
	case "evictionPolicy":
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("evictionPolicy 必须为 string")
		}
		settings.EvictionPolicy = strings.ToLower(v)
	case "maxSessions":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		settings.MaxSessions = v
	case "sessionFlushIntervalMs":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		settings.SessionFlushIntervalMs = v
	case "sessionFlushIntervalJitterPct":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		settings.SessionFlushIntervalJitterPct = v
	case "sessionFlushMaxWorkers":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		settings.SessionFlushMaxWorkers = v
	case "sessionFlushMergeByTable":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		settings.SessionFlushMergeByTable = v
	case "shutdownFlushMaxWorkers":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		settings.ShutdownFlushMaxWorkers = v
	case "shutdownFlushWaveIntervalMs":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		settings.ShutdownFlushWaveIntervalMs = v
	case "flushOnEvict":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		settings.FlushOnEvict = v
	case "negativeCacheEnabled":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		settings.NegativeCacheEnabled = v
	default:
		if strings.HasPrefix(key, "entityTypeLimits.") {
			typeName := strings.TrimPrefix(key, "entityTypeLimits.")
			v, err := toInt(value)
			if err != nil {
				return err
			}
			if settings.EntityTypeLimits == nil {
				settings.EntityTypeLimits = make(map[string]int)
			}
			settings.EntityTypeLimits[typeName] = v
			return nil
		}
		return fmt.Errorf("未知 entityCache 配置项: %s", key)
	}
	return nil
}
