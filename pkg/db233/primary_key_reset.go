package db233

import (
	"errors"
	"fmt"
	"sync"
)

// PrimaryKeyResetBarrier 是单业务主键清理屏障。
// 屏障期间拒绝新的 managed write，等待已准入写入结束，并丢弃该主键的
// Session、WriteBuffer、WAL 和失败队列快照；调用方随后可安全执行事务删除。
type PrimaryKeyResetBarrier struct {
	db                  *Db
	primaryKey          string
	previousUnavailable bool
	mu                  sync.Mutex
	finalized           bool
}

// BeginPrimaryKeyReset 开启单主键清理屏障。
// 调用前业务必须停止该主键的新业务请求并取消业务层 debounce；返回后必须调用 Commit 或 Abort。
func (db *Db) BeginPrimaryKeyReset(primaryKey any) (*PrimaryKeyResetBarrier, error) {
	if db == nil {
		return nil, NewValidationException("Db 不能为 nil")
	}
	key := fmt.Sprint(primaryKey)
	if key == "" || key == "<nil>" {
		return nil, NewValidationException("PrimaryKey 不能为空")
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
		primaryKey:          key,
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
		if err := sessionRepo.discardSessionForPrimaryKeyReset(key); err != nil {
			return fail(fmt.Errorf("丢弃目标 Session: %w", err))
		}
	}

	db.generationMu.Lock()
	if db.generationErr != nil {
		db.generationMu.Unlock()
		return fail(db.generationErr)
	}
	currentGeneration := db.databaseGeneration

	if writeJournal != nil {
		if err := writeJournal.discardPrimaryKeyUnderGenerationBarrier(key, currentGeneration); err != nil {
			db.generationMu.Unlock()
			return fail(fmt.Errorf("丢弃目标 WAL: %w", err))
		}
	}
	if faultTolerantManager != nil {
		if err := faultTolerantManager.discardPrimaryKeyUnderGenerationBarrier(key, currentGeneration); err != nil {
			db.generationMu.Unlock()
			return fail(fmt.Errorf("丢弃目标失败操作: %w", err))
		}
	}
	bufferedRepositories.Range(func(candidate, _ any) bool {
		repository, ok := candidate.(*BaseCrudRepository)
		if ok && repository != nil {
			repository.discardWriteBufferPrimaryKeyUnderGenerationBarrier(key)
		}
		return true
	})
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

func (r *BaseCrudRepository) discardWriteBufferPrimaryKeyUnderGenerationBarrier(primaryKey string) {
	if r == nil {
		return
	}
	r.wbMu.Lock()
	writeBuffer := r.writeBuffer
	r.wbMu.Unlock()
	if writeBuffer != nil {
		writeBuffer.discardPrimaryKeyUnderGenerationBarrier(primaryKey)
	}
}

func (wb *WriteBuffer) discardPrimaryKeyUnderGenerationBarrier(primaryKey string) {
	if wb == nil {
		return
	}
	wb.flushMu.Lock()
	defer wb.flushMu.Unlock()
	wb.mu.Lock()
	defer wb.mu.Unlock()
	for tableName, entities := range wb.pending {
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

func (j *LocalWriteJournal) discardPrimaryKeyUnderGenerationBarrier(primaryKey, expectedGeneration string) error {
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
		if entry != nil && entry.PrimaryKey == primaryKey {
			ids = append(ids, entry.ID)
		}
	}
	j.journalMu.Unlock()
	return j.removeEntriesWithLifecycleLease(ids, expectedGeneration)
}

func (ftm *FaultTolerantManager) discardPrimaryKeyUnderGenerationBarrier(primaryKey, expectedGeneration string) error {
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
		if operation != nil && fmt.Sprint(operation.PrimaryKey) == primaryKey {
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
