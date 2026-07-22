package db233

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// MetricsCollector - 监控数据收集器
// 收集和存储历史监控数据，支持趋势分析和数据导出
type MetricsCollector struct {
	name string

	// 数据存储
	metricsData map[string][]MetricPoint
	maxPoints   int

	// 收集间隔
	collectionInterval time.Duration

	// 数据源
	dataSources []MetricsDataSource

	// 锁
	mu sync.RWMutex

	// 控制
	enabled       bool
	running       bool
	stopCh        chan struct{}
	doneCh        chan struct{}
	reconfigureCh chan struct{}
	lastUpdate    time.Time
}

// MetricPoint - 监控数据点
type MetricPoint struct {
	Timestamp time.Time
	Name      string
	Value     any
	Tags      map[string]string
}

// MetricsDataSource - 监控数据源接口
type MetricsDataSource interface {
	GetMetrics() map[string]any
	GetName() string
}

// 创建监控数据收集器
func NewMetricsCollector(name string) *MetricsCollector {
	return &MetricsCollector{
		name:               name,
		metricsData:        make(map[string][]MetricPoint),
		maxPoints:          1000, // 默认保留1000个数据点
		collectionInterval: 30 * time.Second,
		dataSources:        make([]MetricsDataSource, 0),
		enabled:            true,
		lastUpdate:         time.Now(),
	}
}

// 添加数据源
func (mc *MetricsCollector) AddDataSource(source MetricsDataSource) {
	if source == nil {
		LogWarn("忽略空监控数据源: %s", mc.name)
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.dataSources = append(mc.dataSources, source)
	LogInfo("数据源已添加: %s -> %s", mc.name, source.GetName())
}

// 设置最大数据点数量
func (mc *MetricsCollector) SetMaxPoints(maxPoints int) {
	if maxPoints <= 0 {
		LogWarn("监控数据点上限必须大于 0: %d", maxPoints)
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.maxPoints = maxPoints
	for name, points := range mc.metricsData {
		if len(points) <= maxPoints {
			continue
		}
		trimmed := make([]MetricPoint, maxPoints)
		copy(trimmed, points[len(points)-maxPoints:])
		mc.metricsData[name] = trimmed
	}
}

// 设置收集间隔
func (mc *MetricsCollector) SetCollectionInterval(interval time.Duration) {
	if interval <= 0 {
		LogWarn("监控数据收集间隔必须大于 0: %v", interval)
		return
	}
	mc.mu.Lock()
	mc.collectionInterval = interval
	reconfigureCh := mc.reconfigureCh
	running := mc.running
	mc.mu.Unlock()
	if running && reconfigureCh != nil {
		select {
		case reconfigureCh <- struct{}{}:
		default:
		}
	}
}

// 启用收集器
func (mc *MetricsCollector) Enable() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.enabled = true
	LogInfo("监控数据收集器已启用: %s", mc.name)
}

// 禁用收集器
func (mc *MetricsCollector) Disable() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.enabled = false
	LogInfo("监控数据收集器已禁用: %s", mc.name)
}

// 启动数据收集
func (mc *MetricsCollector) Start() {
	mc.mu.Lock()
	if mc.running {
		mc.mu.Unlock()
		return
	}
	interval := mc.collectionInterval
	if interval <= 0 {
		interval = 30 * time.Second
		mc.collectionInterval = interval
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	reconfigureCh := make(chan struct{}, 1)
	mc.running = true
	mc.stopCh = stopCh
	mc.doneCh = doneCh
	mc.reconfigureCh = reconfigureCh
	mc.mu.Unlock()

	LogInfo("监控数据收集器启动: %s, 间隔: %v", mc.name, interval)
	go mc.run(stopCh, doneCh, reconfigureCh, interval)
}

// 停止数据收集
func (mc *MetricsCollector) Stop() {
	mc.mu.Lock()
	if !mc.running {
		mc.mu.Unlock()
		return
	}
	doneCh := mc.doneCh
	if mc.stopCh != nil {
		close(mc.stopCh)
		mc.stopCh = nil
	}
	mc.mu.Unlock()

	<-doneCh
	mc.mu.Lock()
	if mc.doneCh == doneCh {
		mc.running = false
		mc.doneCh = nil
		mc.reconfigureCh = nil
	}
	mc.mu.Unlock()
	LogInfo("监控数据收集器停止: %s", mc.name)
}

func (mc *MetricsCollector) run(stopCh <-chan struct{}, doneCh chan<- struct{}, reconfigureCh <-chan struct{}, interval time.Duration) {
	defer close(doneCh)
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			mc.collectMetrics()
			mc.mu.RLock()
			interval = mc.collectionInterval
			mc.mu.RUnlock()
			timer.Reset(interval)
		case <-reconfigureCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			mc.mu.RLock()
			interval = mc.collectionInterval
			mc.mu.RUnlock()
			timer.Reset(interval)
		case <-stopCh:
			return
		}
	}
}

// 收集监控数据
func (mc *MetricsCollector) collectMetrics() {
	mc.mu.RLock()
	if !mc.enabled {
		mc.mu.RUnlock()
		return
	}
	sources := append([]MetricsDataSource(nil), mc.dataSources...)
	mc.mu.RUnlock()

	now := time.Now()

	// 从所有数据源收集数据
	for _, source := range sources {
		metrics, sourceName, ok := readMetricsSource(source)
		if !ok {
			continue
		}

		mc.mu.Lock()
		if !mc.enabled {
			mc.mu.Unlock()
			return
		}
		mc.lastUpdate = now
		maxPoints := mc.maxPoints

		for metricName, value := range metrics {
			fullName := fmt.Sprintf("%s.%s", sourceName, metricName)

			point := MetricPoint{
				Timestamp: now,
				Name:      fullName,
				Value:     value,
				Tags: map[string]string{
					"source": sourceName,
					"metric": metricName,
				},
			}

			// 添加到数据存储
			if _, exists := mc.metricsData[fullName]; !exists {
				mc.metricsData[fullName] = make([]MetricPoint, 0)
			}

			mc.metricsData[fullName] = append(mc.metricsData[fullName], point)

			// 限制数据点数量
			if len(mc.metricsData[fullName]) > mc.maxPoints {
				points := mc.metricsData[fullName]
				overflow := len(points) - maxPoints
				copy(points, points[overflow:])
				mc.metricsData[fullName] = points[:maxPoints]
			}
		}
		mc.mu.Unlock()
	}
}

func readMetricsSource(source MetricsDataSource) (metrics map[string]any, name string, ok bool) {
	ok = true
	defer func() {
		if recovered := recover(); recovered != nil {
			LogError("监控数据源采集异常: %s", safeValueForLog(recovered))
			metrics = nil
			name = ""
			ok = false
		}
	}()
	return source.GetMetrics(), source.GetName(), true
}

// 获取指定指标的历史数据
func (mc *MetricsCollector) GetMetricHistory(metricName string, duration time.Duration) []MetricPoint {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	points, exists := mc.metricsData[metricName]
	if !exists {
		return []MetricPoint{}
	}

	cutoff := time.Now().Add(-duration)
	result := make([]MetricPoint, 0)

	for _, point := range points {
		if point.Timestamp.After(cutoff) {
			result = append(result, cloneMetricPoint(point))
		}
	}

	return result
}

// 获取所有指标名称
func (mc *MetricsCollector) GetMetricNames() []string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	names := make([]string, 0, len(mc.metricsData))
	for name := range mc.metricsData {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// 获取最新数据点
func (mc *MetricsCollector) GetLatestMetrics() map[string]MetricPoint {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make(map[string]MetricPoint)

	for name, points := range mc.metricsData {
		if len(points) > 0 {
			latest := points[len(points)-1]
			result[name] = cloneMetricPoint(latest)
		}
	}

	return result
}

// 计算指标统计信息
func (mc *MetricsCollector) GetMetricStats(metricName string, duration time.Duration) map[string]any {
	points := mc.GetMetricHistory(metricName, duration)

	if len(points) == 0 {
		return map[string]any{
			"metric":    metricName,
			"count":     0,
			"available": false,
		}
	}

	stats := map[string]any{
		"metric":     metricName,
		"count":      len(points),
		"available":  true,
		"start_time": points[0].Timestamp,
		"end_time":   points[len(points)-1].Timestamp,
	}

	// 计算数值统计（如果是数值类型）
	values := make([]float64, 0)
	for _, point := range points {
		if val, ok := point.Value.(float64); ok {
			values = append(values, val)
		} else if val, ok := point.Value.(int64); ok {
			values = append(values, float64(val))
		}
	}

	if len(values) > 0 {
		min, max, avg, p95, p99 := mc.calculateStats(values)
		stats["min"] = min
		stats["max"] = max
		stats["avg"] = avg
		stats["p95"] = p95
		stats["p99"] = p99
	}

	return stats
}

// 计算数值统计
func (mc *MetricsCollector) calculateStats(values []float64) (min, max, avg, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0, 0, 0
	}

	min = values[0]
	max = values[0]
	sum := 0.0

	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}

	avg = sum / float64(len(values))

	// 计算百分位数
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	p95Index := int(float64(len(sorted)) * 0.95)
	p99Index := int(float64(len(sorted)) * 0.99)

	if p95Index < len(sorted) {
		p95 = sorted[p95Index]
	}
	if p99Index < len(sorted) {
		p99 = sorted[p99Index]
	}

	return min, max, avg, p95, p99
}

// 导出数据到文件
func (mc *MetricsCollector) ExportToFile(filename string) error {
	mc.mu.RLock()
	metrics := make(map[string][]MetricPoint, len(mc.metricsData))
	for name, points := range mc.metricsData {
		clones := make([]MetricPoint, len(points))
		for index, point := range points {
			clones[index] = cloneMetricPoint(point)
		}
		metrics[name] = clones
	}
	data := map[string]any{
		"collector":    mc.name,
		"export_time":  time.Now(),
		"last_update":  mc.lastUpdate,
		"metrics":      metrics,
		"data_sources": len(mc.dataSources),
	}
	mc.mu.RUnlock()

	encoded, err := marshalSecureExportJSON(data)
	if err != nil {
		return fmt.Errorf("序列化监控数据失败: %w", err)
	}
	if err := writeSecureAtomicFile(filename, encoded); err != nil {
		return fmt.Errorf("导出数据失败: %w", err)
	}

	LogInfo("监控数据已安全导出到文件")
	return nil
}

// 从文件导入数据
func (mc *MetricsCollector) ImportFromFile(filename string) (returnErr error) {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("打开导入文件失败: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
	}()

	var data map[string]any
	decoder := json.NewDecoder(file)

	if err := decoder.Decode(&data); err != nil {
		return fmt.Errorf("解析导入数据失败: %w", err)
	}

	imported := make(map[string][]MetricPoint)
	// 解析指标数据
	if metricsData, ok := data["metrics"].(map[string]any); ok {
		for name, pointsData := range metricsData {
			if pointsArray, ok := pointsData.([]any); ok {
				points := make([]MetricPoint, 0, len(pointsArray))
				for _, pointData := range pointsArray {
					if pointMap, ok := pointData.(map[string]any); ok {
						point := MetricPoint{
							Name: name,
							Tags: make(map[string]string),
						}

						if ts, ok := pointMap["Timestamp"].(string); ok {
							if t, err := time.Parse(time.RFC3339, ts); err == nil {
								point.Timestamp = t
							}
						}

						point.Value = pointMap["Value"]

						if tags, ok := pointMap["Tags"].(map[string]any); ok {
							for k, v := range tags {
								if str, ok := v.(string); ok {
									point.Tags[k] = str
								}
							}
						}

						points = append(points, point)
					}
				}
				imported[name] = points
			}
		}
	}
	mc.mu.Lock()
	for name, points := range imported {
		if len(points) > mc.maxPoints {
			points = append([]MetricPoint(nil), points[len(points)-mc.maxPoints:]...)
		}
		mc.metricsData[name] = points
	}
	mc.mu.Unlock()

	LogInfo("监控数据已从文件导入: %s", filename)
	return nil
}

// 清理过期数据
func (mc *MetricsCollector) CleanupExpiredData(maxAge time.Duration) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for name, points := range mc.metricsData {
		validPoints := make([]MetricPoint, 0)
		for _, point := range points {
			if point.Timestamp.After(cutoff) {
				validPoints = append(validPoints, point)
			} else {
				removed++
			}
		}
		if len(validPoints) == 0 {
			delete(mc.metricsData, name)
		} else {
			mc.metricsData[name] = validPoints
		}
	}

	if removed > 0 {
		LogInfo("已清理过期监控数据: %d 个数据点", removed)
	}
}

func cloneMetricPoint(point MetricPoint) MetricPoint {
	point.Value = cloneDashboardComponent(point.Value)
	if point.Tags != nil {
		tags := make(map[string]string, len(point.Tags))
		for key, value := range point.Tags {
			tags[key] = value
		}
		point.Tags = tags
	}
	return point
}

// 获取收集器状态
func (mc *MetricsCollector) GetStatus() map[string]any {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	totalPoints := 0
	for _, points := range mc.metricsData {
		totalPoints += len(points)
	}

	return map[string]any{
		"name":                mc.name,
		"enabled":             mc.enabled,
		"data_sources":        len(mc.dataSources),
		"metrics_count":       len(mc.metricsData),
		"total_data_points":   totalPoints,
		"max_points":          mc.maxPoints,
		"collection_interval": mc.collectionInterval.String(),
		"last_update":         mc.lastUpdate,
		"running":             mc.running,
	}
}

// 重置收集器
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.metricsData = make(map[string][]MetricPoint)
	mc.lastUpdate = time.Now()

	LogInfo("监控数据收集器已重置: %s", mc.name)
}
