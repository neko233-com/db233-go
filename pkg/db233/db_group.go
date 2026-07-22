package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
)

// DbGroup 数据库组 - Go 版
// 对应 Kotlin 版本的 DbGroup，用于管理同一配置下的多个数据库实例
type DbGroup struct {
	DbGroupConfig            *DbGroupConfig
	GroupName                string
	CreateStrategy           DataSourceCreateStrategy
	ShardingDbStrategy       ShardingDbStrategy
	DatasourceConfigTemplate map[string]any
	DbIdToConfigMap          map[int]*DbConfig
	DbMap                    map[int]*Db
	isInit                   bool
	mu                       sync.RWMutex
}

// 创建 DbGroup
// config: DbGroupConfig 配置
// 返回: *DbGroup 实例
// 返回: error 创建错误
func NewDbGroup(config *DbGroupConfig) (*DbGroup, error) {
	if config == nil {
		return nil, fmt.Errorf("DbGroupConfig 不能为空")
	}
	if config.GroupName == "" {
		return nil, fmt.Errorf("groupName 不能为空")
	}
	if config.DbConfigFetcher == nil {
		return nil, fmt.Errorf("DbConfigFetcher 不能为空")
	}
	template := make(map[string]any, len(config.DatasourceConfigTemplate))
	for key, value := range config.DatasourceConfigTemplate {
		template[key] = cloneDashboardComponent(value)
	}
	dg := &DbGroup{
		DbGroupConfig:            config,
		GroupName:                config.GroupName,
		CreateStrategy:           config.DataSourceCreateStrategy,
		ShardingDbStrategy:       config.ShardingDbStrategy,
		DatasourceConfigTemplate: template,
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
		return nil, NewConfigurationExceptionWithCause(err, "获取数据库组配置失败")
	}
	for _, cfg := range dbConfigs {
		if cfg == nil {
			return nil, fmt.Errorf("DbConfig 不能为空")
		}
		if _, exists := dg.DbIdToConfigMap[cfg.DbId]; exists {
			return nil, fmt.Errorf("重复的 DbId: %d", cfg.DbId)
		}
		dg.DbIdToConfigMap[cfg.DbId] = cfg
	}

	return dg, nil
}

// Init 初始化
// 初始化 DbGroup，创建所有数据库连接
// 返回: error 初始化错误
func (dg *DbGroup) Init() error {
	dg.mu.Lock()
	defer dg.mu.Unlock()
	if dg.isInit {
		return fmt.Errorf("已经初始化过了 groupName = %s", dg.GroupName)
	}
	created := make(map[int]*Db, len(dg.DbIdToConfigMap))
	for _, cfg := range dg.DbIdToConfigMap {
		db, err := dg.createDbByConfig(cfg)
		if err != nil {
			closeErrors := []error{err}
			for _, opened := range created {
				if closeErr := opened.Close(); closeErr != nil {
					closeErrors = append(closeErrors, fmt.Errorf("回滚关闭 Db %d: %w", opened.DbId, closeErr))
				}
			}
			return errors.Join(closeErrors...)
		}
		created[cfg.DbId] = db
	}
	dg.DbMap = created
	dg.isInit = true
	return nil
}

// 根据配置创建 Db 实例
// cfg: 数据库配置
// 返回: *Db 实例
// 返回: error 创建错误
func (dg *DbGroup) createDbByConfig(cfg *DbConfig) (*Db, error) {
	if cfg == nil {
		return nil, fmt.Errorf("DbConfig 不能为空")
	}
	// 合并配置
	config := make(map[string]any)
	for k, v := range dg.DatasourceConfigTemplate {
		config[k] = v
	}
	for k, v := range cfg.DbConfigMap {
		config[k] = v
	}

	var db *sql.DB
	if dg.CreateStrategy != nil {
		createdDriver, err := dg.CreateStrategy.Create(dg.DatasourceConfigTemplate, cfg.DbConfigMap)
		if err != nil {
			return nil, NewConnectionExceptionWithCause(err, fmt.Sprintf("创建数据源失败: dbId=%d strategy=%T", cfg.DbId, dg.CreateStrategy))
		}
		if createdDriver == nil {
			return nil, fmt.Errorf("创建数据源失败: dbId=%d strategy=%T 返回 nil Driver", cfg.DbId, dg.CreateStrategy)
		}
		db = sql.OpenDB(staticDriverConnector{driver: createdDriver, name: configStringValue(config["url"])})
	} else {
		dsn := configStringValue(config["url"])
		if dsn == "" {
			return nil, fmt.Errorf("数据库连接 url 不能为空: dbId=%d", cfg.DbId)
		}
		var err error
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			return nil, NewConnectionExceptionWithCause(err, fmt.Sprintf("打开数据库连接失败: dbId=%d", cfg.DbId))
		}
	}
	if err := db.Ping(); err != nil {
		closeErr := db.Close()
		return nil, NewConnectionExceptionWithCause(errors.Join(err, closeErr), fmt.Sprintf("连接数据库失败: dbId=%d", cfg.DbId))
	}

	return NewDb(db, cfg.DbId, dg), nil
}

type staticDriverConnector struct {
	driver driver.Driver
	name   string
}

func (c staticDriverConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.driver.Open(c.name)
}

func (c staticDriverConnector) Driver() driver.Driver {
	return c.driver
}

// GetDefaultDb 获取默认 Db
// 获取默认数据库实例（dbId = 0）
// 返回: *Db 默认数据库实例
func (dg *DbGroup) GetDefaultDb() *Db {
	dg.mu.RLock()
	defer dg.mu.RUnlock()
	return dg.DbMap[0]
}

// GetDbByShardingId 根据分片 ID 获取 Db
// 根据分片 ID 获取对应的数据库实例
// shardingId: 分片键
// 返回: *Db 数据库实例
// 返回: error 未找到错误
func (dg *DbGroup) GetDbByShardingId(shardingId int64) (*Db, error) {
	dg.mu.RLock()
	strategy := dg.ShardingDbStrategy
	dg.mu.RUnlock()
	if strategy == nil {
		return nil, fmt.Errorf("分片策略未配置: group=%s", dg.GroupName)
	}
	return dg.GetDbByDbId(strategy.CalculateDbId(shardingId))
}

// GetDbByDbId 根据 dbId 获取 Db
// 根据数据库 ID 直接获取数据库实例
// dbId: 数据库 ID
// 返回: *Db 数据库实例
// 返回: error 未找到错误
func (dg *DbGroup) GetDbByDbId(dbId int) (*Db, error) {
	dg.mu.RLock()
	defer dg.mu.RUnlock()
	if db, exists := dg.DbMap[dbId]; exists {
		return db, nil
	}
	return nil, fmt.Errorf("未找到 dbId = %d in group %s", dbId, dg.GroupName)
}

// Destroy 销毁
// 销毁 DbGroup，关闭所有数据库连接
func (dg *DbGroup) Destroy() {
	if err := dg.DestroyStrict(); err != nil {
		LogError("销毁 DbGroup 失败: group=%s err=%s", safeValueForLog(dg.GroupName), safeErrorForLog(err))
	}
}

// DestroyStrict 销毁 DbGroup，并完整返回所有数据库关闭错误。
func (dg *DbGroup) DestroyStrict() error {
	if dg == nil {
		return nil
	}
	dg.mu.Lock()
	databases := make([]*Db, 0, len(dg.DbMap))
	for _, db := range dg.DbMap {
		databases = append(databases, db)
	}
	dg.DbMap = make(map[int]*Db)
	dg.isInit = false
	dg.mu.Unlock()
	var closeErrors []error
	for _, db := range databases {
		if err := db.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("关闭 Db %d: %w", db.DbId, err))
		}
	}
	return errors.Join(closeErrors...)
}

// Shutdown 关闭
// 关闭 DbGroup（同 Destroy）
func (dg *DbGroup) Shutdown() {
	dg.Destroy()
}

// ShutdownStrict 关闭 DbGroup，并返回完整错误链。
func (dg *DbGroup) ShutdownStrict() error {
	return dg.DestroyStrict()
}
