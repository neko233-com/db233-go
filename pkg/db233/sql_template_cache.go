package db233

import (
	"reflect"
	"sync"
)

const defaultSQLTemplateCacheCapacity = 1024

type sqlTemplateCacheKey struct {
	entityType reflect.Type
	tableName  string
	uidColumn  string
}

// SqlTemplateCache 缓存实体级 SQL 模板，减少高频读路径字符串拼接。
type SqlTemplateCache struct {
	findById      map[sqlTemplateCacheKey]string
	insertionKeys []sqlTemplateCacheKey
	nextEvict     int
	capacity      int
	mu            sync.RWMutex
}

var (
	sqlTemplateCacheInstance *SqlTemplateCache
	sqlTemplateCacheOnce     sync.Once
)

// GetSqlTemplateCache 获取 SQL 模板缓存单例。
func GetSqlTemplateCache() *SqlTemplateCache {
	sqlTemplateCacheOnce.Do(func() {
		sqlTemplateCacheInstance = newSqlTemplateCache(defaultSQLTemplateCacheCapacity)
	})
	return sqlTemplateCacheInstance
}

func newSqlTemplateCache(capacity int) *SqlTemplateCache {
	if capacity <= 0 {
		capacity = defaultSQLTemplateCacheCapacity
	}
	return &SqlTemplateCache{
		findById: make(map[sqlTemplateCacheKey]string),
		capacity: capacity,
	}
}

// GetFindByIdSQL 获取 FindById SQL 模板：SELECT * FROM {table} WHERE {pk} = ?
func (c *SqlTemplateCache) GetFindByIdSQL(entityType IDbEntity, tableName, uidColumn string) string {
	key := sqlTemplateCacheKey{
		entityType: reflectTypeOfEntity(entityType),
		tableName:  tableName,
		uidColumn:  uidColumn,
	}

	c.mu.RLock()
	if sql, ok := c.findById[key]; ok {
		c.mu.RUnlock()
		return sql
	}
	c.mu.RUnlock()

	sql := "SELECT * FROM " + tableName + " WHERE " + uidColumn + " = ?"

	c.mu.Lock()
	defer c.mu.Unlock()

	// Misses may race. Recheck under the write lock before inserting.
	if cached, ok := c.findById[key]; ok {
		return cached
	}
	c.ensureInitializedLocked()
	if len(c.findById) >= c.capacity {
		victim := c.insertionKeys[c.nextEvict]
		delete(c.findById, victim)
		c.insertionKeys[c.nextEvict] = key
		c.nextEvict++
		if c.nextEvict == len(c.insertionKeys) {
			c.nextEvict = 0
		}
	} else {
		c.insertionKeys = append(c.insertionKeys, key)
	}
	c.findById[key] = sql
	return sql
}

func (c *SqlTemplateCache) ensureInitializedLocked() {
	if c.capacity <= 0 {
		c.capacity = defaultSQLTemplateCacheCapacity
	}
	if c.findById == nil {
		c.findById = make(map[sqlTemplateCacheKey]string)
	}
}

// Clear 清除所有 SQL 模板缓存。
func (c *SqlTemplateCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.findById = make(map[sqlTemplateCacheKey]string)
	c.insertionKeys = nil
	c.nextEvict = 0
}

func reflectTypeOfEntity(entity IDbEntity) reflect.Type {
	t := reflect.TypeOf(entity)
	if t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}
