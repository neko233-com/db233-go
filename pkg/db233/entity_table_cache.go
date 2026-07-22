package db233

import (
	"reflect"
	"sync"
)

// entityTableNameCache 实体类型 -> 表名（避免热路径重复反射）。
var entityTableNameCache sync.Map // reflect.Type -> string

// ResolveEntityTableName 解析实体表名并缓存（Session 读路径热路径）。
func ResolveEntityTableName(entity IDbEntity) string {
	if isNilStrictValue(entity) {
		return ""
	}
	entityType := reflect.TypeOf(entity)
	if cached, ok := entityTableNameCache.Load(entityType); ok {
		return cached.(string)
	}
	tableName := entity.TableName()
	if tableName == "" {
		baseType := entityType
		for baseType.Kind() == reflect.Ptr {
			baseType = baseType.Elem()
		}
		tableName = StringUtilsInstance.CamelToSnake(baseType.Name())
	}
	entityTableNameCache.Store(entityType, tableName)
	return tableName
}

// ClearEntityTableNameCache 清空表名缓存（测试用）。
func ClearEntityTableNameCache() {
	entityTableNameCache.Clear()
}
