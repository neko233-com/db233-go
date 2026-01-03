package db233

/**
 * 建表策略工厂
 *
 * @author neko233-com
 * @since 2026-01-04
 */
type TableCreationStrategyFactory struct {
	strategies map[DatabaseType]ITableCreationStrategy
}

var strategyFactoryInstance *TableCreationStrategyFactory

/**
 * 获取策略工厂单例
 */
func GetStrategyFactoryInstance() *TableCreationStrategyFactory {
	if strategyFactoryInstance == nil {
		strategyFactoryInstance = &TableCreationStrategyFactory{
			strategies: make(map[DatabaseType]ITableCreationStrategy),
		}
		// 初始化默认策略
		cm := GetCrudManagerInstance()
		strategyFactoryInstance.strategies[DatabaseTypeMySQL] = NewMySQLStrategy(cm)
		// TODO: PostgreSQL 支持将在未来版本中实现
		// strategyFactoryInstance.strategies[DatabaseTypePostgreSQL] = NewPostgreSQLStrategy(cm)
	}
	return strategyFactoryInstance
}

/**
 * 获取建表策略
 *
 * @param dbType 数据库类型，如果为空则使用默认类型（MySQL）
 * @return 建表策略
 */
func (f *TableCreationStrategyFactory) GetStrategy(dbType DatabaseType) ITableCreationStrategy {
	// 如果未指定或无效，默认使用 MySQL
	if dbType == "" || !dbType.IsValid() {
		dbType = DatabaseTypeMySQL
	}

	strategy, exists := f.strategies[dbType]
	if !exists {
		// 如果策略不存在，返回默认的 MySQL 策略
		LogWarn("未找到数据库类型 %s 的策略，使用默认 MySQL 策略", dbType)
		return f.strategies[DatabaseTypeMySQL]
	}

	return strategy
}

/**
 * 注册自定义策略
 *
 * @param dbType 数据库类型
 * @param strategy 策略实现
 */
func (f *TableCreationStrategyFactory) RegisterStrategy(dbType DatabaseType, strategy ITableCreationStrategy) {
	if strategy == nil {
		LogWarn("尝试注册 nil 策略，忽略: 类型=%s", dbType)
		return
	}
	f.strategies[dbType] = strategy
	LogInfo("注册建表策略: 类型=%s", dbType)
}

