package db233

import (
	"fmt"
	"reflect"
	"sync"
)

/**
 * ConcurrentMigrationConfig - 并发迁移配置
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type ConcurrentMigrationConfig struct {
	// 最大并发协程数（0 表示不限制）
	MaxConcurrency int

	// 自动数据库操作权限
	Permission *AutoDbPermission

	// 是否启用并发迁移
	EnableConcurrent bool
}

/**
 * NewDefaultConcurrentMigrationConfig 创建默认并发迁移配置
 */
func NewDefaultConcurrentMigrationConfig() *ConcurrentMigrationConfig {
	return &ConcurrentMigrationConfig{
		MaxConcurrency:   10,                        // 默认最多 10 个并发
		Permission:       NewSafeAutoDbPermission(), // 默认不允许删除列
		EnableConcurrent: true,                      // 默认启用并发
	}
}

/**
 * ConcurrentMigrationManager - 并发迁移管理器
 *
 * 支持多协程并发迁移表，提高 I/O 操作效率
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type ConcurrentMigrationManager struct {
	config *ConcurrentMigrationConfig
}

/**
 * NewConcurrentMigrationManager 创建并发迁移管理器
 */
func NewConcurrentMigrationManager(config *ConcurrentMigrationConfig) *ConcurrentMigrationManager {
	if config == nil {
		config = NewDefaultConcurrentMigrationConfig()
	}
	return &ConcurrentMigrationManager{
		config: config,
	}
}

/**
 * MigrateTablesBatch 批量迁移表（支持并发）
 *
 * @param db 数据库连接
 * @param entities 实体列表
 * @return 迁移结果（表名到错误的映射，成功的表为 nil）
 */
func (m *ConcurrentMigrationManager) MigrateTablesBatch(db *Db, entities []interface{}) map[string]error {
	if len(entities) == 0 {
		return make(map[string]error)
	}

	// 如果未启用并发或实体数量少，直接顺序执行
	if !m.config.EnableConcurrent || len(entities) <= 1 {
		return m.migrateTablesSequential(db, entities)
	}

	// 并发执行
	return m.migrateTablesConcurrent(db, entities)
}

/**
 * migrateTablesSequential 顺序迁移表
 */
func (m *ConcurrentMigrationManager) migrateTablesSequential(db *Db, entities []interface{}) map[string]error {
	results := make(map[string]error)

	for _, entity := range entities {
		tableName := m.getTableName(entity)
		err := m.migrateTable(db, entity)
		results[tableName] = err

		if err != nil {
			LogError("表迁移失败: 表=%s, 错误=%v", tableName, err)
		} else {
			LogInfo("表迁移成功: 表=%s", tableName)
		}
	}

	return results
}

/**
 * migrateTablesConcurrent 并发迁移表
 */
func (m *ConcurrentMigrationManager) migrateTablesConcurrent(db *Db, entities []interface{}) map[string]error {
	results := make(map[string]error)
	resultsMu := sync.Mutex{}

	// 创建工作队列
	jobs := make(chan interface{}, len(entities))
	for _, entity := range entities {
		jobs <- entity
	}
	close(jobs)

	// 确定并发数
	concurrency := m.config.MaxConcurrency
	if concurrency <= 0 || concurrency > len(entities) {
		concurrency = len(entities)
	}

	// 启动工作协程
	wg := sync.WaitGroup{}
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(workerID int) {
			defer wg.Done()

			for entity := range jobs {
				tableName := m.getTableName(entity)
				LogDebug("协程 %d 开始迁移表: %s", workerID, tableName)

				err := m.migrateTable(db, entity)

				resultsMu.Lock()
				results[tableName] = err
				resultsMu.Unlock()

				if err != nil {
					LogError("协程 %d 表迁移失败: 表=%s, 错误=%v", workerID, tableName, err)
				} else {
					LogInfo("协程 %d 表迁移成功: 表=%s", workerID, tableName)
				}
			}
		}(i)
	}

	// 等待所有工作完成
	wg.Wait()

	return results
}

/**
 * migrateTable 迁移单个表
 */
func (m *ConcurrentMigrationManager) migrateTable(db *Db, entity interface{}) error {
	// 获取元数据
	metadata, err := GetEntityMetadataCacheInstance().GetOrBuild(entity)
	if err != nil {
		return fmt.Errorf("获取实体元数据失败: %w", err)
	}

	// 获取策略
	factory := GetStrategyFactoryInstance()
	strategy := factory.GetStrategy(db.DatabaseType)

	// 检查表是否存在
	exists, err := strategy.TableExists(db, metadata.TableName)
	if err != nil {
		return fmt.Errorf("检查表是否存在失败: %w", err)
	}

	if !exists {
		// 表不存在，创建新表（需要 CreateColumn 权限）
		if !m.config.Permission.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
			return fmt.Errorf("表不存在且没有 CreateColumn 权限: 表=%s", metadata.TableName)
		}

		return m.createTable(db, entity, metadata, strategy)
	}

	// 表已存在，检查并更新表结构
	return m.updateTableStructure(db, entity, metadata, strategy)
}

/**
 * createTable 创建表
 */
func (m *ConcurrentMigrationManager) createTable(db *Db, entity interface{}, metadata *EntityMetadata, strategy ITableCreationStrategy) error {
	createSQL, err := strategy.GenerateCreateTableSQL(metadata.TableName, metadata.EntityType, metadata.PrimaryKeyColumn)
	if err != nil {
		return fmt.Errorf("生成建表 SQL 失败: %w", err)
	}

	LogInfo("创建表: 表=%s, SQL=%s", metadata.TableName, createSQL)

	_, err = db.DataSource.Exec(createSQL)
	if err != nil {
		return fmt.Errorf("执行建表 SQL 失败: %w", err)
	}

	return nil
}

/**
 * updateTableStructure 更新表结构
 */
func (m *ConcurrentMigrationManager) updateTableStructure(db *Db, entity interface{}, metadata *EntityMetadata, strategy ITableCreationStrategy) error {
	// 获取现有列
	existingColumns, err := strategy.GetExistingColumns(db, metadata.TableName)
	if err != nil {
		return fmt.Errorf("获取现有列失败: %w", err)
	}

	entityType := metadata.EntityType
	cm := GetCrudManagerInstance()

	// 1. 添加新列（需要 CreateColumn 权限）
	if m.config.Permission.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
		for i := 0; i < entityType.NumField(); i++ {
			field := entityType.Field(i)
			if !field.IsExported() {
				continue
			}

			colName := cm.GetColumnName(field)
			if colName == "" {
				continue
			}

			if !existingColumns[colName] {
				// 列不存在，添加新列
				addSQL, err := strategy.GenerateAddColumnSQL(metadata.TableName, field, colName)
				if err != nil {
					LogError("生成添加列 SQL 失败: 表=%s, 列=%s, 错误=%v", metadata.TableName, colName, err)
					continue
				}

				LogInfo("添加列: 表=%s, 列=%s, SQL=%s", metadata.TableName, colName, addSQL)

				_, err = db.DataSource.Exec(addSQL)
				if err != nil {
					LogError("执行添加列 SQL 失败: 表=%s, 列=%s, 错误=%v", metadata.TableName, colName, err)
				}
			}
		}
	}

	// 2. 删除废弃列（需要 DeleteColumn 权限）
	if m.config.Permission.IsAllowed(EnumAutoDbOperateTypeDeleteColumn) {
		// 构建实体中所有列名的集合
		entityColumns := make(map[string]bool)
		for _, colName := range metadata.AllColumns {
			entityColumns[colName] = true
		}

		// 删除不在实体中的列
		for existingCol := range existingColumns {
			if !entityColumns[existingCol] {
				dropSQL, err := strategy.GenerateDropColumnSQL(metadata.TableName, existingCol)
				if err != nil {
					LogError("生成删除列 SQL 失败: 表=%s, 列=%s, 错误=%v", metadata.TableName, existingCol, err)
					continue
				}

				LogWarn("删除列: 表=%s, 列=%s, SQL=%s", metadata.TableName, existingCol, dropSQL)

				_, err = db.DataSource.Exec(dropSQL)
				if err != nil {
					LogError("执行删除列 SQL 失败: 表=%s, 列=%s, 错误=%v", metadata.TableName, existingCol, err)
				}
			}
		}
	}

	return nil
}

/**
 * getTableName 获取表名
 */
func (m *ConcurrentMigrationManager) getTableName(entity interface{}) string {
	if dbEntity, ok := entity.(IDbEntity); ok {
		return dbEntity.TableName()
	}

	// 尝试从指针类型获取
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr && v.Elem().CanAddr() {
		if dbEntity, ok := v.Interface().(IDbEntity); ok {
			return dbEntity.TableName()
		}
	}

	return "unknown"
}
