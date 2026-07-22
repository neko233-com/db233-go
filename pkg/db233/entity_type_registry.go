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
	types     map[string]reflect.Type
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
			types:     make(map[string]reflect.Type),
		}
	})
	return entityTypeRegistryInstance
}

// Register 注册实体类型工厂（启动时注册所有玩家表实体）。
func (r *EntityTypeRegistry) Register(prototype IDbEntity) {
	if isNilStrictValue(prototype) {
		return
	}
	if err := r.RegisterStrict(prototype); err != nil {
		LogError("实体类型注册失败: type=%T, err=%s", prototype, safeErrorForLog(err))
	}
}

// RegisterStrict 注册实体类型；同一短类型名若来自不同 Go 类型则拒绝，避免
// WAL 仅凭 typeName 回放时静默反序列化为错误实体。
func (r *EntityTypeRegistry) RegisterStrict(prototype IDbEntity) error {
	if r == nil {
		return NewValidationException("EntityTypeRegistry 不能为 nil")
	}
	if isNilStrictValue(prototype) {
		return NewValidationException("实体原型不能为 nil")
	}
	typeName := EntityTypeName(prototype)
	entityType := reflect.TypeOf(prototype)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = make(map[string]func() IDbEntity)
	}
	if r.types == nil {
		r.types = make(map[string]reflect.Type)
	}
	if _, factoryExists := r.factories[typeName]; factoryExists {
		if _, typeExists := r.types[typeName]; !typeExists {
			existingType := entityFactoryType(r.factories[typeName])
			if existingType == nil {
				return NewValidationException(fmt.Sprintf("实体类型名 %s 已注册但无法验证其 Go 类型", typeName))
			}
			r.types[typeName] = existingType
		}
	}
	if existingType, exists := r.types[typeName]; exists {
		if canonicalEntityType(existingType) != canonicalEntityType(entityType) {
			return NewValidationException(fmt.Sprintf(
				"实体类型名冲突: name=%s, existing=%s, incoming=%s",
				typeName,
				qualifiedEntityTypeName(existingType),
				qualifiedEntityTypeName(entityType),
			))
		}
		return nil
	}
	r.factories[typeName] = func() IDbEntity {
		return newEntityOfType(entityType)
	}
	r.types[typeName] = entityType
	return nil
}

func entityFactoryType(factory func() IDbEntity) (entityType reflect.Type) {
	if factory == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			entityType = nil
		}
	}()
	entity := factory()
	if isNilStrictValue(entity) {
		return nil
	}
	return reflect.TypeOf(entity)
}

func canonicalEntityType(entityType reflect.Type) reflect.Type {
	for entityType != nil && entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	return entityType
}

func qualifiedEntityTypeName(entityType reflect.Type) string {
	entityType = canonicalEntityType(entityType)
	if entityType == nil {
		return "<nil>"
	}
	if entityType.PkgPath() == "" {
		return entityType.String()
	}
	return entityType.PkgPath() + "." + entityType.Name()
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
	if isNilStrictValue(entity) {
		return ""
	}
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
	if err := runEntitySerializeHook(entity); err != nil {
		return nil, err
	}
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
	if err := runEntityDeserializeHook(entity); err != nil {
		return nil, err
	}
	return entity, nil
}

func newEntityOfType(t reflect.Type) IDbEntity {
	if t.Kind() == reflect.Ptr {
		return reflect.New(t.Elem()).Interface().(IDbEntity)
	}
	return reflect.New(t).Elem().Interface().(IDbEntity)
}
