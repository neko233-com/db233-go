package db233

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// PrimaryKeyResetBarrier 是单业务主键清理屏障。
// 屏障期间拒绝新的 managed write，等待已准入写入结束，并丢弃该主键的
// Session、WriteBuffer、WAL 和失败队列快照；调用方随后可安全执行事务删除。
type PrimaryKeyResetBarrier struct {
	db                  *Db
	previousUnavailable bool
	mu                  sync.Mutex
	finalized           bool
}

// PrimaryKeyResetTarget 描述需要从 managed recovery 状态中精确丢弃的表和主键。
type PrimaryKeyResetTarget struct {
	TableName  string
	PrimaryKey string
}

// NewPrimaryKeyResetTarget 创建表级精确清理目标。
func NewPrimaryKeyResetTarget(tableName string, primaryKey any) (PrimaryKeyResetTarget, error) {
	key := fmt.Sprint(primaryKey)
	if tableName == "" {
		return PrimaryKeyResetTarget{}, NewValidationException("TableName 不能为空")
	}
	if key == "" || key == "<nil>" {
		return PrimaryKeyResetTarget{}, NewValidationException("PrimaryKey 不能为空")
	}
	return PrimaryKeyResetTarget{TableName: tableName, PrimaryKey: key}, nil
}

// BeginPrimaryKeyReset 开启单主键清理屏障。
// 调用前业务必须停止该主键的新业务请求并取消业务层 debounce；返回后必须调用 Commit、Abort 或 FailClosed。
func (db *Db) BeginPrimaryKeyReset(primaryKey any) (*PrimaryKeyResetBarrier, error) {
	return db.BeginPrimaryKeysReset(primaryKey)
}

// BeginPrimaryKeysReset 开启多主键原子清理屏障。
// 兼容无法提供表名的调用方，会跨所有表丢弃同值主键；新代码应优先使用 BeginPrimaryKeyTargetsReset。
func (db *Db) BeginPrimaryKeysReset(primaryKeys ...any) (*PrimaryKeyResetBarrier, error) {
	targets := make([]PrimaryKeyResetTarget, 0, len(primaryKeys))
	for _, primaryKey := range primaryKeys {
		key := fmt.Sprint(primaryKey)
		if key == "" || key == "<nil>" {
			return nil, NewValidationException("PrimaryKey 不能为空")
		}
		targets = append(targets, PrimaryKeyResetTarget{PrimaryKey: key})
	}
	return db.beginPrimaryKeyReset(primaryKeys, targets)
}

// BeginPrimaryKeyTargetsReset 开启表名+主键精确清理屏障。
// sessionKeys 用于丢弃业务 Session；targets 仅删除对应表的 WriteBuffer、WAL 和失败队列记录。
func (db *Db) BeginPrimaryKeyTargetsReset(
	sessionKeys []any,
	targets []PrimaryKeyResetTarget,
) (*PrimaryKeyResetBarrier, error) {
	return db.beginPrimaryKeyReset(sessionKeys, targets)
}

func (db *Db) beginPrimaryKeyReset(
	sessionKeys []any,
	targets []PrimaryKeyResetTarget,
) (*PrimaryKeyResetBarrier, error) {
	if db == nil {
		return nil, NewValidationException("Db 不能为 nil")
	}
	keys := make([]string, 0, len(sessionKeys))
	seenKeys := make(map[string]struct{}, len(sessionKeys))
	for _, primaryKey := range sessionKeys {
		key := fmt.Sprint(primaryKey)
		if key == "" || key == "<nil>" {
			return nil, NewValidationException("PrimaryKey 不能为空")
		}
		if _, exists := seenKeys[key]; exists {
			continue
		}
		seenKeys[key] = struct{}{}
		keys = append(keys, key)
	}
	normalizedTargets := make([]PrimaryKeyResetTarget, 0, len(targets))
	seenTargets := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.PrimaryKey == "" || target.PrimaryKey == "<nil>" {
			return nil, NewValidationException("Target PrimaryKey 不能为空")
		}
		targetIdentity := strings.ToLower(target.TableName) + "\x00" + target.PrimaryKey
		if _, exists := seenTargets[targetIdentity]; exists {
			continue
		}
		seenTargets[targetIdentity] = struct{}{}
		normalizedTargets = append(normalizedTargets, target)
	}
	if len(keys) == 0 && len(normalizedTargets) == 0 {
		return nil, NewValidationException("SessionKeys 和 Targets 不能同时为空")
	}

	db.rotationMu.Lock()
	db.resourceMu.Lock()
	closing := db.closing || db.closingState.Load()
	previousUnavailable := db.generationUnavailable.Load()
	if !closing {
		db.generationUnavailable.Store(true)
	}
	sessionRepo := db.SessionRepo
	writeJournal := db.WriteJournal
	faultTolerantManager := db.FaultTolerantMgr
	bufferedRepositories := db.bufferedRepositoryRegistryLocked()
	db.resourceMu.Unlock()
	if closing {
		db.rotationMu.Unlock()
		return nil, ErrCrudRepositoryClosed
	}

	barrier := &PrimaryKeyResetBarrier{
		db:                  db,
		previousUnavailable: previousUnavailable,
	}
	fail := func(cause error) (*PrimaryKeyResetBarrier, error) {
		db.generationUnavailable.Store(previousUnavailable)
		db.rotationMu.Unlock()
		return nil, cause
	}

	// unavailable 已发布，新 Session/managed write 会立即失败；先关闭目标 Session，
	// 避免其 dirty 在清理完成后由旧指针再次进入写队列。
	if sessionRepo != nil {
		for _, key := range keys {
			if err := sessionRepo.discardSessionForPrimaryKeyReset(key); err != nil {
				return fail(fmt.Errorf("丢弃目标 Session: primaryKey=%s: %w", safeValueForLog(key), err))
			}
		}
	}

	db.generationMu.Lock()
	if db.generationErr != nil {
		db.generationMu.Unlock()
		return fail(db.generationErr)
	}
	currentGeneration := db.databaseGeneration

	for _, target := range normalizedTargets {
		if writeJournal != nil {
			if err := writeJournal.discardPrimaryKeyUnderGenerationBarrier(target.TableName, target.PrimaryKey, currentGeneration); err != nil {
				db.generationMu.Unlock()
				return fail(fmt.Errorf("丢弃目标 WAL: table=%s, primaryKey=%s: %w",
					safeValueForLog(target.TableName), safeValueForLog(target.PrimaryKey), err))
			}
		}
		if faultTolerantManager != nil {
			if err := faultTolerantManager.discardPrimaryKeyUnderGenerationBarrier(target.TableName, target.PrimaryKey, currentGeneration); err != nil {
				db.generationMu.Unlock()
				return fail(fmt.Errorf("丢弃目标失败操作: table=%s, primaryKey=%s: %w",
					safeValueForLog(target.TableName), safeValueForLog(target.PrimaryKey), err))
			}
		}
		bufferedRepositories.Range(func(candidate, _ any) bool {
			repository, ok := candidate.(*BaseCrudRepository)
			if ok && repository != nil {
				repository.discardWriteBufferPrimaryKeyUnderGenerationBarrier(target.TableName, target.PrimaryKey)
			}
			return true
		})
	}
	return barrier, nil
}

// Commit 在业务删除事务提交后解除屏障。
func (barrier *PrimaryKeyResetBarrier) Commit() error {
	return barrier.finish()
}

// Abort 在业务删除事务回滚后解除屏障；已丢弃的旧内存快照不会恢复。
func (barrier *PrimaryKeyResetBarrier) Abort() error {
	return barrier.finish()
}

// FailClosed 在业务删除事务提交结果未知时结束屏障，但保持数据库 managed write 全局阻断。
// 调用方必须停止服务并人工确认数据库状态；禁止在当前进程继续写库。
func (barrier *PrimaryKeyResetBarrier) FailClosed(cause error) error {
	if barrier == nil || barrier.db == nil {
		return NewValidationException("PrimaryKeyResetBarrier 不能为空")
	}
	if cause == nil {
		cause = NewQueryException("单主键清理事务结果未知")
	}
	barrier.mu.Lock()
	defer barrier.mu.Unlock()
	if barrier.finalized {
		return NewValidationException("PrimaryKeyResetBarrier 已结束")
	}
	barrier.finalized = true
	blockedErr := errors.Join(ErrDatabaseGenerationBlocked, cause)
	barrier.db.generationErr = blockedErr
	barrier.db.generationUnavailable.Store(true)
	barrier.db.generationMu.Unlock()
	barrier.db.rotationMu.Unlock()
	return blockedErr
}

func (barrier *PrimaryKeyResetBarrier) finish() error {
	if barrier == nil || barrier.db == nil {
		return NewValidationException("PrimaryKeyResetBarrier 不能为空")
	}
	barrier.mu.Lock()
	defer barrier.mu.Unlock()
	if barrier.finalized {
		return NewValidationException("PrimaryKeyResetBarrier 已结束")
	}
	barrier.finalized = true
	barrier.db.generationMu.Unlock()
	barrier.db.generationUnavailable.Store(barrier.previousUnavailable)
	barrier.db.rotationMu.Unlock()
	return nil
}

func (r *BaseCrudRepository) discardWriteBufferPrimaryKeyUnderGenerationBarrier(tableName, primaryKey string) {
	if r == nil {
		return
	}
	r.wbMu.Lock()
	writeBuffer := r.writeBuffer
	r.wbMu.Unlock()
	if writeBuffer != nil {
		writeBuffer.discardPrimaryKeyUnderGenerationBarrier(tableName, primaryKey)
	}
}

func (wb *WriteBuffer) discardPrimaryKeyUnderGenerationBarrier(targetTableName, primaryKey string) {
	if wb == nil {
		return
	}
	wb.flushMu.Lock()
	defer wb.flushMu.Unlock()
	wb.mu.Lock()
	defer wb.mu.Unlock()
	for tableName, entities := range wb.pending {
		if targetTableName != "" && !strings.EqualFold(tableName, targetTableName) {
			continue
		}
		if _, exists := entities[primaryKey]; !exists {
			continue
		}
		delete(entities, primaryKey)
		delete(wb.preparationState[tableName], primaryKey)
		delete(wb.preparationErrors[tableName], primaryKey)
		wb.size--
		if len(entities) == 0 {
			delete(wb.pending, tableName)
			delete(wb.preparationState, tableName)
			delete(wb.preparationErrors, tableName)
		}
	}
}

func (j *LocalWriteJournal) discardPrimaryKeyUnderGenerationBarrier(tableName, primaryKey, expectedGeneration string) error {
	if j == nil {
		return nil
	}
	releaseOperation, err := j.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	j.replayMu.Lock()
	defer j.replayMu.Unlock()
	j.journalMu.Lock()
	if err := j.ensureGenerationLocked(); err != nil {
		j.journalMu.Unlock()
		return err
	}
	if j.databaseGeneration != expectedGeneration {
		j.journalMu.Unlock()
		return fmt.Errorf("%w: WAL=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(j.databaseGeneration), safeValueForLog(expectedGeneration))
	}
	if err := j.ensurePendingCacheLoadedLocked(); err != nil {
		j.journalMu.Unlock()
		return err
	}
	ids := make([]string, 0)
	for _, entry := range j.stateLocked().pendingCache {
		if entry != nil &&
			entry.PrimaryKey == primaryKey &&
			(tableName == "" || strings.EqualFold(entry.TableName, tableName)) {
			ids = append(ids, entry.ID)
		}
	}
	j.journalMu.Unlock()
	return j.removeEntriesWithLifecycleLease(ids, expectedGeneration)
}

func (ftm *FaultTolerantManager) discardPrimaryKeyUnderGenerationBarrier(tableName, primaryKey, expectedGeneration string) error {
	if ftm == nil {
		return nil
	}
	releaseOperation, err := ftm.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	ftm.retryMutex.Lock()
	defer ftm.retryMutex.Unlock()
	ftm.recoveryMu.Lock()
	defer ftm.recoveryMu.Unlock()
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	if err := ftm.initializeRecoveryLocked(); err != nil {
		return err
	}
	if ftm.databaseGeneration != expectedGeneration {
		return fmt.Errorf("%w: 失败操作=%s, Db=%s", ErrDatabaseGenerationChanged, safeValueForLog(ftm.databaseGeneration), safeValueForLog(expectedGeneration))
	}
	previous := ftm.failedOps
	remaining := make([]*FailedOperation, 0, len(previous))
	for _, operation := range previous {
		if operation != nil &&
			fmt.Sprint(operation.PrimaryKey) == primaryKey &&
			(tableName == "" || strings.EqualFold(operation.TableName, tableName)) {
			continue
		}
		remaining = append(remaining, operation)
	}
	ftm.failedOps = remaining
	if err := ftm.persistFailedOperationsLocked(); err != nil {
		ftm.failedOps = previous
		return errors.Join(NewQueryException("持久化主键清理后的失败队列失败"), err)
	}
	return nil
}
