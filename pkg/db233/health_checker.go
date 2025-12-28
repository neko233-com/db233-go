package db233

import (
	"context"
	"time"
)

/**
 * HealthChecker - 健康检查器
 *
 * 提供数据库连接和服务的健康检查功能
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type HealthChecker struct {
	db         *Db
	timeout    time.Duration
	checkQuery string
}

/**
 * HealthCheckResult - 健康检查结果
 */
type HealthCheckResult struct {
	Healthy      bool
	Message      string
	Timestamp    time.Time
	ResponseTime time.Duration
	Error        error
}

/**
 * 创建健康检查器
 */
func NewHealthChecker(db *Db) *HealthChecker {
	return &HealthChecker{
		db:         db,
		timeout:    5 * time.Second, // 默认5秒超时
		checkQuery: "SELECT 1",      // 默认健康检查查询
	}
}

/**
 * 设置超时时间
 */
func (hc *HealthChecker) SetTimeout(timeout time.Duration) {
	hc.timeout = timeout
}

/**
 * 设置健康检查查询
 */
func (hc *HealthChecker) SetCheckQuery(query string) {
	hc.checkQuery = query
}

/**
 * 执行健康检查
 */
func (hc *HealthChecker) Check() *HealthCheckResult {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	result := &HealthCheckResult{
		Timestamp: start,
	}

	// 执行健康检查查询
	_, err := hc.db.DataSource.QueryContext(ctx, hc.checkQuery)
	result.ResponseTime = time.Since(start)

	if err != nil {
		result.Healthy = false
		result.Error = err
		result.Message = "数据库连接失败: " + err.Error()
		LogError("健康检查失败: %v", err)
	} else {
		result.Healthy = true
		result.Message = "数据库连接正常"
		LogDebug("健康检查通过，响应时间: %v", result.ResponseTime)
	}

	return result
}

/**
 * 执行异步健康检查
 */
func (hc *HealthChecker) CheckAsync() chan *HealthCheckResult {
	resultChan := make(chan *HealthCheckResult, 1)

	go func() {
		result := hc.Check()
		resultChan <- result
	}()

	return resultChan
}

/**
 * 批量健康检查
 */
func CheckMultipleHealth(checkers map[string]*HealthChecker) map[string]*HealthCheckResult {
	results := make(map[string]*HealthCheckResult)

	// 创建结果通道
	type checkResult struct {
		name   string
		result *HealthCheckResult
	}

	resultChan := make(chan checkResult, len(checkers))

	// 并发执行健康检查
	for name, checker := range checkers {
		go func(name string, checker *HealthChecker) {
			result := checker.Check()
			resultChan <- checkResult{name: name, result: result}
		}(name, checker)
	}

	// 收集结果
	for i := 0; i < len(checkers); i++ {
		r := <-resultChan
		results[r.name] = r.result
	}

	return results
}

/**
 * 数据库连接池健康检查
 */
func (hc *HealthChecker) CheckConnectionPool() *HealthCheckResult {
	result := &HealthCheckResult{
		Timestamp: time.Now(),
	}

	// 检查连接池统计信息
	stats := hc.db.DataSource.Stats()
	result.ResponseTime = time.Since(result.Timestamp)

	// 基本健康检查：能够获取连接
	if stats.OpenConnections == 0 && stats.InUse == 0 {
		result.Healthy = false
		result.Message = "连接池未初始化"
		result.Error = NewConnectionException("连接池未初始化")
		return result
	}

	// 检查连接池是否过载
	if stats.InUse >= stats.MaxOpenConnections && stats.MaxOpenConnections > 0 {
		result.Healthy = false
		result.Message = "连接池过载"
		result.Error = NewConnectionException("连接池过载")
		LogWarn("连接池过载: 使用中的连接 %d, 最大连接数 %d", stats.InUse, stats.MaxOpenConnections)
		return result
	}

	// 检查是否有等待的连接
	if stats.WaitCount > 0 {
		LogWarn("连接池有等待的连接: %d", stats.WaitCount)
	}

	result.Healthy = true
	result.Message = "连接池状态正常"

	return result
}

/**
 * 综合健康检查（包括连接和连接池）
 */
func (hc *HealthChecker) ComprehensiveCheck() map[string]*HealthCheckResult {
	results := make(map[string]*HealthCheckResult)

	// 基本连接检查
	results["connection"] = hc.Check()

	// 连接池检查
	results["connection_pool"] = hc.CheckConnectionPool()

	// 计算整体健康状态
	overallHealthy := true
	for _, result := range results {
		if !result.Healthy {
			overallHealthy = false
			break
		}
	}

	// 添加整体状态
	results["overall"] = &HealthCheckResult{
		Healthy:   overallHealthy,
		Timestamp: time.Now(),
		Message:   "综合健康检查完成",
	}

	return results
}

/**
 * 定期健康检查调度器
 */
type HealthCheckScheduler struct {
	checkers   map[string]*HealthChecker
	interval   time.Duration
	stopChan   chan bool
	lastResult map[string]*HealthCheckResult
}

/**
 * 创建健康检查调度器
 */
func NewHealthCheckScheduler(interval time.Duration) *HealthCheckScheduler {
	return &HealthCheckScheduler{
		checkers: make(map[string]*HealthChecker),
		interval: interval,
		stopChan: make(chan bool),
	}
}

/**
 * 添加健康检查器
 */
func (hcs *HealthCheckScheduler) AddChecker(name string, checker *HealthChecker) {
	hcs.checkers[name] = checker
}

/**
 * 启动定期检查
 */
func (hcs *HealthCheckScheduler) Start() {
	LogInfo("健康检查调度器启动，检查间隔: %v", hcs.interval)

	go func() {
		ticker := time.NewTicker(hcs.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				hcs.lastResult = CheckMultipleHealth(hcs.checkers)

				// 记录不健康的状态
				for name, result := range hcs.lastResult {
					if !result.Healthy {
						LogError("定期健康检查失败 [%s]: %s", name, result.Message)
					} else {
						LogDebug("定期健康检查通过 [%s]: %s", name, result.Message)
					}
				}

			case <-hcs.stopChan:
				LogInfo("健康检查调度器停止")
				return
			}
		}
	}()
}

/**
 * 停止定期检查
 */
func (hcs *HealthCheckScheduler) Stop() {
	hcs.stopChan <- true
}

/**
 * 获取指标数据（实现MetricsDataSource接口）
 */
func (hc *HealthChecker) GetMetrics() map[string]interface{} {
	metrics := make(map[string]interface{})

	// 执行健康检查获取最新状态
	result := hc.Check()

	// 健康状态指标
	if result.Healthy {
		metrics["health_status"] = 1.0
	} else {
		metrics["health_status"] = 0.0
	}

	// 响应时间（毫秒）
	metrics["health_check_response_time_ms"] = float64(result.ResponseTime.Nanoseconds()) / 1000000.0

	// 连接池健康检查
	poolResult := hc.CheckConnectionPool()
	if poolResult.Healthy {
		metrics["connection_pool_health"] = 1.0
	} else {
		metrics["connection_pool_health"] = 0.0
	}

	// 综合健康检查
	comprehensive := hc.ComprehensiveCheck()
	healthyCount := 0
	totalCount := 0

	for _, checkResult := range comprehensive {
		totalCount++
		if checkResult.Healthy {
			healthyCount++
		}
	}

	if totalCount > 0 {
		metrics["overall_health_score"] = float64(healthyCount) / float64(totalCount)
	}

	// 检查频率（每分钟）
	metrics["health_checks_per_minute"] = 1.0 // 简化计算

	return metrics
}

/**
 * 获取数据源名称
 */
func (hc *HealthChecker) GetName() string {
	return "health_checker"
}
