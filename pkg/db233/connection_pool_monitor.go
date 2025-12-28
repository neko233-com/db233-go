package db233

import (
	"fmt"
	"sync"
	"time"
)

/**
 * ConnectionPoolMonitor - 连接池监控器
 *
 * 监控数据库连接池的状态和性能指标
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type ConnectionPoolMonitor struct {
	dbGroupName string
	db          *Db

	// 统计信息
	totalConnections   int64
	activeConnections  int64
	idleConnections    int64
	waitingConnections int64
	maxConnections     int64
	minConnections     int64

	// 性能指标
	connectionWaitTime time.Duration
	queryExecutionTime time.Duration
	totalQueries       int64
	failedQueries      int64
	slowQueries        int64

	// 慢查询阈值
	slowQueryThreshold time.Duration

	// 锁
	mu sync.RWMutex

	// 监控开关
	enabled bool
}

/**
 * 创建连接池监控器
 */
func NewConnectionPoolMonitor(dbGroupName string, db *Db) *ConnectionPoolMonitor {
	return &ConnectionPoolMonitor{
		dbGroupName:        dbGroupName,
		db:                 db,
		slowQueryThreshold: 100 * time.Millisecond, // 默认100ms
		enabled:            true,
	}
}

/**
 * 启用监控
 */
func (cpm *ConnectionPoolMonitor) Enable() {
	cpm.mu.Lock()
	defer cpm.mu.Unlock()
	cpm.enabled = true
	LogInfo("连接池监控已启用: %s", cpm.dbGroupName)
}

/**
 * 禁用监控
 */
func (cpm *ConnectionPoolMonitor) Disable() {
	cpm.mu.Lock()
	defer cpm.mu.Unlock()
	cpm.enabled = false
	LogInfo("连接池监控已禁用: %s", cpm.dbGroupName)
}

/**
 * 设置慢查询阈值
 */
func (cpm *ConnectionPoolMonitor) SetSlowQueryThreshold(threshold time.Duration) {
	cpm.mu.Lock()
	defer cpm.mu.Unlock()
	cpm.slowQueryThreshold = threshold
}

/**
 * 记录连接获取
 */
func (cpm *ConnectionPoolMonitor) RecordConnectionAcquired(waitTime time.Duration) {
	if !cpm.enabled {
		return
	}

	cpm.mu.Lock()
	defer cpm.mu.Unlock()

	cpm.activeConnections++
	cpm.connectionWaitTime += waitTime

	if waitTime > cpm.slowQueryThreshold {
		LogWarn("慢连接获取: %s, 等待时间: %v", cpm.dbGroupName, waitTime)
	}
}

/**
 * 记录连接释放
 */
func (cpm *ConnectionPoolMonitor) RecordConnectionReleased() {
	if !cpm.enabled {
		return
	}

	cpm.mu.Lock()
	defer cpm.mu.Unlock()

	cpm.activeConnections--
	cpm.idleConnections++
}

/**
 * 记录查询执行
 */
func (cpm *ConnectionPoolMonitor) RecordQueryExecution(executionTime time.Duration, success bool) {
	if !cpm.enabled {
		return
	}

	cpm.mu.Lock()
	defer cpm.mu.Unlock()

	cpm.totalQueries++
	cpm.queryExecutionTime += executionTime

	if !success {
		cpm.failedQueries++
	}

	if executionTime > cpm.slowQueryThreshold {
		cpm.slowQueries++
		LogWarn("慢查询: %s, 执行时间: %v", cpm.dbGroupName, executionTime)
	}
}

/**
 * 更新连接池统计信息
 */
func (cpm *ConnectionPoolMonitor) UpdatePoolStats(total, active, idle, waiting, max, min int64) {
	if !cpm.enabled {
		return
	}

	cpm.mu.Lock()
	defer cpm.mu.Unlock()

	cpm.totalConnections = total
	cpm.activeConnections = active
	cpm.idleConnections = idle
	cpm.waitingConnections = waiting
	cpm.maxConnections = max
	cpm.minConnections = min
}

/**
 * 获取监控报告
 */
func (cpm *ConnectionPoolMonitor) GetReport() map[string]interface{} {
	cpm.mu.RLock()
	defer cpm.mu.RUnlock()

	report := make(map[string]interface{})

	// 连接池统计
	report["db_group"] = cpm.dbGroupName
	report["total_connections"] = cpm.totalConnections
	report["active_connections"] = cpm.activeConnections
	report["idle_connections"] = cpm.idleConnections
	report["waiting_connections"] = cpm.waitingConnections
	report["max_connections"] = cpm.maxConnections
	report["min_connections"] = cpm.minConnections

	// 性能指标
	report["total_queries"] = cpm.totalQueries
	report["failed_queries"] = cpm.failedQueries
	report["slow_queries"] = cpm.slowQueries
	report["slow_query_threshold"] = cpm.slowQueryThreshold.String()

	if cpm.totalQueries > 0 {
		report["avg_query_time"] = (cpm.queryExecutionTime / time.Duration(cpm.totalQueries)).String()
		report["failure_rate"] = float64(cpm.failedQueries) / float64(cpm.totalQueries)
	}

	if cpm.activeConnections > 0 {
		report["avg_connection_wait_time"] = (cpm.connectionWaitTime / time.Duration(cpm.activeConnections)).String()
	}

	report["enabled"] = cpm.enabled

	return report
}

/**
 * 重置统计信息
 */
func (cpm *ConnectionPoolMonitor) Reset() {
	cpm.mu.Lock()
	defer cpm.mu.Unlock()

	cpm.connectionWaitTime = 0
	cpm.queryExecutionTime = 0
	cpm.totalQueries = 0
	cpm.failedQueries = 0
	cpm.slowQueries = 0

	LogInfo("连接池监控统计已重置: %s", cpm.dbGroupName)
}

/**
 * 获取指标数据（实现MetricsDataSource接口）
 */
func (cpm *ConnectionPoolMonitor) GetMetrics() map[string]interface{} {
	report := cpm.GetReport()

	metrics := make(map[string]interface{})

	// 连接池统计指标
	if val, ok := report["total_connections"].(int64); ok {
		metrics["total_connections"] = val
	}
	if val, ok := report["active_connections"].(int64); ok {
		metrics["active_connections"] = val
	}
	if val, ok := report["idle_connections"].(int64); ok {
		metrics["idle_connections"] = val
	}
	if val, ok := report["waiting_connections"].(int64); ok {
		metrics["waiting_connections"] = val
	}
	if val, ok := report["max_connections"].(int64); ok {
		metrics["max_connections"] = val
	}

	// 性能指标
	if val, ok := report["total_queries"].(int64); ok {
		metrics["total_queries"] = val
	}
	if val, ok := report["failed_queries"].(int64); ok {
		metrics["failed_queries"] = val
	}
	if val, ok := report["slow_queries"].(int64); ok {
		metrics["slow_queries"] = val
	}

	// 响应时间（转换为毫秒）
	if avgTimeStr, ok := report["avg_query_time"].(string); ok {
		if avgTime, err := time.ParseDuration(avgTimeStr); err == nil {
			metrics["avg_query_time_ms"] = float64(avgTime.Nanoseconds()) / 1000000.0
		}
	}

	if avgWaitStr, ok := report["avg_connection_wait_time"].(string); ok {
		if avgWait, err := time.ParseDuration(avgWaitStr); err == nil {
			metrics["avg_connection_wait_time_ms"] = float64(avgWait.Nanoseconds()) / 1000000.0
		}
	}

	// 计算连接利用率
	if total, ok := report["total_connections"].(int64); ok && total > 0 {
		if active, ok := report["active_connections"].(int64); ok {
			metrics["connection_utilization"] = float64(active) / float64(total)
		}
	}

	return metrics
}

/**
 * 获取数据源名称
 */
func (cpm *ConnectionPoolMonitor) GetName() string {
	return fmt.Sprintf("connection_pool_monitor_%s", cpm.dbGroupName)
}
