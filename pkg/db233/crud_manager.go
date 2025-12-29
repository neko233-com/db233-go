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
 * 获取表名
 */
func (cm *CrudManager) GetTableName(t reflect.Type) string {
	// 检查是否有 table tag
	if t.Kind() == reflect.Struct {
		if tableTag := t.Field(0).Tag.Get("table"); tableTag != "" {
			return tableTag
		}
	}
	// 默认使用类型名转换为 snake_case
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
