package db233

import (
	"fmt"
	"reflect"
	"sync"
	"time"
)

// MonitoringDashboard - 监控仪表板
// 整合所有监控组件，提供统一的监控界面和数据展示
type MonitoringDashboard struct {
	name string

	// 监控组件
	performanceMonitors map[string]*PerformanceMonitor
	connectionMonitors  map[string]*ConnectionPoolMonitor
	healthCheckers      map[string]*HealthChecker
	alertManagers       map[string]*AlertManager
	metricsCollectors   map[string]*MetricsCollector
	metricsAggregators  map[string]*MetricsAggregator

	// 报告生成器
	reportGenerator *MonitoringReportGenerator

	// 仪表板配置
	refreshInterval time.Duration
	autoRefresh     bool

	// 缓存
	lastSnapshot    *DashboardSnapshot
	lastUpdate      time.Time
	snapshotVersion uint64

	// 锁
	mu sync.RWMutex

	// 控制
	enabled       bool
	running       bool
	stopping      bool
	stopCh        chan struct{}
	doneCh        chan struct{}
	reconfigureCh chan struct{}
}

// DashboardSnapshot - 仪表板快照
type DashboardSnapshot struct {
	Timestamp    time.Time
	Summary      DashboardSummary
	Components   map[string]any
	Alerts       []AlertSummary
	HealthStatus map[string]HealthSummary
	Performance  map[string]PerformanceSummary
}

// DashboardSummary - 仪表板摘要
type DashboardSummary struct {
	TotalDatabases    int
	HealthyDatabases  int
	TotalQueries      int64
	ActiveConnections int64
	ActiveAlerts      int
	HealthScore       float64
	ResponseTimeAvg   time.Duration
	ErrorRate         float64
}

// AlertSummary - 告警摘要
type AlertSummary struct {
	ID        string
	Name      string
	Severity  string
	Status    string
	Database  string
	Timestamp time.Time
}

// HealthSummary - 健康摘要
type HealthSummary struct {
	Status       string
	Score        float64
	LastCheck    time.Time
	ResponseTime time.Duration
}

// PerformanceSummary - 性能摘要
type PerformanceSummary struct {
	TotalQueries    int64
	SuccessRate     float64
	AvgResponseTime time.Duration
	SlowQueryRate   float64
	QPS             float64
}

type dashboardSnapshotSources struct {
	version             uint64
	timestamp           time.Time
	performanceMonitors map[string]*PerformanceMonitor
	connectionMonitors  map[string]*ConnectionPoolMonitor
	healthCheckers      map[string]*HealthChecker
	alertManagers       map[string]*AlertManager
	metricsCollectors   map[string]*MetricsCollector
	metricsAggregators  map[string]*MetricsAggregator
}

type dashboardCollectedData struct {
	performanceReports map[string]map[string]any
	healthResults      map[string]*HealthCheckResult
	activeAlerts       map[string][]*Alert
	components         map[string]any
}

func cloneDashboardSourceMap[T any](source map[string]*T) map[string]*T {
	clone := make(map[string]*T, len(source))
	for name, component := range source {
		clone[name] = component
	}
	return clone
}

// 创建监控仪表板
func NewMonitoringDashboard(name string) *MonitoringDashboard {
	dashboard := &MonitoringDashboard{
		name:                name,
		performanceMonitors: make(map[string]*PerformanceMonitor),
		connectionMonitors:  make(map[string]*ConnectionPoolMonitor),
		healthCheckers:      make(map[string]*HealthChecker),
		alertManagers:       make(map[string]*AlertManager),
		metricsCollectors:   make(map[string]*MetricsCollector),
		metricsAggregators:  make(map[string]*MetricsAggregator),
		refreshInterval:     30 * time.Second,
		autoRefresh:         true,
		enabled:             true,
	}

	// 创建报告生成器
	dashboard.reportGenerator = NewMonitoringReportGenerator(name + "_reports")

	return dashboard
}

// 添加性能监控器
func (md *MonitoringDashboard) AddPerformanceMonitor(name string, monitor *PerformanceMonitor) {
	if name == "" || monitor == nil {
		return
	}
	md.mu.Lock()
	defer md.mu.Unlock()

	md.performanceMonitors[name] = monitor
	md.reportGenerator.AddPerformanceMonitor(name, monitor)
	md.snapshotVersion++

	LogInfo("性能监控器已添加到仪表板: %s -> %s", md.name, name)
}

// 添加连接池监控器
func (md *MonitoringDashboard) AddConnectionMonitor(name string, monitor *ConnectionPoolMonitor) {
	if name == "" || monitor == nil {
		return
	}
	md.mu.Lock()
	defer md.mu.Unlock()

	md.connectionMonitors[name] = monitor
	md.reportGenerator.AddConnectionMonitor(name, monitor)
	md.snapshotVersion++

	LogInfo("连接池监控器已添加到仪表板: %s -> %s", md.name, name)
}

// 添加健康检查器
func (md *MonitoringDashboard) AddHealthChecker(name string, checker *HealthChecker) {
	if name == "" || checker == nil {
		return
	}
	md.mu.Lock()
	defer md.mu.Unlock()

	md.healthCheckers[name] = checker
	md.reportGenerator.AddHealthChecker(name, checker)
	md.snapshotVersion++

	LogInfo("健康检查器已添加到仪表板: %s -> %s", md.name, name)
}

// 添加告警管理器
func (md *MonitoringDashboard) AddAlertManager(name string, manager *AlertManager) {
	if name == "" || manager == nil {
		return
	}
	md.mu.Lock()
	defer md.mu.Unlock()

	md.alertManagers[name] = manager
	md.reportGenerator.AddAlertManager(name, manager)
	md.snapshotVersion++

	LogInfo("告警管理器已添加到仪表板: %s -> %s", md.name, name)
}

// 添加指标收集器
func (md *MonitoringDashboard) AddMetricsCollector(name string, collector *MetricsCollector) {
	if name == "" || collector == nil {
		return
	}
	md.mu.Lock()
	defer md.mu.Unlock()

	md.metricsCollectors[name] = collector
	md.reportGenerator.AddMetricsCollector(name, collector)
	md.snapshotVersion++

	LogInfo("指标收集器已添加到仪表板: %s -> %s", md.name, name)
}

// 添加指标聚合器
func (md *MonitoringDashboard) AddMetricsAggregator(name string, aggregator *MetricsAggregator) {
	if name == "" || aggregator == nil {
		return
	}
	md.mu.Lock()
	defer md.mu.Unlock()

	md.metricsAggregators[name] = aggregator
	md.snapshotVersion++

	LogInfo("指标聚合器已添加到仪表板: %s -> %s", md.name, name)
}

// 设置自动刷新间隔
func (md *MonitoringDashboard) SetRefreshInterval(interval time.Duration) {
	if interval <= 0 {
		LogWarn("监控仪表板刷新间隔必须大于 0: %v", interval)
		return
	}
	md.mu.Lock()
	md.refreshInterval = interval
	reconfigureCh := md.reconfigureCh
	running := md.running
	md.mu.Unlock()
	md.notifyReconfigure(running, reconfigureCh)
}

// 启用自动刷新
func (md *MonitoringDashboard) EnableAutoRefresh() {
	md.mu.Lock()
	md.autoRefresh = true
	reconfigureCh := md.reconfigureCh
	running := md.running
	md.mu.Unlock()
	md.notifyReconfigure(running, reconfigureCh)
}

// 禁用自动刷新
func (md *MonitoringDashboard) DisableAutoRefresh() {
	md.mu.Lock()
	md.autoRefresh = false
	reconfigureCh := md.reconfigureCh
	running := md.running
	md.mu.Unlock()
	md.notifyReconfigure(running, reconfigureCh)
}

// 启用仪表板
func (md *MonitoringDashboard) Enable() {
	md.mu.Lock()
	if !md.enabled {
		md.enabled = true
		md.snapshotVersion++
	}
	md.mu.Unlock()
	LogInfo("监控仪表板已启用: %s", md.name)
}

// 禁用仪表板
func (md *MonitoringDashboard) Disable() {
	md.mu.Lock()
	if md.enabled {
		md.enabled = false
		md.snapshotVersion++
	}
	md.mu.Unlock()
	LogInfo("监控仪表板已禁用: %s", md.name)
}

// 启动仪表板
func (md *MonitoringDashboard) Start() {
	for {
		md.mu.Lock()
		if md.running && !md.stopping {
			md.mu.Unlock()
			return
		}
		if md.stopping {
			doneCh := md.doneCh
			md.mu.Unlock()
			if doneCh != nil {
				<-doneCh
			}
			continue
		}

		stopCh := make(chan struct{})
		doneCh := make(chan struct{})
		reconfigureCh := make(chan struct{}, 1)
		md.running = true
		md.stopCh = stopCh
		md.doneCh = doneCh
		md.reconfigureCh = reconfigureCh
		md.mu.Unlock()

		LogInfo("监控仪表板启动: %s", md.name)
		go md.run(stopCh, doneCh, reconfigureCh)
		return
	}
}

// 停止仪表板
func (md *MonitoringDashboard) Stop() {
	md.mu.Lock()
	if !md.running {
		md.mu.Unlock()
		return
	}
	if md.stopping {
		doneCh := md.doneCh
		md.mu.Unlock()
		if doneCh != nil {
			<-doneCh
		}
		return
	}
	md.stopping = true
	doneCh := md.doneCh
	if md.stopCh != nil {
		close(md.stopCh)
		md.stopCh = nil
	}
	md.mu.Unlock()

	<-doneCh
	LogInfo("监控仪表板停止: %s", md.name)
}

func (md *MonitoringDashboard) notifyReconfigure(running bool, reconfigureCh chan struct{}) {
	if !running || reconfigureCh == nil {
		return
	}
	select {
	case reconfigureCh <- struct{}{}:
	default:
	}
}

func (md *MonitoringDashboard) run(stopCh <-chan struct{}, doneCh chan struct{}, reconfigureCh <-chan struct{}) {
	defer func() {
		md.mu.Lock()
		if md.doneCh == doneCh {
			md.running = false
			md.stopping = false
			md.stopCh = nil
			md.doneCh = nil
			md.reconfigureCh = nil
		}
		md.mu.Unlock()
		close(doneCh)
	}()
	var timer *time.Timer
	var timerCh <-chan time.Time
	configure := func() {
		md.mu.RLock()
		autoRefresh := md.autoRefresh
		interval := md.refreshInterval
		md.mu.RUnlock()
		if !autoRefresh {
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timerCh = nil
			return
		}
		if timer == nil {
			timer = time.NewTimer(interval)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		}
		timerCh = timer.C
	}
	configure()
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-timerCh:
			md.refreshSnapshot()
			configure()
		case <-reconfigureCh:
			configure()
		case <-stopCh:
			return
		}
	}
}

// refreshSnapshot 使用短锁捕获组件集合，锁外执行组件方法，再用短锁发布不可变快照。
func (md *MonitoringDashboard) refreshSnapshot() bool {
	sources, ok := md.captureSnapshotSources()
	if !ok {
		return false
	}
	collected := collectDashboardData(sources)
	snapshot := md.buildDashboardSnapshot(sources, collected)
	publishedAt := time.Now()

	md.mu.Lock()
	defer md.mu.Unlock()
	if !md.enabled || md.snapshotVersion != sources.version {
		return false
	}
	if md.lastSnapshot != nil && md.lastSnapshot.Timestamp.After(snapshot.Timestamp) {
		return false
	}
	md.lastSnapshot = snapshot
	md.lastUpdate = publishedAt
	return true
}

func (md *MonitoringDashboard) captureSnapshotSources() (dashboardSnapshotSources, bool) {
	md.mu.RLock()
	defer md.mu.RUnlock()
	if !md.enabled {
		return dashboardSnapshotSources{}, false
	}
	return dashboardSnapshotSources{
		version:             md.snapshotVersion,
		timestamp:           time.Now(),
		performanceMonitors: cloneDashboardSourceMap(md.performanceMonitors),
		connectionMonitors:  cloneDashboardSourceMap(md.connectionMonitors),
		healthCheckers:      cloneDashboardSourceMap(md.healthCheckers),
		alertManagers:       cloneDashboardSourceMap(md.alertManagers),
		metricsCollectors:   cloneDashboardSourceMap(md.metricsCollectors),
		metricsAggregators:  cloneDashboardSourceMap(md.metricsAggregators),
	}, true
}

func collectDashboardData(sources dashboardSnapshotSources) dashboardCollectedData {
	collected := dashboardCollectedData{
		performanceReports: make(map[string]map[string]any, len(sources.performanceMonitors)),
		healthResults:      make(map[string]*HealthCheckResult, len(sources.healthCheckers)),
		activeAlerts:       make(map[string][]*Alert, len(sources.alertManagers)),
		components:         make(map[string]any),
	}
	for name, monitor := range sources.performanceMonitors {
		if monitor == nil {
			continue
		}
		report := monitor.GetDetailedReport()
		collected.performanceReports[name] = report
		collected.components[fmt.Sprintf("performance_%s", name)] = report
	}
	for name, monitor := range sources.connectionMonitors {
		if monitor != nil {
			collected.components[fmt.Sprintf("connection_%s", name)] = monitor.GetReport()
		}
	}
	for name, checker := range sources.healthCheckers {
		if checker != nil {
			collected.healthResults[name] = checker.Check()
		}
	}
	for name, manager := range sources.alertManagers {
		if manager == nil {
			continue
		}
		collected.activeAlerts[name] = manager.GetActiveAlerts()
		collected.components[fmt.Sprintf("alerts_%s", name)] = manager.GetAlertStats()
	}
	for name, collector := range sources.metricsCollectors {
		if collector != nil {
			collected.components[fmt.Sprintf("metrics_%s", name)] = collector.GetStatus()
		}
	}
	for name, aggregator := range sources.metricsAggregators {
		if aggregator != nil {
			collected.components[fmt.Sprintf("aggregator_%s", name)] = aggregator.GetStatus()
		}
	}
	return collected
}

func (md *MonitoringDashboard) buildDashboardSnapshot(sources dashboardSnapshotSources, collected dashboardCollectedData) *DashboardSnapshot {
	snapshot := &DashboardSnapshot{
		Timestamp:    sources.timestamp,
		Summary:      md.generateSummary(sources, collected),
		Components:   collected.components,
		Alerts:       md.generateAlertSummaries(collected.activeAlerts),
		HealthStatus: make(map[string]HealthSummary, len(collected.healthResults)),
		Performance:  make(map[string]PerformanceSummary, len(collected.performanceReports)),
	}
	for name, result := range collected.healthResults {
		snapshot.HealthStatus[name] = md.generateHealthSummary(result)
	}
	for name, report := range collected.performanceReports {
		snapshot.Performance[name] = md.generatePerformanceSummary(report)
	}
	return snapshot
}

// 生成摘要
func (md *MonitoringDashboard) generateSummary(sources dashboardSnapshotSources, collected dashboardCollectedData) DashboardSummary {
	summary := DashboardSummary{}

	// 计算数据库总数
	summary.TotalDatabases = len(sources.performanceMonitors)

	// 计算健康数据库数量
	healthyCount := 0
	for _, result := range collected.healthResults {
		if result != nil && result.Healthy {
			healthyCount++
		}
	}
	summary.HealthyDatabases = healthyCount

	// 计算总查询数和性能指标
	totalQueries := int64(0)
	totalResponseTime := time.Duration(0)
	totalErrors := int64(0)
	activeConnections := int64(0)

	for _, report := range collected.performanceReports {
		if queries, ok := report["total_queries"].(int64); ok {
			totalQueries += queries
		}

		if successRate, ok := report["success_rate"].(float64); ok && successRate >= 0 {
			queries, _ := report["total_queries"].(int64)
			totalErrors += int64(float64(queries) * (1 - successRate))
		}

		if avgTimeStr, ok := report["avg_query_time"].(string); ok {
			if avgTime, err := time.ParseDuration(avgTimeStr); err == nil {
				totalResponseTime += avgTime
			}
		}

		if active, ok := report["active_connections"].(int64); ok {
			activeConnections += active
		}
	}

	summary.TotalQueries = totalQueries
	summary.ActiveConnections = activeConnections

	if len(collected.performanceReports) > 0 {
		summary.ResponseTimeAvg = totalResponseTime / time.Duration(len(collected.performanceReports))
	}

	if totalQueries > 0 {
		summary.ErrorRate = float64(totalErrors) / float64(totalQueries)
	}

	// 计算活跃告警数量
	activeAlerts := 0
	for _, alerts := range collected.activeAlerts {
		activeAlerts += len(alerts)
	}
	summary.ActiveAlerts = activeAlerts

	// 计算健康评分
	if summary.TotalDatabases > 0 {
		healthScore := float64(summary.HealthyDatabases) / float64(summary.TotalDatabases)
		if summary.ErrorRate < 0.1 {
			healthScore += 0.2
		}
		if summary.ActiveAlerts == 0 {
			healthScore += 0.1
		}
		if healthScore > 1 {
			healthScore = 1
		}
		summary.HealthScore = healthScore
	}

	return summary
}

// 生成告警摘要
func (md *MonitoringDashboard) generateAlertSummaries(activeAlerts map[string][]*Alert) []AlertSummary {
	summaries := make([]AlertSummary, 0)

	for managerName, alerts := range activeAlerts {
		for _, alert := range alerts {
			if alert == nil {
				continue
			}
			summary := AlertSummary{
				ID:        alert.ID,
				Name:      alert.Name,
				Severity:  md.alertSeverityToString(alert.Severity),
				Status:    md.alertStatusToString(alert.Status),
				Database:  managerName,
				Timestamp: alert.Timestamp,
			}
			summaries = append(summaries, summary)
		}
	}

	return summaries
}

// 生成健康摘要
func (md *MonitoringDashboard) generateHealthSummary(result *HealthCheckResult) HealthSummary {
	if result == nil {
		return HealthSummary{Status: "unknown"}
	}
	summary := HealthSummary{
		LastCheck:    result.Timestamp,
		ResponseTime: result.ResponseTime,
	}

	if result.Healthy {
		summary.Status = "healthy"
		summary.Score = 1.0
	} else {
		summary.Status = "unhealthy"
		summary.Score = 0.0
	}

	return summary
}

// 生成性能摘要
func (md *MonitoringDashboard) generatePerformanceSummary(report map[string]any) PerformanceSummary {
	summary := PerformanceSummary{}

	if val, ok := report["total_queries"].(int64); ok {
		summary.TotalQueries = val
	}

	if val, ok := report["success_rate"].(float64); ok {
		summary.SuccessRate = val
	}

	if val, ok := report["slow_query_rate"].(float64); ok {
		summary.SlowQueryRate = val
	}

	if avgTimeStr, ok := report["avg_query_time"].(string); ok {
		if avgTime, err := time.ParseDuration(avgTimeStr); err == nil {
			summary.AvgResponseTime = avgTime
		}
	}

	// 计算QPS（假设监控周期为1小时）
	if summary.TotalQueries > 0 {
		summary.QPS = float64(summary.TotalQueries) / time.Hour.Hours()
	}

	return summary
}

// 获取当前快照
func (md *MonitoringDashboard) GetCurrentSnapshot() *DashboardSnapshot {
	for attempt := 0; attempt < 3; attempt++ {
		md.mu.RLock()
		snapshot := md.lastSnapshot
		stale := snapshot == nil || time.Since(md.lastUpdate) > md.refreshInterval
		enabled := md.enabled
		md.mu.RUnlock()
		if !stale || !enabled {
			return cloneDashboardSnapshot(snapshot)
		}
		md.refreshSnapshot()
	}
	md.mu.RLock()
	snapshot := md.lastSnapshot
	md.mu.RUnlock()
	return cloneDashboardSnapshot(snapshot)
}

// 获取仪表板状态
func (md *MonitoringDashboard) GetStatus() map[string]any {
	md.mu.RLock()
	defer md.mu.RUnlock()

	return map[string]any{
		"name":                 md.name,
		"enabled":              md.enabled,
		"auto_refresh":         md.autoRefresh,
		"refresh_interval":     md.refreshInterval.String(),
		"performance_monitors": len(md.performanceMonitors),
		"connection_monitors":  len(md.connectionMonitors),
		"health_checkers":      len(md.healthCheckers),
		"alert_managers":       len(md.alertManagers),
		"metrics_collectors":   len(md.metricsCollectors),
		"metrics_aggregators":  len(md.metricsAggregators),
		"last_update":          md.lastUpdate,
		"has_snapshot":         md.lastSnapshot != nil,
		"running":              md.running,
	}
}

// 生成报告
func (md *MonitoringDashboard) GenerateReport(filename string, format string) error {
	return md.reportGenerator.ExportReport(filename, format)
}

// 获取组件状态
func (md *MonitoringDashboard) GetComponentStatus(componentType, name string) any {
	md.mu.RLock()
	var component any
	switch componentType {
	case "performance":
		component = md.performanceMonitors[name]
	case "connection":
		component = md.connectionMonitors[name]
	case "health":
		component = md.healthCheckers[name]
	case "alerts":
		component = md.alertManagers[name]
	case "metrics":
		component = md.metricsCollectors[name]
	case "aggregator":
		component = md.metricsAggregators[name]
	}
	md.mu.RUnlock()

	switch value := component.(type) {
	case *PerformanceMonitor:
		return value.GetDetailedReport()
	case *ConnectionPoolMonitor:
		return value.GetReport()
	case *HealthChecker:
		return value.ComprehensiveCheck()
	case *AlertManager:
		return value.GetAlertStats()
	case *MetricsCollector:
		return value.GetStatus()
	case *MetricsAggregator:
		return value.GetStatus()
	default:
		return nil
	}
}

func cloneDashboardSnapshot(snapshot *DashboardSnapshot) *DashboardSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.Alerts = append([]AlertSummary(nil), snapshot.Alerts...)
	clone.HealthStatus = make(map[string]HealthSummary, len(snapshot.HealthStatus))
	for name, status := range snapshot.HealthStatus {
		clone.HealthStatus[name] = status
	}
	clone.Performance = make(map[string]PerformanceSummary, len(snapshot.Performance))
	for name, status := range snapshot.Performance {
		clone.Performance[name] = status
	}
	clone.Components = make(map[string]any, len(snapshot.Components))
	for name, component := range snapshot.Components {
		clone.Components[name] = cloneDashboardComponent(component)
	}
	return &clone
}

func cloneDashboardComponent(value any) any {
	if value == nil {
		return nil
	}
	return cloneDashboardValue(reflect.ValueOf(value)).Interface()
}

func cloneDashboardValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.New(value.Type()).Elem()
		clone.Set(cloneDashboardValue(value.Elem()))
		return clone
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			clone.SetMapIndex(iterator.Key(), cloneDashboardValue(iterator.Value()))
		}
		return clone
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			clone.Index(index).Set(cloneDashboardValue(value.Index(index)))
		}
		return clone
	case reflect.Array:
		clone := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			clone.Index(index).Set(cloneDashboardValue(value.Index(index)))
		}
		return clone
	default:
		return value
	}
}

// 工具方法
func (md *MonitoringDashboard) alertSeverityToString(severity AlertSeverity) string {
	switch severity {
	case Info:
		return "info"
	case Warning:
		return "warning"
	case Error:
		return "error"
	case Critical:
		return "critical"
	default:
		return "unknown"
	}
}

func (md *MonitoringDashboard) alertStatusToString(status AlertStatus) string {
	switch status {
	case Active:
		return "active"
	case Resolved:
		return "resolved"
	default:
		return "unknown"
	}
}

// 重置仪表板
func (md *MonitoringDashboard) Reset() {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.lastSnapshot = nil
	md.lastUpdate = time.Now()
	md.snapshotVersion++

	LogInfo("监控仪表板已重置: %s", md.name)
}
