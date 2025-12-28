package db233

/**
 * 不分片策略 - Go 版
 *
 * 用途：不进行分库，所有数据存储在单一数据库中
 *
 * 使用场景：
 * - 数据量小，无需分库
 * - 开发/测试环境
 * - 默认配置（兜底策略）
 *
 * 特点：
 * - 始终返回 0（默认库）
 * - 性能最优，无计算开销
 * - 简化配置
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type ShardingDbStrategyByNoUse struct{}

/**
 * 计算分片数据库 ID
 *
 * 说明：不分片策略，始终返回 0（默认数据库）
 *
 * @param shardingId 分片键（忽略，不使用）
 * @return 始终返回 0，表示使用默认数据库（不分片）
 */
func (s *ShardingDbStrategyByNoUse) CalculateDbId(shardingId int64) int {
	return 0 // 不分片，默认只有一个库
}

/**
 * 单例实例（推荐使用）
 */
var ShardingDbStrategyByNoUseInstance = &ShardingDbStrategyByNoUse{}

/**
 * ShardingDbStrategy100w - 100万分片策略
 *
 * 按 100 万为一个分片单位进行分库
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type ShardingDbStrategy100w struct{}

/**
 * 分片单位：100 万
 */
const SHARDING_UNIT_100W = 1000000

/**
 * 计算分片数据库 ID
 *
 * 计算公式：dbId = toShardingNumber / 1,000,000
 *
 * @param shardingId 分片键
 * @return int 数据库 ID
 */
func (s *ShardingDbStrategy100w) CalculateDbId(shardingId int64) int {
	if shardingId < 0 {
		return 0
	}
	return int(shardingId / SHARDING_UNIT_100W)
}

/**
 * 单例实例
 */
var ShardingDbStrategy100wInstance = &ShardingDbStrategy100w{}
