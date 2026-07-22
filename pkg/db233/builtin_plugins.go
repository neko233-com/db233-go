package db233

import (
	"log"
	"sync/atomic"
	"time"
)

// LoggingPlugin - 日志插件
// 记录 SQL 执行的详细信息
type LoggingPlugin struct {
	*AbstractDb233Plugin
	logFullSQL atomic.Bool
}

// 创建日志插件
func NewLoggingPlugin() *LoggingPlugin {
	return &LoggingPlugin{
		AbstractDb233Plugin: NewAbstractDb233Plugin("logging-plugin"),
	}
}

// 初始化插件
func (p *LoggingPlugin) InitPlugin() {
	log.Println("LoggingPlugin initialized")
}

// SetLogFullSQL 控制是否记录完整 SQL。默认关闭；开启后日志可能包含敏感字面量。
func (p *LoggingPlugin) SetLogFullSQL(enabled bool) {
	p.logFullSQL.Store(enabled)
}

// SQL 执行前记录日志
func (p *LoggingPlugin) PreExecuteSql(context *ExecuteSqlContext) {
	if context == nil {
		log.Print("[SQL-PRE] skipped: nil context")
		return
	}
	// 参数值和数量都可能形成业务侧信道；默认与完整 SQL 模式均不记录。
	log.Printf("[SQL-PRE] %s", sqlForComponentLog(context.Sql, p.logFullSQL.Load()))
}

// SQL 执行后记录日志
func (p *LoggingPlugin) PostExecuteSql(context *ExecuteSqlContext) {
	if context == nil {
		log.Print("[SQL-POST] skipped: nil context")
		return
	}
	duration := context.Duration
	if context.Error != nil {
		log.Printf("[SQL-POST] ERROR - Duration: %v, Error: %s", duration, safeErrorForLog(context.Error))
	} else {
		log.Printf("[SQL-POST] SUCCESS - Duration: %v, AffectedRows: %d", duration, context.AffectedRows)
	}
}

// PerformanceMonitorPlugin - 性能监控插件
// 监控 SQL 执行性能，记录慢查询
type PerformanceMonitorPlugin struct {
	*AbstractDb233Plugin
	slowQueryThreshold time.Duration
	logFullSQL         atomic.Bool
}

// 创建性能监控插件
func NewPerformanceMonitorPlugin(slowQueryThreshold time.Duration) *PerformanceMonitorPlugin {
	return &PerformanceMonitorPlugin{
		AbstractDb233Plugin: NewAbstractDb233Plugin("performance-monitor-plugin"),
		slowQueryThreshold:  slowQueryThreshold,
	}
}

// 初始化插件
func (p *PerformanceMonitorPlugin) InitPlugin() {
	log.Printf("PerformanceMonitorPlugin initialized with threshold: %v", p.slowQueryThreshold)
}

// SetLogFullSQL 控制慢查询日志是否记录完整 SQL。默认关闭。
func (p *PerformanceMonitorPlugin) SetLogFullSQL(enabled bool) {
	p.logFullSQL.Store(enabled)
}

// SQL 执行后检查性能
func (p *PerformanceMonitorPlugin) PostExecuteSql(context *ExecuteSqlContext) {
	if context == nil {
		return
	}
	if context.Duration > p.slowQueryThreshold {
		log.Printf("[SLOW-QUERY] %s, Duration: %v, Threshold: %v",
			sqlForComponentLog(context.Sql, p.logFullSQL.Load()), context.Duration, p.slowQueryThreshold)
	}
}

// MetricsPlugin - 指标收集插件
// 收集 SQL 执行的各项指标
type MetricsPlugin struct {
	*AbstractDb233Plugin
	totalQueries    atomic.Int64
	totalDurationNs atomic.Int64
	errorCount      atomic.Int64
}

// 创建指标收集插件
func NewMetricsPlugin() *MetricsPlugin {
	return &MetricsPlugin{
		AbstractDb233Plugin: NewAbstractDb233Plugin("metrics-plugin"),
	}
}

// 初始化插件
func (p *MetricsPlugin) InitPlugin() {
	log.Println("MetricsPlugin initialized")
	p.totalQueries.Store(0)
	p.totalDurationNs.Store(0)
	p.errorCount.Store(0)
}

// SQL 执行后收集指标
func (p *MetricsPlugin) PostExecuteSql(context *ExecuteSqlContext) {
	if context == nil {
		return
	}
	p.totalQueries.Add(1)
	p.totalDurationNs.Add(int64(context.Duration))
	if context.Error != nil {
		p.errorCount.Add(1)
	}
}

// 获取指标数据
func (p *MetricsPlugin) GetMetrics() map[string]any {
	return map[string]any{
		"total_queries":  int(p.totalQueries.Load()),
		"total_duration": time.Duration(p.totalDurationNs.Load()),
		"error_count":    int(p.errorCount.Load()),
	}
}

// 打印指标报告
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
