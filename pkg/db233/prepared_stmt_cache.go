package db233

import (
	"container/list"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// PreparedStmtCache 按 (DB + SQL) 缓存 *sql.Stmt，减少高 QPS 下重复 Parse。
type PreparedStmtCache struct {
	mu       sync.Mutex
	maxSize  int
	ttl      time.Duration
	entries  map[string]*preparedStmtEntry
	lru      *list.List
	lruIndex map[string]*list.Element
}

type preparedStmtEntry struct {
	key      string
	stmt     *sql.Stmt
	created  time.Time
	lastUsed time.Time
}

var (
	preparedStmtCacheInstance *PreparedStmtCache
	preparedStmtCacheOnce     sync.Once
)

// GetPreparedStmtCache 获取预编译语句缓存单例。
func GetPreparedStmtCache() *PreparedStmtCache {
	preparedStmtCacheOnce.Do(func() {
		def := DefaultCrudPerformanceSettings()
		preparedStmtCacheInstance = NewPreparedStmtCache(def.StmtCacheSize, time.Duration(def.StmtCacheTTLSeconds)*time.Second)
	})
	return preparedStmtCacheInstance
}

// NewPreparedStmtCache 创建预编译缓存（测试用）。
func NewPreparedStmtCache(maxSize int, ttl time.Duration) *PreparedStmtCache {
	if maxSize <= 0 {
		maxSize = 256
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &PreparedStmtCache{
		maxSize:  maxSize,
		ttl:      ttl,
		entries:  make(map[string]*preparedStmtEntry),
		lru:      list.New(),
		lruIndex: make(map[string]*list.Element),
	}
}

// ConfigureFromSettings 按性能配置调整容量与 TTL。
func (c *PreparedStmtCache) ConfigureFromSettings(s CrudPerformanceSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.StmtCacheSize > 0 {
		c.maxSize = s.StmtCacheSize
	}
	if s.StmtCacheTTLSeconds > 0 {
		c.ttl = time.Duration(s.StmtCacheTTLSeconds) * time.Second
	}
	c.evictExpiredLocked()
	for c.lru.Len() > c.maxSize {
		c.evictOldestLocked()
	}
}

func preparedStmtKey(db *sql.DB, sqlText string) string {
	return fmt.Sprintf("%p|%s", db, sqlText)
}

// GetStmt 获取或创建预编译语句。
func (c *PreparedStmtCache) GetStmt(db *sql.DB, sqlText string) (*sql.Stmt, error) {
	if db == nil || sqlText == "" {
		return nil, fmt.Errorf("prepared stmt: db 或 sql 为空")
	}
	key := preparedStmtKey(db, sqlText)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.lruIndex[key]; ok {
		entry := el.Value.(*preparedStmtEntry)
		if now.Sub(entry.created) <= c.ttl {
			c.lru.MoveToBack(el)
			entry.lastUsed = now
			return entry.stmt, nil
		}
		c.removeEntryLocked(key, el)
	}

	c.evictExpiredLocked()
	for c.lru.Len() >= c.maxSize {
		c.evictOldestLocked()
	}

	stmt, err := db.Prepare(sqlText)
	if err != nil {
		return nil, err
	}
	entry := &preparedStmtEntry{key: key, stmt: stmt, created: now, lastUsed: now}
	el := c.lru.PushBack(entry)
	c.entries[key] = entry
	c.lruIndex[key] = el
	return stmt, nil
}

func (c *PreparedStmtCache) evictExpiredLocked() {
	now := time.Now()
	var remove []*list.Element
	for el := c.lru.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*preparedStmtEntry)
		if now.Sub(entry.created) > c.ttl {
			remove = append(remove, el)
		}
	}
	for _, el := range remove {
		entry := el.Value.(*preparedStmtEntry)
		c.removeEntryLocked(entry.key, el)
	}
}

func (c *PreparedStmtCache) evictOldestLocked() {
	if c.lru.Len() == 0 {
		return
	}
	el := c.lru.Front()
	entry := el.Value.(*preparedStmtEntry)
	c.removeEntryLocked(entry.key, el)
}

func (c *PreparedStmtCache) removeEntryLocked(key string, el *list.Element) {
	if entry, ok := c.entries[key]; ok {
		_ = entry.stmt.Close()
	}
	delete(c.entries, key)
	delete(c.lruIndex, key)
	c.lru.Remove(el)
}

// Clear 清空缓存（测试用）。
func (c *PreparedStmtCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		_ = entry.stmt.Close()
	}
	c.entries = make(map[string]*preparedStmtEntry)
	c.lruIndex = make(map[string]*list.Element)
	c.lru.Init()
}

// Len 当前缓存条目数。
func (c *PreparedStmtCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func initPreparedStmtCacheOnChange() {
	GetCrudPerformanceSettings().OnChange(func(s CrudPerformanceSettings) {
		GetPreparedStmtCache().ConfigureFromSettings(s)
	})
}

func init() {
	initPreparedStmtCacheOnChange()
}
