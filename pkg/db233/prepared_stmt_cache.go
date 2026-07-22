package db233

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PreparedStmtCache 按 (DB + SQL) 缓存 *sql.Stmt，减少高 QPS 下重复 Parse。
type PreparedStmtCache struct {
	mu        sync.Mutex
	maxSize   int
	ttl       time.Duration
	entries   map[preparedStmtCacheKey]*preparedStmtEntry
	lru       *list.List
	lruIndex  map[preparedStmtCacheKey]*list.Element
	preparing map[preparedStmtCacheKey]*preparedStmtPrepareCall
	dbEpoch   map[*sql.DB]uint64
	// backgroundErr retains close failures produced by eviction/release paths
	// whose compatibility signatures cannot return an error.
	backgroundErr error
}

type preparedStmtCacheKey struct {
	db      *sql.DB
	sqlText string
}

type preparedStmtEntry struct {
	key      preparedStmtCacheKey
	db       *sql.DB
	stmt     *sql.Stmt
	created  time.Time
	lastUsed time.Time
	refs     int
	retired  bool
	pinned   bool
}

type preparedStmtPrepareCall struct {
	db   *sql.DB
	done chan struct{}
	err  error
}

var (
	errPreparedStmtCacheInvalidated = errors.New("prepared stmt cache 已失效")
	// ErrPreparedStmtCacheFull 表示所有缓存项均被旧 GetStmt API 固定，无法安全淘汰。
	ErrPreparedStmtCacheFull = errors.New("prepared stmt cache 已满且没有可安全淘汰的语句")
)

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
		maxSize:   maxSize,
		ttl:       ttl,
		entries:   make(map[preparedStmtCacheKey]*preparedStmtEntry),
		lru:       list.New(),
		lruIndex:  make(map[preparedStmtCacheKey]*list.Element),
		preparing: make(map[preparedStmtCacheKey]*preparedStmtPrepareCall),
		dbEpoch:   make(map[*sql.DB]uint64),
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
		if !c.evictOldestLocked() {
			break
		}
	}
}

func preparedStmtKey(db *sql.DB, sqlText string) preparedStmtCacheKey {
	return preparedStmtCacheKey{db: db, sqlText: sqlText}
}

// GetStmt 获取或创建预编译语句。返回的语句会固定到 Clear、RemoveDB 或 Db.Close，
// 避免并发淘汰关闭调用方仍在使用的 *sql.Stmt。语句生命周期归缓存管理，
// 调用方不得自行 Close。高基数动态 SQL 应优先使用 AcquireStmtContext，
// 并在执行结束后调用 release。
func (c *PreparedStmtCache) GetStmt(db *sql.DB, sqlText string) (*sql.Stmt, error) {
	key := preparedStmtKey(db, sqlText)
	for {
		stmt, release, err := c.acquireStmtContext(context.Background(), db, sqlText)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		entryElement := c.lruIndex[key]
		if entryElement != nil {
			entry := entryElement.Value.(*preparedStmtEntry)
			if !entry.retired && entry.stmt == stmt {
				entry.pinned = true
				c.mu.Unlock()
				release()
				return stmt, nil
			}
		}
		c.mu.Unlock()
		// 条目可能在 acquire 返回后被并发淘汰；释放旧 lease 后重试，
		// 绝不把即将关闭的 Stmt 交给调用方。
		release()
	}
}

// AcquireStmtContext 获取带租约的预编译语句。调用方必须在 Query/Exec 返回后调用 release。
func (c *PreparedStmtCache) AcquireStmtContext(
	ctx context.Context,
	db *sql.DB,
	sqlText string,
) (*sql.Stmt, func(), error) {
	return c.acquireStmtContext(ctx, db, sqlText)
}

func (c *PreparedStmtCache) acquireStmtContext(ctx context.Context, db *sql.DB, sqlText string) (*sql.Stmt, func(), error) {
	if db == nil || sqlText == "" {
		return nil, func() {}, fmt.Errorf("prepared stmt: db 或 sql 为空")
	}
	if ctx == nil {
		return nil, func() {}, NewValidationException("prepared stmt context 不能为空")
	}
	key := preparedStmtKey(db, sqlText)
	for {
		now := time.Now()
		c.mu.Lock()
		c.ensureInitializedLocked()
		if el, ok := c.lruIndex[key]; ok {
			entry := el.Value.(*preparedStmtEntry)
			if (entry.pinned || now.Sub(entry.created) <= c.ttl) && !entry.retired {
				c.lru.MoveToBack(el)
				entry.lastUsed = now
				entry.refs++
				c.mu.Unlock()
				return entry.stmt, c.releaseFunc(entry), nil
			}
			c.recordBackgroundErrorLocked(c.removeEntryLocked(key, el))
		}

		if call := c.preparing[key]; call != nil {
			done := call.done
			c.mu.Unlock()
			select {
			case <-done:
				if call.err != nil && !errors.Is(call.err, context.Canceled) && !errors.Is(call.err, context.DeadlineExceeded) {
					return nil, func() {}, call.err
				}
				continue
			case <-ctx.Done():
				return nil, func() {}, context.Cause(ctx)
			}
		}

		epoch := c.dbEpoch[db]
		call := &preparedStmtPrepareCall{db: db, done: make(chan struct{})}
		c.preparing[key] = call
		c.mu.Unlock()

		stmt, prepareErr := db.PrepareContext(ctx, sqlText)
		insertedAt := time.Now()

		c.mu.Lock()
		delete(c.preparing, key)
		if prepareErr == nil && c.dbEpoch[db] != epoch {
			closeErr := stmt.Close()
			stmt = nil
			prepareErr = errors.Join(errPreparedStmtCacheInvalidated, closeErr)
		}
		if prepareErr == nil {
			c.evictExpiredLocked()
			for c.lru.Len() >= c.maxSize {
				if !c.evictOldestLocked() {
					closeErr := stmt.Close()
					stmt = nil
					prepareErr = errors.Join(ErrPreparedStmtCacheFull, closeErr)
					break
				}
			}
		}
		if prepareErr == nil {
			entry := &preparedStmtEntry{
				key: key, db: db, stmt: stmt, created: insertedAt, lastUsed: insertedAt, refs: 1,
			}
			el := c.lru.PushBack(entry)
			c.entries[key] = entry
			c.lruIndex[key] = el
			call.err = nil
			close(call.done)
			c.mu.Unlock()
			return stmt, c.releaseFunc(entry), nil
		}
		call.err = prepareErr
		close(call.done)
		c.cleanupDBEpochLocked(db)
		c.mu.Unlock()
		return nil, func() {}, prepareErr
	}
}

func (c *PreparedStmtCache) releaseFunc(entry *preparedStmtEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			if entry.refs > 0 {
				entry.refs--
			}
			if entry.retired && entry.refs == 0 && entry.stmt != nil {
				c.recordBackgroundErrorLocked(entry.stmt.Close())
				entry.stmt = nil
			}
			c.cleanupDBEpochLocked(entry.db)
			c.mu.Unlock()
		})
	}
}

// RemoveDB 关闭并移除指定连接池关联的全部预编译语句。
// 兼容入口会安全记录 Close 错误；严格关闭路径应调用 RemoveDBStrict。
func (c *PreparedStmtCache) RemoveDB(db *sql.DB) {
	if err := c.RemoveDBStrict(db); err != nil {
		LogError("移除数据库预编译缓存失败: %s", safeErrorForLog(err))
	}
}

// RemoveDBStrict 移除指定连接池的全部语句并返回完整 Close 错误链。
func (c *PreparedStmtCache) RemoveDBStrict(db *sql.DB) error {
	if c == nil || db == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitializedLocked()
	c.dbEpoch[db]++
	var closeErrors []error
	for el := c.lru.Front(); el != nil; {
		next := el.Next()
		entry := el.Value.(*preparedStmtEntry)
		if entry.db == db {
			if closeErr := c.removeEntryLocked(entry.key, el); closeErr != nil {
				closeErrors = append(closeErrors, closeErr)
			}
		}
		el = next
	}
	c.cleanupDBEpochLocked(db)
	return errors.Join(closeErrors...)
}

func (c *PreparedStmtCache) evictExpiredLocked() {
	now := time.Now()
	var remove []*list.Element
	for el := c.lru.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*preparedStmtEntry)
		if !entry.pinned && now.Sub(entry.created) > c.ttl {
			remove = append(remove, el)
		}
	}
	for _, el := range remove {
		entry := el.Value.(*preparedStmtEntry)
		c.recordBackgroundErrorLocked(c.removeEntryLocked(entry.key, el))
	}
}

func (c *PreparedStmtCache) evictOldestLocked() bool {
	if c.lru.Len() == 0 {
		return false
	}
	for el := c.lru.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*preparedStmtEntry)
		if entry.pinned {
			continue
		}
		c.recordBackgroundErrorLocked(c.removeEntryLocked(entry.key, el))
		return true
	}
	return false
}

func (c *PreparedStmtCache) removeEntryLocked(key preparedStmtCacheKey, el *list.Element) error {
	var closeErr error
	if entry, ok := c.entries[key]; ok {
		entry.retired = true
		if entry.refs == 0 && entry.stmt != nil {
			closeErr = entry.stmt.Close()
			entry.stmt = nil
		}
	}
	delete(c.entries, key)
	delete(c.lruIndex, key)
	c.lru.Remove(el)
	return closeErr
}

// Clear 清空缓存（测试用）。
func (c *PreparedStmtCache) Clear() {
	if err := c.ClearStrict(); err != nil {
		LogError("清空预编译缓存失败: %s", safeErrorForLog(err))
	}
}

// ClearStrict 清空缓存并传播已关闭语句的 Close 错误。
func (c *PreparedStmtCache) ClearStrict() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitializedLocked()
	dbs := make(map[*sql.DB]struct{})
	var closeErrors []error
	for _, entry := range c.entries {
		dbs[entry.db] = struct{}{}
		entry.retired = true
		c.dbEpoch[entry.db]++
		if entry.refs == 0 && entry.stmt != nil {
			if closeErr := entry.stmt.Close(); closeErr != nil {
				closeErrors = append(closeErrors, closeErr)
			}
			entry.stmt = nil
		}
	}
	for _, call := range c.preparing {
		dbs[call.db] = struct{}{}
		c.dbEpoch[call.db]++
	}
	c.entries = make(map[preparedStmtCacheKey]*preparedStmtEntry)
	c.lruIndex = make(map[preparedStmtCacheKey]*list.Element)
	c.lru.Init()
	for db := range dbs {
		c.cleanupDBEpochLocked(db)
	}
	return errors.Join(closeErrors...)
}

// BackgroundError reports statement Close failures from compatibility-only
// eviction/release paths. It never clears history.
func (c *PreparedStmtCache) BackgroundError() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backgroundErr
}

func (c *PreparedStmtCache) recordBackgroundErrorLocked(err error) {
	if err != nil {
		c.backgroundErr = errors.Join(c.backgroundErr, err)
	}
}

func (c *PreparedStmtCache) ensureInitializedLocked() {
	if c.maxSize <= 0 {
		c.maxSize = 256
	}
	if c.ttl <= 0 {
		c.ttl = 10 * time.Minute
	}
	if c.entries == nil {
		c.entries = make(map[preparedStmtCacheKey]*preparedStmtEntry)
	}
	if c.lru == nil {
		c.lru = list.New()
	}
	if c.lruIndex == nil {
		c.lruIndex = make(map[preparedStmtCacheKey]*list.Element)
	}
	if c.preparing == nil {
		c.preparing = make(map[preparedStmtCacheKey]*preparedStmtPrepareCall)
	}
	if c.dbEpoch == nil {
		c.dbEpoch = make(map[*sql.DB]uint64)
	}
}

func (c *PreparedStmtCache) cleanupDBEpochLocked(db *sql.DB) {
	if db == nil {
		return
	}
	for _, entry := range c.entries {
		if entry.db == db {
			return
		}
	}
	for _, call := range c.preparing {
		if call.db == db {
			return
		}
	}
	delete(c.dbEpoch, db)
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
