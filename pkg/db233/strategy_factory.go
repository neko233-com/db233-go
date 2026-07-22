package db233

import "sync"

// 建表策略工厂
type TableCreationStrategyFactory struct {
	mu         sync.RWMutex
	strategies map[EnumDatabaseType]ITableCreationStrategy
}

var (
	strategyFactoryInstance *TableCreationStrategyFactory
	strategyFactoryOnce     sync.Once
)

// 获取策略工厂单例
func GetStrategyFactoryInstance() *TableCreationStrategyFactory {
	strategyFactoryOnce.Do(func() {
		strategyFactoryInstance = &TableCreationStrategyFactory{
			strategies: make(map[EnumDatabaseType]ITableCreationStrategy),
		}
		// 初始化默认策略
		cm := GetCrudManagerInstance()
		strategyFactoryInstance.strategies[EnumDatabaseTypeMySQL] = NewMySQLStrategy(cm)
		// TODO: PostgreSQL 支持将在未来版本中实现
		// strategyFactoryInstance.strategies[EnumDatabaseTypePostgreSQL] = NewPostgreSQLStrategy(cm)
	})
	return strategyFactoryInstance
}

// 获取建表策略
// dbType: 数据库类型，如果为空则使用默认类型（MySQL）
// 返回: 建表策略
func (f *TableCreationStrategyFactory) GetStrategy(dbType EnumDatabaseType) ITableCreationStrategy {
	// 如果未指定或无效，默认使用 MySQL
	if dbType == "" || !dbType.IsValid() {
		dbType = EnumDatabaseTypeMySQL
	}

	f.mu.RLock()
	strategy, exists := f.strategies[dbType]
	defaultStrategy := f.strategies[EnumDatabaseTypeMySQL]
	f.mu.RUnlock()
	if !exists {
		// 如果策略不存在，返回默认的 MySQL 策略
		LogWarn("未找到数据库类型 %s 的策略，使用默认 MySQL 策略", dbType)
		return defaultStrategy
	}

	return strategy
}

// 注册自定义策略
// dbType: 数据库类型
// strategy: 策略实现
func (f *TableCreationStrategyFactory) RegisterStrategy(dbType EnumDatabaseType, strategy ITableCreationStrategy) {
	if strategy == nil {
		LogWarn("尝试注册 nil 策略，忽略: 类型=%s", dbType)
		return
	}
	f.mu.Lock()
	f.strategies[dbType] = strategy
	f.mu.Unlock()
	LogInfo("注册建表策略: 类型=%s", dbType)
}
