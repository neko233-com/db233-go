package db233

import (
	"log"
	"time"
)

/**
 * LoggingPlugin - 日志插件
 *
 * 记录 SQL 执行的详细信息
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type LoggingPlugin struct {
	*AbstractDb233Plugin
}

/**
 * 创建日志插件
 */
func NewLoggingPlugin() *LoggingPlugin {
	return &LoggingPlugin{
		AbstractDb233Plugin: NewAbstractDb233Plugin("logging-plugin"),
	}
}

/**
 * 初始化插件
 */
func (p *LoggingPlugin) InitPlugin() {
	log.Println("LoggingPlugin initialized")
}

/**
 * SQL 执行前记录日志
 */
func (p *LoggingPlugin) PreExecuteSql(context *ExecuteSqlContext) {
	log.Printf("[SQL-PRE] %s, Params: %v", context.Sql, context.Params)
}

/**
 * SQL 执行后记录日志
 */
func (p *LoggingPlugin) PostExecuteSql(context *ExecuteSqlContext) {
	duration := context.Duration
	if context.Error != nil {
		log.Printf("[SQL-POST] ERROR - Duration: %v, Error: %v", duration, context.Error)
	} else {
		log.Printf("[SQL-POST] SUCCESS - Duration: %v, AffectedRows: %d", duration, context.AffectedRows)
	}
}

/**
 * PerformanceMonitorPlugin - 性能监控插件
 *
 * 监控 SQL 执行性能，记录慢查询
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type PerformanceMonitorPlugin struct {
	*AbstractDb233Plugin
	slowQueryThreshold time.Duration
}

/**
 * 创建性能监控插件
 */
func NewPerformanceMonitorPlugin(slowQueryThreshold time.Duration) *PerformanceMonitorPlugin {
	return &PerformanceMonitorPlugin{
		AbstractDb233Plugin: NewAbstractDb233Plugin("performance-monitor-plugin"),
		slowQueryThreshold:  slowQueryThreshold,
	}
}

/**
 * 初始化插件
 */
func (p *PerformanceMonitorPlugin) InitPlugin() {
	log.Printf("PerformanceMonitorPlugin initialized with threshold: %v", p.slowQueryThreshold)
}

/**
 * SQL 执行后检查性能
 */
func (p *PerformanceMonitorPlugin) PostExecuteSql(context *ExecuteSqlContext) {
	if context.Duration > p.slowQueryThreshold {
		log.Printf("[SLOW-QUERY] SQL: %s, Duration: %v, Threshold: %v",
			context.Sql, context.Duration, p.slowQueryThreshold)
	}
}

/**
 * MetricsPlugin - 指标收集插件
 *
 * 收集 SQL 执行的各项指标
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type MetricsPlugin struct {
	*AbstractDb233Plugin
	metrics map[string]interface{}
}

/**
 * 创建指标收集插件
 */
func NewMetricsPlugin() *MetricsPlugin {
	return &MetricsPlugin{
		AbstractDb233Plugin: NewAbstractDb233Plugin("metrics-plugin"),
		metrics:             make(map[string]interface{}),
	}
}

/**
 * 初始化插件
 */
func (p *MetricsPlugin) InitPlugin() {
	log.Println("MetricsPlugin initialized")
	p.metrics["total_queries"] = 0
	p.metrics["total_duration"] = time.Duration(0)
	p.metrics["error_count"] = 0
}

/**
 * SQL 执行后收集指标
 */
func (p *MetricsPlugin) PostExecuteSql(context *ExecuteSqlContext) {
	// 更新总查询数
	if totalQueries, ok := p.metrics["total_queries"].(int); ok {
		p.metrics["total_queries"] = totalQueries + 1
	}

	// 更新总耗时
	if totalDuration, ok := p.metrics["total_duration"].(time.Duration); ok {
		p.metrics["total_duration"] = totalDuration + context.Duration
	}

	// 更新错误数
	if context.Error != nil {
		if errorCount, ok := p.metrics["error_count"].(int); ok {
			p.metrics["error_count"] = errorCount + 1
		}
	}
}

/**
 * 获取指标数据
 */
func (p *MetricsPlugin) GetMetrics() map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range p.metrics {
		result[k] = v
	}
	return result
}

/**
 * 打印指标报告
 */
func (p *MetricsPlugin) PrintReport() {
	metrics := p.GetMetrics()

	totalQueries := 0
	if val, ok := metrics["total_queries"].(int); ok {
		totalQueries = val
	}

	totalDuration := time.Duration(0)
	if val, ok := metrics["total_duration"].(time.Duration); ok {
		totalDuration = val
	}

	errorCount := 0
	if val, ok := metrics["error_count"].(int); ok {
		errorCount = val
	}

	log.Printf("[METRICS-REPORT] Total Queries: %d, Total Duration: %v, Errors: %d",
		totalQueries, totalDuration, errorCount)

	if totalQueries > 0 {
		avgDuration := totalDuration / time.Duration(totalQueries)
		log.Printf("[METRICS-REPORT] Average Query Time: %v", avgDuration)
	}
}
