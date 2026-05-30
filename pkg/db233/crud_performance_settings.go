package db233

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// CrudPerformanceSettings 游戏高频读写性能配置（支持外部加载与运行时热更新）。
type CrudPerformanceSettings struct {
	// FindByIdsChunkSize IN 查询分块大小。
	FindByIdsChunkSize int `json:"findByIdsChunkSize"`

	// BatchUpsertChunkSize SaveBatchUpsert 分块大小。
	BatchUpsertChunkSize int `json:"batchUpsertChunkSize"`

	// BatchInsertChunkSize SaveBatch 分块大小。
	BatchInsertChunkSize int `json:"batchInsertChunkSize"`

	// ConcurrentMaxWorkers FindByIdConcurrent 最大并发协程数。
	ConcurrentMaxWorkers int `json:"concurrentMaxWorkers"`

	// EnableConcurrentFind 是否启用并发多表查询。
	EnableConcurrentFind bool `json:"enableConcurrentFind"`

	// EnableSqlTemplateCache 是否缓存 FindById 等 SQL 模板。
	EnableSqlTemplateCache bool `json:"enableSqlTemplateCache"`

	// EnablePreparedStmtCache 是否缓存 *sql.Stmt 预编译语句。
	EnablePreparedStmtCache bool `json:"enablePreparedStmtCache"`

	// EnableFastOrmScan 是否启用元数据直扫 ORM（跳过 map/any 中转，降低 GC）。
	EnableFastOrmScan bool `json:"enableFastOrmScan"`

	// EnableRowMapPool 是否复用 Scan 中间缓冲（Query 路径）。
	EnableRowMapPool bool `json:"enableRowMapPool"`

	// EnableAllocPool 内部对象池（字段 map / 批量写 scratch / IN 占位符 / Builder / JSON Buffer）。
	EnableAllocPool bool `json:"enableAllocPool"`

	// EnableColdStartWarmup InitGameDb 时预热连接池/元数据/Stmt/扫描计划。
	EnableColdStartWarmup bool `json:"enableColdStartWarmup"`

	// PoolWarmupRounds 连接池预热 Ping 轮数（0=按 maxIdle 推断）。
	PoolWarmupRounds int `json:"poolWarmupRounds"`

	// StmtCacheSize 预编译语句缓存上限（按 DB+SQL）。
	StmtCacheSize int `json:"stmtCacheSize"`

	// StmtCacheTTLSeconds 预编译语句 TTL（秒）。
	StmtCacheTTLSeconds int `json:"stmtCacheTTLSeconds"`

	// WriteBufferEnabled 是否启用异步写缓冲（高频写场景）。
	WriteBufferEnabled bool `json:"writeBufferEnabled"`

	// WriteBufferFlushIntervalMs 写缓冲刷盘间隔（毫秒）。
	WriteBufferFlushIntervalMs int `json:"writeBufferFlushIntervalMs"`

	// WriteBufferMaxBatchSize 单次刷盘最大实体数。
	WriteBufferMaxBatchSize int `json:"writeBufferMaxBatchSize"`

	// WriteBufferMaxQueueSize 写缓冲队列上限（超出时同步写入）。
	WriteBufferMaxQueueSize int `json:"writeBufferMaxQueueSize"`

	// MaxOpenConns 连接池最大打开连接数。
	MaxOpenConns int `json:"maxOpenConns"`

	// MaxIdleConns 连接池最大空闲连接数。
	MaxIdleConns int `json:"maxIdleConns"`

	// ConnMaxLifetimeSec 连接最大生命周期（秒）。
	ConnMaxLifetimeSec int `json:"connMaxLifetimeSec"`

	// ConnMaxIdleTimeSec 连接最大空闲时间（秒）。
	ConnMaxIdleTimeSec int `json:"connMaxIdleTimeSec"`

	// EnableLocalJournal 是否启用本地 WAL。
	EnableLocalJournal bool `json:"enableLocalJournal"`

	// LocalJournalPath 本地 WAL 目录。
	LocalJournalPath string `json:"localJournalPath"`
}

// DefaultCrudPerformanceSettings 返回默认性能配置。
func DefaultCrudPerformanceSettings() CrudPerformanceSettings {
	return CrudPerformanceSettings{
		FindByIdsChunkSize:         500,
		BatchUpsertChunkSize:       200,
		BatchInsertChunkSize:       200,
		ConcurrentMaxWorkers:       10,
		EnableConcurrentFind:       true,
		EnableSqlTemplateCache:     true,
		EnablePreparedStmtCache:    true,
		EnableFastOrmScan:          true,
		EnableRowMapPool:           true,
		EnableAllocPool:            true,
		EnableColdStartWarmup:      true,
		PoolWarmupRounds:           0,
		StmtCacheSize:              256,
		StmtCacheTTLSeconds:        600,
		WriteBufferEnabled:         false,
		WriteBufferFlushIntervalMs: 100,
		WriteBufferMaxBatchSize:    100,
		WriteBufferMaxQueueSize:    10000,
		MaxOpenConns:               100,
		MaxIdleConns:               20,
		ConnMaxLifetimeSec:         3600,
		ConnMaxIdleTimeSec:         600,
		EnableLocalJournal:         true,
		LocalJournalPath:           ".db233_journal",
	}
}

// CrudPerformanceSettingsManager 全局性能配置管理器（线程安全、支持热更新）。
type CrudPerformanceSettingsManager struct {
	mu       sync.RWMutex
	settings CrudPerformanceSettings
	onChange []func(CrudPerformanceSettings)
}

var (
	performanceSettingsInstance *CrudPerformanceSettingsManager
	performanceSettingsOnce     sync.Once
)

// GetCrudPerformanceSettings 获取性能配置管理器单例。
func GetCrudPerformanceSettings() *CrudPerformanceSettingsManager {
	performanceSettingsOnce.Do(func() {
		performanceSettingsInstance = &CrudPerformanceSettingsManager{
			settings: DefaultCrudPerformanceSettings(),
		}
	})
	return performanceSettingsInstance
}

// Snapshot 获取当前配置快照（读路径无锁竞争外的安全拷贝）。
func (m *CrudPerformanceSettingsManager) Snapshot() CrudPerformanceSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

// Apply 部分热更新（仅覆盖 patch 中非零整型字段；布尔项请用 Set 或 LoadFromJSON）。
func (m *CrudPerformanceSettingsManager) Apply(patch CrudPerformanceSettings) {
	m.mu.Lock()
	merged := mergePerformanceSettingsInts(m.settings, patch)
	m.settings = normalizePerformanceSettings(merged)
	snapshot := m.settings
	callbacks := append([]func(CrudPerformanceSettings){}, m.onChange...)
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb(snapshot)
	}
	LogInfo("性能配置已热更新: concurrentWorkers=%d, findByIdsChunk=%d, writeBuffer=%v",
		snapshot.ConcurrentMaxWorkers, snapshot.FindByIdsChunkSize, snapshot.WriteBufferEnabled)
}

// ApplyFull 全量覆盖配置（LoadFromJSON 使用）。
func (m *CrudPerformanceSettingsManager) ApplyFull(settings CrudPerformanceSettings) {
	m.mu.Lock()
	m.settings = normalizePerformanceSettings(settings)
	snapshot := m.settings
	callbacks := append([]func(CrudPerformanceSettings){}, m.onChange...)
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb(snapshot)
	}
	LogInfo("性能配置已全量加载: concurrentWorkers=%d, findByIdsChunk=%d, writeBuffer=%v",
		snapshot.ConcurrentMaxWorkers, snapshot.FindByIdsChunkSize, snapshot.WriteBufferEnabled)
}

// Set 按 key 动态修改单项配置。
func (m *CrudPerformanceSettingsManager) Set(key string, value any) error {
	m.mu.Lock()
	if err := applyKeyValueToPatch(&m.settings, key, value); err != nil {
		m.mu.Unlock()
		return err
	}
	m.settings = normalizePerformanceSettings(m.settings)
	snapshot := m.settings
	callbacks := append([]func(CrudPerformanceSettings){}, m.onChange...)
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb(snapshot)
	}
	return nil
}

// OnChange 注册配置变更回调（如重启写缓冲、调整连接池等）。
func (m *CrudPerformanceSettingsManager) OnChange(fn func(CrudPerformanceSettings)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = append(m.onChange, fn)
}

// LoadFromFile 从 JSON 文件加载性能配置。
// 支持顶层对象或 {"performance": {...}} 嵌套结构。
func (m *CrudPerformanceSettingsManager) LoadFromFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("读取性能配置文件失败: %w", err)
	}
	return m.LoadFromJSON(data)
}

// LoadFromJSON 从 JSON 字节加载性能配置。
func (m *CrudPerformanceSettingsManager) LoadFromJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("解析性能配置 JSON 失败: %w", err)
	}

	payload := data
	if section, ok := raw["performance"]; ok {
		payload = section
	} else if section, ok := raw["db233"]; ok {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(section, &nested); err == nil {
			if perf, ok := nested["performance"]; ok {
				payload = perf
			}
		}
	}

	var patch CrudPerformanceSettings
	if err := json.Unmarshal(payload, &patch); err != nil {
		return fmt.Errorf("解析 performance 节点失败: %w", err)
	}
	m.ApplyFull(patch)
	return nil
}

// LoadFromConfigManager 从 ConfigManager 加载扁平 key（如 performance.findByIdsChunkSize）。
func (m *CrudPerformanceSettingsManager) LoadFromConfigManager(prefix string) {
	if prefix == "" {
		prefix = "performance"
	}
	cm := GetConfigManager()
	all := cm.GetAll()

	m.mu.Lock()
	changed := false
	for key, value := range all {
		if !strings.HasPrefix(key, prefix+".") {
			continue
		}
		fieldKey := strings.TrimPrefix(key, prefix+".")
		if fieldKey == "" {
			continue
		}
		if err := applyKeyValueToPatch(&m.settings, fieldKey, value); err != nil {
			LogWarn("跳过无效性能配置项: key=%s, err=%v", key, err)
			continue
		}
		changed = true
	}
	if !changed {
		m.mu.Unlock()
		return
	}
	m.settings = normalizePerformanceSettings(m.settings)
	snapshot := m.settings
	callbacks := append([]func(CrudPerformanceSettings){}, m.onChange...)
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb(snapshot)
	}
}

// ToConcurrentCrudConfig 转为并发查询配置（nil config 参数时使用）。
func (s CrudPerformanceSettings) ToConcurrentCrudConfig() *ConcurrentCrudConfig {
	return &ConcurrentCrudConfig{
		MaxConcurrency:   s.ConcurrentMaxWorkers,
		EnableConcurrent: s.EnableConcurrentFind,
	}
}

func mergePerformanceSettingsInts(base, patch CrudPerformanceSettings) CrudPerformanceSettings {
	if patch.FindByIdsChunkSize > 0 {
		base.FindByIdsChunkSize = patch.FindByIdsChunkSize
	}
	if patch.BatchUpsertChunkSize > 0 {
		base.BatchUpsertChunkSize = patch.BatchUpsertChunkSize
	}
	if patch.BatchInsertChunkSize > 0 {
		base.BatchInsertChunkSize = patch.BatchInsertChunkSize
	}
	if patch.ConcurrentMaxWorkers > 0 {
		base.ConcurrentMaxWorkers = patch.ConcurrentMaxWorkers
	}
	if patch.WriteBufferFlushIntervalMs > 0 {
		base.WriteBufferFlushIntervalMs = patch.WriteBufferFlushIntervalMs
	}
	if patch.WriteBufferMaxBatchSize > 0 {
		base.WriteBufferMaxBatchSize = patch.WriteBufferMaxBatchSize
	}
	if patch.WriteBufferMaxQueueSize > 0 {
		base.WriteBufferMaxQueueSize = patch.WriteBufferMaxQueueSize
	}
	if patch.MaxOpenConns > 0 {
		base.MaxOpenConns = patch.MaxOpenConns
	}
	if patch.MaxIdleConns > 0 {
		base.MaxIdleConns = patch.MaxIdleConns
	}
	if patch.ConnMaxLifetimeSec > 0 {
		base.ConnMaxLifetimeSec = patch.ConnMaxLifetimeSec
	}
	if patch.ConnMaxIdleTimeSec > 0 {
		base.ConnMaxIdleTimeSec = patch.ConnMaxIdleTimeSec
	}
	if patch.LocalJournalPath != "" {
		base.LocalJournalPath = patch.LocalJournalPath
	}
	if patch.PoolWarmupRounds > 0 {
		base.PoolWarmupRounds = patch.PoolWarmupRounds
	}
	if patch.StmtCacheSize > 0 {
		base.StmtCacheSize = patch.StmtCacheSize
	}
	if patch.StmtCacheTTLSeconds > 0 {
		base.StmtCacheTTLSeconds = patch.StmtCacheTTLSeconds
	}
	return base
}

func normalizePerformanceSettings(s CrudPerformanceSettings) CrudPerformanceSettings {
	def := DefaultCrudPerformanceSettings()
	if s.FindByIdsChunkSize <= 0 {
		s.FindByIdsChunkSize = def.FindByIdsChunkSize
	}
	if s.BatchUpsertChunkSize <= 0 {
		s.BatchUpsertChunkSize = def.BatchUpsertChunkSize
	}
	if s.BatchInsertChunkSize <= 0 {
		s.BatchInsertChunkSize = def.BatchInsertChunkSize
	}
	if s.ConcurrentMaxWorkers <= 0 {
		s.ConcurrentMaxWorkers = def.ConcurrentMaxWorkers
	}
	if s.WriteBufferFlushIntervalMs <= 0 {
		s.WriteBufferFlushIntervalMs = def.WriteBufferFlushIntervalMs
	}
	if s.WriteBufferMaxBatchSize <= 0 {
		s.WriteBufferMaxBatchSize = def.WriteBufferMaxBatchSize
	}
	if s.WriteBufferMaxQueueSize <= 0 {
		s.WriteBufferMaxQueueSize = def.WriteBufferMaxQueueSize
	}
	if s.StmtCacheSize <= 0 {
		s.StmtCacheSize = def.StmtCacheSize
	}
	if s.StmtCacheTTLSeconds <= 0 {
		s.StmtCacheTTLSeconds = def.StmtCacheTTLSeconds
	}
	return s
}

func applyKeyValueToPatch(patch *CrudPerformanceSettings, key string, value any) error {
	switch key {
	case "findByIdsChunkSize":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.FindByIdsChunkSize = v
	case "batchUpsertChunkSize":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.BatchUpsertChunkSize = v
	case "batchInsertChunkSize":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.BatchInsertChunkSize = v
	case "concurrentMaxWorkers":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.ConcurrentMaxWorkers = v
	case "enableConcurrentFind":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableConcurrentFind = v
	case "enableSqlTemplateCache":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableSqlTemplateCache = v
	case "enablePreparedStmtCache":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnablePreparedStmtCache = v
	case "enableFastOrmScan":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableFastOrmScan = v
	case "enableRowMapPool":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableRowMapPool = v
	case "enableAllocPool":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableAllocPool = v
	case "enableColdStartWarmup":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableColdStartWarmup = v
	case "poolWarmupRounds":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.PoolWarmupRounds = v
	case "stmtCacheSize":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.StmtCacheSize = v
	case "stmtCacheTTLSeconds":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.StmtCacheTTLSeconds = v
	case "writeBufferEnabled":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.WriteBufferEnabled = v
	case "writeBufferFlushIntervalMs":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.WriteBufferFlushIntervalMs = v
	case "writeBufferMaxBatchSize":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.WriteBufferMaxBatchSize = v
	case "writeBufferMaxQueueSize":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.WriteBufferMaxQueueSize = v
	case "maxOpenConns":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.MaxOpenConns = v
	case "maxIdleConns":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.MaxIdleConns = v
	case "connMaxLifetimeSec":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.ConnMaxLifetimeSec = v
	case "connMaxIdleTimeSec":
		v, err := toInt(value)
		if err != nil {
			return err
		}
		patch.ConnMaxIdleTimeSec = v
	case "enableLocalJournal":
		v, err := toBool(value)
		if err != nil {
			return err
		}
		patch.EnableLocalJournal = v
	case "localJournalPath":
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("localJournalPath 必须为 string")
		}
		patch.LocalJournalPath = v
	default:
		return fmt.Errorf("未知性能配置项: %s", key)
	}
	return nil
}

func toInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case json.Number:
		i, err := v.Int64()
		return int(i), err
	case string:
		var i int
		_, err := fmt.Sscan(v, &i)
		return i, err
	default:
		return 0, fmt.Errorf("无法转为 int: %T", value)
	}
}

func toBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		default:
			return false, fmt.Errorf("无法转为 bool: %s", v)
		}
	default:
		return false, fmt.Errorf("无法转为 bool: %T", value)
	}
}
