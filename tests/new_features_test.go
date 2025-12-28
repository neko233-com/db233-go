package tests

import (
	"testing"

	"github.com/SolarisNeko/db233-go/pkg/db233"
)

// 测试配置管理器
func TestConfigManager(t *testing.T) {
	cm := db233.GetConfigManager()

	// 测试设置和获取字符串
	cm.Set("test.string", "hello")
	if val := db233.GetConfigString("test.string", ""); val != "hello" {
		t.Errorf("期望 'hello', 得到 '%s'", val)
	}

	// 测试设置和获取整数
	cm.Set("test.int", 42)
	if val := db233.GetConfigInt("test.int", 0); val != 42 {
		t.Errorf("期望 42, 得到 %d", val)
	}

	// 测试默认值
	if val := db233.GetConfigString("test.nonexistent", "default"); val != "default" {
		t.Errorf("期望 'default', 得到 '%s'", val)
	}
}

// 测试日志系统
func TestLogger(t *testing.T) {
	logger := db233.GetLogger()
	logger.SetLevel(db233.DEBUG)

	// 测试日志级别
	db233.LogDebug("调试信息")
	db233.LogInfo("信息")
	db233.LogWarn("警告")
	db233.LogError("错误")
}

// 测试异常处理
func TestExceptions(t *testing.T) {
	// 测试数据库异常 (使用ConnectionException)
	connErr := db233.NewConnectionException("测试连接错误")
	if connErr.Error() == "" {
		t.Error("期望错误消息不为空")
	}

	// 测试配置异常
	configErr := db233.NewConfigurationException("测试配置错误")
	if configErr.Error() == "" {
		t.Error("期望错误消息不为空")
	}
}

// 测试事务管理器
func TestTransactionManager(t *testing.T) {
	// 注意：这个测试需要数据库连接
	// 在实际测试中，应该使用测试数据库

	tm := db233.NewTransactionManager(nil)

	// 测试事务管理器创建
	if tm == nil {
		t.Error("期望事务管理器不为nil")
	}
}

// 测试健康检查器
func TestHealthChecker(t *testing.T) {
	// 注意：这个测试需要数据库连接
	// 在实际测试中，应该使用测试数据库

	hc := db233.NewHealthChecker(nil)

	// 测试健康检查器创建
	if hc == nil {
		t.Error("期望健康检查器不为nil")
	}
}

// 测试迁移管理器
func TestMigrationManager(t *testing.T) {
	// 注意：这个测试需要数据库连接
	// 在实际测试中，应该使用测试数据库

	mm := db233.NewMigrationManager(nil, "./test_migrations")

	// 测试迁移管理器创建
	if mm == nil {
		t.Error("期望迁移管理器不为nil")
	}
}

// 测试连接池监控器
func TestConnectionPoolMonitor(t *testing.T) {
	cpm := db233.NewConnectionPoolMonitor("test_group", nil)

	// 测试连接池监控器创建
	if cpm == nil {
		t.Error("期望连接池监控器不为nil")
	}
}
