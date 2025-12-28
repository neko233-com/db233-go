package tests

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolarisNeko/db233-go/pkg/db233"
)

/**
 * 实体缓存管理器单元测试
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */

func TestEntityCacheManager_GetOrCreateSelectColumnNameCsv(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()
	ecm.ClearAllCache()

	type TestEntity struct {
		ID   int
		Name string
		Age  int
	}

	entityType := reflect.TypeOf(TestEntity{})
	colNameToValueMap := map[string]interface{}{
		"id":   1,
		"name": "test",
		"age":  25,
	}

	// 第一次调用
	result1 := ecm.GetOrCreateSelectColumnNameCsv(entityType, colNameToValueMap)

	// 第二次调用应该返回缓存的结果
	result2 := ecm.GetOrCreateSelectColumnNameCsv(entityType, colNameToValueMap)

	if result1 != result2 {
		t.Error("Cached results should be identical")
	}

	// 结果应该包含所有列名
	if len(result1) == 0 {
		t.Error("Result should not be empty")
	}

	// 验证包含了所有列
	expectedColumns := []string{"id", "name", "age"}
	for _, col := range expectedColumns {
		if !strings.Contains(result1, col) {
			t.Errorf("Result should contain column '%s'", col)
		}
	}
}

func TestEntityCacheManager_GetOrCreateAllColumnNameCsv(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()
	ecm.ClearAllCache()

	type TestEntity struct {
		ID   int
		Name string
		Age  int
	}

	entityType := reflect.TypeOf(TestEntity{})

	columnNameCreator := func() []string {
		return []string{"id", "name", "age", "created_at"}
	}

	// 第一次调用
	result1 := ecm.GetOrCreateAllColumnNameCsv(entityType, columnNameCreator)

	// 第二次调用应该返回缓存的结果
	result2 := ecm.GetOrCreateAllColumnNameCsv(entityType, columnNameCreator)

	if result1 != result2 {
		t.Error("Cached results should be identical")
	}

	expected := "id,name,age,created_at"
	if result1 != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result1)
	}
}

func TestEntityCacheManager_GetSelectColumnNameSql(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()
	ecm.ClearAllCache()

	type TestEntity struct {
		ID   int
		Name string
	}

	entityType := reflect.TypeOf(TestEntity{})
	colNameToValueMap := map[string]interface{}{
		"id":   1,
		"name": "test",
	}

	// 还没有缓存
	_, exists := ecm.GetSelectColumnNameSql(entityType)
	if exists {
		t.Error("Should not exist before caching")
	}

	// 创建缓存
	ecm.GetOrCreateSelectColumnNameCsv(entityType, colNameToValueMap)

	// 现在应该存在
	cached, exists := ecm.GetSelectColumnNameSql(entityType)
	if !exists {
		t.Error("Should exist after caching")
	}

	if len(cached) == 0 {
		t.Error("Cached value should not be empty")
	}
}

func TestEntityCacheManager_GetAllColumnNameCsv(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()
	ecm.ClearAllCache()

	type TestEntity struct {
		ID int
	}

	entityType := reflect.TypeOf(TestEntity{})

	// 还没有缓存
	_, exists := ecm.GetAllColumnNameCsv(entityType)
	if exists {
		t.Error("Should not exist before caching")
	}

	// 创建缓存
	columnNameCreator := func() []string {
		return []string{"id", "name"}
	}
	ecm.GetOrCreateAllColumnNameCsv(entityType, columnNameCreator)

	// 现在应该存在
	cached, exists := ecm.GetAllColumnNameCsv(entityType)
	if !exists {
		t.Error("Should exist after caching")
	}

	expected := "id,name"
	if cached != expected {
		t.Errorf("Expected '%s', got '%s'", expected, cached)
	}
}

func TestEntityCacheManager_ClearCache(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()
	ecm.ClearAllCache()

	type TestEntity struct {
		ID int
	}

	entityType := reflect.TypeOf(TestEntity{})

	// 创建缓存
	colNameToValueMap := map[string]interface{}{"id": 1}
	ecm.GetOrCreateSelectColumnNameCsv(entityType, colNameToValueMap)

	// 验证缓存存在
	_, exists := ecm.GetSelectColumnNameSql(entityType)
	if !exists {
		t.Error("Cache should exist")
	}

	// 清除缓存
	ecm.ClearCache(entityType)

	// 验证缓存已被清除
	_, exists = ecm.GetSelectColumnNameSql(entityType)
	if exists {
		t.Error("Cache should be cleared")
	}
}

func TestEntityCacheManager_ClearAllCache(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()

	type TestEntity1 struct{ ID int }
	type TestEntity2 struct{ Name string }

	entityType1 := reflect.TypeOf(TestEntity1{})
	entityType2 := reflect.TypeOf(TestEntity2{})

	// 创建缓存
	ecm.GetOrCreateSelectColumnNameCsv(entityType1, map[string]interface{}{"id": 1})
	ecm.GetOrCreateAllColumnNameCsv(entityType2, func() []string { return []string{"name"} })

	// 验证缓存存在
	selectSize, allSize := ecm.GetCacheSize()
	if selectSize == 0 || allSize == 0 {
		t.Error("Cache should exist")
	}

	// 清除所有缓存
	ecm.ClearAllCache()

	// 验证缓存已被清除
	selectSize, allSize = ecm.GetCacheSize()
	if selectSize != 0 || allSize != 0 {
		t.Error("All cache should be cleared")
	}
}

func TestEntityCacheManager_GetCacheSize(t *testing.T) {
	ecm := db233.GetEntityCacheManagerInstance()
	ecm.ClearAllCache()

	type TestEntity1 struct{ ID int }
	type TestEntity2 struct{ Name string }

	entityType1 := reflect.TypeOf(TestEntity1{})
	entityType2 := reflect.TypeOf(TestEntity2{})

	// 初始状态
	selectSize, allSize := ecm.GetCacheSize()
	if selectSize != 0 || allSize != 0 {
		t.Error("Initial cache size should be 0")
	}

	// 添加缓存
	ecm.GetOrCreateSelectColumnNameCsv(entityType1, map[string]interface{}{"id": 1})
	ecm.GetOrCreateAllColumnNameCsv(entityType2, func() []string { return []string{"name"} })

	// 验证大小
	selectSize, allSize = ecm.GetCacheSize()
	if selectSize != 1 || allSize != 1 {
		t.Errorf("Expected cache sizes 1,1, got %d,%d", selectSize, allSize)
	}
}
