package db233

import (
	"database/sql"
	"fmt"
	"sync"
)

/**
 * DbGroup 数据库组 - Go 版
 *
 * 对应 Kotlin 版本的 DbGroup，用于管理同一配置下的多个数据库实例
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type DbGroup struct {
	DbGroupConfig            *DbGroupConfig
	GroupName                string
	CreateStrategy           DataSourceCreateStrategy
	ShardingDbStrategy       ShardingDbStrategy
	DatasourceConfigTemplate map[string]interface{}
	DbIdToConfigMap          map[int]*DbConfig
	DbMap                    map[int]*Db
	isInit                   bool
	mu                       sync.Mutex
}

/**
 * 创建 DbGroup
 *
 * @param config DbGroupConfig 配置
 * @return *DbGroup 实例
 * @return error 创建错误
 */
func NewDbGroup(config *DbGroupConfig) (*DbGroup, error) {
	if config.GroupName == "" {
		return nil, fmt.Errorf("groupName 不能为空")
	}
	dg := &DbGroup{
		DbGroupConfig:            config,
		GroupName:                config.GroupName,
		CreateStrategy:           config.DataSourceCreateStrategy,
		ShardingDbStrategy:       config.ShardingDbStrategy,
		DatasourceConfigTemplate: config.DatasourceConfigTemplate,
		DbIdToConfigMap:          make(map[int]*DbConfig),
		DbMap:                    make(map[int]*Db),
		isInit:                   false,
	}
	if dg.ShardingDbStrategy == nil {
		dg.ShardingDbStrategy = ShardingDbStrategyByNoUseInstance
	}

	// 初始化 dbConfigs
	dbConfigs, err := config.DbConfigFetcher.Fetch(config.GroupName)
	if err != nil {
		return nil, err
	}
	for _, cfg := range dbConfigs {
		if _, exists := dg.DbIdToConfigMap[cfg.DbId]; exists {
			return nil, fmt.Errorf("重复的 DbId: %d", cfg.DbId)
		}
		dg.DbIdToConfigMap[cfg.DbId] = cfg
	}

	return dg, nil
}

// Init 初始化
/**
 * 初始化 DbGroup，创建所有数据库连接
 *
 * @return error 初始化错误
 */
func (dg *DbGroup) Init() error {
	dg.mu.Lock()
	defer dg.mu.Unlock()
	if dg.isInit {
		return fmt.Errorf("已经初始化过了 groupName = %s", dg.GroupName)
	}
	dg.isInit = true

	for _, cfg := range dg.DbIdToConfigMap {
		db, err := dg.createDbByConfig(cfg)
		if err != nil {
			return err
		}
		dg.DbMap[cfg.DbId] = db
	}
	return nil
}

/**
 * 根据配置创建 Db 实例
 *
 * @param cfg 数据库配置
 * @return *Db 实例
 * @return error 创建错误
 */
func (dg *DbGroup) createDbByConfig(cfg *DbConfig) (*Db, error) {
	// 合并配置
	config := make(map[string]interface{})
	for k, v := range dg.DatasourceConfigTemplate {
		config[k] = v
	}
	for k, v := range cfg.DbConfigMap {
		config[k] = v
	}

	// 创建数据源，这里简化，使用 sql.DB
	// 实际中需要根据策略创建
	db, err := sql.Open("mysql", fmt.Sprintf("%v", config["url"]))
	if err != nil {
		return nil, err
	}

	return NewDb(db, cfg.DbId, dg), nil
}

// GetDefaultDb 获取默认 Db
/**
 * 获取默认数据库实例（dbId = 0）
 *
 * @return *Db 默认数据库实例
 */
func (dg *DbGroup) GetDefaultDb() *Db {
	return dg.DbMap[0]
}

// GetDbByShardingId 根据分片 ID 获取 Db
/**
 * 根据分片 ID 获取对应的数据库实例
 *
 * @param shardingId 分片键
 * @return *Db 数据库实例
 * @return error 未找到错误
 */
func (dg *DbGroup) GetDbByShardingId(shardingId int64) (*Db, error) {
	dbId := dg.ShardingDbStrategy.CalculateDbId(shardingId)
	if db, exists := dg.DbMap[dbId]; exists {
		return db, nil
	}
	return nil, fmt.Errorf("未找到 dbId = %d in group %s", dbId, dg.GroupName)
}

// GetDbByDbId 根据 dbId 获取 Db
/**
 * 根据数据库 ID 直接获取数据库实例
 *
 * @param dbId 数据库 ID
 * @return *Db 数据库实例
 * @return error 未找到错误
 */
func (dg *DbGroup) GetDbByDbId(dbId int) (*Db, error) {
	if db, exists := dg.DbMap[dbId]; exists {
		return db, nil
	}
	return nil, fmt.Errorf("未找到 dbId = %d in group %s", dbId, dg.GroupName)
}

// Destroy 销毁
/**
 * 销毁 DbGroup，关闭所有数据库连接
 */
func (dg *DbGroup) Destroy() {
	for _, db := range dg.DbMap {
		db.Close()
	}
}

// Shutdown 关闭
/**
 * 关闭 DbGroup（同 Destroy）
 */
func (dg *DbGroup) Shutdown() {
	dg.Destroy()
}
