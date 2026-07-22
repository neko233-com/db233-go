package db233

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrWriteBufferStopped 表示写缓冲生命周期已结束，不能再接收新数据。
var ErrWriteBufferStopped = errors.New("写缓冲已停止")

const maxWriteBufferRetryInterval = 5 * time.Second

type writeBufferPreparationState uint8

const (
	writeBufferUnprepared writeBufferPreparationState = iota
	writeBufferPrepared
	writeBufferPreparationFailed
)

// WriteBuffer 异步写缓冲：合并高频 Save，按表批量 UPSERT 刷盘。
type WriteBuffer struct {
	repo *BaseCrudRepository

	mu                 sync.Mutex
	pending            map[string]map[string]IDbEntity // tableName -> pkKey -> latest entity
	preparationState   map[string]map[string]writeBufferPreparationState
	preparationErrors  map[string]map[string]error
	size               int
	started            bool
	stopped            bool
	databaseGeneration string

	flushMu        sync.Mutex
	stopLoopOnce   sync.Once
	finalFlushOnce sync.Once
	doneOnce       sync.Once
	stopErrMu      sync.Mutex
	stopErr        error
	stopCh         chan struct{}
	doneCh         chan struct{}

	backgroundErrMu sync.Mutex
	backgroundErr   error
	backgroundFails uint64
}

func newWriteBuffer(repo *BaseCrudRepository) *WriteBuffer {
	generation := ""
	if repo != nil && repo.db != nil {
		generation = repo.db.databaseGenerationSnapshot()
	}
	return newWriteBufferForGeneration(repo, generation)
}

func newWriteBufferForGeneration(repo *BaseCrudRepository, generation string) *WriteBuffer {
	return &WriteBuffer{
		repo:               repo,
		pending:            make(map[string]map[string]IDbEntity),
		preparationState:   make(map[string]map[string]writeBufferPreparationState),
		preparationErrors:  make(map[string]map[string]error),
		databaseGeneration: generation,
		stopCh:             make(chan struct{}),
		doneCh:             make(chan struct{}),
	}
}

// Start 启动后台定时刷盘。
func (wb *WriteBuffer) Start(settings CrudPerformanceSettings) {
	if wb == nil {
		return
	}
	wb.mu.Lock()
	if wb.started || wb.stopped {
		wb.mu.Unlock()
		return
	}
	wb.started = true
	go wb.loop(settings)
	wb.mu.Unlock()
}

// Stop 停止后台刷盘并同步刷完队列。
func (wb *WriteBuffer) Stop() error {
	if wb == nil {
		return nil
	}
	wb.stopLoop()
	wb.finalFlushOnce.Do(func() {
		finalFlushErr := wb.Flush()
		wb.backgroundErrMu.Lock()
		backgroundErr := wb.backgroundErr
		wb.backgroundErrMu.Unlock()
		wb.stopErrMu.Lock()
		wb.stopErr = errors.Join(backgroundErr, finalFlushErr)
		wb.stopErrMu.Unlock()
	})
	wb.stopErrMu.Lock()
	defer wb.stopErrMu.Unlock()
	return wb.stopErr
}

func (wb *WriteBuffer) stopLoop() {
	if wb == nil {
		return
	}
	wb.stopLoopOnce.Do(func() {
		wb.mu.Lock()
		wb.stopped = true
		started := wb.started
		if !started {
			wb.doneOnce.Do(func() { close(wb.doneCh) })
		}
		wb.mu.Unlock()
		if started {
			close(wb.stopCh)
		}
		<-wb.doneCh
	})
}

// stopUnderGenerationLease 停止后台循环，并在调用方持有 Db generation
// 写锁时完成唯一一次最终刷盘。Db.Close 使用该路径，避免 unavailable 门开启
// 后公开 Flush 重入 generation RLock。
func (wb *WriteBuffer) stopUnderGenerationLease(expectedGeneration string) error {
	if wb == nil {
		return nil
	}
	wb.stopLoop()
	wb.finalFlushOnce.Do(func() {
		finalFlushErr := wb.flushUnderGenerationLease(expectedGeneration)
		wb.backgroundErrMu.Lock()
		backgroundErr := wb.backgroundErr
		wb.backgroundErrMu.Unlock()
		wb.stopErrMu.Lock()
		wb.stopErr = errors.Join(backgroundErr, finalFlushErr)
		wb.stopErrMu.Unlock()
	})
	wb.stopErrMu.Lock()
	defer wb.stopErrMu.Unlock()
	return wb.stopErr
}

func (wb *WriteBuffer) loop(initial CrudPerformanceSettings) {
	defer wb.doneOnce.Do(func() { close(wb.doneCh) })
	baseInterval := normalizedWriteBufferInterval(initial.WriteBufferFlushIntervalMs)
	activeInterval := baseInterval
	consecutiveFailures := uint(0)
	ticker := time.NewTicker(activeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-wb.stopCh:
			return
		case <-ticker.C:
			settings := GetCrudPerformanceSettings().Snapshot()
			baseInterval = normalizedWriteBufferInterval(settings.WriteBufferFlushIntervalMs)
			if err := wb.Flush(); err != nil {
				wb.recordBackgroundFlushError(err)
				LogWarn("写缓冲后台 Flush 失败，数据已重新入队: %s", safeErrorForLog(err))
				consecutiveFailures++
				activeInterval = writeBufferRetryInterval(baseInterval, consecutiveFailures)
			} else {
				consecutiveFailures = 0
				activeInterval = baseInterval
			}
			ticker.Reset(activeInterval)
		}
	}
}

func normalizedWriteBufferInterval(milliseconds int) time.Duration {
	if milliseconds <= 0 {
		return 100 * time.Millisecond
	}
	const maxDurationMilliseconds = int64((1<<63 - 1) / time.Millisecond)
	if int64(milliseconds) > maxDurationMilliseconds {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func writeBufferRetryInterval(base time.Duration, consecutiveFailures uint) time.Duration {
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	retry := base
	for attempt := uint(0); attempt < consecutiveFailures; attempt++ {
		if retry >= maxWriteBufferRetryInterval/2 {
			return maxWriteBufferRetryInterval
		}
		retry *= 2
	}
	if retry > maxWriteBufferRetryInterval {
		return maxWriteBufferRetryInterval
	}
	return retry
}

// Enqueue 入队实体（同表同主键保留最新版本）。
func (wb *WriteBuffer) Enqueue(entity IDbEntity) (queued bool, err error) {
	if entity == nil {
		return false, NewValidationException("实体不能为 nil")
	}
	// 短租约绑定本次逻辑写入的 generation，随即释放；深快照可能调用用户
	// Snapshotter，不得持锁。快照后重新获取同一代租约，若期间发生切代则
	// 整体拒绝，禁止旧世界实体进入新 generation。
	databaseGeneration, validationRelease, err := wb.lockDatabaseGeneration()
	if err != nil {
		return false, err
	}
	validationRelease()
	entitySnapshot, err := SnapshotEntity(entity)
	if err != nil {
		return false, err
	}
	releaseGeneration, err := wb.lockExpectedDatabaseGeneration(databaseGeneration)
	if err != nil {
		return false, err
	}
	defer releaseGeneration()
	return wb.enqueueOwnedSnapshotUnderGenerationLease(entitySnapshot, databaseGeneration)
}

// enqueueOwnedSnapshotUnderGenerationLease 接收调用方独占的不可变快照。
// 调用方必须持有 expected generation 租约，且成功后不得再修改
// entitySnapshot。该内部路径用于 Session -> WriteBuffer 所有权转移，
// 避免为安全隔离做第三次深拷贝。
func (wb *WriteBuffer) enqueueOwnedSnapshotUnderGenerationLease(
	entitySnapshot IDbEntity,
	databaseGeneration string,
) (bool, error) {
	if wb == nil || wb.repo == nil {
		return false, NewValidationException("写缓冲未绑定 Repository")
	}
	if isNilStrictValue(entitySnapshot) {
		return false, NewValidationException("实体快照不能为 nil")
	}

	tableName := wb.repo.getTableName(entitySnapshot)
	if tableName == "" {
		return false, NewValidationException("无法获取表名")
	}

	cm := GetCrudManagerInstance()
	pk := cm.GetPrimaryKeyValue(entitySnapshot)
	if wb.repo.isZeroValue(pk) {
		return false, NewValidationException("写缓冲要求实体主键非零值")
	}
	pkKey := fmt.Sprintf("%v", pk)

	settings := GetCrudPerformanceSettings().Snapshot()
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.stopped {
		return false, ErrWriteBufferStopped
	}
	if wb.databaseGeneration != databaseGeneration {
		return false, fmt.Errorf(
			"%w: 写缓冲=%s, 对象=%s",
			ErrDatabaseGenerationChanged,
			safeValueForLog(wb.databaseGeneration),
			safeValueForLog(databaseGeneration),
		)
	}
	if settings.WriteBufferMaxQueueSize > 0 && wb.size >= settings.WriteBufferMaxQueueSize {
		return false, nil
	}

	if wb.pending[tableName] == nil {
		wb.pending[tableName] = make(map[string]IDbEntity)
		wb.preparationState[tableName] = make(map[string]writeBufferPreparationState)
		wb.preparationErrors[tableName] = make(map[string]error)
	}
	if _, exists := wb.pending[tableName][pkKey]; !exists {
		wb.size++
	}
	wb.pending[tableName][pkKey] = entitySnapshot
	wb.preparationState[tableName][pkKey] = writeBufferUnprepared
	delete(wb.preparationErrors[tableName], pkKey)
	return true, nil
}

func (wb *WriteBuffer) queueSize() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.size
}

// Flush 同步刷盘：按表分块 SaveBatchUpsert。
func (wb *WriteBuffer) Flush() error {
	if wb == nil || wb.repo == nil {
		return NewValidationException("写缓冲未绑定 Repository")
	}
	databaseGeneration, releaseGeneration, err := wb.lockDatabaseGeneration()
	if err != nil {
		return err
	}
	defer releaseGeneration()
	return wb.flushUnderGenerationLease(databaseGeneration)
}

// flushUnderGenerationLease 在调用方已经持有 Db generation 租约时刷盘。
// generation transition 持 Db 写锁调用此方法，避免重入读锁死锁。
func (wb *WriteBuffer) flushUnderGenerationLease(expectedGeneration string) (flushErr error) {
	if wb == nil || wb.repo == nil {
		return NewValidationException("写缓冲未绑定 Repository")
	}
	wb.flushMu.Lock()
	defer wb.flushMu.Unlock()

	wb.mu.Lock()
	if wb.databaseGeneration != expectedGeneration {
		current := wb.databaseGeneration
		wb.mu.Unlock()
		return fmt.Errorf(
			"%w: 写缓冲=%s, 当前=%s",
			ErrDatabaseGenerationChanged,
			safeValueForLog(expectedGeneration),
			safeValueForLog(current),
		)
	}
	defer func() {
		if flushErr == nil {
			wb.clearBackgroundFlushError()
		}
	}()
	if wb.size == 0 {
		wb.mu.Unlock()
		return nil
	}
	pending := wb.pending
	preparationState := wb.preparationState
	preparationErrors := wb.preparationErrors
	wb.pending = make(map[string]map[string]IDbEntity)
	wb.preparationState = make(map[string]map[string]writeBufferPreparationState)
	wb.preparationErrors = make(map[string]map[string]error)
	wb.size = 0
	wb.mu.Unlock()

	flushErrors := make([]error, 0, 4)
	suppressedErrors := 0
	failedPending := make(map[string]map[string]IDbEntity)
	failedPreparationState := make(map[string]map[string]writeBufferPreparationState)
	failedPreparationErrors := make(map[string]map[string]error)
	settings := GetCrudPerformanceSettings().Snapshot()
	maxBatch := settings.WriteBufferMaxBatchSize
	if maxBatch <= 0 {
		maxBatch = settings.BatchUpsertChunkSize
	}
	if maxBatch <= 0 {
		maxBatch = 100
	}

	for tableName, entitiesMap := range pending {
		keys := make([]string, 0, len(entitiesMap))
		for key := range entitiesMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		preparedKeys := make([]string, 0, len(keys))
		var entities []IDbEntity
		if EnableAllocPoolEnabled() {
			entities = acquireEntitySlice(len(entitiesMap))
		} else {
			entities = make([]IDbEntity, 0, len(entitiesMap))
		}
		for _, key := range keys {
			entity := entitiesMap[key]
			if preparationState[tableName] == nil {
				preparationState[tableName] = make(map[string]writeBufferPreparationState)
				preparationErrors[tableName] = make(map[string]error)
			}
			switch preparationState[tableName][key] {
			case writeBufferPreparationFailed:
				prepareErr := preparationErrors[tableName][key]
				flushErrors = appendBoundedRecoveryError(
					flushErrors,
					fmt.Errorf("序列化表 %s 主键 %s 已失败，拒绝写入: %w", tableName, key, prepareErr),
					&suppressedErrors,
				)
				if failedPending[tableName] == nil {
					failedPending[tableName] = make(map[string]IDbEntity)
					failedPreparationState[tableName] = make(map[string]writeBufferPreparationState)
					failedPreparationErrors[tableName] = make(map[string]error)
				}
				failedPending[tableName][key] = entity
				failedPreparationState[tableName][key] = writeBufferPreparationFailed
				failedPreparationErrors[tableName][key] = prepareErr
				continue
			case writeBufferUnprepared:
				// 先标记再调用；即使用户 hook panic，也不在自动重试中重复执行副作用。
				preparationState[tableName][key] = writeBufferPrepared
				if err := serializeWriteBufferEntity(entity); err != nil {
					preparationState[tableName][key] = writeBufferPreparationFailed
					preparationErrors[tableName][key] = err
					flushErrors = appendBoundedRecoveryError(
						flushErrors,
						fmt.Errorf("序列化表 %s 主键 %s: %w", tableName, key, err),
						&suppressedErrors,
					)
					if failedPending[tableName] == nil {
						failedPending[tableName] = make(map[string]IDbEntity)
						failedPreparationState[tableName] = make(map[string]writeBufferPreparationState)
						failedPreparationErrors[tableName] = make(map[string]error)
					}
					failedPending[tableName][key] = entity
					failedPreparationState[tableName][key] = writeBufferPreparationFailed
					failedPreparationErrors[tableName][key] = err
					continue
				}
			}
			preparedKeys = append(preparedKeys, key)
			entities = append(entities, entity)
		}
		for start := 0; start < len(entities); start += maxBatch {
			end := start + maxBatch
			if end > len(entities) {
				end = len(entities)
			}
			if err := wb.repo.updateBatchUpsertPreparedUnderGenerationLease(entities[start:end], expectedGeneration); err != nil {
				flushErrors = appendBoundedRecoveryError(
					flushErrors,
					fmt.Errorf("刷新表 %s 的批次 [%d:%d]: %w", tableName, start, end, err),
					&suppressedErrors,
				)
				if failedPending[tableName] == nil {
					failedPending[tableName] = make(map[string]IDbEntity)
					failedPreparationState[tableName] = make(map[string]writeBufferPreparationState)
					failedPreparationErrors[tableName] = make(map[string]error)
				}
				for index := start; index < end; index++ {
					failedPending[tableName][preparedKeys[index]] = entities[index]
					failedPreparationState[tableName][preparedKeys[index]] = writeBufferPrepared
				}
			}
		}
		if EnableAllocPoolEnabled() {
			releaseEntitySlice(entities)
		}
	}
	if len(failedPending) > 0 {
		wb.mu.Lock()
		wb.mergePending(failedPending, failedPreparationState, failedPreparationErrors)
		wb.mu.Unlock()
	}
	if suppressedErrors > 0 {
		flushErrors = append(flushErrors, fmt.Errorf("另有 %d 个写缓冲错误已省略", suppressedErrors))
	}
	return errors.Join(flushErrors...)
}

func (wb *WriteBuffer) lockDatabaseGeneration() (string, func(), error) {
	if wb == nil {
		return "", func() {}, NewValidationException("写缓冲不能为 nil")
	}
	wb.mu.Lock()
	expectedGeneration := wb.databaseGeneration
	wb.mu.Unlock()
	if wb.repo == nil || wb.repo.db == nil {
		return expectedGeneration, func() {}, nil
	}
	if wb.repo.db.isDatabaseGenerationUnavailable() {
		return expectedGeneration, func() {}, ErrDatabaseGenerationBlocked
	}
	current, release, err := wb.repo.db.lockDatabaseGeneration(expectedGeneration)
	return current, release, err
}

func (wb *WriteBuffer) lockExpectedDatabaseGeneration(expectedGeneration string) (func(), error) {
	if wb == nil {
		return func() {}, NewValidationException("写缓冲不能为 nil")
	}
	if wb.repo == nil || wb.repo.db == nil {
		return func() {}, nil
	}
	_, release, err := wb.repo.db.lockDatabaseGeneration(expectedGeneration)
	return release, err
}

// rotateDatabaseGeneration 仅由 Db 在持有 generation 写锁时调用。
func (wb *WriteBuffer) rotateDatabaseGeneration(generation string) {
	if wb == nil {
		return
	}
	wb.flushMu.Lock()
	defer wb.flushMu.Unlock()
	wb.mu.Lock()
	wb.pending = make(map[string]map[string]IDbEntity)
	wb.preparationState = make(map[string]map[string]writeBufferPreparationState)
	wb.preparationErrors = make(map[string]map[string]error)
	wb.size = 0
	wb.databaseGeneration = generation
	wb.mu.Unlock()
}

func (wb *WriteBuffer) mergePending(
	back map[string]map[string]IDbEntity,
	backState map[string]map[string]writeBufferPreparationState,
	backErrors map[string]map[string]error,
) {
	for table, pkMap := range back {
		if wb.pending[table] == nil {
			wb.pending[table] = make(map[string]IDbEntity)
			wb.preparationState[table] = make(map[string]writeBufferPreparationState)
			wb.preparationErrors[table] = make(map[string]error)
		}
		for pk, entity := range pkMap {
			if _, exists := wb.pending[table][pk]; exists {
				continue
			}
			wb.size++
			wb.pending[table][pk] = entity
			wb.preparationState[table][pk] = backState[table][pk]
			if err := backErrors[table][pk]; err != nil {
				wb.preparationErrors[table][pk] = err
			}
		}
	}
}

func serializeWriteBufferEntity(entity IDbEntity) (err error) {
	return runEntitySerializeHook(entity)
}

func (wb *WriteBuffer) recordBackgroundFlushError(err error) {
	if wb == nil || err == nil {
		return
	}
	wb.backgroundErrMu.Lock()
	wb.backgroundErr = err
	wb.backgroundFails++
	wb.backgroundErrMu.Unlock()
}

// clearBackgroundFlushError 只清除“当前故障”；backgroundFails 作为历史
// 计数始终单调。任一成功 Flush（包括最终 Flush）都表明队列
// 已恢复，Stop 不应再返回过期后台错误。
func (wb *WriteBuffer) clearBackgroundFlushError() {
	if wb == nil {
		return
	}
	wb.backgroundErrMu.Lock()
	wb.backgroundErr = nil
	wb.backgroundErrMu.Unlock()
}

// LastBackgroundFlushError 返回最近一次后台 Flush 错误及累计失败次数。
// Stop 会把该错误合并到返回值，确保关闭路径不会吞掉后台失败。
func (wb *WriteBuffer) LastBackgroundFlushError() (error, uint64) {
	if wb == nil {
		return nil, 0
	}
	wb.backgroundErrMu.Lock()
	defer wb.backgroundErrMu.Unlock()
	return wb.backgroundErr, wb.backgroundFails
}
