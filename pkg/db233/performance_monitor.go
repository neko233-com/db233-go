package db233

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

/**
 * PerformanceMonitor - 性能监控器
 *
 * 提供详细的性能监控和指标收集功能
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type PerformanceMonitor struct {
	dbGroupName string
	db          *Db

	// 基础指标
	totalQueries      int64
	successfulQueries int64
	failedQueries     int64
	slowQueries       int64
	verySlowQueries   int64

	// 时间统计
	totalQueryTime    time.Duration
	minQueryTime      time.Duration
	maxQueryTime      time.Duration
	slowQueryTime     time.Duration
	verySlowQueryTime time.Duration

	// 连接统计
	connectionAcquired int64
	connectionReleased int64
	connectionWaitTime time.Duration
	maxWaitTime        time.Duration

	// 事务统计
	totalTransactions  int64
	activeTransactions int64
	committedTx        int64
	rolledBackTx       int64
	txDuration         time.Duration

	// 错误统计
	errorCount map[string]int64
	lastErrors []ErrorRecord

	// 阈值设置
	slowQueryThreshold     time.Duration
	verySlowQueryThreshold time.Duration
	maxErrorsToKeep        int

	// 时间窗口统计
	windowSize  time.Duration
	windowStart time.Time
	windowStats *TimeWindowStats

	// 锁
	mu sync.RWMutex

	// 监控开关
	enabled bool
}

/**
 * ErrorRecord - 错误记录
 */
type ErrorRecord struct {
	Timestamp time.Time
	Error     error
	Query     string
	Duration  time.Duration
}

/**
 * TimeWindowStats - 时间窗口统计
 */
type TimeWindowStats struct {
	StartTime       time.Time
	EndTime         time.Time
	QueryCount      int64
	ErrorCount      int64
	AvgResponseTime time.Duration
	P95ResponseTime time.Duration
	P99ResponseTime time.Duration
	ResponseTimes   []time.Duration
}

/**
 * 创建性能监控器
 */
func NewPerformanceMonitor(dbGroupName string, db *Db) *PerformanceMonitor {
	pm := &PerformanceMonitor{
		dbGroupName:            dbGroupName,
		db:                     db,
		errorCount:             make(map[string]int64),
		lastErrors:             make([]ErrorRecord, 0),
		slowQueryThreshold:     100 * time.Millisecond,
		verySlowQueryThreshold: 1000 * time.Millisecond, // 1秒
		maxErrorsToKeep:        100,
		windowSize:             5 * time.Minute,
		windowStart:            time.Now(),
		enabled:                true,
		minQueryTime:           time.Hour, // 初始化为较大值
	}

	pm.windowStats = &TimeWindowStats{
		StartTime:     time.Now(),
		ResponseTimes: make([]time.Duration, 0),
	}

	return pm
}

/**
 * 启用监控
 */
func (pm *PerformanceMonitor) Enable() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.enabled = true
	LogInfo("性能监控已启用: %s", pm.dbGroupName)
}

/**
 * 禁用监控
 */
func (pm *PerformanceMonitor) Disable() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.enabled = false
	LogInfo("性能监控已禁用: %s", pm.dbGroupName)
}

/**
 * 设置慢查询阈值
 */
func (pm *PerformanceMonitor) SetSlowQueryThreshold(threshold time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.slowQueryThreshold = threshold
}

/**
 * 设置非常慢查询阈值
 */
func (pm *PerformanceMonitor) SetVerySlowQueryThreshold(threshold time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.verySlowQueryThreshold = threshold
}

/**
 * 记录查询执行
 */
func (pm *PerformanceMonitor) RecordQuery(query string, duration time.Duration, success bool, err error) {
	if !pm.enabled {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.totalQueries++

	if success {
		pm.successfulQueries++
	} else {
		pm.failedQueries++

		// 记录错误
		if err != nil {
			errorType := fmt.Sprintf("%T", err)
			pm.errorCount[errorType]++

			// 保留最近的错误
			errorRecord := ErrorRecord{
				Timestamp: time.Now(),
				Error:     err,
				Query:     query,
				Duration:  duration,
			}

			pm.lastErrors = append(pm.lastErrors, errorRecord)
			if len(pm.lastErrors) > pm.maxErrorsToKeep {
				pm.lastErrors = pm.lastErrors[1:]
			}
		}
	}

	// 更新时间统计
	pm.totalQueryTime += duration

	if duration < pm.minQueryTime {
		pm.minQueryTime = duration
	}
	if duration > pm.maxQueryTime {
		pm.maxQueryTime = duration
	}

	// 慢查询统计
	if duration >= pm.slowQueryThreshold {
		pm.slowQueries++
		pm.slowQueryTime += duration
	}

	if duration >= pm.verySlowQueryThreshold {
		pm.verySlowQueries++
		pm.verySlowQueryTime += duration
		LogWarn("非常慢查询 [%s]: %v, 查询: %s", pm.dbGroupName, duration, query)
	}

	// 时间窗口统计
	pm.updateTimeWindowStats(duration)
}

/**
 * 记录连接获取
 */
func (pm *PerformanceMonitor) RecordConnectionAcquired(waitTime time.Duration) {
	if !pm.enabled {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.connectionAcquired++
	pm.connectionWaitTime += waitTime

	if waitTime > pm.maxWaitTime {
		pm.maxWaitTime = waitTime
	}
}

/**
 * 记录连接释放
 */
func (pm *PerformanceMonitor) RecordConnectionReleased() {
	if !pm.enabled {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.connectionReleased++
}

/**
 * 记录事务开始
 */
func (pm *PerformanceMonitor) RecordTransactionStart() {
	if !pm.enabled {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.totalTransactions++
	pm.activeTransactions++
}

/**
 * 记录事务结束
 */
func (pm *PerformanceMonitor) RecordTransactionEnd(duration time.Duration, committed bool) {
	if !pm.enabled {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.activeTransactions--
	pm.txDuration += duration

	if committed {
		pm.committedTx++
	} else {
		pm.rolledBackTx++
	}
}

/**
 * 更新时间窗口统计
 */
func (pm *PerformanceMonitor) updateTimeWindowStats(duration time.Duration) {
	now := time.Now()

	// 检查是否需要重置窗口
	if now.Sub(pm.windowStart) >= pm.windowSize {
		pm.windowStart = now
		pm.windowStats = &TimeWindowStats{
			StartTime:     now,
			ResponseTimes: make([]time.Duration, 0),
		}
	}

	pm.windowStats.EndTime = now
	pm.windowStats.QueryCount++
	pm.windowStats.ResponseTimes = append(pm.windowStats.ResponseTimes, duration)

	// 计算百分位数
	if len(pm.windowStats.ResponseTimes) > 0 {
		sorted := make([]time.Duration, len(pm.windowStats.ResponseTimes))
		copy(sorted, pm.windowStats.ResponseTimes)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i] < sorted[j]
		})

		n := len(sorted)
		pm.windowStats.AvgResponseTime = pm.totalQueryTime / time.Duration(pm.totalQueries)

		p95Index := int(float64(n) * 0.95)
		if p95Index < n {
			pm.windowStats.P95ResponseTime = sorted[p95Index]
		}

		p99Index := int(float64(n) * 0.99)
		if p99Index < n {
			pm.windowStats.P99ResponseTime = sorted[p99Index]
		}
	}
}

/**
 * 获取详细监控报告
 */
func (pm *PerformanceMonitor) GetDetailedReport() map[string]interface{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	report := make(map[string]interface{})

	// 基础信息
	report["db_group"] = pm.dbGroupName
	report["enabled"] = pm.enabled
	report["timestamp"] = time.Now()

	// 查询统计
	report["total_queries"] = pm.totalQueries
	report["successful_queries"] = pm.successfulQueries
	report["failed_queries"] = pm.failedQueries
	report["slow_queries"] = pm.slowQueries
	report["very_slow_queries"] = pm.verySlowQueries

	// 成功率和错误率
	if pm.totalQueries > 0 {
		report["success_rate"] = float64(pm.successfulQueries) / float64(pm.totalQueries)
		report["error_rate"] = float64(pm.failedQueries) / float64(pm.totalQueries)
		report["slow_query_rate"] = float64(pm.slowQueries) / float64(pm.totalQueries)
		report["very_slow_query_rate"] = float64(pm.verySlowQueries) / float64(pm.totalQueries)
	}

	// 时间统计
	report["total_query_time"] = pm.totalQueryTime.String()
	report["min_query_time"] = pm.minQueryTime.String()
	report["max_query_time"] = pm.maxQueryTime.String()
	report["avg_query_time"] = "0s"

	if pm.totalQueries > 0 {
		report["avg_query_time"] = (pm.totalQueryTime / time.Duration(pm.totalQueries)).String()
	}

	if pm.successfulQueries > 0 {
		report["avg_successful_query_time"] = (pm.totalQueryTime / time.Duration(pm.successfulQueries)).String()
	}

	// 慢查询时间统计
	if pm.slowQueries > 0 {
		report["avg_slow_query_time"] = (pm.slowQueryTime / time.Duration(pm.slowQueries)).String()
	}
	if pm.verySlowQueries > 0 {
		report["avg_very_slow_query_time"] = (pm.verySlowQueryTime / time.Duration(pm.verySlowQueries)).String()
	}

	// 连接统计
	report["connection_acquired"] = pm.connectionAcquired
	report["connection_released"] = pm.connectionReleased
	report["total_connection_wait_time"] = pm.connectionWaitTime.String()
	report["max_connection_wait_time"] = pm.maxWaitTime.String()

	if pm.connectionAcquired > 0 {
		report["avg_connection_wait_time"] = (pm.connectionWaitTime / time.Duration(pm.connectionAcquired)).String()
	}

	// 事务统计
	report["total_transactions"] = pm.totalTransactions
	report["active_transactions"] = pm.activeTransactions
	report["committed_transactions"] = pm.committedTx
	report["rolled_back_transactions"] = pm.rolledBackTx
	report["total_transaction_time"] = pm.txDuration.String()

	if pm.totalTransactions > 0 {
		report["avg_transaction_time"] = (pm.txDuration / time.Duration(pm.totalTransactions)).String()
		report["transaction_commit_rate"] = float64(pm.committedTx) / float64(pm.totalTransactions)
	}

	// 错误统计
	report["error_types"] = pm.errorCount
	report["error_count"] = len(pm.lastErrors)

	// 最近错误
	recentErrors := make([]map[string]interface{}, 0, len(pm.lastErrors))
	for _, err := range pm.lastErrors {
		recentErrors = append(recentErrors, map[string]interface{}{
			"timestamp": err.Timestamp,
			"error":     err.Error.Error(),
			"query":     err.Query,
			"duration":  err.Duration.String(),
		})
	}
	report["recent_errors"] = recentErrors

	// 时间窗口统计
	report["time_window"] = map[string]interface{}{
		"start_time":        pm.windowStats.StartTime,
		"end_time":          pm.windowStats.EndTime,
		"query_count":       pm.windowStats.QueryCount,
		"avg_response_time": pm.windowStats.AvgResponseTime.String(),
		"p95_response_time": pm.windowStats.P95ResponseTime.String(),
		"p99_response_time": pm.windowStats.P99ResponseTime.String(),
	}

	// 阈值设置
	report["thresholds"] = map[string]interface{}{
		"slow_query_threshold":      pm.slowQueryThreshold.String(),
		"very_slow_query_threshold": pm.verySlowQueryThreshold.String(),
	}

	return report
}

/**
 * 获取摘要报告
 */
func (pm *PerformanceMonitor) GetSummaryReport() map[string]interface{} {
	report := pm.GetDetailedReport()

	// 只保留关键指标
	summary := map[string]interface{}{
		"db_group":            report["db_group"],
		"timestamp":           report["timestamp"],
		"total_queries":       report["total_queries"],
		"success_rate":        report["success_rate"],
		"avg_query_time":      report["avg_query_time"],
		"slow_query_rate":     report["slow_query_rate"],
		"active_transactions": report["active_transactions"],
		"error_count":         report["error_count"],
	}

	return summary
}

/**
 * 重置统计信息
 */
func (pm *PerformanceMonitor) Reset() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.totalQueries = 0
	pm.successfulQueries = 0
	pm.failedQueries = 0
	pm.slowQueries = 0
	pm.verySlowQueries = 0

	pm.totalQueryTime = 0
	pm.minQueryTime = time.Hour
	pm.maxQueryTime = 0
	pm.slowQueryTime = 0
	pm.verySlowQueryTime = 0

	pm.connectionAcquired = 0
	pm.connectionReleased = 0
	pm.connectionWaitTime = 0
	pm.maxWaitTime = 0

	pm.totalTransactions = 0
	pm.activeTransactions = 0
	pm.committedTx = 0
	pm.rolledBackTx = 0
	pm.txDuration = 0

	pm.errorCount = make(map[string]int64)
	pm.lastErrors = make([]ErrorRecord, 0)

	pm.windowStart = time.Now()
	pm.windowStats = &TimeWindowStats{
		StartTime:     time.Now(),
		ResponseTimes: make([]time.Duration, 0),
	}

	LogInfo("性能监控统计已重置: %s", pm.dbGroupName)
}

/**
 * 获取指标数据（实现MetricsDataSource接口）
 */
func (pm *PerformanceMonitor) GetMetrics() map[string]interface{} {
	report := pm.GetDetailedReport()

	// 转换报告为指标格式
	metrics := make(map[string]interface{})

	// 基础查询指标
	if val, ok := report["total_queries"].(int64); ok {
		metrics["total_queries"] = val
	}
	if val, ok := report["successful_queries"].(int64); ok {
		metrics["successful_queries"] = val
	}
	if val, ok := report["failed_queries"].(int64); ok {
		metrics["failed_queries"] = val
	}

	// 成功率
	if val, ok := report["success_rate"].(float64); ok {
		metrics["success_rate"] = val
	}

	// 响应时间（转换为毫秒）
	if avgTimeStr, ok := report["avg_query_time"].(string); ok {
		if avgTime, err := time.ParseDuration(avgTimeStr); err == nil {
			metrics["avg_query_time_ms"] = float64(avgTime.Nanoseconds()) / 1000000.0
		}
	}

	// 慢查询指标
	if val, ok := report["slow_queries"].(int64); ok {
		metrics["slow_queries"] = val
	}
	if val, ok := report["very_slow_queries"].(int64); ok {
		metrics["very_slow_queries"] = val
	}

	// 连接指标
	if val, ok := report["connection_acquired"].(int64); ok {
		metrics["connection_acquired"] = val
	}
	if val, ok := report["active_connections"].(int64); ok {
		metrics["active_connections"] = val
	}

	// 事务指标
	if val, ok := report["total_transactions"].(int64); ok {
		metrics["total_transactions"] = val
	}
	if val, ok := report["active_transactions"].(int64); ok {
		metrics["active_transactions"] = val
	}

	// 错误指标
	if val, ok := report["error_count"].(int); ok {
		metrics["error_count"] = val
	}

	return metrics
}

/**
 * 获取数据源名称
 */
func (pm *PerformanceMonitor) GetName() string {
	return fmt.Sprintf("performance_monitor_%s", pm.dbGroupName)
}
