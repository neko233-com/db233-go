package db233

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
)

/**
 * ConfigManager - 配置管理器
 *
 * 提供统一的配置管理功能，支持从文件、环境变量等多种方式加载配置
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type ConfigManager struct {
	configs map[string]interface{}
	mu      sync.RWMutex
}

var configManagerInstance *ConfigManager
var configManagerOnce sync.Once

/**
 * 获取配置管理器单例实例
 */
func GetConfigManager() *ConfigManager {
	configManagerOnce.Do(func() {
		configManagerInstance = &ConfigManager{
			configs: make(map[string]interface{}),
		}
	})
	return configManagerInstance
}

/**
 * 从JSON文件加载配置
 */
func (cm *ConfigManager) LoadFromFile(filename string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("解析JSON配置失败: %w", err)
	}

	// 合并配置
	for key, value := range config {
		cm.configs[key] = value
	}

	LogInfo("配置已从文件加载: %s", filename)
	return nil
}

/**
 * 从环境变量加载配置
 */
func (cm *ConfigManager) LoadFromEnv(prefix string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	envVars := os.Environ()
	for _, envVar := range envVars {
		if len(prefix) > 0 && len(envVar) > len(prefix) && envVar[:len(prefix)] == prefix {
			// 解析环境变量
			key := envVar[len(prefix)+1:] // 跳过前缀和等号
			value := os.Getenv(prefix + "_" + key)
			if value != "" {
				cm.configs[key] = value
			}
		}
	}

	LogInfo("配置已从环境变量加载，前缀: %s", prefix)
}

/**
 * 获取字符串配置值
 */
func (cm *ConfigManager) GetString(key string, defaultValue string) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if value, exists := cm.configs[key]; exists {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return defaultValue
}

/**
 * 获取整数配置值
 */
func (cm *ConfigManager) GetInt(key string, defaultValue int) int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if value, exists := cm.configs[key]; exists {
		switch v := value.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		}
	}
	return defaultValue
}

/**
 * 获取布尔配置值
 */
func (cm *ConfigManager) GetBool(key string, defaultValue bool) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if value, exists := cm.configs[key]; exists {
		if b, ok := value.(bool); ok {
			return b
		}
	}
	return defaultValue
}

/**
 * 设置配置值
 */
func (cm *ConfigManager) Set(key string, value interface{}) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.configs[key] = value
	LogDebug("配置已设置: %s = %v", key, value)
}

/**
 * 获取所有配置
 */
func (cm *ConfigManager) GetAll() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[string]interface{})
	for k, v := range cm.configs {
		result[k] = v
	}
	return result
}

/**
 * 清除所有配置
 */
func (cm *ConfigManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.configs = make(map[string]interface{})
	LogInfo("所有配置已清除")
}

/**
 * 便捷方法：获取默认配置管理器的字符串值
 */
func GetConfigString(key string, defaultValue string) string {
	return GetConfigManager().GetString(key, defaultValue)
}

/**
 * 便捷方法：获取默认配置管理器的整数值
 */
func GetConfigInt(key string, defaultValue int) int {
	return GetConfigManager().GetInt(key, defaultValue)
}

/**
 * 便捷方法：获取默认配置管理器的布尔值
 */
func GetConfigBool(key string, defaultValue bool) bool {
	return GetConfigManager().GetBool(key, defaultValue)
}

/**
 * 便捷方法：设置默认配置管理器的值
 */
func SetConfig(key string, value interface{}) {
	GetConfigManager().Set(key, value)
}
