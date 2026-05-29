package db233

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// EntityTypeRegistry 实体类型注册表（WAL 回放时按类型名反序列化）。
type EntityTypeRegistry struct {
	factories map[string]func() IDbEntity
	mu        sync.RWMutex
}

var (
	entityTypeRegistryInstance *EntityTypeRegistry
	entityTypeRegistryOnce     sync.Once
)

// GetEntityTypeRegistry 获取实体类型注册表单例。
func GetEntityTypeRegistry() *EntityTypeRegistry {
	entityTypeRegistryOnce.Do(func() {
		entityTypeRegistryInstance = &EntityTypeRegistry{
			factories: make(map[string]func() IDbEntity),
		}
	})
	return entityTypeRegistryInstance
}

// Register 注册实体类型工厂（启动时注册所有玩家表实体）。
func (r *EntityTypeRegistry) Register(prototype IDbEntity) {
	if prototype == nil {
		return
	}
	typeName := EntityTypeName(prototype)
	r.mu.Lock()
	r.factories[typeName] = func() IDbEntity {
		return cloneEntityPrototype(prototype)
	}
	r.mu.Unlock()
}

// Create 按类型名创建实体实例。
func (r *EntityTypeRegistry) Create(typeName string) (IDbEntity, error) {
	r.mu.RLock()
	factory, ok := r.factories[typeName]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("未注册的实体类型: %s", typeName)
	}
	return factory(), nil
}

// EntityTypeName 获取实体类型名。
func EntityTypeName(entity IDbEntity) string {
	t := reflect.TypeOf(entity)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

// SerializeEntity 序列化实体为 JSON（保存前调用 SerializeBeforeSaveDb）。
func SerializeEntity(entity IDbEntity) ([]byte, error) {
	if entity == nil {
		return nil, fmt.Errorf("实体不能为 nil")
	}
	entity.SerializeBeforeSaveDb()
	return json.Marshal(entity)
}

// DeserializeEntity 从 JSON 反序列化到实体。
func DeserializeEntity(typeName string, data []byte) (IDbEntity, error) {
	entity, err := GetEntityTypeRegistry().Create(typeName)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, entity); err != nil {
		return nil, err
	}
	entity.DeserializeAfterLoadDb()
	return entity, nil
}

func cloneEntityPrototype(prototype IDbEntity) IDbEntity {
	t := reflect.TypeOf(prototype)
	if t.Kind() == reflect.Ptr {
		return reflect.New(t.Elem()).Interface().(IDbEntity)
	}
	return reflect.New(t).Elem().Interface().(IDbEntity)
}
