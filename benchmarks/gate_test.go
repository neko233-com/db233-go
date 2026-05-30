package benchmarks

import "testing"

// TestReleaseGate 发版门禁：串联框架对比 + 稳定性（需 MySQL，勿 -short）。
// 等价于 scripts/run-benchmark.ps1 的 Phase 3+4。
// 运行: cd benchmarks && go test -run TestReleaseGate -timeout 8m -v
func TestReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过发版门禁")
	}
	t.Run("FrameworkCompare", TestFrameworkCompare_Report)
	t.Run("StabilityTrafficBurst", TestStability_TrafficBurst)
	t.Run("StabilityConnectionPoolSpike", TestStability_ConnectionPoolSpike)
	t.Run("StabilityLRUBurst", TestStability_LRUBurst)
	t.Run("StabilityWALBurst", TestStability_WALBurst)
}
