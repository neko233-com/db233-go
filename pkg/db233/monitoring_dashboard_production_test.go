package db233

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMonitoringDashboardRefreshDoesNotHoldDashboardLock(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	state := newScriptedDBState(scriptedStep{
		kind:          "query",
		queryContains: "SELECT 1",
		columns:       []string{"one"},
		rows:          [][]driver.Value{{int64(1)}},
		driverEntered: entered,
		driverRelease: release,
	})
	dashboard := NewMonitoringDashboard("lock-free-refresh")
	dashboard.AddHealthChecker("primary", NewHealthChecker(NewDb(openScriptedDB(t, state), 1, nil)))

	snapshotDone := make(chan *DashboardSnapshot, 1)
	go func() {
		snapshotDone <- dashboard.GetCurrentSnapshot()
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("健康检查未进入阻塞驱动")
	}

	statusDone := make(chan map[string]any, 1)
	go func() {
		statusDone <- dashboard.GetStatus()
	}()
	select {
	case status := <-statusDone:
		if got := status["health_checkers"].(int); got != 1 {
			close(release)
			t.Fatalf("健康检查器数量=%d want=1", got)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		t.Fatal("外部健康检查期间 Dashboard 锁被长期占用")
	}

	close(release)
	select {
	case snapshot := <-snapshotDone:
		if snapshot == nil || snapshot.HealthStatus["primary"].Status != "healthy" {
			t.Fatalf("健康快照异常: %+v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("释放健康检查后快照未完成")
	}
}

func TestMonitoringDashboardStartDuringStopRestartsAfterOldRun(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	step := scriptedStep{
		kind:          "query",
		queryContains: "SELECT 1",
		columns:       []string{"one"},
		rows:          [][]driver.Value{{int64(1)}},
		driverEntered: entered,
		driverRelease: release,
	}
	state := newScriptedDBState(step)
	state.repeatQuery = &scriptedStep{
		kind:          "query",
		queryContains: "SELECT 1",
		columns:       []string{"one"},
		rows:          [][]driver.Value{{int64(1)}},
	}
	dashboard := NewMonitoringDashboard("restart-boundary")
	dashboard.AddHealthChecker("primary", NewHealthChecker(NewDb(openScriptedDB(t, state), 1, nil)))
	dashboard.SetRefreshInterval(time.Millisecond)

	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		dashboard.DisableAutoRefresh()
		dashboard.Stop()
	})
	dashboard.Start()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("自动刷新未进入阻塞健康检查")
	}

	stopDone := make(chan struct{})
	go func() {
		dashboard.Stop()
		close(stopDone)
	}()
	waitForDashboardState(t, dashboard, func(running, stopping bool) bool {
		return running && stopping
	})

	restartDone := make(chan struct{})
	go func() {
		dashboard.Start()
		close(restartDone)
	}()
	select {
	case <-restartDone:
		t.Fatal("旧运行尚未结束时 Start 不应提前返回")
	case <-time.After(20 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Stop 未完成")
	}
	select {
	case <-restartDone:
	case <-time.After(time.Second):
		t.Fatal("Stop 完成后重启请求丢失")
	}
	status := dashboard.GetStatus()
	if running, _ := status["running"].(bool); !running {
		t.Fatal("Stop 窗口中的 Start 未启动新一代运行循环")
	}
}

func TestMonitoringDashboardSnapshotDeepCopiesNestedComponentData(t *testing.T) {
	monitor := NewPerformanceMonitor("nested", nil)
	monitor.RecordQuery("SELECT * FROM users WHERE token = 'deep-copy-secret'", time.Millisecond, false, errors.New("failed"))
	dashboard := NewMonitoringDashboard("deep-copy")
	dashboard.AddPerformanceMonitor("nested", monitor)

	first := dashboard.GetCurrentSnapshot()
	component := first.Components["performance_nested"].(map[string]any)
	recentErrors := component["recent_errors"].([]map[string]any)
	errorTypes := component["error_types"].(map[string]int64)
	thresholds := component["thresholds"].(map[string]any)
	recentErrors[0]["query"] = "tampered"
	for errorType := range errorTypes {
		errorTypes[errorType] = 999
	}
	thresholds["slow_query_threshold"] = "999h"

	second := dashboard.GetCurrentSnapshot()
	component = second.Components["performance_nested"].(map[string]any)
	if got := component["recent_errors"].([]map[string]any)[0]["query"].(string); got == "tampered" {
		t.Fatal("嵌套 []map 快照被调用方污染")
	}
	for _, count := range component["error_types"].(map[string]int64) {
		if count == 999 {
			t.Fatal("嵌套强类型 map 快照被调用方污染")
		}
	}
	if got := component["thresholds"].(map[string]any)["slow_query_threshold"]; got == "999h" {
		t.Fatal("嵌套 map[string]any 快照被调用方污染")
	}
}

func TestPerformanceMonitorRedactsQueryFromReportsAndLogsByDefault(t *testing.T) {
	previousLogger := defaultLogger
	var output bytes.Buffer
	defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
	t.Cleanup(func() { defaultLogger = previousLogger })

	secret := "monitor-production-secret"
	query := "SELECT * FROM users WHERE token = '" + secret + "'"
	monitor := NewPerformanceMonitor("privacy", nil)
	monitor.SetVerySlowQueryThreshold(time.Nanosecond)
	monitor.RecordQuery(query, time.Millisecond, false, errors.New("query failed"))

	reportText := fmt.Sprintf("%v", monitor.GetDetailedReport())
	if strings.Contains(reportText, secret) {
		t.Fatalf("默认性能报告泄漏 SQL 字面量: %s", reportText)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("默认慢查询日志泄漏 SQL 字面量: %s", output.String())
	}
	if !strings.Contains(reportText, "SQLVerb:") || !strings.Contains(output.String(), "SQLVerb:") ||
		strings.Contains(reportText, "SQLHash:") || strings.Contains(output.String(), "SQLHash:") ||
		strings.Contains(reportText, "SQLLength:") || strings.Contains(output.String(), "SQLLength:") {
		t.Fatalf("安全 SQL 摘要缺失: report=%s log=%s", reportText, output.String())
	}

	output.Reset()
	optIn := NewPerformanceMonitor("privacy-opt-in", nil)
	optIn.SetVerySlowQueryThreshold(time.Nanosecond)
	optIn.SetLogFullSQL(true)
	optIn.RecordQuery(query, time.Millisecond, false, errors.New("query failed"))
	if report := fmt.Sprintf("%v", optIn.GetDetailedReport()); strings.Contains(report, secret) {
		t.Fatalf("显式开启后错误报告仍不得保留完整 SQL: %s", report)
	}
	if !strings.Contains(output.String(), secret) {
		t.Fatalf("显式开启后日志未保留完整 SQL: %s", output.String())
	}
}

func TestMonitoringDashboardConcurrentRefreshAndLifecycle(t *testing.T) {
	dashboard := NewMonitoringDashboard("concurrent")
	dashboard.DisableAutoRefresh()
	monitor := NewPerformanceMonitor("concurrent", nil)
	dashboard.AddPerformanceMonitor("primary", monitor)

	const workers = 16
	const iterations = 50
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				switch worker % 4 {
				case 0:
					dashboard.Start()
					dashboard.Stop()
				case 1:
					dashboard.refreshSnapshot()
				case 2:
					snapshot := dashboard.GetCurrentSnapshot()
					if snapshot != nil {
						snapshot.Components[fmt.Sprintf("caller-%d", worker)] = iteration
					}
				case 3:
					monitor.RecordQuery("SELECT 1", time.Microsecond, true, nil)
					_ = dashboard.GetStatus()
				}
			}
		}(worker)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Dashboard 高并发生命周期/刷新测试超时")
	}
	dashboard.Stop()
	if running, _ := dashboard.GetStatus()["running"].(bool); running {
		t.Fatal("最终 Stop 后 Dashboard 仍处于运行状态")
	}
	if snapshot := dashboard.GetCurrentSnapshot(); snapshot == nil {
		t.Fatal("高并发刷新后快照为空")
	}
}

func waitForDashboardState(t *testing.T, dashboard *MonitoringDashboard, condition func(running, stopping bool) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		dashboard.mu.RLock()
		running := dashboard.running
		stopping := dashboard.stopping
		dashboard.mu.RUnlock()
		if condition(running, stopping) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待 Dashboard 生命周期状态超时")
}
