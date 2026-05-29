package db233

import (
	"reflect"
	"sync"
)

// SqlTemplateCache 缓存实体级 SQL 模板，减少高频读路径字符串拼接。
type SqlTemplateCache struct {
	findById map[reflect.Type]string
	mu       sync.RWMutex
}

var (
	sqlTemplateCacheInstance *SqlTemplateCache
	sqlTemplateCacheOnce     sync.Once
)

// GetSqlTemplateCache 获取 SQL 模板缓存单例。
func GetSqlTemplateCache() *SqlTemplateCache {
	sqlTemplateCacheOnce.Do(func() {
		sqlTemplateCacheInstance = &SqlTemplateCache{
			findById: make(map[reflect.Type]string),
		}
	})
	return sqlTemplateCacheInstance
}

// GetFindByIdSQL 获取 FindById SQL 模板：SELECT * FROM {table} WHERE {pk} = ?
func (c *SqlTemplateCache) GetFindByIdSQL(entityType IDbEntity, tableName, uidColumn string) string {
	t := reflectTypeOfEntity(entityType)

	c.mu.RLock()
	if sql, ok := c.findById[t]; ok {
		c.mu.RUnlock()
		return sql
	}
	c.mu.RUnlock()

	sql := "SELECT * FROM " + tableName + " WHERE " + uidColumn + " = ?"

	c.mu.Lock()
	c.findById[t] = sql
	c.mu.Unlock()
	return sql
}

// Clear 清除所有 SQL 模板缓存。
func (c *SqlTemplateCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.findById = make(map[reflect.Type]string)
}

func reflectTypeOfEntity(entity IDbEntity) reflect.Type {
	t := reflect.TypeOf(entity)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}
