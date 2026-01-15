package db233

import (
	"fmt"
	"reflect"
	"sync"
)

/**
 * EntityMetadata - 实体元数据
 *
 * 缓存实体的结构信息，避免重复反射
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type EntityMetadata struct {
	// 实体类型
	EntityType reflect.Type

	// 表名
	TableName string

	// 主键列名
	PrimaryKeyColumn string

	// 主键字段名（struct field name）
	PrimaryKeyFieldName string

	// 列名到字段索引的映射
	ColumnToFieldIndex map[string]int

	// 字段名到列名的映射
	FieldNameToColumn map[string]string

	// 所有列名列表（按字段顺序）
	AllColumns []string

	// 是否有自增主键
	HasAutoIncrement bool
}

/**
 * EntityMetadataCache - 实体元数据缓存
 *
 * 线程安全的实体元数据缓存，提高性能
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type EntityMetadataCache struct {
	// 类型到元数据的映射
	cache map[reflect.Type]*EntityMetadata

	// 读写锁（保证并发安全）
	mu sync.RWMutex
}

var (
	entityMetadataCacheInstance *EntityMetadataCache
	entityMetadataCacheOnce     sync.Once
)

/**
 * GetEntityMetadataCacheInstance 获取单例实例
 */
func GetEntityMetadataCacheInstance() *EntityMetadataCache {
	entityMetadataCacheOnce.Do(func() {
		entityMetadataCacheInstance = &EntityMetadataCache{
			cache: make(map[reflect.Type]*EntityMetadata),
		}
	})
	return entityMetadataCacheInstance
}

/**
 * GetOrBuild 获取或构建实体元数据
 *
 * @param entity 实体实例（可以是指针或值）
 * @return *EntityMetadata 实体元数据
 * @return error 错误信息
 */
func (c *EntityMetadataCache) GetOrBuild(entity interface{}) (*EntityMetadata, error) {
	t := reflect.TypeOf(entity)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// 先尝试从缓存读取（读锁）
	c.mu.RLock()
	if metadata, exists := c.cache[t]; exists {
		c.mu.RUnlock()
		return metadata, nil
	}
	c.mu.RUnlock()

	// 缓存未命中，构建元数据（写锁）
	c.mu.Lock()
	defer c.mu.Unlock()

	// 双重检查，防止并发情况下重复构建
	if metadata, exists := c.cache[t]; exists {
		return metadata, nil
	}

	// 构建元数据
	metadata, err := c.buildMetadata(entity, t)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	c.cache[t] = metadata
	return metadata, nil
}

/**
 * buildMetadata 构建实体元数据（支持嵌入结构体）
 */
func (c *EntityMetadataCache) buildMetadata(entity interface{}, entityType reflect.Type) (*EntityMetadata, error) {
	metadata := &EntityMetadata{
		EntityType:         entityType,
		ColumnToFieldIndex: make(map[string]int),
		FieldNameToColumn:  make(map[string]string),
		AllColumns:         make([]string, 0),
	}

	// 获取表名
	if dbEntity, ok := entity.(IDbEntity); ok {
		metadata.TableName = dbEntity.TableName()
	} else {
		// 尝试从指针类型获取
		v := reflect.ValueOf(entity)
		if v.Kind() == reflect.Ptr && v.Elem().CanAddr() {
			if dbEntity, ok := v.Interface().(IDbEntity); ok {
				metadata.TableName = dbEntity.TableName()
			}
		}
	}

	if metadata.TableName == "" {
		return nil, fmt.Errorf("无法获取表名，实体必须实现 IDbEntity 接口")
	}

	// 扫描字段（递归处理嵌入结构体）
	c.scanFields(entityType, metadata, []int{})

	// 如果没有找到主键，使用默认值 "id"
	if metadata.PrimaryKeyColumn == "" {
		metadata.PrimaryKeyColumn = "id"
		LogWarn("实体 %s 未找到主键字段，使用默认主键列名: id", entityType.Name())
	}

	return metadata, nil
}

/**
 * scanFields 扫描字段（递归处理嵌入结构体）
 * @param t 类型
 * @param metadata 元数据
 * @param parentIndex 父字段索引路径（用于嵌入字段）
 */
func (c *EntityMetadataCache) scanFields(t reflect.Type, metadata *EntityMetadata, parentIndex []int) {
	cm := GetCrudManagerInstance()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// 跳过未导出字段
		if !field.IsExported() {
			continue
		}

		// 当前字段的完整索引路径
		currentIndex := append(append([]int{}, parentIndex...), i)

		// 处理嵌入结构体（Anonymous field）
		if field.Anonymous {
			// 获取嵌入字段的类型
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归扫描
			if embeddedType.Kind() == reflect.Struct {
				LogDebug("扫描嵌入结构体: %s -> %s", t.Name(), field.Name)
				c.scanFields(embeddedType, metadata, currentIndex[:len(currentIndex)-1])
				continue
			}
		}

		// 获取列名（自动处理 db:"-" 和无 db 标签的情况）
		columnName := cm.GetColumnName(field)
		if columnName == "" {
			// 跳过标记为 "-" 或没有 db 标签的字段
			continue
		}

		// 检查是否为主键
		if cm.IsPrimaryKey(field) {
			metadata.PrimaryKeyColumn = columnName
			metadata.PrimaryKeyFieldName = field.Name

			// 检查是否自增（支持两种方式）
			if cm.IsAutoIncrement(field) {
				metadata.HasAutoIncrement = true
			}
		}

		// 记录映射关系（使用最后一个索引，因为嵌入字段会被提升到父级）
		fieldIndex := currentIndex[len(currentIndex)-1]
		if len(parentIndex) == 0 {
			// 非嵌入字段，直接使用索引
			metadata.ColumnToFieldIndex[columnName] = fieldIndex
		} else {
			// 嵌入字段，使用当前索引（Go会自动提升嵌入字段）
			metadata.ColumnToFieldIndex[columnName] = fieldIndex
		}

		metadata.FieldNameToColumn[field.Name] = columnName
		metadata.AllColumns = append(metadata.AllColumns, columnName)
	}
}

/**
 * Clear 清空缓存
 */
func (c *EntityMetadataCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[reflect.Type]*EntityMetadata)
}

/**
 * Remove 移除指定类型的缓存
 */
func (c *EntityMetadataCache) Remove(entityType reflect.Type) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, entityType)
}

/**
 * containsOption 检查 db 标签是否包含指定选项
 */
func containsOption(dbTag, option string) bool {
	if dbTag == "" {
		return false
	}
	// 标签格式：column_name,option1,option2,...
	parts := splitDbTag(dbTag)
	for i := 1; i < len(parts); i++ {
		if parts[i] == option {
			return true
		}
	}
	return false
}

/**
 * splitDbTag 分割 db 标签
 */
func splitDbTag(dbTag string) []string {
	if dbTag == "" {
		return []string{}
	}
	parts := make([]string, 0)
	for _, part := range splitString(dbTag, ",") {
		trimmed := trimSpace(part)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

/**
 * splitString 分割字符串
 */
func splitString(s, sep string) []string {
	result := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

/**
 * trimSpace 去除首尾空格
 */
func trimSpace(s string) string {
	start := 0
	end := len(s)

	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}
