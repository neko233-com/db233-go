package db233

import (
	"database/sql/driver"
)

/**
 * DbGroupConfig 配置 - Go 版
 *
 * 对应 Kotlin 版本的 DbGroupConfig
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type DbGroupConfig struct {
	// GroupName 数据库组名
	GroupName string

	// DatasourceConfigTemplate 连接池配置模板
	DatasourceConfigTemplate map[string]interface{}

	// DataSourceCreateStrategy 数据源创建策略
	DataSourceCreateStrategy DataSourceCreateStrategy

	// ShardingDbStrategy 分片策略
	ShardingDbStrategy ShardingDbStrategy

	// DbConfigFetcher 数据库配置获取器
	DbConfigFetcher DbConfigFetcher
}

/**
 * DbConfig 数据库配置 - Go 版
 *
 * 对应 Kotlin 版本的 DbConfig
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type DbConfig struct {
	// DbId 数据库分片 ID
	DbId int

	// DbGroup 所属数据库组
	DbGroup *DbGroup

	// DbConfigMap 数据库配置映射
	DbConfigMap map[string]interface{}
}

/**
 * DbConfigFetcher 接口 - 数据库配置获取器
 *
 * 用途：定义如何获取数据库配置
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type DbConfigFetcher interface {
	/**
	 * 获取数据库配置列表
	 *
	 * @param groupName 组名
	 * @return 数据库配置列表
	 */
	Fetch(groupName string) ([]*DbConfig, error)
}

/**
 * DataSourceCreateStrategy 接口 - 数据源创建策略
 *
 * 用途：定义如何创建数据源
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type DataSourceCreateStrategy interface {
	/**
	 * 创建数据源
	 *
	 * @param template 配置模板
	 * @param config 具体配置
	 * @return 数据源驱动
	 */
	Create(template map[string]interface{}, config map[string]interface{}) (driver.Driver, error)
}

/**
 * ShardingDbStrategy 接口 - 分库分片策略
 *
 * 用途：定义数据库分片的计算策略，根据分片键计算目标数据库 ID
 *
 * 使用场景：
 * - 单库单表 → 多库多表的水平拆分
 * - 根据用户 ID、订单 ID 等进行分库
 * - 支持自定义分片算法
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type ShardingDbStrategy interface {
	/**
	 * 计算分片数据库 ID
	 *
	 * @param shardingId 用于计算分片的数字 ID（例如：用户ID、订单ID）
	 * @return 分片数据库 ID，0 表示默认数据源
	 */
	CalculateDbId(shardingId int64) int
}
