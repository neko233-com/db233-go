package db233

import (
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleMetricSource struct {
	count atomic.Int64
}

func (s *lifecycleMetricSource) GetMetrics() map[string]any {
	s.count.Add(1)
	return map[string]any{"count": float64(s.count.Load())}
}

func (s *lifecycleMetricSource) GetName() string { return "lifecycle" }

type mutableMetricSource struct {
	mu      sync.RWMutex
	metrics map[string]any
}

func (s *mutableMetricSource) GetMetrics() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]any, len(s.metrics))
	for name, value := range s.metrics {
		result[name] = value
	}
	return result
}

func (s *mutableMetricSource) GetName() string { return "mutable" }

func (s *mutableMetricSource) set(metrics map[string]any) {
	s.mu.Lock()
	s.metrics = metrics
	s.mu.Unlock()
}

func waitForLifecycleCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待后台组件状态超时")
}

func TestMetricsCollectorLifecycleIsIdempotentAndRestartable(t *testing.T) {
	source := &lifecycleMetricSource{}
	collector := NewMetricsCollector("lifecycle")
	collector.AddDataSource(source)
	collector.SetCollectionInterval(2 * time.Millisecond)
	collector.Start()
	collector.Start()
	waitForLifecycleCondition(t, func() bool { return source.count.Load() >= 2 })

	var stopGroup sync.WaitGroup
	for i := 0; i < 8; i++ {
		stopGroup.Add(1)
		go func() {
			defer stopGroup.Done()
			collector.Stop()
		}()
	}
	stopGroup.Wait()
	stoppedAt := source.count.Load()
	time.Sleep(10 * time.Millisecond)
	if got := source.count.Load(); got != stoppedAt {
		t.Fatalf("Stop 后仍在采集: before=%d after=%d", stoppedAt, got)
	}

	collector.Start()
	waitForLifecycleCondition(t, func() bool { return source.count.Load() > stoppedAt })
	collector.Stop()
}

func TestMonitoringAndHealthSchedulersStopWithoutLeak(t *testing.T) {
	dashboard := NewMonitoringDashboard("lifecycle")
	dashboard.DisableAutoRefresh()
	dashboard.Start()
	dashboard.Start()
	dashboard.Stop()
	dashboard.Stop()
	dashboard.Start()
	dashboard.Stop()

	scheduler := NewHealthCheckScheduler(time.Millisecond)
	scheduler.Start()
	scheduler.Start()
	time.Sleep(3 * time.Millisecond)
	scheduler.Stop()
	scheduler.Stop()
	scheduler.Start()
	scheduler.Stop()
}

func TestHealthCheckerClosesRows(t *testing.T) {
	closeErr := errors.New("close rows")
	state := newScriptedDBState(scriptedStep{
		kind:          "query",
		queryContains: "SELECT 1",
		columns:       []string{"one"},
		rows:          [][]driver.Value{{int64(1)}},
		closeErr:      closeErr,
	})
	sqlDB := openScriptedDB(t, state)
	result := NewHealthChecker(NewDb(sqlDB, 1, nil)).Check()
	if result.Healthy {
		t.Fatal("Rows.Close 失败时健康检查不应通过")
	}
	if !errors.Is(result.Error, closeErr) {
		t.Fatalf("健康检查错误 = %v, want %v", result.Error, closeErr)
	}
}

func TestDashboardAndTrackingSnapshotsAreIndependent(t *testing.T) {
	dashboard := NewMonitoringDashboard("snapshot")
	first := dashboard.GetCurrentSnapshot()
	if first == nil {
		t.Fatal("仪表板快照为空")
	}
	first.HealthStatus["mutated"] = HealthSummary{Status: "bad"}
	second := dashboard.GetCurrentSnapshot()
	if _, exists := second.HealthStatus["mutated"]; exists {
		t.Fatal("调用方修改污染了仪表板内部快照")
	}

	reloader := NewTrackingSchemaReloader(nil, "unused", time.Second, TrackingSchemaApplyOptions{})
	reloader.mu.Lock()
	reloader.current = &TrackingSchema{Tables: []TrackingTable{{
		Name:    "events",
		Columns: []TrackingColumn{{Name: "kind", Enum: []string{"a"}}},
	}}}
	reloader.mu.Unlock()
	current := reloader.Current()
	current.Tables[0].Columns[0].Enum[0] = "changed"
	if got := reloader.Current().Tables[0].Columns[0].Enum[0]; got != "a" {
		t.Fatalf("调用方修改污染了热重载器状态: %s", got)
	}
	reloader.Stop()
}

func TestPerformanceMonitorUsesBoundedO1Window(t *testing.T) {
	monitor := NewPerformanceMonitor("bounded", nil)
	monitor.windowSampleLimit = 32
	for i := 0; i < 100; i++ {
		success := i%2 == 0
		var recordErr error
		if !success {
			recordErr = errors.New("query failed")
		}
		monitor.RecordQuery("SELECT 1", time.Duration(i+1)*time.Microsecond, success, recordErr)
	}
	report := monitor.GetDetailedReport()
	window := report["time_window"].(map[string]any)
	if got := window["sample_count"].(int); got != 32 {
		t.Fatalf("窗口样本数 = %d, want 32", got)
	}
	if got := window["error_count"].(int64); got != 50 {
		t.Fatalf("窗口错误数 = %d, want 50", got)
	}
	if got := report["avg_successful_query_time"].(string); got != "50µs" {
		t.Fatalf("成功查询平均耗时 = %s, want 50µs", got)
	}

	monitor.RecordTransactionEnd(time.Second, true)
	if got := monitor.GetDetailedReport()["committed_transactions"].(int64); got != 0 {
		t.Fatalf("未匹配事务结束被计入提交: %d", got)
	}
}

func TestMetricsAggregatorRefreshReplacesStaleDataAndClonesSamples(t *testing.T) {
	source := &mutableMetricSource{metrics: map[string]any{"first": float64(1)}}
	aggregator := NewMetricsAggregator("snapshot")
	aggregator.SetCacheDuration(0)
	aggregator.AddDataSource(source)
	if err := aggregator.RefreshMetrics(); err != nil {
		t.Fatal(err)
	}
	metric, ok := aggregator.GetAggregatedMetric("first")
	if !ok || len(metric.DataPoints) != 1 {
		t.Fatalf("首次聚合结果异常: %+v exists=%v", metric, ok)
	}
	metric.DataPoints[0] = 99
	if got, _ := aggregator.GetAggregatedMetric("first"); got.DataPoints[0] != 1 {
		t.Fatal("调用方修改污染聚合器样本")
	}

	source.set(map[string]any{"second": float64(2)})
	if err := aggregator.RefreshMetrics(); err != nil {
		t.Fatal(err)
	}
	if _, exists := aggregator.GetAggregatedMetric("first"); exists {
		t.Fatal("刷新后仍保留已消失指标")
	}
}

func TestConnectionPoolMonitorAcquireConsumesIdleConnection(t *testing.T) {
	monitor := NewConnectionPoolMonitor("pool", nil)
	monitor.UpdatePoolStats(3, 1, 2, 0, 10, 0)
	monitor.RecordConnectionAcquired(time.Millisecond)

	report := monitor.GetReport()
	if got := report["active_connections"]; got != int64(2) {
		t.Fatalf("active_connections=%v want=2", got)
	}
	if got := report["idle_connections"]; got != int64(1) {
		t.Fatalf("idle_connections=%v want=1", got)
	}
	monitor.RecordConnectionReleased()
	report = monitor.GetReport()
	if got := report["active_connections"]; got != int64(1) {
		t.Fatalf("release active_connections=%v want=1", got)
	}
	if got := report["idle_connections"]; got != int64(2) {
		t.Fatalf("release idle_connections=%v want=2", got)
	}
}
