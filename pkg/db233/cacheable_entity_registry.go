package db233

import (
	"fmt"
	"sync"
)

// CacheableEntitySpec 可缓存实体声明（JPA 风格 XxxEntity）。
type CacheableEntitySpec struct {
	// Prototype 实体原型（如 &PlayerBagEntity{}）。
	Prototype IDbEntity

	// MaxInstances 该类型在缓存中最大实例数；0 表示使用配置文件 entityTypeLimits 或不限制。
	MaxInstances int
}

// CacheableEntityRegistry 可缓存实体白名单（仅注册的 XxxEntity 可走 Session 缓存）。
type CacheableEntityRegistry struct {
	mu          sync.RWMutex
	cacheable   map[string]struct{}
	maxByType   map[string]int
	defaultMax  int
}

var (
	cacheableRegistryInstance *CacheableEntityRegistry
	cacheableRegistryOnce     sync.Once
)

// GetCacheableEntityRegistry 获取可缓存实体注册表单例。
func GetCacheableEntityRegistry() *CacheableEntityRegistry {
	cacheableRegistryOnce.Do(func() {
		cacheableRegistryInstance = &CacheableEntityRegistry{
			cacheable: make(map[string]struct{}),
			maxByType: make(map[string]int),
		}
	})
	return cacheableRegistryInstance
}

// Register 注册可缓存实体类型。
func (r *CacheableEntityRegistry) Register(spec CacheableEntitySpec) {
	if spec.Prototype == nil {
		return
	}
	typeName := EntityTypeName(spec.Prototype)
	r.mu.Lock()
	r.cacheable[typeName] = struct{}{}
	if spec.MaxInstances > 0 {
		r.maxByType[typeName] = spec.MaxInstances
	}
	r.mu.Unlock()
	GetEntityTypeRegistry().Register(spec.Prototype)
}

// RegisterBatch 批量注册。
func (r *CacheableEntityRegistry) RegisterBatch(specs []CacheableEntitySpec) {
	for _, spec := range specs {
		r.Register(spec)
	}
}

// IsCacheable 判断实体类型是否允许缓存。
func (r *CacheableEntityRegistry) IsCacheable(entity IDbEntity) bool {
	if entity == nil {
		return false
	}
	typeName := EntityTypeName(entity)
	r.mu.RLock()
	_, ok := r.cacheable[typeName]
	r.mu.RUnlock()
	return ok
}

// IsCacheableType 按类型名判断。
func (r *CacheableEntityRegistry) IsCacheableType(typeName string) bool {
	r.mu.RLock()
	_, ok := r.cacheable[typeName]
	r.mu.RUnlock()
	return ok
}

// MaxInstances 获取某实体类型的最大缓存实例数（0 表示不限制）。
func (r *CacheableEntityRegistry) MaxInstances(typeName string) int {
	r.mu.RLock()
	if v, ok := r.maxByType[typeName]; ok {
		r.mu.RUnlock()
		return v
	}
	r.mu.RUnlock()

	settings := GetEntityCacheSettings().Snapshot()
	if v, ok := settings.EntityTypeLimits[typeName]; ok && v > 0 {
		return v
	}
	return 0
}

// FilterCacheable 从加载列表中过滤出可缓存的实体原型。
func (r *CacheableEntityRegistry) FilterCacheable(entityTypes []IDbEntity) []IDbEntity {
	result := make([]IDbEntity, 0, len(entityTypes))
	for _, e := range entityTypes {
		if r.IsCacheable(e) {
			result = append(result, e)
		}
	}
	return result
}

// RequireCacheable 若未注册则返回错误。
func (r *CacheableEntityRegistry) RequireCacheable(entity IDbEntity) error {
	if !r.IsCacheable(entity) {
		return NewValidationException(fmt.Sprintf("实体类型 %s 未注册为可缓存类型", EntityTypeName(entity)))
	}
	return nil
}
