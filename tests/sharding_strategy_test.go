package tests

import (
	"testing"

	"github.com/SolarisNeko/db233-go/pkg/db233"
)

/**
 * ShardingDbStrategy 单元测试
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
func TestShardingDbStrategyByNoUse_CalculateDbId(t *testing.T) {
	strategy := db233.ShardingDbStrategyByNoUseInstance

	// 测试各种分片 ID
	testCases := []int64{0, 1, 100, 1000, -1, 999999}

	for _, shardingId := range testCases {
		dbId := strategy.CalculateDbId(shardingId)
		if dbId != 0 {
			t.Errorf("不分片策略应该始终返回 0，但对于 shardingId=%d 返回了 %d", shardingId, dbId)
		}
	}
}

func TestShardingDbStrategy100w_CalculateDbId(t *testing.T) {
	strategy := db233.ShardingDbStrategy100wInstance

	testCases := []struct {
		shardingId int64
		expected   int
	}{
		{0, 0},
		{999999, 0},
		{1000000, 1},
		{1999999, 1},
		{2000000, 2},
		{-1, 0}, // 负数返回 0
		{5000000, 5},
	}

	for _, tc := range testCases {
		result := strategy.CalculateDbId(tc.shardingId)
		if result != tc.expected {
			t.Errorf("CalculateDbId(%d) = %d, expected %d", tc.shardingId, result, tc.expected)
		}
	}
}

func TestShardingDbStrategy100w_Singleton(t *testing.T) {
	instance1 := db233.ShardingDbStrategy100wInstance
	instance2 := db233.ShardingDbStrategy100wInstance

	if instance1 != instance2 {
		t.Error("单例实例应该相同")
	}
}
