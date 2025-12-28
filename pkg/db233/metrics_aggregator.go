package db233

import (
	"sort"
	"sync"
	"time"
)

/**
 * MetricsAggregator - 指标聚合器
 *
 * 聚合多个监控数据源的指标，提供统一的指标查询和计算接口
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type MetricsAggregator struct {
	name string

	// 数据源
	dataSources []MetricsDataSource

	// 聚合指标缓存
	aggregatedMetrics map[string]AggregatedMetric
	cacheDuration     time.Duration
	lastAggregation   time.Time

	// 聚合配置
	aggregationRules map[string]AggregationRule

	// 锁
	mu sync.RWMutex

	// 控制
	enabled bool
}

/**
 * AggregatedMetric - 聚合指标
 */
type AggregatedMetric struct {
	Name       string
	Value      interface{}
	Count      int
	Sum        float64
	Avg        float64
	Min        float64
	Max        float64
	P50        float64
	P95        float64
	P99        float64
	LastUpdate time.Time
	DataPoints []float64
}

/**
 * AggregationRule - 聚合规则
 */
type AggregationRule struct {
	MetricPattern string
	Aggregation   AggregationType
	TimeWindow    time.Duration
	Enabled       bool
}

/**
 * AggregationType - 聚合类型
 */
type AggregationType int

const (
	Sum AggregationType = iota
	Avg
	Min
	Max
	Count
	Percentile
	Rate
)

/**
 * 创建指标聚合器
 */
func NewMetricsAggregator(name string) *MetricsAggregator {
	return &MetricsAggregator{
		name:              name,
		dataSources:       make([]MetricsDataSource, 0),
		aggregatedMetrics: make(map[string]AggregatedMetric),
		cacheDuration:     30 * time.Second, // 默认30秒缓存
		lastAggregation:   time.Now().Add(-time.Hour),
		aggregationRules:  make(map[string]AggregationRule),
		enabled:           true,
	}
}

/**
 * 添加数据源
 */
func (ma *MetricsAggregator) AddDataSource(source MetricsDataSource) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.dataSources = append(ma.dataSources, source)
	LogInfo("数据源已添加到聚合器: %s -> %s", ma.name, source.GetName())
}

/**
 * 添加聚合规则
 */
func (ma *MetricsAggregator) AddAggregationRule(name string, rule AggregationRule) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.aggregationRules[name] = rule
	LogInfo("聚合规则已添加: %s -> %s", ma.name, name)
}

/**
 * 设置缓存持续时间
 */
func (ma *MetricsAggregator) SetCacheDuration(duration time.Duration) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.cacheDuration = duration
}

/**
 * 启用聚合器
 */
func (ma *MetricsAggregator) Enable() {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.enabled = true
	LogInfo("指标聚合器已启用: %s", ma.name)
}

/**
 * 禁用聚合器
 */
func (ma *MetricsAggregator) Disable() {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.enabled = false
	LogInfo("指标聚合器已禁用: %s", ma.name)
}

/**
 * 获取聚合指标
 */
func (ma *MetricsAggregator) GetAggregatedMetric(name string) (AggregatedMetric, bool) {
	ma.mu.RLock()
	defer ma.mu.RUnlock()

	metric, exists := ma.aggregatedMetrics[name]
	return metric, exists
}

/**
 * 获取所有聚合指标
 */
func (ma *MetricsAggregator) GetAllAggregatedMetrics() map[string]AggregatedMetric {
	ma.mu.RLock()
	defer ma.mu.RUnlock()

	result := make(map[string]AggregatedMetric)
	for k, v := range ma.aggregatedMetrics {
		result[k] = v
	}

	return result
}

/**
 * 获取聚合指标值
 */
func (ma *MetricsAggregator) GetAggregatedValue(name string) interface{} {
	if metric, exists := ma.GetAggregatedMetric(name); exists {
		return metric.Value
	}
	return nil
}

/**
 * 刷新聚合指标
 */
func (ma *MetricsAggregator) RefreshMetrics() error {
	if !ma.enabled {
		return nil
	}

	ma.mu.Lock()
	defer ma.mu.Unlock()

	now := time.Now()

	// 检查缓存是否过期
	if now.Sub(ma.lastAggregation) < ma.cacheDuration {
		return nil // 使用缓存
	}

	// 收集所有数据源的指标
	allMetrics := make(map[string][]interface{})

	for _, source := range ma.dataSources {
		sourceMetrics := source.GetMetrics()

		for metricName, value := range sourceMetrics {
			if _, exists := allMetrics[metricName]; !exists {
				allMetrics[metricName] = make([]interface{}, 0)
			}
			allMetrics[metricName] = append(allMetrics[metricName], value)
		}
	}

	// 应用聚合规则
	for ruleName, rule := range ma.aggregationRules {
		if !rule.Enabled {
			continue
		}

		matchingMetrics := ma.findMatchingMetrics(rule.MetricPattern, allMetrics)
		if len(matchingMetrics) == 0 {
			continue
		}

		aggregated := ma.aggregateMetrics(ruleName, matchingMetrics, rule.Aggregation)
		ma.aggregatedMetrics[ruleName] = aggregated
	}

	// 聚合未配置规则的指标（使用默认聚合）
	for metricName, values := range allMetrics {
		if _, exists := ma.aggregatedMetrics[metricName]; !exists {
			aggregated := ma.aggregateMetrics(metricName, values, Avg) // 默认使用平均值
			ma.aggregatedMetrics[metricName] = aggregated
		}
	}

	ma.lastAggregation = now
	return nil
}

/**
 * 查找匹配的指标
 */
func (ma *MetricsAggregator) findMatchingMetrics(pattern string, allMetrics map[string][]interface{}) []interface{} {
	matching := make([]interface{}, 0)

	for metricName, values := range allMetrics {
		if ma.matchesPattern(metricName, pattern) {
			matching = append(matching, values...)
		}
	}

	return matching
}

/**
 * 检查指标名称是否匹配模式
 */
func (ma *MetricsAggregator) matchesPattern(metricName, pattern string) bool {
	// 简单模式匹配，支持通配符 *
	if pattern == "*" {
		return true
	}

	// 简单前缀匹配
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(metricName) >= len(prefix) && metricName[:len(prefix)] == prefix
	}

	return metricName == pattern
}

/**
 * 聚合指标值
 */
func (ma *MetricsAggregator) aggregateMetrics(name string, values []interface{}, aggType AggregationType) AggregatedMetric {
	metric := AggregatedMetric{
		Name:       name,
		LastUpdate: time.Now(),
		DataPoints: make([]float64, 0),
	}

	// 转换数值类型
	numericValues := make([]float64, 0, len(values))
	for _, value := range values {
		if f, ok := ma.toFloat64(value); ok {
			numericValues = append(numericValues, f)
			metric.DataPoints = append(metric.DataPoints, f)
		}
	}

	if len(numericValues) == 0 {
		metric.Value = 0
		return metric
	}

	metric.Count = len(numericValues)

	// 计算基本统计
	sort.Float64s(numericValues)
	metric.Min = numericValues[0]
	metric.Max = numericValues[len(numericValues)-1]

	sum := 0.0
	for _, v := range numericValues {
		sum += v
	}
	metric.Sum = sum
	metric.Avg = sum / float64(len(numericValues))

	// 计算百分位数
	metric.P50 = ma.calculatePercentile(numericValues, 50)
	metric.P95 = ma.calculatePercentile(numericValues, 95)
	metric.P99 = ma.calculatePercentile(numericValues, 99)

	// 根据聚合类型设置最终值
	switch aggType {
	case Sum:
		metric.Value = metric.Sum
	case Avg:
		metric.Value = metric.Avg
	case Min:
		metric.Value = metric.Min
	case Max:
		metric.Value = metric.Max
	case Count:
		metric.Value = float64(metric.Count)
	case Percentile:
		metric.Value = metric.P95 // 默认使用P95
	case Rate:
		// 速率计算需要时间窗口，这里简化处理
		metric.Value = metric.Avg
	default:
		metric.Value = metric.Avg
	}

	return metric
}

/**
 * 转换为float64
 */
func (ma *MetricsAggregator) toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

/**
 * 计算百分位数
 */
func (ma *MetricsAggregator) calculatePercentile(sortedValues []float64, percentile int) float64 {
	if len(sortedValues) == 0 {
		return 0
	}

	if len(sortedValues) == 1 {
		return sortedValues[0]
	}

	index := (float64(percentile) / 100.0) * float64(len(sortedValues)-1)
	lower := int(index)
	upper := lower + 1

	if upper >= len(sortedValues) {
		return sortedValues[len(sortedValues)-1]
	}

	weight := index - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}

/**
 * 获取聚合器状态
 */
func (ma *MetricsAggregator) GetStatus() map[string]interface{} {
	ma.mu.RLock()
	defer ma.mu.RUnlock()

	return map[string]interface{}{
		"name":              ma.name,
		"enabled":           ma.enabled,
		"data_sources":      len(ma.dataSources),
		"aggregation_rules": len(ma.aggregationRules),
		"cached_metrics":    len(ma.aggregatedMetrics),
		"cache_duration":    ma.cacheDuration.String(),
		"last_aggregation":  ma.lastAggregation,
	}
}

/**
 * 获取指标摘要
 */
func (ma *MetricsAggregator) GetMetricsSummary() map[string]interface{} {
	summary := map[string]interface{}{
		"total_metrics": len(ma.aggregatedMetrics),
		"metrics":       make([]map[string]interface{}, 0),
	}

	for name, metric := range ma.aggregatedMetrics {
		metricSummary := map[string]interface{}{
			"name":  name,
			"value": metric.Value,
			"count": metric.Count,
			"avg":   metric.Avg,
			"min":   metric.Min,
			"max":   metric.Max,
			"p95":   metric.P95,
			"p99":   metric.P99,
		}
		summary["metrics"] = append(summary["metrics"].([]map[string]interface{}), metricSummary)
	}

	return summary
}

/**
 * 重置聚合器
 */
func (ma *MetricsAggregator) Reset() {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	ma.aggregatedMetrics = make(map[string]AggregatedMetric)
	ma.lastAggregation = time.Now().Add(-time.Hour)

	LogInfo("指标聚合器已重置: %s", ma.name)
}

/**
 * 创建预定义的聚合规则
 */
func CreateDefaultAggregationRules() map[string]AggregationRule {
	rules := make(map[string]AggregationRule)

	// 查询性能聚合
	rules["query_performance"] = AggregationRule{
		MetricPattern: "*query*",
		Aggregation:   Avg,
		TimeWindow:    time.Minute,
		Enabled:       true,
	}

	// 连接池聚合
	rules["connection_pool"] = AggregationRule{
		MetricPattern: "*connection*",
		Aggregation:   Avg,
		TimeWindow:    time.Minute,
		Enabled:       true,
	}

	// 错误率聚合
	rules["error_rate"] = AggregationRule{
		MetricPattern: "*error*",
		Aggregation:   Sum,
		TimeWindow:    time.Minute,
		Enabled:       true,
	}

	// 健康状态聚合
	rules["health_status"] = AggregationRule{
		MetricPattern: "*health*",
		Aggregation:   Avg,
		TimeWindow:    time.Minute,
		Enabled:       true,
	}

	return rules
}

/**
 * 快速创建聚合器（带默认规则）
 */
func NewDefaultMetricsAggregator(name string) *MetricsAggregator {
	aggregator := NewMetricsAggregator(name)

	// 添加默认聚合规则
	defaultRules := CreateDefaultAggregationRules()
	for ruleName, rule := range defaultRules {
		aggregator.AddAggregationRule(ruleName, rule)
	}

	return aggregator
}
