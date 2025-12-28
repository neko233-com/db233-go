package db233

import (
	"reflect"
	"strings"
	"sync"
)

/**
 * EntityCacheManager - 实体缓存管理器
 *
 * 对应 Kotlin 版本的 EntityCacheManager
 * 缓存实体的元数据信息，如列名、SQL等
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type EntityCacheManager struct {
	// 类型到选择列名SQL的映射
	typeToSelectColumnNameSqlMap map[reflect.Type]string

	// 类型到所有列名CSV的映射
	typeToAllColumnNameCsvMap map[reflect.Type]string

	// 读写锁
	mu sync.RWMutex
}

/**
 * 单例实例
 */
var entityCacheManagerInstance *EntityCacheManager
var entityCacheManagerOnce sync.Once

/**
 * 获取单例实例
 */
func GetEntityCacheManagerInstance() *EntityCacheManager {
	entityCacheManagerOnce.Do(func() {
		entityCacheManagerInstance = &EntityCacheManager{
			typeToSelectColumnNameSqlMap: make(map[reflect.Type]string),
			typeToAllColumnNameCsvMap:    make(map[reflect.Type]string),
		}
	})
	return entityCacheManagerInstance
}

/**
 * 获取或创建选择列名CSV
 */
func (ecm *EntityCacheManager) GetOrCreateSelectColumnNameCsv(entityType reflect.Type, colNameToValueMap map[string]interface{}) string {
	ecm.mu.Lock()
	defer ecm.mu.Unlock()

	if cached, exists := ecm.typeToSelectColumnNameSqlMap[entityType]; exists {
		return cached
	}

	// 构建列名字符串
	var columnNames []string
	for colName := range colNameToValueMap {
		columnNames = append(columnNames, colName)
	}

	result := strings.Join(columnNames, ",")
	ecm.typeToSelectColumnNameSqlMap[entityType] = result

	return result
}

/**
 * 获取或创建所有列名CSV
 */
func (ecm *EntityCacheManager) GetOrCreateAllColumnNameCsv(entityType reflect.Type, columnNameCreator func() []string) string {
	ecm.mu.Lock()
	defer ecm.mu.Unlock()

	if cached, exists := ecm.typeToAllColumnNameCsvMap[entityType]; exists {
		return cached
	}

	columnNames := columnNameCreator()
	result := strings.Join(columnNames, ",")

	ecm.typeToAllColumnNameCsvMap[entityType] = result
	return result
}

/**
 * 获取缓存的列名SQL
 */
func (ecm *EntityCacheManager) GetSelectColumnNameSql(entityType reflect.Type) (string, bool) {
	ecm.mu.RLock()
	defer ecm.mu.RUnlock()

	sql, exists := ecm.typeToSelectColumnNameSqlMap[entityType]
	return sql, exists
}

/**
 * 获取缓存的所有列名CSV
 */
func (ecm *EntityCacheManager) GetAllColumnNameCsv(entityType reflect.Type) (string, bool) {
	ecm.mu.RLock()
	defer ecm.mu.RUnlock()

	csv, exists := ecm.typeToAllColumnNameCsvMap[entityType]
	return csv, exists
}

/**
 * 清除指定类型的缓存
 */
func (ecm *EntityCacheManager) ClearCache(entityType reflect.Type) {
	ecm.mu.Lock()
	defer ecm.mu.Unlock()

	delete(ecm.typeToSelectColumnNameSqlMap, entityType)
	delete(ecm.typeToAllColumnNameCsvMap, entityType)
}

/**
 * 清除所有缓存
 */
func (ecm *EntityCacheManager) ClearAllCache() {
	ecm.mu.Lock()
	defer ecm.mu.Unlock()

	ecm.typeToSelectColumnNameSqlMap = make(map[reflect.Type]string)
	ecm.typeToAllColumnNameCsvMap = make(map[reflect.Type]string)
}

/**
 * 获取缓存大小
 */
func (ecm *EntityCacheManager) GetCacheSize() (selectCacheSize, allColumnCacheSize int) {
	ecm.mu.RLock()
	defer ecm.mu.RUnlock()

	return len(ecm.typeToSelectColumnNameSqlMap), len(ecm.typeToAllColumnNameCsvMap)
}
