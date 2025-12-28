package db233

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

/**
 * MonitoringReportGenerator - 监控报告生成器
 *
 * 生成详细的监控报告，支持多种格式输出
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type MonitoringReportGenerator struct {
	name string

	// 数据源
	performanceMonitors map[string]*PerformanceMonitor
	connectionMonitors  map[string]*ConnectionPoolMonitor
	healthCheckers      map[string]*HealthChecker
	metricsCollectors   map[string]*MetricsCollector
	alertManagers       map[string]*AlertManager

	// 报告配置
	reportTitle   string
	reportPeriod  time.Duration
	includeCharts bool
	outputFormats []string
}

/**
 * ReportData - 报告数据结构
 */
type ReportData struct {
	Title       string                 `json:"title"`
	GeneratedAt time.Time              `json:"generated_at"`
	Period      string                 `json:"period"`
	Summary     ReportSummary          `json:"summary"`
	Details     ReportDetails          `json:"details"`
	Charts      map[string]interface{} `json:"charts,omitempty"`
}

/**
 * ReportSummary - 报告摘要
 */
type ReportSummary struct {
	TotalDatabases   int     `json:"total_databases"`
	HealthyDatabases int     `json:"healthy_databases"`
	TotalQueries     int64   `json:"total_queries"`
	AvgResponseTime  string  `json:"avg_response_time"`
	ErrorRate        float64 `json:"error_rate"`
	ActiveAlerts     int     `json:"active_alerts"`
	HealthScore      float64 `json:"health_score"`
}

/**
 * ReportDetails - 报告详情
 */
type ReportDetails struct {
	Databases []DatabaseReport `json:"databases"`
	Alerts    []AlertReport    `json:"alerts"`
	Trends    []TrendReport    `json:"trends"`
}

/**
 * DatabaseReport - 数据库报告
 */
type DatabaseReport struct {
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	HealthScore  float64           `json:"health_score"`
	Performance  PerformanceReport `json:"performance"`
	Connections  ConnectionReport  `json:"connections"`
	HealthChecks []HealthReport    `json:"health_checks"`
}

/**
 * PerformanceReport - 性能报告
 */
type PerformanceReport struct {
	TotalQueries    int64   `json:"total_queries"`
	SuccessRate     float64 `json:"success_rate"`
	AvgResponseTime string  `json:"avg_response_time"`
	SlowQueryRate   float64 `json:"slow_query_rate"`
	ErrorRate       float64 `json:"error_rate"`
	QPS             float64 `json:"qps"`
}

/**
 * ConnectionReport - 连接报告
 */
type ConnectionReport struct {
	ActiveConnections    int64   `json:"active_connections"`
	IdleConnections      int64   `json:"idle_connections"`
	WaitingConnections   int64   `json:"waiting_connections"`
	AvgWaitTime          string  `json:"avg_wait_time"`
	ConnectionEfficiency float64 `json:"connection_efficiency"`
}

/**
 * HealthReport - 健康报告
 */
type HealthReport struct {
	CheckType    string    `json:"check_type"`
	Status       string    `json:"status"`
	ResponseTime string    `json:"response_time"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

/**
 * AlertReport - 告警报告
 */
type AlertReport struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Severity  string    `json:"severity"`
	Status    string    `json:"status"`
	Metric    string    `json:"metric"`
	Value     string    `json:"value"`
	Threshold string    `json:"threshold"`
	Timestamp time.Time `json:"timestamp"`
	Duration  string    `json:"duration,omitempty"`
}

/**
 * TrendReport - 趋势报告
 */
type TrendReport struct {
	Metric string       `json:"metric"`
	Period string       `json:"period"`
	Data   []TrendPoint `json:"data"`
	Trend  string       `json:"trend"`
	Change float64      `json:"change_percent"`
}

/**
 * TrendPoint - 趋势数据点
 */
type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

/**
 * 创建监控报告生成器
 */
func NewMonitoringReportGenerator(name string) *MonitoringReportGenerator {
	return &MonitoringReportGenerator{
		name:                name,
		performanceMonitors: make(map[string]*PerformanceMonitor),
		connectionMonitors:  make(map[string]*ConnectionPoolMonitor),
		healthCheckers:      make(map[string]*HealthChecker),
		metricsCollectors:   make(map[string]*MetricsCollector),
		alertManagers:       make(map[string]*AlertManager),
		reportTitle:         "数据库监控报告",
		reportPeriod:        time.Hour,
		includeCharts:       true,
		outputFormats:       []string{"json", "text"},
	}
}

/**
 * 添加性能监控器
 */
func (rg *MonitoringReportGenerator) AddPerformanceMonitor(name string, monitor *PerformanceMonitor) {
	rg.performanceMonitors[name] = monitor
}

/**
 * 添加连接池监控器
 */
func (rg *MonitoringReportGenerator) AddConnectionMonitor(name string, monitor *ConnectionPoolMonitor) {
	rg.connectionMonitors[name] = monitor
}

/**
 * 添加健康检查器
 */
func (rg *MonitoringReportGenerator) AddHealthChecker(name string, checker *HealthChecker) {
	rg.healthCheckers[name] = checker
}

/**
 * 添加指标收集器
 */
func (rg *MonitoringReportGenerator) AddMetricsCollector(name string, collector *MetricsCollector) {
	rg.metricsCollectors[name] = collector
}

/**
 * 添加告警管理器
 */
func (rg *MonitoringReportGenerator) AddAlertManager(name string, manager *AlertManager) {
	rg.alertManagers[name] = manager
}

/**
 * 设置报告标题
 */
func (rg *MonitoringReportGenerator) SetReportTitle(title string) {
	rg.reportTitle = title
}

/**
 * 设置报告周期
 */
func (rg *MonitoringReportGenerator) SetReportPeriod(period time.Duration) {
	rg.reportPeriod = period
}

/**
 * 设置是否包含图表
 */
func (rg *MonitoringReportGenerator) SetIncludeCharts(include bool) {
	rg.includeCharts = include
}

/**
 * 设置输出格式
 */
func (rg *MonitoringReportGenerator) SetOutputFormats(formats []string) {
	rg.outputFormats = formats
}

/**
 * 生成报告数据
 */
func (rg *MonitoringReportGenerator) GenerateReportData() *ReportData {
	report := &ReportData{
		Title:       rg.reportTitle,
		GeneratedAt: time.Now(),
		Period:      rg.reportPeriod.String(),
		Summary:     rg.generateSummary(),
		Details:     rg.generateDetails(),
	}

	if rg.includeCharts {
		report.Charts = rg.generateCharts()
	}

	return report
}

/**
 * 生成摘要
 */
func (rg *MonitoringReportGenerator) generateSummary() ReportSummary {
	summary := ReportSummary{}

	// 计算数据库总数
	summary.TotalDatabases = len(rg.performanceMonitors)

	// 计算健康数据库数量
	healthyCount := 0
	totalQueries := int64(0)
	totalResponseTime := time.Duration(0)
	totalErrors := int64(0)

	for _, checker := range rg.healthCheckers {
		result := checker.Check()
		if result.Healthy {
			healthyCount++
		}
	}

	summary.HealthyDatabases = healthyCount

	// 计算性能指标
	for _, monitor := range rg.performanceMonitors {
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
	}

	summary.TotalQueries = totalQueries

	if len(rg.performanceMonitors) > 0 {
		summary.AvgResponseTime = (totalResponseTime / time.Duration(len(rg.performanceMonitors))).String()
	}

	if totalQueries > 0 {
		summary.ErrorRate = float64(totalErrors) / float64(totalQueries)
	}

	// 计算活跃告警数量
	activeAlerts := 0
	for _, manager := range rg.alertManagers {
		activeAlerts += len(manager.GetActiveAlerts())
	}
	summary.ActiveAlerts = activeAlerts

	// 计算健康评分
	if summary.TotalDatabases > 0 {
		healthScore := float64(summary.HealthyDatabases) / float64(summary.TotalDatabases)
		if summary.ErrorRate < 0.1 { // 错误率低于10%加分
			healthScore += 0.2
		}
		if summary.ActiveAlerts == 0 { // 无活跃告警加分
			healthScore += 0.1
		}
		summary.HealthScore = healthScore
	}

	return summary
}

/**
 * 生成详情
 */
func (rg *MonitoringReportGenerator) generateDetails() ReportDetails {
	details := ReportDetails{
		Databases: rg.generateDatabaseReports(),
		Alerts:    rg.generateAlertReports(),
		Trends:    rg.generateTrendReports(),
	}

	return details
}

/**
 * 生成数据库报告
 */
func (rg *MonitoringReportGenerator) generateDatabaseReports() []DatabaseReport {
	reports := make([]DatabaseReport, 0)

	// 为每个数据库生成报告
	for name := range rg.performanceMonitors {
		report := DatabaseReport{
			Name:         name,
			Performance:  PerformanceReport{},
			Connections:  ConnectionReport{},
			HealthChecks: make([]HealthReport, 0),
		}

		// 性能报告
		if monitor, exists := rg.performanceMonitors[name]; exists {
			perfData := monitor.GetDetailedReport()
			report.Performance = rg.extractPerformanceReport(perfData)
		}

		// 连接报告
		if monitor, exists := rg.connectionMonitors[name]; exists {
			connData := monitor.GetReport()
			report.Connections = rg.extractConnectionReport(connData)
		}

		// 健康检查报告
		if checker, exists := rg.healthCheckers[name]; exists {
			healthResults := checker.ComprehensiveCheck()
			for checkType, result := range healthResults {
				healthReport := HealthReport{
					CheckType:    checkType,
					Status:       rg.boolToStatus(result.Healthy),
					ResponseTime: result.ResponseTime.String(),
					Message:      result.Message,
					Timestamp:    result.Timestamp,
				}
				report.HealthChecks = append(report.HealthChecks, healthReport)
			}
		}

		// 计算健康评分
		report.HealthScore = rg.calculateHealthScore(&report)
		report.Status = rg.healthScoreToStatus(report.HealthScore)

		reports = append(reports, report)
	}

	return reports
}

/**
 * 提取性能报告
 */
func (rg *MonitoringReportGenerator) extractPerformanceReport(data map[string]interface{}) PerformanceReport {
	report := PerformanceReport{}

	if val, ok := data["total_queries"].(int64); ok {
		report.TotalQueries = val
	}

	if val, ok := data["success_rate"].(float64); ok {
		report.SuccessRate = val
	}

	if val, ok := data["avg_query_time"].(string); ok {
		report.AvgResponseTime = val
	}

	if val, ok := data["slow_query_rate"].(float64); ok {
		report.SlowQueryRate = val
	}

	if val, ok := data["error_rate"].(float64); ok {
		report.ErrorRate = val
	}

	// 计算QPS (假设报告周期为1小时)
	if report.TotalQueries > 0 {
		report.QPS = float64(report.TotalQueries) / rg.reportPeriod.Hours()
	}

	return report
}

/**
 * 提取连接报告
 */
func (rg *MonitoringReportGenerator) extractConnectionReport(data map[string]interface{}) ConnectionReport {
	report := ConnectionReport{}

	if val, ok := data["active_connections"].(int64); ok {
		report.ActiveConnections = val
	}

	if val, ok := data["idle_connections"].(int64); ok {
		report.IdleConnections = val
	}

	if val, ok := data["waiting_connections"].(int64); ok {
		report.WaitingConnections = val
	}

	if val, ok := data["avg_connection_wait_time"].(string); ok {
		report.AvgWaitTime = val
	}

	// 计算连接效率
	totalConnections := report.ActiveConnections + report.IdleConnections
	if totalConnections > 0 {
		report.ConnectionEfficiency = float64(report.ActiveConnections) / float64(totalConnections)
	}

	return report
}

/**
 * 生成告警报告
 */
func (rg *MonitoringReportGenerator) generateAlertReports() []AlertReport {
	reports := make([]AlertReport, 0)

	for managerName, manager := range rg.alertManagers {
		alerts := manager.GetActiveAlerts()

		for _, alert := range alerts {
			report := AlertReport{
				ID:        alert.ID,
				Name:      alert.Name,
				Severity:  rg.alertSeverityToString(alert.Severity),
				Status:    rg.alertStatusToString(alert.Status),
				Metric:    alert.Metric,
				Value:     fmt.Sprintf("%v", alert.Value),
				Threshold: fmt.Sprintf("%v", alert.Threshold),
				Timestamp: alert.Timestamp,
			}

			if alert.Duration != nil {
				report.Duration = alert.Duration.String()
			}

			reports = append(reports, report)
		}

		// 也包含最近的历史告警
		history := manager.GetAlertHistory(10) // 最近10个
		for _, alert := range history {
			if alert.Status == Resolved { // 只包含已解决的
				report := AlertReport{
					ID:        alert.ID,
					Name:      alert.Name,
					Severity:  rg.alertSeverityToString(alert.Severity),
					Status:    rg.alertStatusToString(alert.Status),
					Metric:    alert.Metric,
					Value:     fmt.Sprintf("%v", alert.Value),
					Threshold: fmt.Sprintf("%v", alert.Threshold),
					Timestamp: alert.Timestamp,
				}

				if alert.Duration != nil {
					report.Duration = alert.Duration.String()
				}

				reports = append(reports, report)
			}
		}
	}

	// 按时间排序
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].Timestamp.After(reports[j].Timestamp)
	})

	return reports
}

/**
 * 生成趋势报告
 */
func (rg *MonitoringReportGenerator) generateTrendReports() []TrendReport {
	reports := make([]TrendReport, 0)

	for _, collector := range rg.metricsCollectors {
		metrics := collector.GetMetricNames()

		for _, metricName := range metrics {
			stats := collector.GetMetricStats(metricName, rg.reportPeriod)

			if available, ok := stats["available"].(bool); !ok || !available {
				continue
			}

			trend := TrendReport{
				Metric: metricName,
				Period: rg.reportPeriod.String(),
				Data:   make([]TrendPoint, 0),
			}

			// 获取历史数据点
			history := collector.GetMetricHistory(metricName, rg.reportPeriod)
			for _, point := range history {
				if val, ok := point.Value.(float64); ok {
					trend.Data = append(trend.Data, TrendPoint{
						Timestamp: point.Timestamp,
						Value:     val,
					})
				}
			}

			// 计算趋势
			if len(trend.Data) >= 2 {
				first := trend.Data[0].Value
				last := trend.Data[len(trend.Data)-1].Value

				if first > 0 {
					trend.Change = ((last - first) / first) * 100
				}

				if trend.Change > 5 {
					trend.Trend = "上升"
				} else if trend.Change < -5 {
					trend.Trend = "下降"
				} else {
					trend.Trend = "稳定"
				}
			}

			reports = append(reports, trend)
		}
	}

	return reports
}

/**
 * 生成图表数据
 */
func (rg *MonitoringReportGenerator) generateCharts() map[string]interface{} {
	charts := make(map[string]interface{})

	// 性能趋势图表
	charts["performance_trends"] = rg.generatePerformanceChart()

	// 连接池图表
	charts["connection_charts"] = rg.generateConnectionChart()

	// 健康状态图表
	charts["health_charts"] = rg.generateHealthChart()

	return charts
}

/**
 * 生成性能图表
 */
func (rg *MonitoringReportGenerator) generatePerformanceChart() map[string]interface{} {
	chart := map[string]interface{}{
		"type":   "line",
		"title":  "性能指标趋势",
		"series": make([]map[string]interface{}, 0),
	}

	// 为每个数据库创建系列
	for name, collector := range rg.metricsCollectors {
		if perfMonitor, exists := rg.performanceMonitors[name]; exists {
			series := map[string]interface{}{
				"name": fmt.Sprintf("%s - 查询数", name),
				"data": make([]map[string]interface{}, 0),
			}

			// 获取查询数历史数据
			history := collector.GetMetricHistory(fmt.Sprintf("%s.total_queries", name), rg.reportPeriod)
			for _, point := range history {
				if val, ok := point.Value.(float64); ok {
					series["data"] = append(series["data"].([]map[string]interface{}), map[string]interface{}{
						"x": point.Timestamp.Unix(),
						"y": val,
					})
				}
			}

			chart["series"] = append(chart["series"].([]map[string]interface{}), series)
		}
	}

	return chart
}

/**
 * 生成连接图表
 */
func (rg *MonitoringReportGenerator) generateConnectionChart() map[string]interface{} {
	chart := map[string]interface{}{
		"type":  "bar",
		"title": "连接池状态",
		"data":  make([]map[string]interface{}, 0),
	}

	for name, monitor := range rg.connectionMonitors {
		report := monitor.GetReport()

		chart["data"] = append(chart["data"].([]map[string]interface{}), map[string]interface{}{
			"name":    name,
			"active":  report["active_connections"],
			"idle":    report["idle_connections"],
			"waiting": report["waiting_connections"],
		})
	}

	return chart
}

/**
 * 生成健康图表
 */
func (rg *MonitoringReportGenerator) generateHealthChart() map[string]interface{} {
	chart := map[string]interface{}{
		"type":  "pie",
		"title": "数据库健康状态",
		"data":  make([]map[string]interface{}, 0),
	}

	healthy := 0
	unhealthy := 0

	for _, checker := range rg.healthCheckers {
		result := checker.Check()
		if result.Healthy {
			healthy++
		} else {
			unhealthy++
		}
	}

	chart["data"] = []map[string]interface{}{
		{"name": "健康", "value": healthy, "color": "#00ff00"},
		{"name": "不健康", "value": unhealthy, "color": "#ff0000"},
	}

	return chart
}

/**
 * 计算健康评分
 */
func (rg *MonitoringReportGenerator) calculateHealthScore(report *DatabaseReport) float64 {
	score := 0.0

	// 性能评分 (40%)
	if report.Performance.SuccessRate >= 0.95 {
		score += 0.4
	} else if report.Performance.SuccessRate >= 0.90 {
		score += 0.3
	} else if report.Performance.SuccessRate >= 0.80 {
		score += 0.2
	}

	// 连接效率评分 (30%)
	if report.Connections.ConnectionEfficiency >= 0.7 {
		score += 0.3
	} else if report.Connections.ConnectionEfficiency >= 0.5 {
		score += 0.2
	} else if report.Connections.ConnectionEfficiency >= 0.3 {
		score += 0.1
	}

	// 健康检查评分 (30%)
	healthyChecks := 0
	for _, check := range report.HealthChecks {
		if check.Status == "healthy" {
			healthyChecks++
		}
	}

	if len(report.HealthChecks) > 0 {
		healthRatio := float64(healthyChecks) / float64(len(report.HealthChecks))
		score += healthRatio * 0.3
	}

	return score
}

/**
 * 工具方法
 */
func (rg *MonitoringReportGenerator) boolToStatus(healthy bool) string {
	if healthy {
		return "healthy"
	}
	return "unhealthy"
}

func (rg *MonitoringReportGenerator) healthScoreToStatus(score float64) string {
	if score >= 0.8 {
		return "excellent"
	} else if score >= 0.6 {
		return "good"
	} else if score >= 0.4 {
		return "warning"
	}
	return "critical"
}

func (rg *MonitoringReportGenerator) alertSeverityToString(severity AlertSeverity) string {
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

func (rg *MonitoringReportGenerator) alertStatusToString(status AlertStatus) string {
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
 * 导出报告
 */
func (rg *MonitoringReportGenerator) ExportReport(filename string, format string) error {
	report := rg.GenerateReportData()

	switch strings.ToLower(format) {
	case "json":
		return rg.exportJSONReport(report, filename)
	case "text":
		return rg.exportTextReport(report, filename)
	default:
		return fmt.Errorf("不支持的格式: %s", format)
	}
}

/**
 * 导出JSON报告
 */
func (rg *MonitoringReportGenerator) exportJSONReport(report *ReportData, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("导出JSON报告失败: %w", err)
	}

	LogInfo("JSON监控报告已导出: %s", filename)
	return nil
}

/**
 * 导出文本报告
 */
func (rg *MonitoringReportGenerator) exportTextReport(report *ReportData, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	// 生成文本报告
	text := rg.generateTextReport(report)

	if _, err := file.WriteString(text); err != nil {
		return fmt.Errorf("写入文本报告失败: %w", err)
	}

	LogInfo("文本监控报告已导出: %s", filename)
	return nil
}

/**
 * 生成文本报告
 */
func (rg *MonitoringReportGenerator) generateTextReport(report *ReportData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== %s ===\n", report.Title))
	sb.WriteString(fmt.Sprintf("生成时间: %s\n", report.GeneratedAt.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("报告周期: %s\n\n", report.Period))

	// 摘要
	sb.WriteString("=== 摘要 ===\n")
	sb.WriteString(fmt.Sprintf("数据库总数: %d\n", report.Summary.TotalDatabases))
	sb.WriteString(fmt.Sprintf("健康数据库: %d\n", report.Summary.HealthyDatabases))
	sb.WriteString(fmt.Sprintf("总查询数: %d\n", report.Summary.TotalQueries))
	sb.WriteString(fmt.Sprintf("平均响应时间: %s\n", report.Summary.AvgResponseTime))
	sb.WriteString(fmt.Sprintf("错误率: %.2f%%\n", report.Summary.ErrorRate*100))
	sb.WriteString(fmt.Sprintf("活跃告警: %d\n", report.Summary.ActiveAlerts))
	sb.WriteString(fmt.Sprintf("健康评分: %.2f\n\n", report.Summary.HealthScore))

	// 数据库详情
	sb.WriteString("=== 数据库详情 ===\n")
	for _, db := range report.Details.Databases {
		sb.WriteString(fmt.Sprintf("数据库: %s (%s, 评分: %.2f)\n", db.Name, db.Status, db.HealthScore))
		sb.WriteString(fmt.Sprintf("  性能 - 查询数: %d, 成功率: %.2f%%, 平均响应: %s\n",
			db.Performance.TotalQueries, db.Performance.SuccessRate*100, db.Performance.AvgResponseTime))
		sb.WriteString(fmt.Sprintf("  连接 - 活跃: %d, 空闲: %d, 等待: %d\n",
			db.Connections.ActiveConnections, db.Connections.IdleConnections, db.Connections.WaitingConnections))
		sb.WriteString("  健康检查:\n")
		for _, check := range db.HealthChecks {
			sb.WriteString(fmt.Sprintf("    %s: %s (%s)\n", check.CheckType, check.Status, check.ResponseTime))
		}
		sb.WriteString("\n")
	}

	// 告警
	if len(report.Details.Alerts) > 0 {
		sb.WriteString("=== 告警 ===\n")
		for _, alert := range report.Details.Alerts {
			sb.WriteString(fmt.Sprintf("%s [%s] %s: %s\n",
				alert.Timestamp.Format("15:04:05"), alert.Severity, alert.Name, alert.Status))
			if alert.Duration != "" {
				sb.WriteString(fmt.Sprintf("  持续时间: %s\n", alert.Duration))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
