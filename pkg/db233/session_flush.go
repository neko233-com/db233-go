package db233

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

func jitterDuration(base time.Duration, jitterPct int) time.Duration {
	if base <= 0 || jitterPct <= 0 {
		return base
	}
	if jitterPct > 100 {
		jitterPct = 100
	}
	baseNanos := int64(base)
	// 先除后乘并补余数，避免接近 MaxInt64 的 duration 乘百分比溢出。
	delta := (baseNanos/100)*int64(jitterPct) + (baseNanos%100)*int64(jitterPct)/100
	if delta <= 0 {
		return base
	}
	span := 2*uint64(delta) + 1
	sample := rand.Uint64N(span)
	var result time.Duration
	if sample <= uint64(delta) {
		decrease := time.Duration(uint64(delta) - sample)
		result = base - decrease
	} else {
		increase := time.Duration(sample - uint64(delta))
		maxIncrease := time.Duration(1<<63-1) - base
		if increase > maxIncrease {
			result = time.Duration(1<<63 - 1)
		} else {
			result = base + increase
		}
	}
	if result < time.Millisecond {
		return time.Millisecond
	}
	return result
}

func saturatedMilliseconds(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	const maxDuration = time.Duration(1<<63 - 1)
	maxMilliseconds := int64(maxDuration / time.Millisecond)
	if int64(value) > maxMilliseconds {
		return maxDuration
	}
	return time.Duration(value) * time.Millisecond
}

func buildFlushBatchTasks(entities []IDbEntity, chunkSize int, tableNameOf func(IDbEntity) string) [][]IDbEntity {
	if len(entities) == 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	}
	if chunkSize <= 0 {
		chunkSize = 200
	}

	groups := groupEntitiesByTable(entities, tableNameOf)
	tasks := make([][]IDbEntity, 0, len(groups))
	for _, group := range groups {
		for start := 0; start < len(group); start += chunkSize {
			end := start + chunkSize
			if end > len(group) {
				end = len(group)
			}
			batch := make([]IDbEntity, end-start)
			copy(batch, group[start:end])
			tasks = append(tasks, batch)
		}
	}
	return tasks
}

func (sr *SessionRepository) flushEntityBatches(tasks [][]IDbEntity, maxWorkers int, waveIntervalMs int) error {
	return sr.flushEntityBatchesWithWriter(
		tasks,
		maxWorkers,
		waveIntervalMs,
		func(entities []IDbEntity) error {
			return sr.repo.updateBatchUpsertWithFlushSource(entities, FlushWriteSourceSession)
		},
	)
}

func (sr *SessionRepository) flushPreparedEntityBatchesUnderGenerationLease(
	tasks [][]IDbEntity,
	maxWorkers int,
	waveIntervalMs int,
	databaseGeneration string,
) error {
	return sr.flushEntityBatchesWithWriter(
		tasks,
		maxWorkers,
		waveIntervalMs,
		func(entities []IDbEntity) error {
			return sr.repo.updateBatchUpsertPreparedUnderGenerationLeaseWithFlushSource(
				entities,
				databaseGeneration,
				FlushWriteSourceSession,
			)
		},
	)
}

func (sr *SessionRepository) flushEntityBatchesWithWriter(
	tasks [][]IDbEntity,
	maxWorkers int,
	waveIntervalMs int,
	writer func([]IDbEntity) error,
) error {
	if len(tasks) == 0 {
		return nil
	}
	if writer == nil {
		return NewValidationException("Session flush writer 不能为 nil")
	}
	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	if waveIntervalMs <= 0 || len(tasks) <= maxWorkers {
		return sr.runFlushBatchWaveWithWriter(tasks, maxWorkers, writer)
	}

	for start := 0; start < len(tasks); start += maxWorkers {
		end := start + maxWorkers
		if end > len(tasks) {
			end = len(tasks)
		}
		if err := sr.runFlushBatchWaveWithWriter(tasks[start:end], maxWorkers, writer); err != nil {
			return err
		}
		if end < len(tasks) {
			time.Sleep(saturatedMilliseconds(waveIntervalMs))
		}
	}
	return nil
}

func (sr *SessionRepository) runFlushBatchWave(tasks [][]IDbEntity, maxWorkers int) error {
	return sr.runFlushBatchWaveWithWriter(
		tasks,
		maxWorkers,
		func(entities []IDbEntity) error {
			return sr.repo.updateBatchUpsertWithFlushSource(entities, FlushWriteSourceSession)
		},
	)
}

func (sr *SessionRepository) runFlushBatchWaveWithWriter(
	tasks [][]IDbEntity,
	maxWorkers int,
	writer func([]IDbEntity) error,
) error {
	if len(tasks) == 0 {
		return nil
	}
	if writer == nil {
		return NewValidationException("Session flush writer 不能为 nil")
	}
	if maxWorkers <= 0 {
		maxWorkers = 8
	}
	if maxWorkers > len(tasks) {
		maxWorkers = len(tasks)
	}

	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	for _, batch := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(entities []IDbEntity) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := writer(entities); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(batch)
	}
	wg.Wait()
	return firstErr
}

func (sr *SessionRepository) acquireFlushSlot() func() {
	max := entityCacheSettingsSnapshot().SessionFlushMaxWorkers
	if max <= 0 {
		max = 8
	}
	sr.flushSemMu.Lock()
	if sr.flushSem == nil || cap(sr.flushSem) != max {
		sr.flushSem = make(chan struct{}, max)
	}
	sem := sr.flushSem
	sr.flushSemMu.Unlock()
	sem <- struct{}{}
	// release 必须绑定本次 acquire 使用的 channel；运行期调并发度会替换
	// sr.flushSem，重新读取字段会从错误 channel 接收并永久阻塞。
	return func() {
		<-sem
	}
}

func (sr *SessionRepository) flushSession(session *PlayerSession, includeWriteBuffer bool) error {
	releaseSlot := sr.acquireFlushSlot()
	defer releaseSlot()
	return session.flushInternal(includeWriteBuffer)
}

func (sr *SessionRepository) drainAllDirtyByPlayer() (map[string][]sessionDirtySnapshot, []func()) {
	drained := make(map[string][]sessionDirtySnapshot)
	releases := make([]func(), 0)
	sr.sessions.Range(func(key, value any) bool {
		playerID := key.(string)
		session := value.(*PlayerSession)
		release, err := session.beginLocalOperation()
		if err != nil {
			return true
		}
		if entities := session.takeDirty(); len(entities) > 0 {
			drained[playerID] = entities
			releases = append(releases, release)
		} else {
			release()
		}
		return true
	})
	return drained, releases
}

func releaseSessionOperations(releases []func()) {
	for _, release := range releases {
		release()
	}
}

func (sr *SessionRepository) restoreDirtyByPlayer(drained map[string][]sessionDirtySnapshot) {
	for playerID, snapshots := range drained {
		if v, ok := sr.sessions.Load(playerID); ok {
			v.(*PlayerSession).restoreDirty(snapshots)
		}
	}
}

func prepareSessionDirtyByPlayer(
	drained map[string][]sessionDirtySnapshot,
) (prepared []IDbEntity, failed map[string][]sessionDirtySnapshot, preparationErr error) {
	failed = make(map[string][]sessionDirtySnapshot)
	preparationErrors := make([]error, 0, 4)
	for playerID, snapshots := range drained {
		playerPrepared, playerFailed, err := prepareSessionDirtySnapshots(snapshots)
		// prepareSessionDirtySnapshots mutates the slice state in place. Store it
		// back explicitly because map index expressions are not addressable.
		drained[playerID] = snapshots
		prepared = append(prepared, playerPrepared...)
		if len(playerFailed) > 0 {
			failed[playerID] = playerFailed
		}
		if err != nil {
			preparationErrors = append(preparationErrors, err)
		}
	}
	return prepared, failed, errors.Join(preparationErrors...)
}

func (sr *SessionRepository) flushAllDirtyMerged(settings EntityCacheSettings) error {
	drained, releases := sr.drainAllDirtyByPlayer()
	defer releaseSessionOperations(releases)
	if len(drained) == 0 {
		return nil
	}

	all, preparationFailed, preparationErr := prepareSessionDirtyByPlayer(drained)

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	tasks := buildFlushBatchTasks(all, chunkSize, sr.repo.getTableName)
	writeErr := sr.flushPreparedEntityBatchesUnderGenerationLease(
		tasks,
		settings.SessionFlushMaxWorkers,
		0,
		sr.databaseGeneration,
	)
	if writeErr != nil {
		sr.restoreDirtyByPlayer(drained)
	} else {
		sr.restoreDirtyByPlayer(preparationFailed)
	}
	return errors.Join(preparationErr, writeErr)
}

func (sr *SessionRepository) flushAllDirtyPerSession(settings EntityCacheSettings) error {
	maxWorkers := settings.SessionFlushMaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	var sessions []*PlayerSession
	sr.sessions.Range(func(_, value any) bool {
		sessions = append(sessions, value.(*PlayerSession))
		return true
	})
	if len(sessions) == 0 {
		return nil
	}

	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	for _, session := range sessions {
		wg.Add(1)
		sem <- struct{}{}
		go func(s *PlayerSession) {
			defer wg.Done()
			defer func() { <-sem }()
			release, operationErr := s.beginLocalOperation()
			if operationErr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = operationErr
				}
				errMu.Unlock()
				return
			}
			defer release()
			if err := s.flushInternal(false); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(session)
	}
	wg.Wait()
	return firstErr
}

func (sr *SessionRepository) flushAllShutdown() error {
	settings := entityCacheSettingsSnapshot()
	drained, releases := sr.drainAllDirtyByPlayer()
	defer releaseSessionOperations(releases)
	all, preparationFailed, preparationErr := prepareSessionDirtyByPlayer(drained)

	maxWorkers := settings.ShutdownFlushMaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = settings.SessionFlushMaxWorkers
	}
	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	var writeErr error
	if len(all) > 0 {
		chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
		tasks := buildFlushBatchTasks(all, chunkSize, sr.repo.getTableName)
		writeErr = sr.flushPreparedEntityBatchesUnderGenerationLease(
			tasks,
			maxWorkers,
			settings.ShutdownFlushWaveIntervalMs,
			sr.databaseGeneration,
		)
	}
	if writeErr != nil {
		sr.restoreDirtyByPlayer(drained)
	} else {
		sr.restoreDirtyByPlayer(preparationFailed)
	}
	firstErr := errors.Join(preparationErr, writeErr)

	if err := sr.repo.flushWriteBufferUnderGenerationLease(sr.databaseGeneration); err != nil {
		firstErr = errors.Join(firstErr, fmt.Errorf("FlushWriteBuffer: %w", err))
	}
	return firstErr
}

// flushAllForGenerationTransitionUnderLease 在调用方同时持有 Session generation
// 写锁和 Db generation 写锁时，严格刷出旧代 Session 脏数据。它不能调用公开
// UpdateBatchUpsert/FlushWriteBuffer，否则会重入 Db generation 读锁并死锁。
func (sr *SessionRepository) flushAllForGenerationTransitionUnderLease(expectedGeneration string) error {
	if sr == nil {
		return nil
	}
	if sr.repo == nil {
		return NewValidationException("SessionRepository 未绑定 Repository")
	}
	if sr.databaseGeneration != expectedGeneration {
		return fmt.Errorf(
			"%w: SessionRepository=%s, 当前=%s",
			ErrDatabaseGenerationChanged,
			safeValueForLog(sr.databaseGeneration),
			safeValueForLog(expectedGeneration),
		)
	}

	drained, releases := sr.drainAllDirtyByPlayer()
	defer releaseSessionOperations(releases)
	if len(drained) == 0 {
		return nil
	}

	all, preparationFailed, preparationErr := prepareSessionDirtyByPlayer(drained)
	var writeErr error
	if len(all) > 0 {
		writeErr = sr.repo.updateBatchUpsertPreparedUnderGenerationLeaseWithFlushSource(
			all,
			expectedGeneration,
			FlushWriteSourceSession,
		)
	}
	if writeErr != nil {
		sr.restoreDirtyByPlayer(drained)
	} else {
		sr.restoreDirtyByPlayer(preparationFailed)
	}
	if err := errors.Join(preparationErr, writeErr); err != nil {
		return fmt.Errorf("刷写 generation=%s 的 Session 脏数据: %w", safeValueForLog(expectedGeneration), err)
	}
	return nil
}
