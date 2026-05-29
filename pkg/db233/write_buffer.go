package db233

import (
	"fmt"
	"sync"
	"time"
)

// WriteBuffer 异步写缓冲：合并高频 Save，按表批量 UPSERT 刷盘。
type WriteBuffer struct {
	repo *BaseCrudRepository

	mu      sync.Mutex
	pending map[string]map[string]IDbEntity // tableName -> pkKey -> latest entity
	size    int

	stopCh chan struct{}
	doneCh chan struct{}
}

func newWriteBuffer(repo *BaseCrudRepository) *WriteBuffer {
	return &WriteBuffer{
		repo:    repo,
		pending: make(map[string]map[string]IDbEntity),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start 启动后台定时刷盘。
func (wb *WriteBuffer) Start(settings CrudPerformanceSettings) {
	go wb.loop(settings)
}

// Stop 停止后台刷盘并同步刷完队列。
func (wb *WriteBuffer) Stop() error {
	close(wb.stopCh)
	<-wb.doneCh
	return wb.Flush()
}

func (wb *WriteBuffer) loop(initial CrudPerformanceSettings) {
	defer close(wb.doneCh)
	interval := time.Duration(initial.WriteBufferFlushIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-wb.stopCh:
			return
		case <-ticker.C:
			settings := GetCrudPerformanceSettings().Snapshot()
			if settings.WriteBufferFlushIntervalMs > 0 {
				newInterval := time.Duration(settings.WriteBufferFlushIntervalMs) * time.Millisecond
				if newInterval != interval {
					interval = newInterval
					ticker.Reset(interval)
				}
			}
			_ = wb.Flush()
		}
	}
}

// Enqueue 入队实体（同表同主键保留最新版本）。
func (wb *WriteBuffer) Enqueue(entity IDbEntity) (queued bool, err error) {
	if entity == nil {
		return false, NewValidationException("实体不能为 nil")
	}

	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.WriteBufferMaxQueueSize > 0 && wb.queueSize() >= settings.WriteBufferMaxQueueSize {
		return false, nil
	}

	entity.SerializeBeforeSaveDb()
	tableName := wb.repo.getTableName(entity)
	if tableName == "" {
		return false, NewValidationException("无法获取表名")
	}

	cm := GetCrudManagerInstance()
	pk := cm.GetPrimaryKeyValue(entity)
	if wb.repo.isZeroValue(pk) {
		return false, NewValidationException("写缓冲要求实体主键非零值")
	}
	pkKey := fmt.Sprintf("%v", pk)

	wb.mu.Lock()
	defer wb.mu.Unlock()

	if wb.pending[tableName] == nil {
		wb.pending[tableName] = make(map[string]IDbEntity)
	}
	if _, exists := wb.pending[tableName][pkKey]; !exists {
		wb.size++
	}
	wb.pending[tableName][pkKey] = entity
	return true, nil
}

func (wb *WriteBuffer) queueSize() int {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.size
}

// Flush 同步刷盘：按表分块 SaveBatchUpsert。
func (wb *WriteBuffer) Flush() error {
	wb.mu.Lock()
	if wb.size == 0 {
		wb.mu.Unlock()
		return nil
	}
	pending := wb.pending
	wb.pending = make(map[string]map[string]IDbEntity)
	wb.size = 0
	wb.mu.Unlock()

	var firstErr error
	for _, entitiesMap := range pending {
		entities := make([]IDbEntity, 0, len(entitiesMap))
		for _, entity := range entitiesMap {
			entities = append(entities, entity)
		}
		if err := wb.repo.UpdateBatchUpsert(entities); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
