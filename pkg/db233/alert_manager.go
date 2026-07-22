package db233

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sync"
	"time"
)

// AlertManager - 告警管理器
// 基于阈值的监控告警系统，支持多种告警类型和通知机制
type AlertManager struct {
	name string

	// 告警规则
	alertRules []AlertRule

	// 活跃告警
	activeAlerts map[string]*Alert

	// 告警历史
	alertHistory []*Alert

	// 通知器
	notifiers []AlertNotifier

	// 配置
	maxHistorySize int
	cooldownPeriod time.Duration

	// 锁
	mu sync.RWMutex

	// 控制
	enabled    bool
	stopped    bool
	notifierWG sync.WaitGroup
}

// AlertRule - 告警规则
type AlertRule struct {
	ID          string
	Name        string
	Description string
	Metric      string
	Condition   AlertCondition
	Threshold   any
	Severity    AlertSeverity
	Cooldown    time.Duration
	Enabled     bool
}

// AlertCondition - 告警条件
type AlertCondition int

const (
	GreaterThan AlertCondition = iota
	LessThan
	Equal
	NotEqual
	GreaterThanOrEqual
	LessThanOrEqual
)

// AlertSeverity - 告警严重程度
type AlertSeverity int

const (
	Info AlertSeverity = iota
	Warning
	Error
	Critical
)

// Alert - 告警实例
type Alert struct {
	ID          string
	RuleID      string
	Name        string
	Description string
	Severity    AlertSeverity
	Metric      string
	Value       any
	Threshold   any
	Condition   string
	Timestamp   time.Time
	Status      AlertStatus
	ResolvedAt  *time.Time
	Duration    *time.Duration
}

// AlertStatus - 告警状态
type AlertStatus int

const (
	Active AlertStatus = iota
	Resolved
)

// AlertNotifier - 告警通知器接口
type AlertNotifier interface {
	Notify(alert *Alert) error
	GetName() string
}

// 创建告警管理器
func NewAlertManager(name string) *AlertManager {
	return &AlertManager{
		name:           name,
		alertRules:     make([]AlertRule, 0),
		activeAlerts:   make(map[string]*Alert),
		alertHistory:   make([]*Alert, 0),
		notifiers:      make([]AlertNotifier, 0),
		maxHistorySize: 1000,
		cooldownPeriod: 5 * time.Minute,
		enabled:        true,
	}
}

// 添加告警规则
func (am *AlertManager) AddAlertRule(rule AlertRule) {
	rule.Threshold = cloneDashboardComponent(rule.Threshold)
	am.mu.Lock()
	defer am.mu.Unlock()

	// 锁内直接替换，避免调用 RemoveAlertRule 造成非重入锁死锁。
	for index, existing := range am.alertRules {
		if existing.ID == rule.ID {
			am.alertRules[index] = rule
			LogWarn("告警规则ID已存在，已替换: %s", rule.ID)
			return
		}
	}

	am.alertRules = append(am.alertRules, rule)
	LogInfo("告警规则已添加: %s (%s)", rule.Name, rule.ID)
}

// 移除告警规则
func (am *AlertManager) RemoveAlertRule(ruleID string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	for i, rule := range am.alertRules {
		if rule.ID == ruleID {
			am.alertRules = append(am.alertRules[:i], am.alertRules[i+1:]...)
			LogInfo("告警规则已移除: %s", ruleID)
			break
		}
	}
}

// 添加通知器
func (am *AlertManager) AddNotifier(notifier AlertNotifier) {
	if notifier == nil {
		return
	}
	name := safeAlertNotifierName(notifier)
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.stopped {
		return
	}
	am.notifiers = append(am.notifiers, notifier)
	LogInfo("告警通知器已添加: %s -> %s", am.name, name)
}

// 设置最大历史记录大小
func (am *AlertManager) SetMaxHistorySize(size int) {
	if size <= 0 {
		LogWarn("告警历史上限必须大于 0: %d", size)
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	am.maxHistorySize = size
	if len(am.alertHistory) > size {
		trimmed := make([]*Alert, size)
		copy(trimmed, am.alertHistory[len(am.alertHistory)-size:])
		am.alertHistory = trimmed
	}
}

// 设置冷却周期
func (am *AlertManager) SetCooldownPeriod(period time.Duration) {
	if period < 0 {
		LogWarn("告警冷却周期不能为负数: %v", period)
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	am.cooldownPeriod = period
}

// 启用告警管理器
func (am *AlertManager) Enable() {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.stopped {
		return
	}
	am.enabled = true
	LogInfo("告警管理器已启用: %s", am.name)
}

// 禁用告警管理器
func (am *AlertManager) Disable() {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.enabled = false
	LogInfo("告警管理器已禁用: %s", am.name)
}

// 检查指标并触发告警
func (am *AlertManager) CheckMetric(metricName string, value any) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if !am.enabled || am.stopped {
		return
	}

	now := time.Now()

	for _, rule := range am.alertRules {
		if !rule.Enabled {
			continue
		}

		if rule.Metric != metricName {
			continue
		}

		alertID := fmt.Sprintf("%s_%s", rule.ID, metricName)
		activeAlert, active := am.activeAlerts[alertID]
		conditionMatched := am.evaluateCondition(value, rule.Condition, rule.Threshold)
		if !conditionMatched {
			// 解决告警不受通知冷却期影响。
			if active {
				am.resolveAlert(activeAlert, now)
			}
			continue
		}
		if active {
			cooldown := rule.Cooldown
			if cooldown <= 0 {
				cooldown = am.cooldownPeriod
			}
			if now.Sub(activeAlert.Timestamp) < cooldown {
				continue // 在冷却期内，跳过
			}
			// 冷却结束后重复触发前先结束旧实例，避免历史中遗留永久 Active。
			am.resolveAlert(activeAlert, now)
		}
		am.triggerAlert(&rule, metricName, value, now)
	}
}

// 评估告警条件
func (am *AlertManager) evaluateCondition(value any, condition AlertCondition, threshold any) bool {
	comparison, comparable := am.compareValues(value, threshold)
	if !comparable {
		return condition == NotEqual
	}
	switch condition {
	case GreaterThan:
		return comparison > 0
	case LessThan:
		return comparison < 0
	case Equal:
		return comparison == 0
	case NotEqual:
		return comparison != 0
	case GreaterThanOrEqual:
		return comparison >= 0
	case LessThanOrEqual:
		return comparison <= 0
	default:
		return false
	}
}

// 比较两个值
func (am *AlertManager) compareValues(a, b any) (int, bool) {
	if left, leftInfinity, leftOK := alertNumericValue(a); leftOK {
		if right, rightInfinity, rightOK := alertNumericValue(b); rightOK {
			if leftInfinity != 0 || rightInfinity != 0 {
				switch {
				case leftInfinity < rightInfinity:
					return -1, true
				case leftInfinity > rightInfinity:
					return 1, true
				default:
					return 0, true
				}
			}
			return left.Cmp(right), true
		}
	}
	leftValue := reflect.ValueOf(a)
	rightValue := reflect.ValueOf(b)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return 0, false
	}
	switch leftValue.Kind() {
	case reflect.String:
		if rightValue.Kind() != reflect.String {
			return 0, false
		}
		left, right := leftValue.String(), rightValue.String()
		if left < right {
			return -1, true
		}
		if left > right {
			return 1, true
		}
		return 0, true
	case reflect.Bool:
		if rightValue.Kind() != reflect.Bool {
			return 0, false
		}
		left, right := leftValue.Bool(), rightValue.Bool()
		if left == right {
			return 0, true
		}
		if !left {
			return -1, true
		}
		return 1, true
	default:
		return 0, false
	}
}

func alertNumericValue(value any) (*big.Rat, int, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil, 0, false
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return new(big.Rat).SetInt64(reflected.Int()), 0, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		integer := new(big.Int).SetUint64(reflected.Uint())
		return new(big.Rat).SetInt(integer), 0, true
	case reflect.Float32, reflect.Float64:
		floating := reflected.Float()
		if math.IsNaN(floating) {
			return nil, 0, false
		}
		if math.IsInf(floating, -1) {
			return nil, -1, true
		}
		if math.IsInf(floating, 1) {
			return nil, 1, true
		}
		return new(big.Rat).SetFloat64(floating), 0, true
	default:
		return nil, 0, false
	}
}

// 触发告警
func (am *AlertManager) triggerAlert(rule *AlertRule, metricName string, value any, timestamp time.Time) {
	alertID := fmt.Sprintf("%s_%s", rule.ID, metricName)

	alert := &Alert{
		ID:          alertID,
		RuleID:      rule.ID,
		Name:        rule.Name,
		Description: rule.Description,
		Severity:    rule.Severity,
		Metric:      metricName,
		Value:       cloneDashboardComponent(value),
		Threshold:   cloneDashboardComponent(rule.Threshold),
		Condition:   am.conditionToString(rule.Condition),
		Timestamp:   timestamp,
		Status:      Active,
	}

	am.activeAlerts[alertID] = alert
	am.addToHistory(alert)

	// 发送通知
	for _, notifier := range am.notifiers {
		am.notifierWG.Add(1)
		notification := cloneAlert(alert)
		notifierName := safeAlertNotifierName(notifier)
		go func(notifier AlertNotifier, notifierName string, alert *Alert) {
			defer am.notifierWG.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					LogError("告警通知发生 panic [%s]: %s", notifierName, safeValueForLog(recovered))
				}
			}()
			if err := notifier.Notify(alert); err != nil {
				LogError("告警通知失败 [%s]: %s", safeValueForLog(notifierName), safeErrorForLog(err))
			}
		}(notifier, notifierName, notification)
	}

	LogWarn("告警触发: %s - %s (值=%s, 阈值=%s)", alert.Name, alert.Metric, safeValueForLog(alert.Value), safeValueForLog(alert.Threshold))
}

// 解决告警
func (am *AlertManager) resolveAlert(alert *Alert, resolvedAt time.Time) {
	alert.Status = Resolved
	alert.ResolvedAt = &resolvedAt

	duration := resolvedAt.Sub(alert.Timestamp)
	alert.Duration = &duration

	delete(am.activeAlerts, alert.ID)

	LogInfo("告警已解决: %s - 持续时间: %v", alert.Name, duration)
}

// 将告警添加到历史记录
func (am *AlertManager) addToHistory(alert *Alert) {
	am.alertHistory = append(am.alertHistory, alert)

	// 限制历史记录大小
	if len(am.alertHistory) > am.maxHistorySize {
		trimmed := make([]*Alert, am.maxHistorySize)
		copy(trimmed, am.alertHistory[len(am.alertHistory)-am.maxHistorySize:])
		am.alertHistory = trimmed
	}
}

// 条件转换为字符串
func (am *AlertManager) conditionToString(condition AlertCondition) string {
	switch condition {
	case GreaterThan:
		return ">"
	case LessThan:
		return "<"
	case Equal:
		return "=="
	case NotEqual:
		return "!="
	case GreaterThanOrEqual:
		return ">="
	case LessThanOrEqual:
		return "<="
	default:
		return "unknown"
	}
}

// 获取活跃告警
func (am *AlertManager) GetActiveAlerts() []*Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	alerts := make([]*Alert, 0, len(am.activeAlerts))
	for _, alert := range am.activeAlerts {
		alerts = append(alerts, cloneAlert(alert))
	}

	return alerts
}

// 获取告警历史
func (am *AlertManager) GetAlertHistory(limit int) []*Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if limit <= 0 || limit > len(am.alertHistory) {
		limit = len(am.alertHistory)
	}

	history := make([]*Alert, limit)
	for index, alert := range am.alertHistory[len(am.alertHistory)-limit:] {
		history[index] = cloneAlert(alert)
	}

	return history
}

// 获取告警统计
func (am *AlertManager) GetAlertStats() map[string]any {
	am.mu.RLock()
	defer am.mu.RUnlock()

	stats := map[string]any{
		"active_alerts": len(am.activeAlerts),
		"total_history": len(am.alertHistory),
		"rules_count":   len(am.alertRules),
		"notifiers":     len(am.notifiers),
	}

	// 按严重程度统计
	severityCount := make(map[string]int)
	for _, alert := range am.activeAlerts {
		severity := am.severityToString(alert.Severity)
		severityCount[severity]++
	}
	stats["active_by_severity"] = severityCount

	return stats
}

// 严重程度转换为字符串
func (am *AlertManager) severityToString(severity AlertSeverity) string {
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

// 获取告警规则
func (am *AlertManager) GetAlertRules() []AlertRule {
	am.mu.RLock()
	defer am.mu.RUnlock()

	rules := make([]AlertRule, len(am.alertRules))
	copy(rules, am.alertRules)
	for index := range rules {
		rules[index].Threshold = cloneDashboardComponent(rules[index].Threshold)
	}

	return rules
}

func cloneAlert(alert *Alert) *Alert {
	if alert == nil {
		return nil
	}
	clone := *alert
	clone.Value = cloneDashboardComponent(alert.Value)
	clone.Threshold = cloneDashboardComponent(alert.Threshold)
	if alert.ResolvedAt != nil {
		resolvedAt := *alert.ResolvedAt
		clone.ResolvedAt = &resolvedAt
	}
	if alert.Duration != nil {
		duration := *alert.Duration
		clone.Duration = &duration
	}
	return &clone
}

// Stop 禁止新告警并等待已启动通知完成。可并发、重复调用。
func (am *AlertManager) Stop() {
	am.mu.Lock()
	am.stopped = true
	am.enabled = false
	am.mu.Unlock()
	am.notifierWG.Wait()
}

// 获取管理器状态
func (am *AlertManager) GetStatus() map[string]any {
	am.mu.RLock()
	defer am.mu.RUnlock()

	return map[string]any{
		"name":            am.name,
		"enabled":         am.enabled,
		"stopped":         am.stopped,
		"rules_count":     len(am.alertRules),
		"active_alerts":   len(am.activeAlerts),
		"history_size":    len(am.alertHistory),
		"max_history":     am.maxHistorySize,
		"cooldown_period": am.cooldownPeriod.String(),
		"notifiers":       len(am.notifiers),
	}
}

// 获取指标数据（实现MetricsDataSource接口）
func (am *AlertManager) GetMetrics() map[string]any {
	stats := am.GetAlertStats()

	metrics := make(map[string]any)

	// 告警数量指标
	if val, ok := stats["active_alerts"].(int); ok {
		metrics["active_alerts"] = val
	}

	// 告警规则数量
	if val, ok := stats["rules_count"].(int); ok {
		metrics["alert_rules_count"] = val
	}

	// 按严重程度统计
	if severityStats, ok := stats["active_by_severity"].(map[string]int); ok {
		for severity, count := range severityStats {
			metrics[fmt.Sprintf("active_alerts_%s", severity)] = count
		}
	}

	// 历史告警数量
	if val, ok := stats["total_history"].(int); ok {
		metrics["total_alerts_history"] = val
	}

	return metrics
}

// 获取数据源名称
func (am *AlertManager) GetName() string {
	return fmt.Sprintf("alert_manager_%s", am.name)
}

// 日志通知器 - 简单的日志通知器实现
type LogAlertNotifier struct {
	name string
}

// 创建日志通知器
func NewLogAlertNotifier(name string) *LogAlertNotifier {
	return &LogAlertNotifier{name: name}
}

// 发送通知
func (n *LogAlertNotifier) Notify(alert *Alert) error {
	if alert == nil {
		return NewValidationException("告警不能为空")
	}
	severity := ""
	switch alert.Severity {
	case Info:
		severity = "INFO"
	case Warning:
		severity = "WARN"
	case Error:
		severity = "ERROR"
	case Critical:
		severity = "CRITICAL"
	}

	LogWarn("[%s] 告警通知 [%s]: %s (值=%s)",
		n.name, severity, alert.Name, safeValueForLog(alert.Value))

	return nil
}

// 获取通知器名称
func (n *LogAlertNotifier) GetName() string {
	return n.name
}

func safeAlertNotifierName(notifier AlertNotifier) (name string) {
	name = fmt.Sprintf("%T", notifier)
	defer func() {
		if recover() != nil {
			name = fmt.Sprintf("%T", notifier)
		}
	}()
	if configured := notifier.GetName(); configured != "" {
		name = configured
	}
	return name
}
