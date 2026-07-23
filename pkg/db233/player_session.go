package db233

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrSessionRepositoryClosed 表示 Session 仓储已停止接收新操作。
var ErrSessionRepositoryClosed = errors.New("Session Repository 已关闭")

type sessionDirtyPreparationState uint8

const (
	sessionDirtyUnprepared sessionDirtyPreparationState = iota
	sessionDirtyPrepared
	sessionDirtyPreparationFailed
)

type sessionDirtySnapshot struct {
	tableName        string
	entity           IDbEntity
	preparationState sessionDirtyPreparationState
	preparationErr   error
	version          uint64
}

// PlayerSession 玩家 Session L1 缓存：登录后内存驻留，读走内存，写走 dirty + 可选延迟刷库。
type PlayerSession struct {
	PlayerID string
	repo     *BaseCrudRepository
	owner    *SessionRepository

	mu                     sync.RWMutex
	entities               map[string]IDbEntity // tableName -> entity（正缓存）
	dirty                  map[string]IDbEntity // tableName -> 与业务对象隔离的待落库深快照
	dirtyPreparationState  map[string]sessionDirtyPreparationState
	dirtyPreparationErrors map[string]error
	dirtyVersions          map[string]uint64
	nextDirtyVersion       uint64
	absentTables           map[string]struct{} // 负缓存：已确认无记录（需开启负缓存）
	negativeCacheOverride  *bool               // nil=跟随全局；非 nil=Session 级动态开关
	loaded                 bool
	databaseGeneration     string

	lifecycleMu sync.Mutex
	inflight    sync.WaitGroup
	closing     bool
	closed      bool
}

func newPlayerSession(playerID string, repo *BaseCrudRepository, owner *SessionRepository) *PlayerSession {
	generation := ""
	if owner != nil {
		owner.generationMu.RLock()
		generation = owner.databaseGeneration
		owner.generationMu.RUnlock()
	}
	return newPlayerSessionForGeneration(playerID, repo, owner, generation)
}

func newPlayerSessionForGeneration(playerID string, repo *BaseCrudRepository, owner *SessionRepository, generation string) *PlayerSession {
	return &PlayerSession{
		PlayerID:               playerID,
		repo:                   repo,
		owner:                  owner,
		entities:               make(map[string]IDbEntity),
		dirty:                  make(map[string]IDbEntity),
		dirtyPreparationState:  make(map[string]sessionDirtyPreparationState),
		dirtyPreparationErrors: make(map[string]error),
		dirtyVersions:          make(map[string]uint64),
		absentTables:           make(map[string]struct{}),
		databaseGeneration:     generation,
	}
}

// beginOperation 同时登记仓储级与 Session 级在途操作。两个 Add 都在各自
// admission mutex 下完成，因此 CloseAdmissionAndWait/beginClose 不会与 Add 竞态。
func (s *PlayerSession) beginOperation() (func(), error) {
	if s == nil {
		return func() {}, NewValidationException("PlayerSession 不能为 nil")
	}
	releaseOwner := func() {}
	if s.owner != nil {
		var err error
		releaseOwner, err = s.owner.beginOperation()
		if err != nil {
			return func() {}, err
		}
	}

	releaseSession, err := s.beginLocalOperation()
	if err != nil {
		releaseOwner()
		return func() {}, err
	}
	return func() {
		releaseSession()
		releaseOwner()
	}, nil
}

func (s *PlayerSession) beginLocalOperation() (func(), error) {
	if s == nil {
		return func() {}, NewValidationException("PlayerSession 不能为 nil")
	}
	s.lifecycleMu.Lock()
	if s.closing || s.closed {
		s.lifecycleMu.Unlock()
		return func() {}, ErrSessionRepositoryClosed
	}
	s.inflight.Add(1)
	s.lifecycleMu.Unlock()
	return s.inflight.Done, nil
}

func (s *PlayerSession) beginClose() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.closing = true
	s.lifecycleMu.Unlock()
	s.inflight.Wait()
}

func (s *PlayerSession) finishClose(closed bool) {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.closed = closed
	s.closing = false
	s.lifecycleMu.Unlock()
}

// Load 并发加载可缓存实体到 L1（仅加载已注册为可缓存的类型）。
func (s *PlayerSession) Load(entityTypes []IDbEntity) error {
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	releaseGeneration, generationErr := s.lockSessionGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()
	return s.load(entityTypes)
}

func (s *PlayerSession) load(entityTypes []IDbEntity) error {
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
			if _, exists := s.entities[tableName]; exists {
				s.entities[tableName] = item.Entity
				delete(s.absentTables, tableName)
				continue
			}
			if err := s.owner.tryTrackEntity(item.Entity); err != nil {
				LogWarn("实体缓存限额已满，跳过: playerID=%s, type=%s, err=%s", safeValueForLog(s.PlayerID), safeValueForLog(EntityTypeName(item.Entity)), safeErrorForLog(err))
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
	releaseOperation, err := s.beginOperation()
	if err != nil {
		return
	}
	defer releaseOperation()
	releaseGeneration, err := s.lockSessionGeneration()
	if err != nil {
		return
	}
	defer releaseGeneration()
	s.mu.Lock()
	v := enabled
	s.negativeCacheOverride = &v
	s.mu.Unlock()
}

// ClearNegativeCacheOverride 恢复跟随全局 negativeCacheEnabled 配置。
func (s *PlayerSession) ClearNegativeCacheOverride() {
	releaseOperation, err := s.beginOperation()
	if err != nil {
		return
	}
	defer releaseOperation()
	releaseGeneration, err := s.lockSessionGeneration()
	if err != nil {
		return
	}
	defer releaseGeneration()
	s.mu.Lock()
	s.negativeCacheOverride = nil
	s.mu.Unlock()
}

// NegativeCacheEnabled 当前 Session 是否启用负缓存。
func (s *PlayerSession) NegativeCacheEnabled() bool {
	releaseOperation, err := s.beginOperation()
	if err != nil {
		return false
	}
	defer releaseOperation()
	releaseGeneration, err := s.lockSessionGeneration()
	if err != nil {
		return false
	}
	defer releaseGeneration()
	return s.negativeCacheEnabled()
}

func (s *PlayerSession) negativeCacheEnabled() bool {
	s.mu.RLock()
	override := s.negativeCacheOverride
	s.mu.RUnlock()
	if override != nil {
		return *override
	}
	return entityCacheSettingsSnapshot().IsNegativeCacheEnabled()
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
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return nil
	}
	defer releaseOperation()
	release, err := s.lockSessionGeneration()
	if err != nil {
		return nil
	}
	defer release()
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
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return false
	}
	defer releaseOperation()
	release, err := s.lockSessionGeneration()
	if err != nil {
		return false
	}
	defer release()
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
	return entityCacheSettingsSnapshot().IsNegativeCacheEnabled()
}

// GetOrLoad 读正缓存；负缓存命中则不查库；不可缓存类型始终直查 DB。
func (s *PlayerSession) GetOrLoad(prototype IDbEntity) (IDbEntity, error) {
	if prototype == nil {
		return nil, NewValidationException("实体原型不能为 nil")
	}
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return nil, operationErr
	}
	defer releaseOperation()
	release, err := s.lockSessionGeneration()
	if err != nil {
		return nil, err
	}
	defer release()
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
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	// 先做一次短租约校验，使旧 Session/切代屏障优先于实体注册或快照错误；
	// 随即释放，绝不在用户自定义 Snapshotter 执行期间持锁。
	validationRelease, validationErr := s.lockDatabaseGeneration()
	if validationErr != nil {
		return validationErr
	}
	validationRelease()
	if err := GetCacheableEntityRegistry().RequireCacheable(entity); err != nil {
		return err
	}
	// entities 保留业务对象，维持 Get/原地修改的易用契约；dirty 必须是完全
	// 隔离的深快照，后台 SerializeBeforeSaveDb/SQL 才不会与下一帧业务修改
	// map、slice、pointer 或字段发生 data race/撕裂。
	dirtySnapshot, err := SnapshotEntity(entity)
	if err != nil {
		return err
	}
	// 自定义 Snapshotter 允许调用业务代码，因此不能在持有 generation RLock
	// 时执行；否则遇到正在等待的切代写锁时，重入数据库可能自锁。快照完成
	// 后再获取租约并发布，切代已开始则本次 Put 整体失败且不会进入 dirty。
	release, err := s.lockDatabaseGeneration()
	if err != nil {
		return err
	}
	if err := s.putCacheOnly(entity); err != nil {
		release()
		return err
	}
	tableName := s.repo.getTableName(entity)
	if s.owner != nil {
		s.owner.touchSession(s.PlayerID)
	}
	if entityCacheSettingsSnapshot().IsDeferredWrite() {
		s.storeDirtySnapshot(tableName, dirtySnapshot, sessionDirtyUnprepared, nil)
		release()
		return nil
	}
	// 常规写缓冲路径直接转移唯一深快照；只有缓冲禁用/
	// 已满的同步 fallback 才生成第二份快照，使可能被 hook
	// 修改的 SQL 对象与失败后的 pristine dirty 互相隔离。
	queued, enqueueErr := s.repo.tryEnqueueOwnedSnapshotUnderGenerationLease(dirtySnapshot, s.databaseGeneration)
	if queued {
		release()
		return nil
	}
	if enqueueErr != nil {
		s.storeDirtySnapshot(tableName, dirtySnapshot, sessionDirtyUnprepared, nil)
		release()
		return enqueueErr
	}
	// Snapshotter 可执行业务代码，禁止在 generation RLock 中调用。
	release()
	bufferSnapshot, snapshotErr := SnapshotEntity(dirtySnapshot)
	if snapshotErr != nil {
		retryRelease, retryErr := s.lockDatabaseGeneration()
		if retryErr != nil {
			return errors.Join(snapshotErr, retryErr)
		}
		s.storeDirtySnapshot(tableName, dirtySnapshot, sessionDirtyUnprepared, nil)
		retryRelease()
		return snapshotErr
	}
	retryRelease, retryErr := s.lockDatabaseGeneration()
	if retryErr != nil {
		return retryErr
	}
	defer retryRelease()
	version := s.storeDirtySnapshot(tableName, dirtySnapshot, sessionDirtyUnprepared, nil)
	if err := s.repo.saveSnapshotSynchronouslyUnderGenerationLease(bufferSnapshot, s.databaseGeneration); err != nil {
		return err
	}
	s.clearDirtySnapshotIfVersion(tableName, version)
	return nil
}

// MarkDirty 原地修改后标记 dirty（不替换 entities 引用外的逻辑同 Put）。
func (s *PlayerSession) MarkDirty(entity IDbEntity) error {
	return s.Put(entity)
}

func (s *PlayerSession) putCacheOnly(entity IDbEntity) error {
	tableName := ResolveEntityTableName(entity)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.entities[tableName]; !exists {
		if s.owner != nil {
			if err := s.owner.tryTrackEntity(entity); err != nil {
				return err
			}
		}
	}
	s.entities[tableName] = entity
	delete(s.absentTables, tableName)
	return nil
}

// Flush 强制刷写 dirty 到 DB（WAL 保护），Session 退出时必须调用。
func (s *PlayerSession) Flush() error {
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	release, err := s.lockDatabaseGeneration()
	if err != nil {
		return err
	}
	defer release()
	if s.owner != nil {
		return s.owner.flushSession(s, true)
	}
	return s.flushInternal(true)
}

func (s *PlayerSession) storeDirtySnapshot(
	tableName string,
	entity IDbEntity,
	state sessionDirtyPreparationState,
	preparationErr error,
) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty == nil {
		s.dirty = make(map[string]IDbEntity)
	}
	if s.dirtyPreparationState == nil {
		s.dirtyPreparationState = make(map[string]sessionDirtyPreparationState)
	}
	if s.dirtyPreparationErrors == nil {
		s.dirtyPreparationErrors = make(map[string]error)
	}
	if s.dirtyVersions == nil {
		s.dirtyVersions = make(map[string]uint64)
	}
	s.nextDirtyVersion++
	version := s.nextDirtyVersion
	s.dirty[tableName] = entity
	s.dirtyPreparationState[tableName] = state
	s.dirtyVersions[tableName] = version
	if preparationErr != nil {
		s.dirtyPreparationErrors[tableName] = preparationErr
	} else {
		delete(s.dirtyPreparationErrors, tableName)
	}
	return version
}

func (s *PlayerSession) clearDirtySnapshotIfVersion(tableName string, version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtyVersions[tableName] != version {
		return
	}
	delete(s.dirty, tableName)
	delete(s.dirtyPreparationState, tableName)
	delete(s.dirtyPreparationErrors, tableName)
	delete(s.dirtyVersions, tableName)
}

func (s *PlayerSession) takeDirty() []sessionDirtySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.dirty) == 0 {
		return nil
	}
	out := make([]sessionDirtySnapshot, 0, len(s.dirty))
	for tableName, entity := range s.dirty {
		out = append(out, sessionDirtySnapshot{
			tableName:        tableName,
			entity:           entity,
			preparationState: s.dirtyPreparationState[tableName],
			preparationErr:   s.dirtyPreparationErrors[tableName],
			version:          s.dirtyVersions[tableName],
		})
	}
	s.dirty = make(map[string]IDbEntity)
	s.dirtyPreparationState = make(map[string]sessionDirtyPreparationState)
	s.dirtyPreparationErrors = make(map[string]error)
	s.dirtyVersions = make(map[string]uint64)
	return out
}

func (s *PlayerSession) restoreDirty(snapshots []sessionDirtySnapshot) {
	if len(snapshots) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtyPreparationState == nil {
		s.dirtyPreparationState = make(map[string]sessionDirtyPreparationState)
	}
	if s.dirtyPreparationErrors == nil {
		s.dirtyPreparationErrors = make(map[string]error)
	}
	if s.dirtyVersions == nil {
		s.dirtyVersions = make(map[string]uint64)
	}
	for _, snapshot := range snapshots {
		tableName := snapshot.tableName
		if tableName == "" && s.repo != nil {
			tableName = s.repo.getTableName(snapshot.entity)
		}
		// takeDirty 后可能已有更新版本 Put 进来。失败快照只在该表仍为空时
		// 恢复，禁止旧快照覆盖更新版本。
		if _, newerExists := s.dirty[tableName]; newerExists {
			continue
		}
		s.dirty[tableName] = snapshot.entity
		s.dirtyPreparationState[tableName] = snapshot.preparationState
		s.dirtyVersions[tableName] = snapshot.version
		if snapshot.version > s.nextDirtyVersion {
			s.nextDirtyVersion = snapshot.version
		}
		if snapshot.preparationErr != nil {
			s.dirtyPreparationErrors[tableName] = snapshot.preparationErr
		}
	}
}

func prepareSessionDirtySnapshots(
	snapshots []sessionDirtySnapshot,
) (prepared []IDbEntity, failed []sessionDirtySnapshot, preparationErr error) {
	if len(snapshots) == 0 {
		return nil, nil, nil
	}
	prepared = make([]IDbEntity, 0, len(snapshots))
	failed = make([]sessionDirtySnapshot, 0)
	preparationErrors := make([]error, 0, 4)
	suppressedErrors := 0
	for index := range snapshots {
		snapshot := &snapshots[index]
		switch snapshot.preparationState {
		case sessionDirtyPreparationFailed:
			err := snapshot.preparationErr
			if err == nil {
				err = NewValidationException("Session dirty 快照处于失败状态但缺少错误原因")
				snapshot.preparationErr = err
			}
			failed = append(failed, *snapshot)
			preparationErrors = appendBoundedRecoveryError(
				preparationErrors,
				fmt.Errorf("Session dirty 快照准备已失败: Table=%s: %w", safeValueForLog(snapshot.tableName), err),
				&suppressedErrors,
			)
			continue
		case sessionDirtyUnprepared:
			// 在调用 hook 前先发布 Prepared；即使 hook panic，也不会对同一逻辑
			// 快照重复执行可能非幂等的副作用。
			snapshot.preparationState = sessionDirtyPrepared
			if err := serializeWriteBufferEntity(snapshot.entity); err != nil {
				snapshot.preparationState = sessionDirtyPreparationFailed
				snapshot.preparationErr = err
				failed = append(failed, *snapshot)
				preparationErrors = appendBoundedRecoveryError(
					preparationErrors,
					fmt.Errorf("准备 Session dirty 快照: Table=%s: %w", safeValueForLog(snapshot.tableName), err),
					&suppressedErrors,
				)
				continue
			}
		}
		prepared = append(prepared, snapshot.entity)
	}
	if suppressedErrors > 0 {
		preparationErrors = append(preparationErrors, fmt.Errorf("另有 %d 个 Session dirty 快照错误已省略", suppressedErrors))
	}
	return prepared, failed, errors.Join(preparationErrors...)
}

func (s *PlayerSession) flushInternal(includeWriteBuffer bool) error {
	dirtySnapshots := s.takeDirty()
	preparedEntities, preparationFailed, preparationErr := prepareSessionDirtySnapshots(dirtySnapshots)
	var writeErr error
	if len(preparedEntities) > 0 {
		writeErr = s.repo.updateBatchUpsertPreparedUnderGenerationLeaseWithFlushSource(
			preparedEntities,
			s.databaseGeneration,
			FlushWriteSourceSession,
		)
	}
	if writeErr != nil {
		s.restoreDirty(dirtySnapshots)
	} else {
		s.restoreDirty(preparationFailed)
	}
	firstErr := errors.Join(preparationErr, writeErr)
	if includeWriteBuffer {
		if err := s.repo.flushWriteBufferUnderGenerationLease(s.databaseGeneration); err != nil {
			firstErr = errors.Join(firstErr, err)
		}
	}
	return firstErr
}

// FlushDirtyOnly 仅刷 dirty，不关闭 Session（定时刷写用）。
func (s *PlayerSession) FlushDirtyOnly() error {
	releaseOperation, operationErr := s.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	release, err := s.lockDatabaseGeneration()
	if err != nil {
		return err
	}
	defer release()
	if s.owner != nil {
		releaseSlot := s.owner.acquireFlushSlot()
		defer releaseSlot()
	}
	return s.flushInternal(false)
}

func (s *PlayerSession) lockDatabaseGeneration() (func(), error) {
	if s == nil || s.owner == nil {
		return func() {}, nil
	}
	return s.owner.lockDatabaseGeneration(s.databaseGeneration)
}

// lockSessionGeneration 仅持有 Session generation 租约。适用于后续
// 会调用 Repository 公开方法（其自行获取 Db RLock）的路径；
// transition 先获取 Session WLock 再获取 Db WLock，因此该租约
// 仍能保证操作不跨代，同时避免嵌套 Db RLock 自锁。
func (s *PlayerSession) lockSessionGeneration() (func(), error) {
	if s == nil || s.owner == nil {
		return func() {}, nil
	}
	return s.owner.lockSessionGeneration(s.databaseGeneration)
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

func (s *PlayerSession) clearAfterSuccessfulShutdown() {
	if s == nil {
		return
	}
	s.beginClose()
	s.releaseEntityCounts()
	s.mu.Lock()
	s.entities = nil
	s.dirty = nil
	s.dirtyPreparationState = nil
	s.dirtyPreparationErrors = nil
	s.dirtyVersions = nil
	s.absentTables = nil
	s.loaded = false
	s.mu.Unlock()
	s.finishClose(true)
}

// IsLoaded 是否已完成登录加载。
func (s *PlayerSession) IsLoaded() bool {
	releaseOperation, err := s.beginOperation()
	if err != nil {
		return false
	}
	defer releaseOperation()
	releaseGeneration, err := s.lockSessionGeneration()
	if err != nil {
		return false
	}
	defer releaseGeneration()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// DirtyCount 待落库实体数量。
func (s *PlayerSession) DirtyCount() int {
	releaseOperation, err := s.beginOperation()
	if err != nil {
		return 0
	}
	defer releaseOperation()
	releaseGeneration, err := s.lockSessionGeneration()
	if err != nil {
		return 0
	}
	defer releaseGeneration()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.dirty)
}

// SessionRepository 管理在线玩家 Session（游戏逻辑服入口）。
type SessionRepository struct {
	repo *BaseCrudRepository

	admissionMu     sync.Mutex
	admissionClosed bool
	admissionPaused bool
	inflight        sync.WaitGroup

	sessions     sync.Map // playerID -> *PlayerSession
	sessionOpsMu sync.Mutex
	sessionOps   map[string]*sessionOperation
	lru          *sessionLRU
	entityCounts map[string]int
	countMu      sync.Mutex

	flushStop       chan struct{}
	flushDone       chan struct{}
	settingsChanged chan struct{}
	stopOnce        sync.Once
	cancelSettings  func()

	flushSemMu sync.Mutex
	flushSem   chan struct{}

	flushRunning       sync.Mutex // 定时刷写重叠保护（上一 tick 未完成则跳过）
	generationMu       sync.RWMutex
	databaseGeneration string
}

type sessionOperation struct {
	kind    string
	done    chan struct{}
	session *PlayerSession
	err     error
}

func (sr *SessionRepository) beginOperation() (func(), error) {
	if sr == nil {
		return func() {}, ErrSessionRepositoryClosed
	}
	sr.admissionMu.Lock()
	if sr.admissionClosed {
		sr.admissionMu.Unlock()
		return func() {}, ErrSessionRepositoryClosed
	}
	if sr.admissionPaused {
		sr.admissionMu.Unlock()
		return func() {}, ErrDatabaseGenerationBlocked
	}
	sr.inflight.Add(1)
	sr.admissionMu.Unlock()
	return sr.inflight.Done, nil
}

// pauseAdmissionAndWait 原子暂停新操作，并等待已经准入的操作离场。
// generation transition 失败或结束后必须调用 resumeAdmission；永久关闭使用
// CloseAdmissionAndWait。所有 WaitGroup.Add 都受 admissionMu 保护，因而不会
// 与这里的 Wait 并发。
func (sr *SessionRepository) pauseAdmissionAndWait() error {
	if sr == nil {
		return nil
	}
	sr.admissionMu.Lock()
	if sr.admissionClosed {
		sr.admissionMu.Unlock()
		return ErrSessionRepositoryClosed
	}
	sr.admissionPaused = true
	sr.admissionMu.Unlock()
	sr.inflight.Wait()
	return nil
}

func (sr *SessionRepository) resumeAdmission() {
	if sr == nil {
		return
	}
	sr.admissionMu.Lock()
	sr.admissionPaused = false
	sr.admissionMu.Unlock()
}

// CloseAdmissionAndWait 原子封闭新操作，并等待全部已准入操作完成。
// 关闭后不可重新开放；最终 FlushAll 必须由 Db.Close 使用内部关闭路径执行。
func (sr *SessionRepository) CloseAdmissionAndWait() {
	if sr == nil {
		return
	}
	sr.admissionMu.Lock()
	sr.admissionClosed = true
	sr.admissionPaused = false
	sr.admissionMu.Unlock()
	sr.inflight.Wait()
}

const (
	sessionOperationOpen  = "open"
	sessionOperationClose = "close"
)

// NewSessionRepository 创建 Session 仓储并启动定时刷写（若配置启用）。
func NewSessionRepository(repo *BaseCrudRepository) *SessionRepository {
	settings := entityCacheSettingsSnapshot()
	generation := ""
	if repo != nil && repo.db != nil {
		generation = repo.db.databaseGenerationSnapshot()
	}
	sr := &SessionRepository{
		repo:               repo,
		sessionOps:         make(map[string]*sessionOperation),
		lru:                newSessionLRU(settings.MaxSessions),
		entityCounts:       make(map[string]int),
		flushStop:          make(chan struct{}),
		flushDone:          make(chan struct{}),
		settingsChanged:    make(chan struct{}, 1),
		databaseGeneration: generation,
	}
	sr.cancelSettings = GetEntityCacheSettings().Subscribe(func(EntityCacheSettings) {
		sr.notifySettingsChanged()
	})
	go sr.startPeriodicFlush(settings)
	return sr
}

// Stop 停止定时刷写协程。
func (sr *SessionRepository) Stop() {
	if sr == nil || sr.flushStop == nil || sr.flushDone == nil {
		return
	}
	sr.stopOnce.Do(func() {
		if sr.cancelSettings != nil {
			sr.cancelSettings()
		}
		close(sr.flushStop)
	})
	<-sr.flushDone
}

// SetFlushInterval 动态调整定时刷写间隔（毫秒）；0 表示关闭定时刷写。
func (sr *SessionRepository) SetFlushInterval(intervalMs int) {
	if err := sr.SetFlushIntervalStrict(intervalMs); err != nil {
		LogError("Session 刷写间隔配置失败: %s", safeErrorForLog(err))
	}
}

// SetFlushIntervalStrict 动态调整定时刷写间隔并传播非法 duration。
func (sr *SessionRepository) SetFlushIntervalStrict(intervalMs int) error {
	if sr == nil {
		return NewValidationException("SessionRepository 不能为 nil")
	}
	return GetEntityCacheSettings().Set("sessionFlushIntervalMs", intervalMs)
}

func (sr *SessionRepository) notifySettingsChanged() {
	select {
	case sr.settingsChanged <- struct{}{}:
	default:
	}
}

func (sr *SessionRepository) startPeriodicFlush(settings EntityCacheSettings) {
	defer close(sr.flushDone)

	var timer *time.Timer
	var timerC <-chan time.Time
	resetTimer := func(current EntityCacheSettings) {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if !current.Enabled || current.SessionFlushIntervalMs <= 0 {
			timerC = nil
			return
		}
		delay := jitterDuration(
			saturatedMilliseconds(current.SessionFlushIntervalMs),
			current.SessionFlushIntervalJitterPct,
		)
		if timer == nil {
			timer = time.NewTimer(delay)
		} else {
			timer.Reset(delay)
		}
		timerC = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	sr.applyRuntimeSettings(settings)
	resetTimer(settings)
	for {
		select {
		case <-sr.flushStop:
			return
		case <-sr.settingsChanged:
			settings = entityCacheSettingsSnapshot()
			sr.applyRuntimeSettings(settings)
			resetTimer(settings)
		case <-timerC:
			if err := sr.FlushAllDirty(); err != nil {
				LogWarn("Session 定时刷写失败: %s", safeErrorForLog(err))
			}
			settings = entityCacheSettingsSnapshot()
			sr.applyRuntimeSettings(settings)
			resetTimer(settings)
		}
	}
}

func (sr *SessionRepository) applyRuntimeSettings(settings EntityCacheSettings) {
	releaseOperation, operationErr := sr.beginOperation()
	if operationErr != nil {
		return
	}
	defer releaseOperation()
	release, err := sr.lockDatabaseGeneration(sr.databaseGenerationSnapshot())
	if err != nil {
		return
	}
	defer release()
	if sr.lru == nil {
		return
	}
	for _, playerID := range sr.lru.SetMaxSize(settings.MaxSessions) {
		if err := sr.evictSession(playerID, settings.FlushOnEvict); err != nil {
			LogWarn("LRU 缩容淘汰 Session 失败: playerID=%s, err=%s", safeValueForLog(playerID), safeErrorForLog(err))
		}
	}
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
	releaseOperation, operationErr := sr.beginOperation()
	if operationErr != nil {
		return nil, operationErr
	}
	defer releaseOperation()
	generation := sr.databaseGenerationSnapshot()
	release, err := sr.lockSessionGeneration(generation)
	if err != nil {
		return nil, err
	}
	defer release()

	var operation *sessionOperation
	for {
		sr.sessionOpsMu.Lock()
		if inFlight := sr.sessionOps[playerID]; inFlight != nil {
			sr.sessionOpsMu.Unlock()
			<-inFlight.done
			if inFlight.kind == sessionOperationOpen {
				return inFlight.session, inFlight.err
			}
			continue
		}
		if existing, ok := sr.sessions.Load(playerID); ok {
			sr.sessionOpsMu.Unlock()
			sr.touchSession(playerID)
			return existing.(*PlayerSession), nil
		}
		operation = &sessionOperation{kind: sessionOperationOpen, done: make(chan struct{})}
		sr.sessionOps[playerID] = operation
		sr.sessionOpsMu.Unlock()
		break
	}

	settings := entityCacheSettingsSnapshot()
	if settings.Enabled && sr.lru != nil {
		evictedPlayerIDs := sr.lru.Add(playerID)
		for index, evicted := range evictedPlayerIDs {
			if err := sr.evictSession(evicted, settings.FlushOnEvict); err != nil {
				// removeSession 已将失败的旧 Session 及 dirty 完整恢复。
				// 新 playerID 尚未发布，回滚本次 LRU 准入，并恢复尚未
				// 执行的被选中条目，严格保证 MaxSessions 不被突破。
				sr.lru.Remove(playerID)
				for _, pending := range evictedPlayerIDs[index+1:] {
					sr.lru.Restore(pending)
				}
				openErr := fmt.Errorf("LRU 淘汰 Session 刷写失败: playerID=%s: %w", safeValueForLog(evicted), err)
				sr.finishSessionOperation(playerID, operation, nil, openErr)
				return nil, openErr
			}
		}
	}

	session := newPlayerSessionForGeneration(playerID, sr.repo, sr, generation)
	if err := session.load(entityTypes); err != nil {
		session.releaseEntityCounts()
		if sr.lru != nil {
			sr.lru.Remove(playerID)
		}
		sr.finishSessionOperation(playerID, operation, nil, err)
		return nil, err
	}
	sr.sessions.Store(playerID, session)
	sr.finishSessionOperation(playerID, operation, session, nil)
	LogDebug("玩家 Session 已打开: playerID=%s", safeValueForLog(playerID))
	return session, nil
}

func (sr *SessionRepository) evictSession(playerID string, flushFirst bool) error {
	err := sr.removeSession(playerID, flushFirst)
	if err == nil {
		LogDebug("玩家 Session 已 LRU 淘汰: playerID=%s", safeValueForLog(playerID))
	}
	return err
}

// GetSession 获取在线 Session。
func (sr *SessionRepository) GetSession(playerID string) *PlayerSession {
	releaseOperation, err := sr.beginOperation()
	if err != nil {
		return nil
	}
	defer releaseOperation()
	if v, ok := sr.sessions.Load(playerID); ok {
		sr.touchSession(playerID)
		return v.(*PlayerSession)
	}
	return nil
}

// CloseSession 下线：强制刷写所有 dirty 到 DB 并移除 Session。
func (sr *SessionRepository) CloseSession(playerID string) error {
	if playerID == "" {
		return NewValidationException("PlayerID 不能为空")
	}
	releaseOperation, operationErr := sr.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	release, err := sr.lockDatabaseGeneration(sr.databaseGenerationSnapshot())
	if err != nil {
		return err
	}
	defer release()
	if err := sr.removeSession(playerID, true); err != nil {
		return fmt.Errorf("玩家下线落库失败(数据已写 WAL): playerID=%s, err=%w", safeValueForLog(playerID), err)
	}
	LogDebug("玩家 Session 已关闭: playerID=%s", safeValueForLog(playerID))
	return nil
}

// discardSessionForPrimaryKeyReset 丢弃目标 Session 且不刷写 dirty。
// 仅允许 Db 在已发布全局 managed-write 屏障后调用。
func (sr *SessionRepository) discardSessionForPrimaryKeyReset(playerID string) error {
	if sr == nil {
		return nil
	}
	return sr.removeSession(playerID, false)
}

func (sr *SessionRepository) removeSession(playerID string, flushFirst bool) error {
	var operation *sessionOperation
	var session *PlayerSession
	for {
		sr.sessionOpsMu.Lock()
		if inFlight := sr.sessionOps[playerID]; inFlight != nil {
			sr.sessionOpsMu.Unlock()
			<-inFlight.done
			if inFlight.kind == sessionOperationClose {
				return inFlight.err
			}
			continue
		}
		value, ok := sr.sessions.LoadAndDelete(playerID)
		if !ok {
			sr.sessionOpsMu.Unlock()
			if sr.lru != nil {
				sr.lru.Remove(playerID)
			}
			return nil
		}
		session = value.(*PlayerSession)
		operation = &sessionOperation{kind: sessionOperationClose, done: make(chan struct{})}
		sr.sessionOps[playerID] = operation
		sr.sessionOpsMu.Unlock()
		break
	}
	session.beginClose()

	var err error
	if flushFirst {
		err = sr.flushSession(session, true)
	}
	if err != nil {
		session.finishClose(false)
		sr.sessions.Store(playerID, session)
		if sr.lru != nil {
			sr.lru.Restore(playerID)
		}
	} else {
		session.finishClose(true)
		session.releaseEntityCounts()
		if sr.lru != nil {
			sr.lru.Remove(playerID)
		}
	}
	sr.finishSessionOperation(playerID, operation, session, err)
	return err
}

func (sr *SessionRepository) finishSessionOperation(
	playerID string,
	operation *sessionOperation,
	session *PlayerSession,
	err error,
) {
	sr.sessionOpsMu.Lock()
	operation.session = session
	operation.err = err
	if sr.sessionOps[playerID] == operation {
		delete(sr.sessionOps, playerID)
	}
	close(operation.done)
	sr.sessionOpsMu.Unlock()
}

// FlushAllDirty 定时刷写：跨 Session 合并或按 Session 并发刷盘（有界 worker）。
func (sr *SessionRepository) FlushAllDirty() error {
	releaseOperation, operationErr := sr.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	release, err := sr.lockDatabaseGeneration(sr.databaseGenerationSnapshot())
	if err != nil {
		return err
	}
	defer release()
	if !sr.tryBeginPeriodicFlush() {
		return nil
	}
	defer sr.endPeriodicFlush()

	settings := entityCacheSettingsSnapshot()
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
	releaseOperation, operationErr := sr.beginOperation()
	if operationErr != nil {
		return operationErr
	}
	defer releaseOperation()
	return sr.flushAllAfterAdmissionClosed()
}

func (sr *SessionRepository) flushAllAfterAdmissionClosed() error {
	release, err := sr.lockDatabaseGeneration(sr.databaseGenerationSnapshot())
	if err != nil {
		return err
	}
	defer release()
	sr.flushRunning.Lock()
	defer sr.flushRunning.Unlock()
	return sr.flushAllShutdown()
}

func (sr *SessionRepository) databaseGenerationSnapshot() string {
	if sr == nil {
		return ""
	}
	sr.generationMu.RLock()
	defer sr.generationMu.RUnlock()
	return sr.databaseGeneration
}

// clearAfterSuccessfulShutdown 仅在最终 Session flush 全部成功后释放在线缓存。
// 失败时保留对象与 dirty，便于调用方诊断；成功时外部即使仍持有 Session
// 指针，也只能观察到 closed/空状态，不会永久保留大对象图。
func (sr *SessionRepository) clearAfterSuccessfulShutdown() {
	if sr == nil {
		return
	}
	sr.sessions.Range(func(key, value any) bool {
		sr.sessions.Delete(key)
		if session, ok := value.(*PlayerSession); ok && session != nil {
			session.clearAfterSuccessfulShutdown()
		}
		return true
	})
	if sr.lru != nil {
		sr.lru.Clear()
	}
	sr.sessionOpsMu.Lock()
	sr.sessionOps = make(map[string]*sessionOperation)
	sr.sessionOpsMu.Unlock()
	sr.countMu.Lock()
	sr.entityCounts = make(map[string]int)
	sr.countMu.Unlock()
}

func (sr *SessionRepository) lockDatabaseGeneration(expected string) (func(), error) {
	if sr == nil {
		return func() {}, nil
	}
	sr.generationMu.RLock()
	if sr.databaseGeneration != expected {
		sr.generationMu.RUnlock()
		return func() {}, fmt.Errorf(
			"%w: SessionRepository=%s, 对象=%s",
			ErrDatabaseGenerationChanged,
			safeValueForLog(sr.databaseGeneration),
			safeValueForLog(expected),
		)
	}
	if sr.repo != nil && sr.repo.db != nil {
		_, releaseDatabaseGeneration, err := sr.repo.db.lockDatabaseGeneration(expected)
		if err != nil {
			sr.generationMu.RUnlock()
			return func() {}, err
		}
		return func() {
			releaseDatabaseGeneration()
			sr.generationMu.RUnlock()
		}, nil
	}
	return sr.generationMu.RUnlock, nil
}

func (sr *SessionRepository) lockSessionGeneration(expected string) (func(), error) {
	if sr == nil {
		return func() {}, nil
	}
	sr.generationMu.RLock()
	if sr.databaseGeneration != expected {
		sr.generationMu.RUnlock()
		return func() {}, fmt.Errorf(
			"%w: SessionRepository=%s, 对象=%s",
			ErrDatabaseGenerationChanged,
			safeValueForLog(sr.databaseGeneration),
			safeValueForLog(expected),
		)
	}
	return sr.generationMu.RUnlock, nil
}

// rotateDatabaseGenerationLocked 仅由 Db 在持有 Session generation 写锁时调用。
func (sr *SessionRepository) rotateDatabaseGenerationLocked(generation string) {
	if sr == nil {
		return
	}
	sr.sessions.Range(func(key, value any) bool {
		playerID, _ := key.(string)
		session, _ := value.(*PlayerSession)
		sr.sessions.Delete(key)
		if session != nil {
			session.beginClose()
			session.releaseEntityCounts()
			session.mu.Lock()
			session.entities = nil
			session.dirty = nil
			session.dirtyPreparationState = nil
			session.dirtyPreparationErrors = nil
			session.dirtyVersions = nil
			session.absentTables = nil
			session.negativeCacheOverride = nil
			session.loaded = false
			session.mu.Unlock()
			session.finishClose(true)
		}
		if sr.lru != nil {
			sr.lru.Remove(playerID)
		}
		return true
	})
	sr.databaseGeneration = generation
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
