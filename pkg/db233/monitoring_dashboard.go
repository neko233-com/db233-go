package db233

import (
	"fmt"
	"sync"
	"time"
)

/**
 * MonitoringDashboard - 监控仪表板
 *
 * 整合所有监控组件，提供统一的监控界面和数据展示
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
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
	lastSnapshot *DashboardSnapshot
	lastUpdate   time.Time

	// 锁
	mu sync.RWMutex

	// 控制
	enabled  bool
	stopChan chan bool
}

/**
 * DashboardSnapshot - 仪表板快照
 */
type DashboardSnapshot struct {
	Timestamp    time.Time
	Summary      DashboardSummary
	Components   map[string]interface{}
	Alerts       []AlertSummary
	HealthStatus map[string]HealthSummary
	Performance  map[string]PerformanceSummary
}

/**
 * DashboardSummary - 仪表板摘要
 */
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

/**
 * AlertSummary - 告警摘要
 */
type AlertSummary struct {
	ID        string
	Name      string
	Severity  string
	Status    string
	Database  string
	Timestamp time.Time
}

/**
 * HealthSummary - 健康摘要
 */
type HealthSummary struct {
	Status       string
	Score        float64
	LastCheck    time.Time
	ResponseTime time.Duration
}

/**
 * PerformanceSummary - 性能摘要
 */
type PerformanceSummary struct {
	TotalQueries    int64
	SuccessRate     float64
	AvgResponseTime time.Duration
	SlowQueryRate   float64
	QPS             float64
}

/**
 * 创建监控仪表板
 */
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
		stopChan:            make(chan bool),
	}

	// 创建报告生成器
	dashboard.reportGenerator = NewMonitoringReportGenerator(name + "_reports")

	return dashboard
}

/**
 * 添加性能监控器
 */
func (md *MonitoringDashboard) AddPerformanceMonitor(name string, monitor *PerformanceMonitor) {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.performanceMonitors[name] = monitor
	md.reportGenerator.AddPerformanceMonitor(name, monitor)

	LogInfo("性能监控器已添加到仪表板: %s -> %s", md.name, name)
}

/**
 * 添加连接池监控器
 */
func (md *MonitoringDashboard) AddConnectionMonitor(name string, monitor *ConnectionPoolMonitor) {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.connectionMonitors[name] = monitor
	md.reportGenerator.AddConnectionMonitor(name, monitor)

	LogInfo("连接池监控器已添加到仪表板: %s -> %s", md.name, name)
}

/**
 * 添加健康检查器
 */
func (md *MonitoringDashboard) AddHealthChecker(name string, checker *HealthChecker) {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.healthCheckers[name] = checker
	md.reportGenerator.AddHealthChecker(name, checker)

	LogInfo("健康检查器已添加到仪表板: %s -> %s", md.name, name)
}

/**
 * 添加告警管理器
 */
func (md *MonitoringDashboard) AddAlertManager(name string, manager *AlertManager) {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.alertManagers[name] = manager
	md.reportGenerator.AddAlertManager(name, manager)

	LogInfo("告警管理器已添加到仪表板: %s -> %s", md.name, name)
}

/**
 * 添加指标收集器
 */
func (md *MonitoringDashboard) AddMetricsCollector(name string, collector *MetricsCollector) {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.metricsCollectors[name] = collector
	md.reportGenerator.AddMetricsCollector(name, collector)

	LogInfo("指标收集器已添加到仪表板: %s -> %s", md.name, name)
}

/**
 * 添加指标聚合器
 */
func (md *MonitoringDashboard) AddMetricsAggregator(name string, aggregator *MetricsAggregator) {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.metricsAggregators[name] = aggregator

	LogInfo("指标聚合器已添加到仪表板: %s -> %s", md.name, name)
}

/**
 * 设置自动刷新间隔
 */
func (md *MonitoringDashboard) SetRefreshInterval(interval time.Duration) {
	md.mu.Lock()
	defer md.mu.Unlock()
	md.refreshInterval = interval
}

/**
 * 启用自动刷新
 */
func (md *MonitoringDashboard) EnableAutoRefresh() {
	md.mu.Lock()
	defer md.mu.Unlock()
	md.autoRefresh = true
}

/**
 * 禁用自动刷新
 */
func (md *MonitoringDashboard) DisableAutoRefresh() {
	md.mu.Lock()
	defer md.mu.Unlock()
	md.autoRefresh = false
}

/**
 * 启用仪表板
 */
func (md *MonitoringDashboard) Enable() {
	md.mu.Lock()
	defer md.mu.Unlock()
	md.enabled = true
	LogInfo("监控仪表板已启用: %s", md.name)
}

/**
 * 禁用仪表板
 */
func (md *MonitoringDashboard) Disable() {
	md.mu.Lock()
	defer md.mu.Unlock()
	md.enabled = false
	LogInfo("监控仪表板已禁用: %s", md.name)
}

/**
 * 启动仪表板
 */
func (md *MonitoringDashboard) Start() {
	LogInfo("监控仪表板启动: %s", md.name)

	if md.autoRefresh {
		go func() {
			ticker := time.NewTicker(md.refreshInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					md.refreshSnapshot()
				case <-md.stopChan:
					LogInfo("监控仪表板停止: %s", md.name)
					return
				}
			}
		}()
	}
}

/**
 * 停止仪表板
 */
func (md *MonitoringDashboard) Stop() {
	select {
	case md.stopChan <- true:
		// 成功发送停止信号
	default:
		// channel已满或没有接收者，忽略
	}
}

/**
 * 刷新快照
 */
func (md *MonitoringDashboard) refreshSnapshot() {
	if !md.enabled {
		return
	}

	md.mu.Lock()
	defer md.mu.Unlock()

	snapshot := &DashboardSnapshot{
		Timestamp:    time.Now(),
		Summary:      md.generateSummary(),
		Components:   make(map[string]interface{}),
		Alerts:       md.generateAlertSummaries(),
		HealthStatus: make(map[string]HealthSummary),
		Performance:  make(map[string]PerformanceSummary),
	}

	// 收集各组件状态
	for name, checker := range md.healthCheckers {
		snapshot.HealthStatus[name] = md.generateHealthSummary(name, checker)
	}

	for name, monitor := range md.performanceMonitors {
		snapshot.Performance[name] = md.generatePerformanceSummary(monitor)
	}

	// 收集组件状态信息
	components := make(map[string]interface{})

	for name, monitor := range md.performanceMonitors {
		components[fmt.Sprintf("performance_%s", name)] = monitor.GetDetailedReport()
	}

	for name, monitor := range md.connectionMonitors {
		components[fmt.Sprintf("connection_%s", name)] = monitor.GetReport()
	}

	for name, manager := range md.alertManagers {
		components[fmt.Sprintf("alerts_%s", name)] = manager.GetAlertStats()
	}

	for name, collector := range md.metricsCollectors {
		components[fmt.Sprintf("metrics_%s", name)] = collector.GetStatus()
	}

	for name, aggregator := range md.metricsAggregators {
		components[fmt.Sprintf("aggregator_%s", name)] = aggregator.GetStatus()
	}

	snapshot.Components = components
	md.lastSnapshot = snapshot
	md.lastUpdate = time.Now()
}

/**
 * 生成摘要
 */
func (md *MonitoringDashboard) generateSummary() DashboardSummary {
	summary := DashboardSummary{}

	// 计算数据库总数
	summary.TotalDatabases = len(md.performanceMonitors)

	// 计算健康数据库数量
	healthyCount := 0
	for _, checker := range md.healthCheckers {
		result := checker.Check()
		if result.Healthy {
			healthyCount++
		}
	}
	summary.HealthyDatabases = healthyCount

	// 计算总查询数和性能指标
	totalQueries := int64(0)
	totalResponseTime := time.Duration(0)
	totalErrors := int64(0)
	activeConnections := int64(0)

	for _, monitor := range md.performanceMonitors {
		report := monitor.GetDetailedReport()

		if queries, ok := report["total_queries"].(int64); ok {
			totalQueries += queries
		}

		if successRate, ok := report["success_rate"].(float64); ok && successRate > 0 {
			totalErrors += int64(float64(totalQueries) * (1 - successRate))
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

	if len(md.performanceMonitors) > 0 {
		summary.ResponseTimeAvg = totalResponseTime / time.Duration(len(md.performanceMonitors))
	}

	if totalQueries > 0 {
		summary.ErrorRate = float64(totalErrors) / float64(totalQueries)
	}

	// 计算活跃告警数量
	activeAlerts := 0
	for _, manager := range md.alertManagers {
		activeAlerts += len(manager.GetActiveAlerts())
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
		summary.HealthScore = healthScore
	}

	return summary
}

/**
 * 生成告警摘要
 */
func (md *MonitoringDashboard) generateAlertSummaries() []AlertSummary {
	summaries := make([]AlertSummary, 0)

	for managerName, manager := range md.alertManagers {
		alerts := manager.GetActiveAlerts()

		for _, alert := range alerts {
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

/**
 * 生成健康摘要
 */
func (md *MonitoringDashboard) generateHealthSummary(name string, checker *HealthChecker) HealthSummary {
	result := checker.Check()

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

/**
 * 生成性能摘要
 */
func (md *MonitoringDashboard) generatePerformanceSummary(monitor *PerformanceMonitor) PerformanceSummary {
	report := monitor.GetDetailedReport()

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

/**
 * 获取当前快照
 */
func (md *MonitoringDashboard) GetCurrentSnapshot() *DashboardSnapshot {
	md.mu.RLock()
	defer md.mu.RUnlock()

	// 如果没有快照或太旧，刷新一个
	if md.lastSnapshot == nil || time.Since(md.lastUpdate) > md.refreshInterval {
		md.mu.RUnlock()
		md.refreshSnapshot()
		md.mu.RLock()
	}

	return md.lastSnapshot
}

/**
 * 获取仪表板状态
 */
func (md *MonitoringDashboard) GetStatus() map[string]interface{} {
	md.mu.RLock()
	defer md.mu.RUnlock()

	return map[string]interface{}{
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
	}
}

/**
 * 生成报告
 */
func (md *MonitoringDashboard) GenerateReport(filename string, format string) error {
	return md.reportGenerator.ExportReport(filename, format)
}

/**
 * 获取组件状态
 */
func (md *MonitoringDashboard) GetComponentStatus(componentType, name string) interface{} {
	md.mu.RLock()
	defer md.mu.RUnlock()

	switch componentType {
	case "performance":
		if monitor, exists := md.performanceMonitors[name]; exists {
			return monitor.GetDetailedReport()
		}
	case "connection":
		if monitor, exists := md.connectionMonitors[name]; exists {
			return monitor.GetReport()
		}
	case "health":
		if checker, exists := md.healthCheckers[name]; exists {
			return checker.ComprehensiveCheck()
		}
	case "alerts":
		if manager, exists := md.alertManagers[name]; exists {
			return manager.GetAlertStats()
		}
	case "metrics":
		if collector, exists := md.metricsCollectors[name]; exists {
			return collector.GetStatus()
		}
	case "aggregator":
		if aggregator, exists := md.metricsAggregators[name]; exists {
			return aggregator.GetStatus()
		}
	}

	return nil
}

/**
 * 工具方法
 */
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

/**
 * 重置仪表板
 */
func (md *MonitoringDashboard) Reset() {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.lastSnapshot = nil
	md.lastUpdate = time.Now()

	LogInfo("监控仪表板已重置: %s", md.name)
}
