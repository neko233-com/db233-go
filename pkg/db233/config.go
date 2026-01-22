package db233

import (
	"database/sql/driver"
)

// DbGroupConfig 配置 - Go 版
// 对应 Kotlin 版本的 DbGroupConfig
type DbGroupConfig struct {
	// GroupName 数据库组名
	GroupName string

	// DatasourceConfigTemplate 连接池配置模板
	DatasourceConfigTemplate map[string]any

	// DataSourceCreateStrategy 数据源创建策略
	DataSourceCreateStrategy DataSourceCreateStrategy

	// ShardingDbStrategy 分片策略
	ShardingDbStrategy ShardingDbStrategy

	// DbConfigFetcher 数据库配置获取器
	DbConfigFetcher DbConfigFetcher
}

// DbConfig 数据库配置 - Go 版
// 对应 Kotlin 版本的 DbConfig
type DbConfig struct {
	// DbId 数据库分片 ID
	DbId int

	// DbGroup 所属数据库组
	DbGroup *DbGroup

	// DbConfigMap 数据库配置映射
	DbConfigMap map[string]any
}

// DbConfigFetcher 接口 - 数据库配置获取器
// 用途：定义如何获取数据库配置
type DbConfigFetcher interface {
	// 获取数据库配置列表
	// groupName: 组名
	// 返回: 数据库配置列表
	Fetch(groupName string) ([]*DbConfig, error)
}

// DataSourceCreateStrategy 接口 - 数据源创建策略
// 用途：定义如何创建数据源
type DataSourceCreateStrategy interface {
	// 创建数据源
	// template: 配置模板
	// config: 具体配置
	// 返回: 数据源驱动
	Create(template map[string]any, config map[string]any) (driver.Driver, error)
}

// ShardingDbStrategy 接口 - 分库分片策略
// 用途：定义数据库分片的计算策略，根据分片键计算目标数据库 ID
// 使用场景：
// - 单库单表 → 多库多表的水平拆分
// - 根据用户 ID、订单 ID 等进行分库
// - 支持自定义分片算法
type ShardingDbStrategy interface {
	// 计算分片数据库 ID
	// shardingId: 用于计算分片的数字 ID（例如：用户ID、订单ID）
	// 返回: 分片数据库 ID，0 表示默认数据源
	CalculateDbId(shardingId int64) int
}
