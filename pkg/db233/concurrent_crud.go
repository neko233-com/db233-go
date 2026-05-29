package db233

import (
	"sync"
)

// ConcurrentCrudConfig 并发 CRUD 配置（登录多表加载等场景）。
type ConcurrentCrudConfig struct {
	// MaxConcurrency 最大并发协程数（0 表示不限制，默认 10）。
	MaxConcurrency int

	// EnableConcurrent 是否启用并发（默认 true）。
	EnableConcurrent bool
}

// NewDefaultConcurrentCrudConfig 创建默认并发 CRUD 配置。
func NewDefaultConcurrentCrudConfig() *ConcurrentCrudConfig {
	return &ConcurrentCrudConfig{
		MaxConcurrency:   10,
		EnableConcurrent: true,
	}
}

// FindByIdConcurrentItem 并发 FindById 的单项结果。
type FindByIdConcurrentItem struct {
	// EntityType 查询时传入的实体类型原型（用于识别表/类型）。
	EntityType IDbEntity

	// Entity 查询结果；未找到时为 nil。
	Entity IDbEntity

	// Err 查询错误；未找到记录时不视为错误。
	Err error
}

// FindByIdConcurrent 并发按同一主键查询多个实体类型。
// 典型场景：玩家登录时并行加载 30+ 张玩家表，将串行 N 次查库降为约 1 个 RTT 量级。
func (r *BaseCrudRepository) FindByIdConcurrent(id any, entityTypes []IDbEntity, config *ConcurrentCrudConfig) []FindByIdConcurrentItem {
	if len(entityTypes) == 0 {
		return []FindByIdConcurrentItem{}
	}

	if config == nil {
		config = GetCrudPerformanceSettings().Snapshot().ToConcurrentCrudConfig()
	}

	results := make([]FindByIdConcurrentItem, len(entityTypes))
	for i, entityType := range entityTypes {
		results[i].EntityType = entityType
	}

	if id == nil {
		err := NewValidationException("查询ID不能为 nil")
		for i := range results {
			results[i].Err = err
		}
		return results
	}

	if !config.EnableConcurrent || len(entityTypes) <= 1 {
		for i, entityType := range entityTypes {
			if entityType == nil {
				results[i].Err = NewValidationException("实体类型不能为 nil")
				continue
			}
			entity, err := r.FindById(id, entityType)
			results[i].Entity = entity
			results[i].Err = err
		}
		return results
	}

	concurrency := config.MaxConcurrency
	if concurrency <= 0 || concurrency > len(entityTypes) {
		concurrency = len(entityTypes)
	}

	jobs := make(chan int, len(entityTypes))
	for i := range entityTypes {
		jobs <- i
	}
	close(jobs)

	wg := sync.WaitGroup{}
	wg.Add(concurrency)

	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				entityType := entityTypes[idx]
				if entityType == nil {
					results[idx].Err = NewValidationException("实体类型不能为 nil")
					continue
				}
				entity, err := r.FindById(id, entityType)
				results[idx].Entity = entity
				results[idx].Err = err
			}
		}()
	}

	wg.Wait()
	return results
}
