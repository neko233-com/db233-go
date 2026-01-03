package db233

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

/**
 * IDbEntity - 数据库实体接口
 *
 * 所有数据库实体必须实现此接口，提供自定义表名
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type IDbEntity interface {
	/**
	 * 获取表名
	 *
	 * @return string 表名
	 */
	TableName() string

	/**
	 * 获取数据库唯一ID列名
	 * 如果返回空字符串，则使用默认主键列（通常是 "id"）
	 *
	 * @return string 唯一ID列名，如果为空则使用默认主键
	 */
	GetDbUid() string

	/**
	 * 保存到数据库前的序列化钩子
	 * 在数据保存到数据库之前调用，可以用于数据转换、加密等操作
	 * 此方法在 Save 和 Update 操作前调用
	 */
	SerializeBeforeSaveDb()

	/**
	 * 从数据库加载后的反序列化钩子
	 * 在数据从数据库加载后调用，可以用于数据转换、解密等操作
	 * 此方法在 FindById、FindAll、FindByCondition 等查询操作后调用
	 */
	DeserializeAfterLoadDb()
}

/**
 * CrudManager - CRUD 管理器
 *
 * 管理实体类的元数据，包括表结构、列信息、主键等
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type CrudManager struct {
	// tableName 到主键列名列表的映射
	tableNamePkColNameListMap map[string][]string

	// tableName 到所有列名的映射
	tableNameToColNameMap map[string][]string

	// tableName -> pk对象 -> colName -> value 的映射
	tableToPkToColValueMap map[string]map[interface{}]map[string]interface{}

	// 已扫描过的类集合
	metadataClassSet map[reflect.Type]bool

	// 锁
	mu sync.RWMutex
}

var crudManagerInstance *CrudManager
var crudManagerOnce sync.Once

/**
 * 获取单例实例
 */
func GetCrudManagerInstance() *CrudManager {
	crudManagerOnce.Do(func() {
		crudManagerInstance = &CrudManager{
			tableNamePkColNameListMap: make(map[string][]string),
			tableNameToColNameMap:     make(map[string][]string),
			tableToPkToColValueMap:    make(map[string]map[interface{}]map[string]interface{}),
			metadataClassSet:          make(map[reflect.Type]bool),
		}
	})
	return crudManagerInstance
}

/**
 * 自动初始化实体
 */
func (cm *CrudManager) AutoInitEntity(entityType interface{}) *CrudManager {
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

/**
 * 检查实体注解（Go 中使用 struct tag）
 */
func (cm *CrudManager) checkEntityAnnotation(t reflect.Type) error {
	// Go 中没有注解，但我们可以使用 struct tag
	// 这里简化处理，假设所有 struct 都是实体
	return nil
}

/**
 * 初始化实体类元数据
 */
func (cm *CrudManager) initEntityClassMetadata(entityTypes []reflect.Type) {
	cm.initTableColumnMetadataByClass(entityTypes)
	cm.initTablePrimaryKeyMetadataByClass(entityTypes)
}

/**
 * 懒初始化或抛出错误
 */
func (cm *CrudManager) AutoLazyInitOrThrowError(obj interface{}) error {
	if reflect.TypeOf(obj).Kind() == reflect.Ptr && reflect.TypeOf(obj).Elem().Kind() == reflect.Interface {
		return NewDb233Exception("对象类型错误，不能是接口")
	}

	if cm.IsContainsEntity(obj) {
		return nil
	}

	return cm.configClassLazy(obj)
}

/**
 * 配置类懒初始化
 */
func (cm *CrudManager) configClassLazy(obj interface{}) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.IsContainsEntity(obj) {
		return nil
	}

	t := reflect.TypeOf(obj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	cm.initEntityClassMetadata([]reflect.Type{t})
	return nil
}

/**
 * 是否不包含实体
 */
func (cm *CrudManager) IsNotContainsEntity(obj interface{}) bool {
	return !cm.IsContainsEntity(obj)
}

/**
 * 是否包含实体
 */
// IsContainsEntity 检查是否包含实体
func (cm *CrudManager) IsContainsEntity(obj interface{}) bool {
	t := reflect.TypeOf(obj)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return cm.metadataClassSet[t]
}

/**
 * 初始化表列元数据
 */
func (cm *CrudManager) initTableColumnMetadataByClass(entityTypes []reflect.Type) {
	for _, t := range entityTypes {
		tableName := cm.GetTableName(t)

		colList := make([]string, 0)

		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			dbTag := field.Tag.Get("db")
			if dbTag == "" || dbTag == "-" {
				continue
			}
			colName := cm.GetColumnName(field)
			colList = append(colList, colName)
		}

		cm.tableNameToColNameMap[tableName] = colList
	}
}

/**
 * 初始化表主键元数据
 */
func (cm *CrudManager) initTablePrimaryKeyMetadataByClass(entityTypes []reflect.Type) {
	for _, t := range entityTypes {
		tableName := cm.GetTableName(t)

		pkList := make([]string, 0)

		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			dbTag := field.Tag.Get("db")
			if dbTag == "" || dbTag == "-" {
				continue
			}
			if cm.IsPrimaryKey(field) {
				colName := cm.GetColumnName(field)
				pkList = append(pkList, colName)
			}
		}

		if len(pkList) > 0 {
			cm.tableNamePkColNameListMap[tableName] = pkList
		}
	}
}

/**
 * 获取表名（从 IDbEntity 接口）
 *
 * @param entity 实现了 IDbEntity 接口的实体
 * @return string 表名
 */
func (cm *CrudManager) GetTableNameFromEntity(entity IDbEntity) string {
	return entity.TableName()
}

/**
 * 获取表名（从 reflect.Type，内部会尝试创建实例并检查 IDbEntity 接口）
 *
 * @param t 实体类型
 * @return string 表名
 */
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

/**
 * 获取列名
 */
func (cm *CrudManager) GetColumnName(field reflect.StructField) string {
	if colTag := field.Tag.Get("column"); colTag != "" {
		return colTag
	}
	if field.Name == "ID" || field.Name == "Id" {
		return "id"
	}
	return StringUtilsInstance.CamelToSnake(field.Name)
}

/**
 * 是否为主键
 */
func (cm *CrudManager) IsPrimaryKey(field reflect.StructField) bool {
	return strings.Contains(field.Tag.Get("db"), "primary_key") ||
		field.Name == "ID" || field.Name == "Id"
}

/**
 * 获取表到主键列列表的映射
 */
func (cm *CrudManager) GetTableToPkColListMap() map[string][]string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make(map[string][]string)
	for k, v := range cm.tableNamePkColNameListMap {
		result[k] = append([]string(nil), v...)
	}
	return result
}

/**
 * 自动创建表
 */
func (cm *CrudManager) AutoCreateTable(db *Db, entityType interface{}) error {
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

	// 检查表是否已存在
	exists, err := cm.tableExists(db, tableName)
	if err != nil {
		return err
	}
	if exists {
		LogInfo("表已存在，跳过创建: %s", tableName)
		return nil
	}

	// 生成建表SQL
	createSQL, err := cm.generateCreateTableSQL(t)
	if err != nil {
		return err
	}

	// 执行建表
	_, err = db.DataSource.Exec(createSQL)
	if err != nil {
		return NewQueryExceptionWithCause(err, "创建表失败: "+tableName)
	}

	LogInfo("表创建成功: %s", tableName)
	return nil
}

/**
 * 检查表是否存在
 */
func (cm *CrudManager) tableExists(db *Db, tableName string) (bool, error) {
	query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
	row := db.DataSource.QueryRow(query, tableName)

	var count int
	err := row.Scan(&count)
	if err != nil {
		return false, NewQueryExceptionWithCause(err, "检查表存在性失败")
	}

	return count > 0, nil
}

/**
 * 生成建表SQL
 */
func (cm *CrudManager) generateCreateTableSQL(t reflect.Type) (string, error) {
	tableName := cm.GetTableName(t)
	if tableName == "" {
		return "", NewDb233Exception("无法获取表名")
	}

	var columns []string
	var primaryKeys []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		dbTag := field.Tag.Get("db")
		if dbTag == "" || dbTag == "-" {
			continue
		}

		colName := cm.GetColumnName(field)
		colType := cm.getSQLType(field)
		colDef := fmt.Sprintf("`%s` %s", colName, colType)

		// 检查是否自增
		if strings.Contains(field.Tag.Get("db"), "auto_increment") {
			colDef += " AUTO_INCREMENT"
		}

		// 检查是否可空
		if !strings.Contains(field.Tag.Get("db"), "not_null") && !cm.IsPrimaryKey(field) {
			colDef += " NULL"
		} else {
			colDef += " NOT NULL"
		}

		columns = append(columns, colDef)

		if cm.IsPrimaryKey(field) {
			primaryKeys = append(primaryKeys, fmt.Sprintf("`%s`", colName))
		}
	}

	if len(primaryKeys) > 0 {
		columns = append(columns, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(primaryKeys, ", ")))
	}

	createSQL := fmt.Sprintf("CREATE TABLE `%s` (\n\t%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci", tableName, strings.Join(columns, ",\n\t"))

	return createSQL, nil
}

/**
 * 获取SQL类型
 */
func (cm *CrudManager) getSQLType(field reflect.StructField) string {
	fieldType := field.Type

	// 检查tag中的类型定义
	if typeTag := field.Tag.Get("type"); typeTag != "" {
		return typeTag
	}

	switch fieldType.Kind() {
	case reflect.Int, reflect.Int32:
		return "INT"
	case reflect.Int64:
		return "BIGINT"
	case reflect.Float32:
		return "FLOAT"
	case reflect.Float64:
		return "DOUBLE"
	case reflect.String:
		size := 255
		if sizeTag := field.Tag.Get("size"); sizeTag != "" {
			if s, err := strconv.Atoi(sizeTag); err == nil {
				size = s
			}
		}
		return fmt.Sprintf("VARCHAR(%d)", size)
	case reflect.Bool:
		return "TINYINT(1)"
	case reflect.Struct:
		if fieldType == reflect.TypeOf(time.Time{}) {
			return "TIMESTAMP"
		}
	}

	return "VARCHAR(255)"
}

/**
 * 自动迁移表（创建或修改表结构）
 */
func (cm *CrudManager) AutoMigrateTable(db *Db, entityType interface{}) error {
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

	// 检查表是否已存在
	exists, err := cm.tableExists(db, tableName)
	if err != nil {
		return err
	}

	if !exists {
		// 表不存在，创建表
		return cm.AutoCreateTable(db, entityType)
	}

	// 表存在，检查并添加缺失的列
	return cm.alterTableAddMissingColumns(db, t)
}

/**
 * 修改表添加缺失的列
 */
func (cm *CrudManager) alterTableAddMissingColumns(db *Db, t reflect.Type) error {
	tableName := cm.GetTableName(t)
	if tableName == "" {
		return NewDb233Exception("无法获取表名")
	}

	// 获取现有列
	existingColumns, err := cm.getExistingColumns(db, tableName)
	if err != nil {
		return err
	}

	// 获取实体定义的列
	entityColumns := make(map[string]reflect.StructField)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		dbTag := field.Tag.Get("db")
		if dbTag == "" || dbTag == "-" {
			continue
		}
		colName := cm.GetColumnName(field)
		entityColumns[colName] = field
	}

	// 找出缺失的列
	var alterStatements []string
	for colName, field := range entityColumns {
		if _, exists := existingColumns[colName]; !exists {
			colType := cm.getSQLType(field)
			colDef := fmt.Sprintf("ADD COLUMN `%s` %s", colName, colType)

			// 检查是否自增
			if strings.Contains(field.Tag.Get("db"), "auto_increment") {
				colDef += " AUTO_INCREMENT"
			}

			// 检查是否可空
			if !strings.Contains(field.Tag.Get("db"), "not_null") && !cm.IsPrimaryKey(field) {
				colDef += " NULL"
			} else {
				colDef += " NOT NULL"
			}

			alterStatements = append(alterStatements, colDef)
		}
	}

	if len(alterStatements) == 0 {
		LogInfo("表结构已是最新: %s", tableName)
		return nil
	}

	// 执行ALTER TABLE
	alterSQL := fmt.Sprintf("ALTER TABLE `%s` %s", tableName, strings.Join(alterStatements, ", "))
	_, err = db.DataSource.Exec(alterSQL)
	if err != nil {
		return NewQueryExceptionWithCause(err, "修改表结构失败: "+tableName)
	}

	LogInfo("表结构更新成功: %s", tableName)
	return nil
}

/**
 * 获取现有表的列信息
 */
func (cm *CrudManager) getExistingColumns(db *Db, tableName string) (map[string]bool, error) {
	query := "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?"
	rows, err := db.DataSource.Query(query, tableName)
	if err != nil {
		return nil, NewQueryExceptionWithCause(err, "获取表列信息失败")
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var colName string
		if err := rows.Scan(&colName); err != nil {
			return nil, NewQueryExceptionWithCause(err, "扫描列名失败")
		}
		columns[colName] = true
	}

	return columns, nil
}
