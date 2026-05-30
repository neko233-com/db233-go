package db233

import (
	"fmt"
	"sync"
	"time"
)

// PlayerSession 玩家 Session L1 缓存：登录后内存驻留，读走内存，写走 dirty + 可选延迟刷库。
type PlayerSession struct {
	PlayerID string
	repo     *BaseCrudRepository
	owner    *SessionRepository

	mu                  sync.RWMutex
	entities            map[string]IDbEntity // tableName -> entity（正缓存）
	dirty               map[string]IDbEntity // tableName -> 待落库实体
	absentTables        map[string]struct{}  // 负缓存：已确认无记录（需开启负缓存）
	negativeCacheOverride *bool              // nil=跟随全局；非 nil=Session 级动态开关
	loaded              bool
}

func newPlayerSession(playerID string, repo *BaseCrudRepository, owner *SessionRepository) *PlayerSession {
	return &PlayerSession{
		PlayerID:     playerID,
		repo:         repo,
		owner:        owner,
		entities:     make(map[string]IDbEntity),
		dirty:        make(map[string]IDbEntity),
		absentTables: make(map[string]struct{}),
	}
}

// Load 并发加载可缓存实体到 L1（仅加载已注册为可缓存的类型）。
func (s *PlayerSession) Load(entityTypes []IDbEntity) error {
	if s.PlayerID == "" {
		return NewValidationException("PlayerID 不能为空")
	}
	cacheable := GetCacheableEntityRegistry().FilterCacheable(entityTypes)
	if len(cacheable) == 0 {
		s.mu.Lock()
		s.loaded = true
		s.mu.Unlock()
		return nil
	}

	results := s.repo.FindByIdConcurrent(s.PlayerID, cacheable, nil)
	var firstErr error
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range results {
		if item.EntityType == nil {
			continue
		}
		if item.Err != nil && firstErr == nil {
			firstErr = item.Err
			continue
		}
		if item.Entity != nil {
			tableName := ResolveEntityTableName(item.EntityType)
			if err := s.owner.tryTrackEntity(item.Entity); err != nil {
				LogWarn("实体缓存限额已满，跳过: playerID=%s, type=%s, err=%v", s.PlayerID, EntityTypeName(item.Entity), err)
				continue
			}
			s.entities[tableName] = item.Entity
		} else if s.negativeCacheEnabledLocked() {
			tableName := ResolveEntityTableName(item.EntityType)
			s.absentTables[tableName] = struct{}{}
		}
	}
	s.loaded = true
	return firstErr
}

// SetNegativeCacheEnabled Session 级动态开关负缓存（不影响全局配置；测试/运维用）。
func (s *PlayerSession) SetNegativeCacheEnabled(enabled bool) {
	s.mu.Lock()
	v := enabled
	s.negativeCacheOverride = &v
	s.mu.Unlock()
}

// ClearNegativeCacheOverride 恢复跟随全局 negativeCacheEnabled 配置。
func (s *PlayerSession) ClearNegativeCacheOverride() {
	s.mu.Lock()
	s.negativeCacheOverride = nil
	s.mu.Unlock()
}

// NegativeCacheEnabled 当前 Session 是否启用负缓存。
func (s *PlayerSession) NegativeCacheEnabled() bool {
	return s.negativeCacheEnabled()
}

func (s *PlayerSession) negativeCacheEnabled() bool {
	s.mu.RLock()
	override := s.negativeCacheOverride
	s.mu.RUnlock()
	if override != nil {
		return *override
	}
	return GetEntityCacheSettings().Snapshot().IsNegativeCacheEnabled()
}

func (s *PlayerSession) tableNameOf(prototype IDbEntity) string {
	return ResolveEntityTableName(prototype)
}

func (s *PlayerSession) markAbsent(tableName string) {
	if !s.negativeCacheEnabled() || tableName == "" {
		return
	}
	s.mu.Lock()
	s.absentTables[tableName] = struct{}{}
	s.mu.Unlock()
}

func (s *PlayerSession) clearAbsent(tableName string) {
	s.mu.Lock()
	delete(s.absentTables, tableName)
	s.mu.Unlock()
}

// Get 从 L1 正缓存读取；不查库。
func (s *PlayerSession) Get(prototype IDbEntity) IDbEntity {
	if prototype == nil {
		return nil
	}
	if s.owner != nil {
		s.owner.touchSession(s.PlayerID)
	}
	tableName := s.tableNameOf(prototype)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entities[tableName]
}

// IsResolved 该表是否已解析：有实体，或（负缓存开启且已确认无记录）。
func (s *PlayerSession) IsResolved(prototype IDbEntity) bool {
	if prototype == nil {
		return false
	}
	tableName := s.tableNameOf(prototype)
	s.mu.RLock()
	_, hasEntity := s.entities[tableName]
	_, isAbsent := s.absentTables[tableName]
	negOn := s.negativeCacheEnabledLocked()
	s.mu.RUnlock()
	if hasEntity {
		return true
	}
	return negOn && isAbsent
}

func (s *PlayerSession) negativeCacheEnabledLocked() bool {
	if s.negativeCacheOverride != nil {
		return *s.negativeCacheOverride
	}
	return GetEntityCacheSettings().Snapshot().IsNegativeCacheEnabled()
}

// GetOrLoad 读正缓存；负缓存命中则不查库；不可缓存类型始终直查 DB。
func (s *PlayerSession) GetOrLoad(prototype IDbEntity) (IDbEntity, error) {
	if prototype == nil {
		return nil, NewValidationException("实体原型不能为 nil")
	}
	tableName := s.tableNameOf(prototype)
	s.mu.RLock()
	if entity, ok := s.entities[tableName]; ok {
		s.mu.RUnlock()
		if s.owner != nil {
			s.owner.touchSession(s.PlayerID)
		}
		return entity, nil
	}
	absentHit := s.negativeCacheEnabledLocked() && s.isAbsentLocked(tableName)
	s.mu.RUnlock()
	if absentHit && GetCacheableEntityRegistry().IsCacheable(prototype) {
		if s.owner != nil {
			s.owner.touchSession(s.PlayerID)
		}
		return nil, nil
	}

	if !GetCacheableEntityRegistry().IsCacheable(prototype) {
		return s.repo.FindById(s.PlayerID, prototype)
	}
	entity, err := s.repo.FindById(s.PlayerID, prototype)
	if err != nil {
		return nil, err
	}
	if entity != nil {
		if err := s.putCacheOnly(entity); err != nil {
			return entity, err
		}
	} else {
		s.markAbsent(tableName)
	}
	if s.owner != nil {
		s.owner.touchSession(s.PlayerID)
	}
	return entity, nil
}

func (s *PlayerSession) isAbsentLocked(tableName string) bool {
	_, ok := s.absentTables[tableName]
	return ok
}

// Put 更新 L1 并标记 dirty；缓存模式下延迟刷库，否则走写缓冲。
func (s *PlayerSession) Put(entity IDbEntity) error {
	if entity == nil {
		return NewValidationException("实体不能为 nil")
	}
	if err := GetCacheableEntityRegistry().RequireCacheable(entity); err != nil {
		return err
	}
	if err := s.putCacheOnly(entity); err != nil {
		return err
	}
	s.mu.Lock()
	s.dirty[s.repo.getTableName(entity)] = entity
	s.mu.Unlock()

	if s.owner != nil {
		s.owner.touchSession(s.PlayerID)
	}
	if GetEntityCacheSettings().Snapshot().IsDeferredWrite() {
		return nil
	}
	return s.repo.SaveBuffered(entity)
}

// MarkDirty 原地修改后标记 dirty（不替换 entities 引用外的逻辑同 Put）。
func (s *PlayerSession) MarkDirty(entity IDbEntity) error {
	return s.Put(entity)
}

func (s *PlayerSession) putCacheOnly(entity IDbEntity) error {
	tableName := ResolveEntityTableName(entity)
	s.mu.Lock()
	_, exists := s.entities[tableName]
	s.mu.Unlock()
	if !exists {
		if s.owner != nil {
			if err := s.owner.tryTrackEntity(entity); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	s.entities[tableName] = entity
	delete(s.absentTables, tableName)
	s.mu.Unlock()
	return nil
}

// Flush 强制刷写 dirty 到 DB（WAL 保护），Session 退出时必须调用。
func (s *PlayerSession) Flush() error {
	if s.owner != nil {
		return s.owner.flushSession(s, true)
	}
	return s.flushInternal(true)
}

func (s *PlayerSession) takeDirty() []IDbEntity {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.dirty) == 0 {
		return nil
	}
	out := make([]IDbEntity, 0, len(s.dirty))
	for _, entity := range s.dirty {
		out = append(out, entity)
	}
	s.dirty = make(map[string]IDbEntity)
	return out
}

func (s *PlayerSession) restoreDirty(entities []IDbEntity) {
	if len(entities) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entity := range entities {
		s.dirty[s.repo.getTableName(entity)] = entity
	}
}

func (s *PlayerSession) flushInternal(includeWriteBuffer bool) error {
	dirtyEntities := s.takeDirty()
	var firstErr error
	if len(dirtyEntities) > 0 {
		if err := s.repo.UpdateBatchUpsert(dirtyEntities); err != nil {
			firstErr = err
			s.restoreDirty(dirtyEntities)
		}
	}
	if includeWriteBuffer {
		if err := s.repo.FlushWriteBuffer(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// FlushDirtyOnly 仅刷 dirty，不关闭 Session（定时刷写用）。
func (s *PlayerSession) FlushDirtyOnly() error {
	if s.owner != nil {
		s.owner.acquireFlushSlot()
		defer s.owner.releaseFlushSlot()
	}
	return s.flushInternal(false)
}

// Release 释放 Session 占用的实体计数（内部用）。
func (s *PlayerSession) releaseEntityCounts() {
	s.mu.RLock()
	entities := make([]IDbEntity, 0, len(s.entities))
	for _, e := range s.entities {
		entities = append(entities, e)
	}
	s.mu.RUnlock()
	if s.owner != nil {
		for _, e := range entities {
			s.owner.untrackEntity(e)
		}
	}
}

// IsLoaded 是否已完成登录加载。
func (s *PlayerSession) IsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// DirtyCount 待落库实体数量。
func (s *PlayerSession) DirtyCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.dirty)
}

// SessionRepository 管理在线玩家 Session（游戏逻辑服入口）。
type SessionRepository struct {
	repo *BaseCrudRepository

	sessions   sync.Map // playerID -> *PlayerSession
	lru        *sessionLRU
	entityCounts map[string]int
	countMu    sync.Mutex

	flushStop    chan struct{}
	flushDone    chan struct{}
	flushStarted bool

	flushSemMu sync.Mutex
	flushSem   chan struct{}

	flushRunning sync.Mutex // 定时刷写重叠保护（上一 tick 未完成则跳过）
}

// NewSessionRepository 创建 Session 仓储并启动定时刷写（若配置启用）。
func NewSessionRepository(repo *BaseCrudRepository) *SessionRepository {
	settings := GetEntityCacheSettings().Snapshot()
	sr := &SessionRepository{
		repo:         repo,
		lru:          newSessionLRU(settings.MaxSessions),
		entityCounts: make(map[string]int),
		flushStop:    make(chan struct{}),
		flushDone:    make(chan struct{}),
	}
	if settings.Enabled && settings.SessionFlushIntervalMs > 0 {
		sr.flushStarted = true
		sr.startPeriodicFlush(time.Duration(settings.SessionFlushIntervalMs) * time.Millisecond)
	} else {
		close(sr.flushDone)
	}
	GetEntityCacheSettings().OnChange(func(s EntityCacheSettings) {
		if sr.lru != nil {
			sr.lru.SetMaxSize(s.MaxSessions)
		}
	})
	return sr
}

// Stop 停止定时刷写协程。
func (sr *SessionRepository) Stop() {
	if !sr.flushStarted {
		return
	}
	close(sr.flushStop)
	<-sr.flushDone
}

// SetFlushInterval 动态调整定时刷写间隔（毫秒）；0 表示关闭定时刷写。
func (sr *SessionRepository) SetFlushInterval(intervalMs int) {
	_ = GetEntityCacheSettings().Set("sessionFlushIntervalMs", intervalMs)
}

func (sr *SessionRepository) startPeriodicFlush(interval time.Duration) {
	go func() {
		defer close(sr.flushDone)
		settings := GetEntityCacheSettings().Snapshot()
		timer := time.NewTimer(jitterDuration(interval, settings.SessionFlushIntervalJitterPct))
		defer timer.Stop()
		for {
			select {
			case <-sr.flushStop:
				return
			case <-timer.C:
				settings = GetEntityCacheSettings().Snapshot()
				newInterval := time.Duration(settings.SessionFlushIntervalMs) * time.Millisecond
				if settings.SessionFlushIntervalMs <= 0 {
					continue
				}
				if newInterval != interval {
					interval = newInterval
				}
				_ = sr.FlushAllDirty()
				timer.Reset(jitterDuration(interval, settings.SessionFlushIntervalJitterPct))
			}
		}
	}()
}

func (sr *SessionRepository) touchSession(playerID string) {
	if sr.lru != nil {
		sr.lru.Touch(playerID)
	}
}

func (sr *SessionRepository) tryTrackEntity(entity IDbEntity) error {
	if entity == nil {
		return nil
	}
	typeName := EntityTypeName(entity)
	max := GetCacheableEntityRegistry().MaxInstances(typeName)
	if max <= 0 {
		return nil
	}
	sr.countMu.Lock()
	defer sr.countMu.Unlock()
	if sr.entityCounts[typeName] >= max {
		return fmt.Errorf("实体类型 %s 已达缓存上限 %d", typeName, max)
	}
	sr.entityCounts[typeName]++
	return nil
}

func (sr *SessionRepository) untrackEntity(entity IDbEntity) {
	if entity == nil {
		return
	}
	typeName := EntityTypeName(entity)
	sr.countMu.Lock()
	defer sr.countMu.Unlock()
	if sr.entityCounts[typeName] > 0 {
		sr.entityCounts[typeName]--
	}
}

// OpenSession 登录：LRU 管理 + 加载可缓存实体到 L1。
func (sr *SessionRepository) OpenSession(playerID string, entityTypes []IDbEntity) (*PlayerSession, error) {
	if playerID == "" {
		return nil, NewValidationException("PlayerID 不能为空")
	}
	if existing, ok := sr.sessions.Load(playerID); ok {
		sr.touchSession(playerID)
		return existing.(*PlayerSession), nil
	}

	settings := GetEntityCacheSettings().Snapshot()
	if settings.Enabled && sr.lru != nil {
		if evicted := sr.lru.Add(playerID); evicted != "" {
			if err := sr.evictSession(evicted, settings.FlushOnEvict); err != nil {
				LogWarn("LRU 淘汰 Session 刷写失败: playerID=%s, err=%v", evicted, err)
			}
		}
	}

	session := newPlayerSession(playerID, sr.repo, sr)
	if err := session.Load(entityTypes); err != nil {
		session.releaseEntityCounts()
		if sr.lru != nil {
			sr.lru.Remove(playerID)
		}
		return nil, err
	}
	sr.sessions.Store(playerID, session)
	LogDebug("玩家 Session 已打开: playerID=%s", playerID)
	return session, nil
}

func (sr *SessionRepository) evictSession(playerID string, flushFirst bool) error {
	v, ok := sr.sessions.LoadAndDelete(playerID)
	if !ok {
		if sr.lru != nil {
			sr.lru.Remove(playerID)
		}
		return nil
	}
	session := v.(*PlayerSession)
	if flushFirst {
		if err := session.Flush(); err != nil {
			sr.sessions.Store(playerID, session)
			return err
		}
	}
	session.releaseEntityCounts()
	if sr.lru != nil {
		sr.lru.Remove(playerID)
	}
	LogDebug("玩家 Session 已 LRU 淘汰: playerID=%s", playerID)
	return nil
}

// GetSession 获取在线 Session。
func (sr *SessionRepository) GetSession(playerID string) *PlayerSession {
	if v, ok := sr.sessions.Load(playerID); ok {
		sr.touchSession(playerID)
		return v.(*PlayerSession)
	}
	return nil
}

// CloseSession 下线：强制刷写所有 dirty 到 DB 并移除 Session。
func (sr *SessionRepository) CloseSession(playerID string) error {
	v, ok := sr.sessions.LoadAndDelete(playerID)
	if !ok {
		return nil
	}
	session := v.(*PlayerSession)
	if err := session.Flush(); err != nil {
		sr.sessions.Store(playerID, session)
		return fmt.Errorf("玩家下线落库失败(数据已写 WAL): playerID=%s, err=%w", playerID, err)
	}
	session.releaseEntityCounts()
	if sr.lru != nil {
		sr.lru.Remove(playerID)
	}
	LogDebug("玩家 Session 已关闭: playerID=%s", playerID)
	return nil
}

// FlushAllDirty 定时刷写：跨 Session 合并或按 Session 并发刷盘（有界 worker）。
func (sr *SessionRepository) FlushAllDirty() error {
	if !sr.tryBeginPeriodicFlush() {
		return nil
	}
	defer sr.endPeriodicFlush()

	settings := GetEntityCacheSettings().Snapshot()
	if settings.SessionFlushMergeByTable {
		return sr.flushAllDirtyMerged(settings)
	}
	return sr.flushAllDirtyPerSession(settings)
}

func (sr *SessionRepository) tryBeginPeriodicFlush() bool {
	return sr.flushRunning.TryLock()
}

func (sr *SessionRepository) endPeriodicFlush() {
	sr.flushRunning.Unlock()
}

// FlushAll 关服：收集全部 dirty，按表合并分波刷盘，再刷 WriteBuffer。
func (sr *SessionRepository) FlushAll() error {
	return sr.flushAllShutdown()
}

// OnlineCount 在线玩家数。
func (sr *SessionRepository) OnlineCount() int {
	count := 0
	sr.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// EntityCacheCount 某实体类型当前缓存实例数。
func (sr *SessionRepository) EntityCacheCount(typeName string) int {
	sr.countMu.Lock()
	defer sr.countMu.Unlock()
	return sr.entityCounts[typeName]
}
