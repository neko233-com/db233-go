package db233

import (
	"reflect"
	"strings"
	"sync"
)

/**
 * CrudManager - CRUD 管理器
 *
 * 管理实体类的元数据，包括表结构、列信息、主键等
 *
 * @author SolarisNeko
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
 * 获取表到主键到列值的映射
 */
func (cm *CrudManager) GetTableToPkToColValueMap() map[string]map[interface{}]map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make(map[string]map[interface{}]map[string]interface{})
	for k, v := range cm.tableToPkToColValueMap {
		result[k] = make(map[interface{}]map[string]interface{})
		for k2, v2 := range v {
			result[k][k2] = make(map[string]interface{})
			for k3, v3 := range v2 {
				result[k][k2][k3] = v3
			}
		}
	}
	return result
}
