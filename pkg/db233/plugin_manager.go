package db233

import (
	"sync"
)

/**
 * Db233PluginManager - Db233 插件管理器
 *
 * 对应 Kotlin 版本的 Db233PluginManager
 * 管理全局插件的注册、移除和查询
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type Db233PluginManager struct {
	// 全局插件存储
	globalPlugins map[string]Db233Plugin
	mu            sync.RWMutex
}

/**
 * 单例实例
 */
var pluginManagerInstance *Db233PluginManager
var pluginManagerOnce sync.Once

/**
 * 获取单例实例
 */
func GetPluginManagerInstance() *Db233PluginManager {
	pluginManagerOnce.Do(func() {
		pluginManagerInstance = &Db233PluginManager{
			globalPlugins: make(map[string]Db233Plugin),
		}
	})
	return pluginManagerInstance
}

/**
 * 添加全局插件
 */
func (pm *Db233PluginManager) AddGlobalPlugin(plugin Db233Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 初始化插件
	plugin.InitPlugin()

	pm.globalPlugins[plugin.GetPluginName()] = plugin
}

/**
 * 移除全局插件
 */
func (pm *Db233PluginManager) RemoveGlobalPlugin(plugin Db233Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.globalPlugins, plugin.GetPluginName())
}

/**
 * 根据插件名称移除插件
 */
func (pm *Db233PluginManager) RemoveGlobalPluginByName(pluginName string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.globalPlugins, pluginName)
}

/**
 * 获取所有已注册的插件
 */
func (pm *Db233PluginManager) GetAll() []Db233Plugin {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugins := make([]Db233Plugin, 0, len(pm.globalPlugins))
	for _, plugin := range pm.globalPlugins {
		plugins = append(plugins, plugin)
	}
	return plugins
}

/**
 * 根据插件名称获取插件
 */
func (pm *Db233PluginManager) GetPlugin(pluginName string) Db233Plugin {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	return pm.globalPlugins[pluginName]
}

/**
 * 检查插件是否已注册
 */
func (pm *Db233PluginManager) HasPlugin(pluginName string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	_, exists := pm.globalPlugins[pluginName]
	return exists
}

/**
 * 移除所有插件
 */
func (pm *Db233PluginManager) RemoveAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.globalPlugins = make(map[string]Db233Plugin)
}

/**
 * 获取插件数量
 */
func (pm *Db233PluginManager) Size() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	return len(pm.globalPlugins)
}

/**
 * 执行插件钩子 - 开始
 */
func (pm *Db233PluginManager) ExecuteBegin() {
	plugins := pm.GetAll()
	for _, plugin := range plugins {
		plugin.Begin()
	}
}

/**
 * 执行插件钩子 - SQL 执行前
 */
func (pm *Db233PluginManager) ExecutePreSql(context *ExecuteSqlContext) {
	plugins := pm.GetAll()
	for _, plugin := range plugins {
		plugin.PreExecuteSql(context)
	}
}

/**
 * 执行插件钩子 - SQL 执行后
 */
func (pm *Db233PluginManager) ExecutePostSql(context *ExecuteSqlContext) {
	plugins := pm.GetAll()
	for _, plugin := range plugins {
		plugin.PostExecuteSql(context)
	}
}

/**
 * 执行插件钩子 - 结束
 */
func (pm *Db233PluginManager) ExecuteEnd() {
	plugins := pm.GetAll()
	for _, plugin := range plugins {
		plugin.End()
	}
}
