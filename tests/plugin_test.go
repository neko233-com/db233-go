package tests

import (
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

/**
 * 插件系统单元测试
 *
 * @author neko233-com
 * @since 2025-12-28
 */

func TestAbstractDb233Plugin_GetPluginName(t *testing.T) {
	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	if plugin.GetPluginName() != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got '%s'", plugin.GetPluginName())
	}
}

func TestAbstractDb233Plugin_InitPlugin(t *testing.T) {
	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	// 默认实现不应该panic
	plugin.InitPlugin()
}

func TestAbstractDb233Plugin_Hooks(t *testing.T) {
	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	context := db233.NewExecuteSqlContext("SELECT 1", []interface{}{})

	// 默认实现不应该panic
	plugin.Begin()
	plugin.PreExecuteSql(context)
	plugin.PostExecuteSql(context)
	plugin.End()
}

func TestAbstractDb233Plugin_String(t *testing.T) {
	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	expected := "Plugin(name='test-plugin')"
	if plugin.String() != expected {
		t.Errorf("Expected '%s', got '%s'", expected, plugin.String())
	}
}

func TestDb233PluginManager_AddGlobalPlugin(t *testing.T) {
	pm := db233.GetPluginManagerInstance()
	pm.RemoveAll() // 清空

	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	pm.AddGlobalPlugin(plugin)

	if !pm.HasPlugin("test-plugin") {
		t.Error("Plugin should be registered")
	}

	if pm.Size() != 1 {
		t.Errorf("Expected 1 plugin, got %d", pm.Size())
	}
}

func TestDb233PluginManager_RemoveGlobalPlugin(t *testing.T) {
	pm := db233.GetPluginManagerInstance()
	pm.RemoveAll()

	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	pm.AddGlobalPlugin(plugin)
	pm.RemoveGlobalPlugin(plugin)

	if pm.HasPlugin("test-plugin") {
		t.Error("Plugin should be removed")
	}
}

func TestDb233PluginManager_RemoveGlobalPluginByName(t *testing.T) {
	pm := db233.GetPluginManagerInstance()
	pm.RemoveAll()

	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	pm.AddGlobalPlugin(plugin)
	pm.RemoveGlobalPluginByName("test-plugin")

	if pm.HasPlugin("test-plugin") {
		t.Error("Plugin should be removed")
	}
}

func TestDb233PluginManager_GetPlugin(t *testing.T) {
	pm := db233.GetPluginManagerInstance()
	pm.RemoveAll()

	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	pm.AddGlobalPlugin(plugin)

	retrieved := pm.GetPlugin("test-plugin")
	if retrieved == nil {
		t.Error("Plugin should be found")
	}

	if retrieved.GetPluginName() != "test-plugin" {
		t.Error("Retrieved plugin name should match")
	}
}

func TestDb233PluginManager_GetAll(t *testing.T) {
	pm := db233.GetPluginManagerInstance()
	pm.RemoveAll()

	plugin1 := db233.NewAbstractDb233Plugin("plugin1")
	plugin2 := db233.NewAbstractDb233Plugin("plugin2")

	pm.AddGlobalPlugin(plugin1)
	pm.AddGlobalPlugin(plugin2)

	all := pm.GetAll()
	if len(all) != 2 {
		t.Errorf("Expected 2 plugins, got %d", len(all))
	}
}

func TestDb233PluginManager_RemoveAll(t *testing.T) {
	pm := db233.GetPluginManagerInstance()

	plugin := db233.NewAbstractDb233Plugin("test-plugin")
	pm.AddGlobalPlugin(plugin)
	pm.RemoveAll()

	if pm.Size() != 0 {
		t.Errorf("Expected 0 plugins after RemoveAll, got %d", pm.Size())
	}
}

func TestLoggingPlugin_PreExecuteSql(t *testing.T) {
	plugin := db233.NewLoggingPlugin()
	context := db233.NewExecuteSqlContext("SELECT * FROM users", []interface{}{1})

	// 不应该panic
	plugin.PreExecuteSql(context)
}

func TestLoggingPlugin_PostExecuteSql(t *testing.T) {
	plugin := db233.NewLoggingPlugin()
	context := db233.NewExecuteSqlContext("SELECT * FROM users", []interface{}{1})
	context.SetResult([]interface{}{}, 1)

	// 不应该panic
	plugin.PostExecuteSql(context)
}

func TestPerformanceMonitorPlugin_PostExecuteSql(t *testing.T) {
	plugin := db233.NewPerformanceMonitorPlugin(100 * time.Millisecond)
	context := db233.NewExecuteSqlContext("SELECT * FROM users", []interface{}{1})

	// 快速查询
	context.Duration = 50 * time.Millisecond
	plugin.PostExecuteSql(context)

	// 慢查询
	context.Duration = 200 * time.Millisecond
	plugin.PostExecuteSql(context)
}

func TestMetricsPlugin_PostExecuteSql(t *testing.T) {
	plugin := db233.NewMetricsPlugin()
	plugin.InitPlugin() // 确保初始化

	context := db233.NewExecuteSqlContext("SELECT * FROM users", []interface{}{1})
	context.SetResult([]interface{}{}, 1)

	plugin.PostExecuteSql(context)

	metrics := plugin.GetMetrics()
	if metrics["total_queries"].(int) != 1 {
		t.Error("Total queries should be 1")
	}
}

func TestMetricsPlugin_PrintReport(t *testing.T) {
	plugin := db233.NewMetricsPlugin()
	context := db233.NewExecuteSqlContext("SELECT * FROM users", []interface{}{1})
	context.SetResult([]interface{}{}, 1)

	plugin.PostExecuteSql(context)

	// 不应该panic
	plugin.PrintReport()
}

func TestExecuteSqlContext_NewExecuteSqlContext(t *testing.T) {
	context := db233.NewExecuteSqlContext("SELECT 1", []interface{}{1})

	if context.Sql != "SELECT 1" {
		t.Error("SQL should be set")
	}

	if len(context.Params) != 1 {
		t.Error("Params should be set")
	}

	if context.Attributes == nil {
		t.Error("Attributes should be initialized")
	}
}

func TestExecuteSqlContext_MarkStart(t *testing.T) {
	context := db233.NewExecuteSqlContext("SELECT 1", nil)
	context.MarkStart()

	if context.StartTime.IsZero() {
		t.Error("Start time should be set")
	}
}

func TestExecuteSqlContext_SetResult(t *testing.T) {
	context := db233.NewExecuteSqlContext("SELECT 1", nil)
	context.SetResult("result", 1)

	if context.Result != "result" {
		t.Error("Result should be set")
	}

	if context.AffectedRows != 1 {
		t.Error("Affected rows should be set")
	}

	if context.EndTime.IsZero() {
		t.Error("End time should be set")
	}
}

func TestExecuteSqlContext_SetError(t *testing.T) {
	context := db233.NewExecuteSqlContext("SELECT 1", nil)
	testErr := db233.NewDb233Exception("test error")
	context.SetError(testErr)

	if context.Error != testErr {
		t.Error("Error should be set")
	}

	if context.EndTime.IsZero() {
		t.Error("End time should be set")
	}
}

func TestExecuteSqlContext_Attributes(t *testing.T) {
	context := db233.NewExecuteSqlContext("SELECT 1", nil)

	context.SetAttribute("key", "value")
	value := context.GetAttribute("key")

	if value != "value" {
		t.Error("Attribute should be set and retrieved")
	}
}
