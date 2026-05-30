package db233

import (
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
	delta := int64(base) * int64(jitterPct) / 100
	if delta <= 0 {
		return base
	}
	offset := rand.Int64N(2*delta+1) - delta
	result := base + time.Duration(offset)
	if result < time.Millisecond {
		return time.Millisecond
	}
	return result
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
	if len(tasks) == 0 {
		return nil
	}
	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	if waveIntervalMs <= 0 || len(tasks) <= maxWorkers {
		return sr.runFlushBatchWave(tasks, maxWorkers)
	}

	for start := 0; start < len(tasks); start += maxWorkers {
		end := start + maxWorkers
		if end > len(tasks) {
			end = len(tasks)
		}
		if err := sr.runFlushBatchWave(tasks[start:end], maxWorkers); err != nil {
			return err
		}
		if end < len(tasks) {
			time.Sleep(time.Duration(waveIntervalMs) * time.Millisecond)
		}
	}
	return nil
}

func (sr *SessionRepository) runFlushBatchWave(tasks [][]IDbEntity, maxWorkers int) error {
	if len(tasks) == 0 {
		return nil
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
			if err := sr.repo.UpdateBatchUpsert(entities); err != nil {
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

func (sr *SessionRepository) acquireFlushSlot() {
	max := GetEntityCacheSettings().Snapshot().SessionFlushMaxWorkers
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
}

func (sr *SessionRepository) releaseFlushSlot() {
	sr.flushSemMu.Lock()
	sem := sr.flushSem
	sr.flushSemMu.Unlock()
	if sem != nil {
		<-sem
	}
}

func (sr *SessionRepository) flushSession(session *PlayerSession, includeWriteBuffer bool) error {
	sr.acquireFlushSlot()
	defer sr.releaseFlushSlot()
	return session.flushInternal(includeWriteBuffer)
}

func (sr *SessionRepository) drainAllDirtyByPlayer() map[string][]IDbEntity {
	drained := make(map[string][]IDbEntity)
	sr.sessions.Range(func(key, value any) bool {
		playerID := key.(string)
		session := value.(*PlayerSession)
		if entities := session.takeDirty(); len(entities) > 0 {
			drained[playerID] = entities
		}
		return true
	})
	return drained
}

func (sr *SessionRepository) restoreDirtyByPlayer(drained map[string][]IDbEntity) {
	for playerID, entities := range drained {
		if v, ok := sr.sessions.Load(playerID); ok {
			v.(*PlayerSession).restoreDirty(entities)
		}
	}
}

func (sr *SessionRepository) flushAllDirtyMerged(settings EntityCacheSettings) error {
	drained := sr.drainAllDirtyByPlayer()
	if len(drained) == 0 {
		return nil
	}

	var all []IDbEntity
	for _, entities := range drained {
		all = append(all, entities...)
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	tasks := buildFlushBatchTasks(all, chunkSize, sr.repo.getTableName)
	if err := sr.flushEntityBatches(tasks, settings.SessionFlushMaxWorkers, 0); err != nil {
		sr.restoreDirtyByPlayer(drained)
		return err
	}
	return nil
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
	settings := GetEntityCacheSettings().Snapshot()
	drained := sr.drainAllDirtyByPlayer()

	var all []IDbEntity
	for _, entities := range drained {
		all = append(all, entities...)
	}

	maxWorkers := settings.ShutdownFlushMaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = settings.SessionFlushMaxWorkers
	}
	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	var firstErr error
	if len(all) > 0 {
		chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
		tasks := buildFlushBatchTasks(all, chunkSize, sr.repo.getTableName)
		if err := sr.flushEntityBatches(tasks, maxWorkers, settings.ShutdownFlushWaveIntervalMs); err != nil {
			sr.restoreDirtyByPlayer(drained)
			firstErr = err
		}
	}

	if err := sr.repo.FlushWriteBuffer(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("FlushWriteBuffer: %w", err)
	}
	return firstErr
}
