package db233

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// IDbEntity 是数据库实体接口。
// 所有数据库实体必须实现此接口，提供自定义表名。
// 主键信息通过 struct tag 自动扫描（primary_key:"true" 或字段名为 ID/Id）。
type IDbEntity interface {
	// TableName 返回表名。
	TableName() string

	// SerializeBeforeSaveDb 是保存到数据库前的序列化钩子。
	// 在数据保存到数据库之前调用，可以用于数据转换、加密等操作。
	// 此方法在 Save 和 Update 操作前调用。
	SerializeBeforeSaveDb()

	// DeserializeAfterLoadDb 是从数据库加载后的反序列化钩子。
	// 在数据从数据库加载后调用，可以用于数据转换、解密等操作。
	// 此方法在 FindById、FindAll、FindByCondition 等查询操作后调用。
	DeserializeAfterLoadDb()
}

// ITableMetaDataProvider 表元数据提供者接口（可选实现）。
// 如果实体需要管理索引，可以实现此接口。
// 自动迁移时会检查实体是否实现此接口，如果实现则使用返回的元数据来管理索引。
type ITableMetaDataProvider interface {
	// GetTableMetaData 返回表元数据。
	// 返回 nil 表示不管理索引。
	GetTableMetaData() *TableMetaData
}

// CrudManager 是 CRUD 管理器，管理实体类的元数据，包括表结构、列信息、主键等。
type CrudManager struct {
	// tableName 到主键列名列表的映射
	tableNamePkColNameListMap map[string][]string

	// tableName 到所有列名的映射
	tableNameToColNameMap map[string][]string

	// tableName -> pk对象 -> colName -> value 的映射
	tableToPkToColValueMap map[string]map[any]map[string]any

	// 已扫描过的类集合
	metadataClassSet map[reflect.Type]bool

	// 类型到主键列名的缓存（优化性能）
	typeToPrimaryKeyColumnCache map[reflect.Type]string

	// 锁（保证并发安全）
	mu sync.RWMutex
}

var crudManagerInstance *CrudManager
var crudManagerOnce sync.Once

// GetCrudManagerInstance 获取单例实例。
func GetCrudManagerInstance() *CrudManager {
	crudManagerOnce.Do(func() {
		crudManagerInstance = &CrudManager{
			tableNamePkColNameListMap:   make(map[string][]string),
			tableNameToColNameMap:       make(map[string][]string),
			tableToPkToColValueMap:      make(map[string]map[any]map[string]any),
			metadataClassSet:            make(map[reflect.Type]bool),
			typeToPrimaryKeyColumnCache: make(map[reflect.Type]string),
		}
	})
	return crudManagerInstance
}

// AutoInitEntity 自动初始化实体。
func (cm *CrudManager) AutoInitEntity(entityType any) *CrudManager {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	t := reflect.TypeOf(entityType)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if cm.metadataClassSet[t] {
		return cm
	}

	cm.metadataClassSet[t] = true
	cm.initEntityClassMetadata([]reflect.Type{t})

	return cm
}

// checkEntityAnnotation 检查实体注解（Go 中使用 struct tag）。
func (cm *CrudManager) checkEntityAnnotation(t reflect.Type) error {
	// Go 中没有注解，但我们可以使用 struct tag
	// 这里简化处理，假设所有 struct 都是实体
	return nil
}

// initEntityClassMetadata 初始化实体类元数据。
func (cm *CrudManager) initEntityClassMetadata(entityTypes []reflect.Type) {
	cm.initTableColumnMetadataByClass(entityTypes)
	cm.initTablePrimaryKeyMetadataByClass(entityTypes)
}

// AutoLazyInitOrThrowError 懒初始化或抛出错误。
func (cm *CrudManager) AutoLazyInitOrThrowError(obj any) error {
	if reflect.TypeOf(obj).Kind() == reflect.Ptr && reflect.TypeOf(obj).Elem().Kind() == reflect.Interface {
		return NewDb233Exception("对象类型错误，不能是接口")
	}

	if cm.IsContainsEntity(obj) {
		return nil
	}

	return cm.configClassLazy(obj)
}

// configClassLazy 配置类懒初始化。
func (cm *CrudManager) configClassLazy(obj any) error {
	t := reflect.TypeOf(obj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// 先检查是否已存在（使用读锁）
	cm.mu.RLock()
	if cm.metadataClassSet[t] {
		cm.mu.RUnlock()
		return nil
	}
	cm.mu.RUnlock()

	// 初始化（使用写锁）
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 双重检查，防止并发初始化
	if cm.metadataClassSet[t] {
		return nil
	}

	cm.initEntityClassMetadata([]reflect.Type{t})
	return nil
}

// IsNotContainsEntity 检查是否不包含实体。
func (cm *CrudManager) IsNotContainsEntity(obj any) bool {
	return !cm.IsContainsEntity(obj)
}

// IsContainsEntity 检查是否包含实体（并发安全）。
func (cm *CrudManager) IsContainsEntity(obj any) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	t := reflect.TypeOf(obj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return cm.metadataClassSet[t]
}

// initTableColumnMetadataByClass 初始化表列元数据，支持嵌入结构体。
func (cm *CrudManager) initTableColumnMetadataByClass(entityTypes []reflect.Type) {
	for _, t := range entityTypes {
		tableName := cm.GetTableName(t)

		colList := make([]string, 0)
		cm.collectColumnsRecursive(t, &colList)

		cm.tableNameToColNameMap[tableName] = colList
	}
}

// collectColumnsRecursive 递归收集列名，支持嵌入结构体。
func (cm *CrudManager) collectColumnsRecursive(t reflect.Type, colList *[]string) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// 处理嵌入结构体（Anonymous field）
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归收集
			if embeddedType.Kind() == reflect.Struct {
				cm.collectColumnsRecursive(embeddedType, colList)
				continue
			}
		}

		colName := cm.GetColumnName(field)
		if colName == "" {
			// 跳过没有有效列名的字段（db:"-" 或没有 db 标签）
			continue
		}
		*colList = append(*colList, colName)
	}
}

// initTablePrimaryKeyMetadataByClass 初始化表主键元数据，支持嵌入结构体。
func (cm *CrudManager) initTablePrimaryKeyMetadataByClass(entityTypes []reflect.Type) {
	for _, t := range entityTypes {
		tableName := cm.GetTableName(t)

		pkList := make([]string, 0)
		cm.collectPrimaryKeysRecursive(t, &pkList)

		if len(pkList) > 0 {
			cm.tableNamePkColNameListMap[tableName] = pkList
		}
	}
}

// collectPrimaryKeysRecursive 递归收集主键列名，支持嵌入结构体。
func (cm *CrudManager) collectPrimaryKeysRecursive(t reflect.Type, pkList *[]string) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// 处理嵌入结构体（Anonymous field）
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归收集
			if embeddedType.Kind() == reflect.Struct {
				cm.collectPrimaryKeysRecursive(embeddedType, pkList)
				continue
			}
		}

		colName := cm.GetColumnName(field)
		if colName == "" {
			// 跳过没有有效列名的字段（db:"-" 或没有 db 标签）
			continue
		}
		if cm.IsPrimaryKey(field) {
			*pkList = append(*pkList, colName)
		}
	}
}

// GetTableNameFromEntity 获取表名（从 IDbEntity 接口）。
// entity: 实现了 IDbEntity 接口的实体。
// 返回: 表名。
func (cm *CrudManager) GetTableNameFromEntity(entity IDbEntity) string {
	return entity.TableName()
}

// GetTableName 获取表名（从 reflect.Type，内部会尝试创建实例并检查 IDbEntity 接口）。
// t: 实体类型。
// 返回: 表名。
func (cm *CrudManager) GetTableName(t reflect.Type) string {
	// 尝试创建实例并检查是否实现了 IDbEntity 接口
	if t.Kind() == reflect.Struct {
		// 创建指针实例
		instancePtr := reflect.New(t).Interface()
		if entity, ok := instancePtr.(IDbEntity); ok {
			tableName := entity.TableName()
			if tableName != "" {
				return tableName
			}
		}

		// 如果指针类型不实现，尝试值类型
		instanceValue := reflect.New(t).Elem().Interface()
		if entity, ok := instanceValue.(IDbEntity); ok {
			tableName := entity.TableName()
			if tableName != "" {
				return tableName
			}
		}

		// 检查是否有 table tag（向后兼容）
		if t.NumField() > 0 {
			if tableTag := t.Field(0).Tag.Get("table"); tableTag != "" {
				return tableTag
			}
		}
	}
	// 默认使用类型名转换为 snake_case（向后兼容）
	return StringUtilsInstance.CamelToSnake(t.Name())
}

// GetColumnName 获取列名。
func (cm *CrudManager) GetColumnName(field reflect.StructField) string {
	// 优先使用 db 标签
	if dbTag := field.Tag.Get("db"); dbTag != "" {
		if dbTag == "-" {
			// 明确标记为跳过
			return ""
		}
		// 解析标签，获取列名（标签格式：column_name,options...）
		tagParts := strings.Split(dbTag, ",")
		columnName := strings.TrimSpace(tagParts[0])
		if columnName == "" || columnName == "-" {
			// 列名为空或"-"，返回空字符串表示跳过
			return ""
		}

		// 检查是否有 skip 选项
		for i := 1; i < len(tagParts); i++ {
			if strings.TrimSpace(tagParts[i]) == "skip" {
				// 明确标记为 skip，返回空字符串表示跳过
				return ""
			}
		}

		return columnName
	}
	// 没有 db 标签，返回空字符串（要求必须显式声明 db 标签）
	return ""
}

// IsPrimaryKey 检查是否为主键。
// 支持的标记方式（按优先级）：
// 1. primary_key:"true" - 独立标签（推荐）
// 2. 字段名为 ID 或 Id（默认约定）
// 注意：不再支持 db:"column_name,primary_key" 格式，请使用独立标签。
func (cm *CrudManager) IsPrimaryKey(field reflect.StructField) bool {
	// 优先检查独立的 primary_key 标签
	if field.Tag.Get("primary_key") == "true" {
		return true
	}
	// 检查字段名是否为 ID 或 Id（默认约定）
	if field.Name == "ID" || field.Name == "Id" {
		return true
	}
	return false
}

// IsAutoIncrement 检查是否为自增字段。
// 支持的标记方式：
// 1. auto_increment:"true" - 独立标签（推荐）
// 注意：不再支持 db:"column_name,auto_increment" 格式，请使用独立标签。
func (cm *CrudManager) IsAutoIncrement(field reflect.StructField) bool {
	// 检查独立的 auto_increment 标签
	if field.Tag.Get("auto_increment") == "true" {
		return true
	}
	return false
}

// GetPrimaryKeyColumnName 获取实体的主键列名（自动扫描 struct tag，支持嵌入结构体，带缓存）。
// entity: 实体实例。
// 返回: 主键列名，如果未找到则返回 "id"。
func (cm *CrudManager) GetPrimaryKeyColumnName(entity any) string {
	t := reflect.TypeOf(entity)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// 先尝试从缓存读取（使用读锁）
	cm.mu.RLock()
	if cached, exists := cm.typeToPrimaryKeyColumnCache[t]; exists {
		cm.mu.RUnlock()
		return cached
	}
	cm.mu.RUnlock()

	// 缓存未命中，扫描字段（使用写锁）
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 双重检查，防止并发情况下重复扫描
	if cached, exists := cm.typeToPrimaryKeyColumnCache[t]; exists {
		return cached
	}

	// 递归扫描所有字段（包括嵌入结构体），查找 primary_key 标记
	colName := cm.findPrimaryKeyColumnRecursive(t)
	if colName != "" {
		// 缓存结果
		cm.typeToPrimaryKeyColumnCache[t] = colName
		return colName
	}

	// 默认返回 "id" 并缓存
	cm.typeToPrimaryKeyColumnCache[t] = "id"
	return "id"
}

// findPrimaryKeyColumnRecursive 递归查找主键列名，支持嵌入结构体。
// 功能说明：
// 1. 类似 JPA 的 @Id 继承机制，自动从父类（嵌入结构体）中查找主键
// 2. 支持多层嵌套的结构体继承
// 3. 优先查找嵌入结构体中的主键，然后查找当前层级的主键
// 使用场景：
// - BaseEntity 中定义 ID，子类自动继承
// - 多层继承：BaseEntity -> AbstractPlayerEntity -> ConcretePlayerEntity
// t: 结构体类型。
// 返回: 主键列名，未找到返回空字符串。
func (cm *CrudManager) findPrimaryKeyColumnRecursive(t reflect.Type) string {
	// 遍历当前类型的所有字段
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// 处理嵌入结构体（Anonymous field）- 相当于 Java 的继承
		// Go 中的匿名字段会被提升，类似于 Java 的继承机制
		if field.Anonymous {
			embeddedType := field.Type
			// 如果是指针类型，获取其指向的实际类型
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}

			// 如果嵌入的是结构体，递归查找其中的主键
			// 这相当于在父类中查找 @Id 注解的字段
			if embeddedType.Kind() == reflect.Struct {
				colName := cm.findPrimaryKeyColumnRecursive(embeddedType)
				if colName != "" {
					// 在嵌入结构体（父类）中找到了主键
					return colName
				}
			}
		}

		// 检查当前层级的字段是否为主键
		// 支持三种标记方式：
		// primary_key:"true" - 独立的主键标签（推荐）
		// 或字段名为 ID/Id（默认约定）
		// 3. 字段名为 ID 或 Id - 默认约定
		if cm.IsPrimaryKey(field) {
			colName := cm.GetColumnName(field)
			if colName != "" {
				// 找到主键字段，返回其列名
				return colName
			}
		}
	}

	// 未找到主键
	return ""
}

// GetPrimaryKeyValue 获取实体的主键值（自动从 struct 字段读取，支持嵌入结构体）。
// entity: 实体实例。
// 返回: 主键值，如果未找到则返回 nil。
func (cm *CrudManager) GetPrimaryKeyValue(entity any) any {
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	return cm.findPrimaryKeyValueRecursive(v, v.Type())
}

// findPrimaryKeyValueRecursive 递归查找主键值，支持嵌入结构体。
func (cm *CrudManager) findPrimaryKeyValueRecursive(v reflect.Value, t reflect.Type) any {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i)

		// 处理嵌入结构体（Anonymous field）
		if field.Anonymous {
			embeddedType := field.Type
			embeddedValue := fieldValue

			// 如果是指针，需要解引用
			if embeddedType.Kind() == reflect.Ptr {
				if embeddedValue.IsNil() {
					continue // 跳过 nil 嵌入结构体
				}
				embeddedValue = embeddedValue.Elem()
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归查找
			if embeddedType.Kind() == reflect.Struct {
				pkValue := cm.findPrimaryKeyValueRecursive(embeddedValue, embeddedType)
				if pkValue != nil {
					return pkValue
				}
			}
		}

		// 检查当前字段是否为主键
		if cm.IsPrimaryKey(field) {
			if fieldValue.CanInterface() {
				return fieldValue.Interface()
			}
		}
	}

	return nil
}

// GetTableToPkColListMap 获取表到主键列列表的映射。
func (cm *CrudManager) GetTableToPkColListMap() map[string][]string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make(map[string][]string)
	for k, v := range cm.tableNamePkColNameListMap {
		result[k] = append([]string(nil), v...)
	}
	return result
}

// ClearPrimaryKeyCache 清除主键缓存（用于测试）。
func (cm *CrudManager) ClearPrimaryKeyCache() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.typeToPrimaryKeyColumnCache = make(map[reflect.Type]string)
}

// AutoCreateTable 自动创建表。
func (cm *CrudManager) AutoCreateTable(db *Db, entityType any) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	t := reflect.TypeOf(entityType)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	tableName := cm.GetTableName(t)
	if tableName == "" {
		return NewDb233Exception("无法获取表名")
	}

	// 获取建表策略
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)

	// 检查表是否已存在
	exists, err := strategy.TableExists(db, tableName)
	if err != nil {
		return err
	}
	if exists {
		LogInfo("表已存在，跳过创建: %s", tableName)
		return nil
	}

	// 获取主键列名（已持有写锁，使用递归扫描支持嵌入结构体）
	var uidColumn string
	if t.Kind() == reflect.Struct {
		// 检查缓存
		if cached, exists := cm.typeToPrimaryKeyColumnCache[t]; exists {
			uidColumn = cached
		} else {
			// 使用递归扫描查找主键（支持嵌入结构体）
			uidColumn = cm.findPrimaryKeyColumnRecursive(t)
			if uidColumn == "" {
				uidColumn = "id"
			}
			cm.typeToPrimaryKeyColumnCache[t] = uidColumn
		}
	}

	// 生成建表SQL
	createSQL, err := strategy.GenerateCreateTableSQL(tableName, t, uidColumn)
	if err != nil {
		return err
	}

	// 执行建表
	_, err = db.DataSource.Exec(createSQL)
	if err != nil {
		return NewQueryExceptionWithCause(err, "创建表失败: "+tableName)
	}

	LogInfo("表创建成功: 数据库类型=%s, 表=%s", strategy.GetDatabaseType(), tableName)
	return nil
}

// tableExists 检查表是否存在（已废弃，使用策略模式）。
// Deprecated: 使用 ITableCreationStrategy.TableExists 代替。
func (cm *CrudManager) tableExists(db *Db, tableName string) (bool, error) {
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)
	return strategy.TableExists(db, tableName)
}

// generateCreateTableSQL 生成建表SQL（已废弃，使用策略模式）。
// Deprecated: 使用 ITableCreationStrategy.GenerateCreateTableSQL 代替。
func (cm *CrudManager) generateCreateTableSQL(t reflect.Type) (string, error) {
	// 此方法已废弃，保留仅为向后兼容
	// 实际应该通过 AutoCreateTable 调用策略
	return "", NewDb233Exception("此方法已废弃，请使用 AutoCreateTable")
}

// getSQLType 获取SQL类型（已废弃，使用策略模式）。
// Deprecated: 使用 ITableCreationStrategy.GetSQLType 代替。
func (cm *CrudManager) getSQLType(field reflect.StructField) string {
	// 此方法已废弃，保留仅为向后兼容
	// 实际应该通过策略获取
	return "VARCHAR(255)"
}

// AutoMigrateTableSimple 自动迁移表（创建或修改表结构，包括索引）- 简化版本，使用默认权限。
func (cm *CrudManager) AutoMigrateTableSimple(db *Db, entityType any) error {
	t := reflect.TypeOf(entityType)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	tableName := cm.GetTableName(t)
	if tableName == "" {
		return NewDb233Exception("无法获取表名")
	}

	// 获取建表策略
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)

	// 检查表是否已存在
	exists, err := strategy.TableExists(db, tableName)
	if err != nil {
		return err
	}

	if !exists {
		// 表不存在，创建表（AutoCreateTable 会自己获取锁）
		return cm.AutoCreateTable(db, entityType)
	}

	// 表存在，获取锁后检查并添加缺失的列和索引
	cm.mu.Lock()
	defer cm.mu.Unlock()

	err = cm.alterTableAddMissingColumns(db, t)
	if err != nil {
		return err
	}

	// 迁移索引（如果实体实现了 ITableMetaDataProvider 接口）
	if entity, ok := entityType.(IDbEntity); ok {
		metaData := GetTableMetaData(entity)
		if metaData != nil && len(metaData.Indexes) > 0 {
			permissions := NewDefaultAutoDbPermission()
			if err := cm.migrateIndexes(db, tableName, metaData.Indexes, permissions); err != nil {
				LogError("索引迁移失败: 表=%s, 错误=%v", tableName, err)
				// 索引迁移失败不影响列迁移，只记录错误
			}
		}
	}

	return nil
}

// alterTableAddMissingColumns 修改表添加缺失的列。
func (cm *CrudManager) alterTableAddMissingColumns(db *Db, t reflect.Type) error {
	tableName := cm.GetTableName(t)
	if tableName == "" {
		return NewDb233Exception("无法获取表名")
	}

	// 获取建表策略
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)

	// 获取现有列
	existingColumns, err := strategy.GetExistingColumns(db, tableName)
	if err != nil {
		return err
	}

	// 获取主键列名（已持有写锁，使用递归扫描支持嵌入结构体）
	var uidColumn string
	if t.Kind() == reflect.Struct {
		// 检查缓存
		if cached, exists := cm.typeToPrimaryKeyColumnCache[t]; exists {
			uidColumn = cached
		} else {
			// 使用递归扫描查找主键（支持嵌入结构体）
			uidColumn = cm.findPrimaryKeyColumnRecursive(t)
			if uidColumn == "" {
				uidColumn = "id"
			}
			cm.typeToPrimaryKeyColumnCache[t] = uidColumn
		}
	}

	// 获取实体定义的列（使用统一的 GetColumnName 方法）
	entityColumns := make(map[string]reflect.StructField)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			LogDebug("跳过未导出字段: 表=%s, 字段=%s", tableName, field.Name)
			continue
		}
		colName := cm.GetColumnName(field)
		if colName == "" {
			LogDebug("跳过无有效列名的字段: 表=%s, 字段=%s", tableName, field.Name)
			continue
		}
		entityColumns[colName] = field
	}

	// 找出缺失的列
	var alterStatements []string
	for colName, field := range entityColumns {
		if _, exists := existingColumns[colName]; !exists {
			// 使用策略生成添加列的 SQL（新的3参数版本）
			alterSQL, err := strategy.GenerateAddColumnSQL(tableName, field, colName)
			if err != nil {
				LogError("生成添加列SQL失败: 表=%s, 列=%s, 错误=%v", tableName, colName, err)
				continue
			}
			alterStatements = append(alterStatements, alterSQL)
			LogDebug("准备添加缺失的列: 表=%s, 列=%s, SQL=%s", tableName, colName, alterSQL)
		}
	}

	if len(alterStatements) == 0 {
		LogInfo("表结构已是最新: %s", tableName)
		return nil
	}

	// 执行ALTER TABLE（每个语句单独执行，因为不同数据库的语法可能不同）
	for _, alterSQL := range alterStatements {
		_, err = db.DataSource.Exec(alterSQL)
		if err != nil {
			return NewQueryExceptionWithCause(err, "修改表结构失败: "+tableName+", SQL: "+alterSQL)
		}
	}

	LogInfo("表结构更新成功: 数据库类型=%s, 表=%s", strategy.GetDatabaseType(), tableName)
	return nil
}

// getExistingColumns 获取现有表的列信息（已废弃，使用策略模式）。
// Deprecated: 使用 ITableCreationStrategy.GetExistingColumns 代替。
func (cm *CrudManager) getExistingColumns(db *Db, tableName string) (map[string]bool, error) {
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)
	return strategy.GetExistingColumns(db, tableName)
}

// AutoCreateTableWithPermissions 带权限控制的自动创建表。
func (cm *CrudManager) AutoCreateTableWithPermissions(db *Db, entityType any, permissions *AutoDbPermission) error {
	if permissions == nil {
		permissions = NewDefaultAutoDbPermission()
	}

	// 检查是否允许创建表
	if !permissions.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
		LogWarn("创建表操作被禁用，跳过: 实体=%v", entityType)
		return nil
	}

	return cm.AutoCreateTable(db, entityType)
}

// AutoMigrateTable 自动迁移表（支持创建列、更新列、删除列）。
func (cm *CrudManager) AutoMigrateTable(db *Db, entityType any, permissions *AutoDbPermission) error {
	if permissions == nil {
		permissions = NewDefaultAutoDbPermission()
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	t := reflect.TypeOf(entityType)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	tableName := cm.GetTableName(t)
	if tableName == "" {
		return NewDb233Exception("无法获取表名")
	}

	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)

	// 检查表是否存在
	exists, err := strategy.TableExists(db, tableName)
	if err != nil {
		return err
	}

	// 表不存在，创建表
	if !exists {
		if !permissions.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
			LogWarn("创建表操作被禁用: 表=%s", tableName)
			return nil
		}
		return cm.AutoCreateTable(db, entityType)
	}

	// 表已存在，检查列差异
	LogInfo("开始迁移表: 表=%s", tableName)

	// 获取现有列
	existingColumns, err := strategy.GetTableColumns(db, tableName)
	if err != nil {
		return fmt.Errorf("获取表列信息失败: %w", err)
	}

	// 获取实体字段
	entityColumns := cm.getEntityColumns(t)

	// 找出需要添加的列
	columnsToAdd := make(map[string]reflect.StructField)
	for colName, field := range entityColumns {
		if _, exists := existingColumns[colName]; !exists {
			columnsToAdd[colName] = field
		}
	}

	// 找出需要删除的列
	columnsToDelete := make([]string, 0)
	for colName := range existingColumns {
		if _, exists := entityColumns[colName]; !exists {
			columnsToDelete = append(columnsToDelete, colName)
		}
	}

	// 添加列
	if len(columnsToAdd) > 0 && permissions.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
		for colName, field := range columnsToAdd {
			sql, err := strategy.GenerateAddColumnSQL(tableName, field, colName)
			if err != nil {
				LogError("生成添加列SQL失败: 表=%s, 列=%s, 错误=%v", tableName, colName, err)
				continue
			}

			_, err = db.DataSource.Exec(sql)
			if err != nil {
				LogError("添加列失败: 表=%s, 列=%s, 错误=%v", tableName, colName, err)
			} else {
				LogInfo("添加列成功: 表=%s, 列=%s", tableName, colName)
			}
		}
	}

	// 删除列
	if len(columnsToDelete) > 0 && permissions.IsAllowed(EnumAutoDbOperateTypeDeleteColumn) {
		for _, colName := range columnsToDelete {
			sql, err := strategy.GenerateDropColumnSQL(tableName, colName)
			if err != nil {
				LogError("生成删除列SQL失败: 表=%s, 列=%s, 错误=%v", tableName, colName, err)
				continue
			}

			_, err = db.DataSource.Exec(sql)
			if err != nil {
				LogError("删除列失败: 表=%s, 列=%s, 错误=%v", tableName, colName, err)
			} else {
				LogInfo("删除列成功: 表=%s, 列=%s", tableName, colName)
			}
		}
	}

	LogInfo("表迁移完成: 表=%s, 添加列=%d, 删除列=%d", tableName, len(columnsToAdd), len(columnsToDelete))

	// 迁移索引（如果实体实现了 ITableMetaDataProvider 接口）
	if entity, ok := entityType.(IDbEntity); ok {
		metaData := GetTableMetaData(entity)
		if metaData != nil && len(metaData.Indexes) > 0 {
			if err := cm.migrateIndexes(db, tableName, metaData.Indexes, permissions); err != nil {
				LogError("索引迁移失败: 表=%s, 错误=%v", tableName, err)
				// 索引迁移失败不影响列迁移，只记录错误
			}
		}
	}

	return nil
}

// migrateIndexes 迁移索引（增删改）。
func (cm *CrudManager) migrateIndexes(db *Db, tableName string, expectedIndexes []*IndexMetaData, permissions *AutoDbPermission) error {
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)

	// 获取现有索引
	existingIndexes, err := strategy.GetExistingIndexes(db, tableName)
	if err != nil {
		return fmt.Errorf("获取现有索引失败: %w", err)
	}

	// 构建期望索引的映射（索引名 -> 索引信息）
	expectedIndexMap := make(map[string]*IndexMetaData)
	for _, idx := range expectedIndexes {
		expectedIndexMap[idx.IndexName] = idx
	}

	// 找出需要添加的索引
	indexesToAdd := make([]*IndexMetaData, 0)
	for _, expectedIdx := range expectedIndexes {
		if _, exists := existingIndexes[expectedIdx.IndexName]; !exists {
			indexesToAdd = append(indexesToAdd, expectedIdx)
		} else {
			// 检查索引是否需要更新（列不同）
			existingIdx := existingIndexes[expectedIdx.IndexName]
			if !indexEqual(existingIdx, expectedIdx) {
				// 先删除旧索引，再添加新索引
				dropSQL, err := strategy.GenerateDropIndexSQL(tableName, existingIdx.IndexName)
				if err != nil {
					LogError("生成删除索引SQL失败: 表=%s, 索引=%s, 错误=%v", tableName, existingIdx.IndexName, err)
					continue
				}
				_, err = db.DataSource.Exec(dropSQL)
				if err != nil {
					LogError("删除旧索引失败: 表=%s, 索引=%s, 错误=%v", tableName, existingIdx.IndexName, err)
					continue
				}
				LogInfo("删除旧索引: 表=%s, 索引=%s", tableName, existingIdx.IndexName)
				indexesToAdd = append(indexesToAdd, expectedIdx)
			}
		}
	}

	// 找出需要删除的索引
	indexesToDelete := make([]string, 0)
	for existingName := range existingIndexes {
		if _, exists := expectedIndexMap[existingName]; !exists {
			indexesToDelete = append(indexesToDelete, existingName)
		}
	}

	// 添加索引
	if len(indexesToAdd) > 0 && permissions.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
		for _, idx := range indexesToAdd {
			createSQL, err := strategy.GenerateCreateIndexSQL(tableName, idx)
			if err != nil {
				LogError("生成创建索引SQL失败: 表=%s, 索引=%s, 错误=%v", tableName, idx.IndexName, err)
				continue
			}

			_, err = db.DataSource.Exec(createSQL)
			if err != nil {
				LogError("创建索引失败: 表=%s, 索引=%s, 错误=%v", tableName, idx.IndexName, err)
			} else {
				LogInfo("创建索引成功: 表=%s, 索引=%s, 列=%v", tableName, idx.IndexName, idx.Columns)
			}
		}
	}

	// 删除索引
	if len(indexesToDelete) > 0 && permissions.IsAllowed(EnumAutoDbOperateTypeDeleteColumn) {
		for _, indexName := range indexesToDelete {
			dropSQL, err := strategy.GenerateDropIndexSQL(tableName, indexName)
			if err != nil {
				LogError("生成删除索引SQL失败: 表=%s, 索引=%s, 错误=%v", tableName, indexName, err)
				continue
			}

			_, err = db.DataSource.Exec(dropSQL)
			if err != nil {
				LogError("删除索引失败: 表=%s, 索引=%s, 错误=%v", tableName, indexName, err)
			} else {
				LogInfo("删除索引成功: 表=%s, 索引=%s", tableName, indexName)
			}
		}
	}

	if len(indexesToAdd) > 0 || len(indexesToDelete) > 0 {
		LogInfo("索引迁移完成: 表=%s, 添加索引=%d, 删除索引=%d", tableName, len(indexesToAdd), len(indexesToDelete))
	}

	return nil
}

// indexEqual 比较两个索引是否相等（索引名和列相同）。
func indexEqual(idx1, idx2 *IndexMetaData) bool {
	if idx1 == nil || idx2 == nil {
		return false
	}
	if idx1.IndexName != idx2.IndexName {
		return false
	}
	if len(idx1.Columns) != len(idx2.Columns) {
		return false
	}
	for i, col := range idx1.Columns {
		if col != idx2.Columns[i] {
			return false
		}
	}
	return true
}

// getEntityColumns 获取实体的所有列。
func (cm *CrudManager) getEntityColumns(t reflect.Type) map[string]reflect.StructField {
	columns := make(map[string]reflect.StructField)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("db")

		// 跳过没有 db 标签或标记为忽略的字段
		if tag == "" || tag == "-" {
			continue
		}

		// 解析列名
		tagParts := strings.Split(tag, ",")
		columnName := strings.TrimSpace(tagParts[0])
		if columnName == "" || columnName == "-" {
			continue
		}

		// 检查是否有 skip 选项
		skip := false
		for _, part := range tagParts[1:] {
			if strings.TrimSpace(part) == "skip" {
				skip = true
				break
			}
		}

		if !skip {
			columns[columnName] = field
		}
	}

	return columns
}

// AutoMigrateAllTablesConcurrently 并发迁移所有表。
func (cm *CrudManager) AutoMigrateAllTablesConcurrently(db *Db, entityTypes []any, permissions *AutoDbPermission) error {
	if permissions == nil {
		permissions = NewSafeAutoDbPermission()
	}

	// 使用新的并发迁移管理器
	config := &ConcurrentMigrationConfig{
		MaxConcurrency:   10,
		Permission:       permissions,
		EnableConcurrent: true,
	}

	migrationManager := NewConcurrentMigrationManager(config)
	results := migrationManager.MigrateTablesBatch(db, entityTypes)

	// 检查失败的任务
	failedCount := 0
	for tableName, err := range results {
		if err != nil {
			failedCount++
			LogError("迁移失败: 表=%s, 错误=%v", tableName, err)
		}
	}

	if failedCount > 0 {
		return fmt.Errorf("并发迁移完成，但有 %d 个任务失败", failedCount)
	}

	return nil
}
