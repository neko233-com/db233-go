package db233

import (
	"testing"
)

/**
 * DbManager 单元测试
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
func TestDbManager_GetInstance(t *testing.T) {
	manager1 := GetInstance()
	manager2 := GetInstance()

	if manager1 != manager2 {
		t.Error("GetInstance 应该返回单例实例")
	}
}

func TestDbManager_AddDbGroup(t *testing.T) {
	manager := GetInstance()

	// 创建模拟配置
	config := &DbGroupConfig{
		GroupName:       "test_group",
		DbConfigFetcher: &MockDbConfigFetcher{},
	}

	dbGroup, err := NewDbGroup(config)
	if err != nil {
		t.Fatalf("创建 DbGroup 失败: %v", err)
	}

	err = manager.AddDbGroup(dbGroup)
	if err != nil {
		t.Fatalf("添加 DbGroup 失败: %v", err)
	}

	retrieved, err := manager.GetDbGroup("test_group")
	if err != nil {
		t.Fatalf("获取 DbGroup 失败: %v", err)
	}

	if retrieved.GroupName != "test_group" {
		t.Error("获取的 DbGroup 名称不匹配")
	}
}

func TestDbManager_RemoveDbGroup(t *testing.T) {
	manager := GetInstance()

	config := &DbGroupConfig{
		GroupName:       "test_group_remove",
		DbConfigFetcher: &MockDbConfigFetcher{},
	}

	dbGroup, _ := NewDbGroup(config)
	manager.AddDbGroup(dbGroup)

	manager.RemoveDbGroup("test_group_remove")

	_, err := manager.GetDbGroup("test_group_remove")
	if err == nil {
		t.Error("移除后仍能获取到 DbGroup")
	}
}

// MockDbConfigFetcher 模拟数据库配置获取器
type MockDbConfigFetcher struct{}

func (m *MockDbConfigFetcher) Fetch(groupName string) ([]*DbConfig, error) {
	// 返回空列表，避免实际创建数据库连接
	return []*DbConfig{}, nil
}
