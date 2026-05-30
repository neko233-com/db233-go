package db233

import (
	"sync"
)

// entityTableNameCache 实体类型 -> 表名（避免热路径重复反射）。
var entityTableNameCache sync.Map // string -> string

// ResolveEntityTableName 解析实体表名并缓存（Session 读路径热路径）。
func ResolveEntityTableName(entity IDbEntity) string {
	if entity == nil {
		return ""
	}
	typeName := EntityTypeName(entity)
	if cached, ok := entityTableNameCache.Load(typeName); ok {
		return cached.(string)
	}
	tableName := entity.TableName()
	if tableName == "" {
		tableName = StringUtilsInstance.CamelToSnake(typeName)
	}
	entityTableNameCache.Store(typeName, tableName)
	return tableName
}

// ClearEntityTableNameCache 清空表名缓存（测试用）。
func ClearEntityTableNameCache() {
	entityTableNameCache = sync.Map{}
}
