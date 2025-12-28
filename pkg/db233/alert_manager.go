package db233

import (
	"fmt"
	"sync"
	"time"
)

/**
 * AlertManager - 告警管理器
 *
 * 基于阈值的监控告警系统，支持多种告警类型和通知机制
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
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
	enabled  bool
	stopChan chan bool
}

/**
 * AlertRule - 告警规则
 */
type AlertRule struct {
	ID          string
	Name        string
	Description string
	Metric      string
	Condition   AlertCondition
	Threshold   interface{}
	Severity    AlertSeverity
	Cooldown    time.Duration
	Enabled     bool
}

/**
 * AlertCondition - 告警条件
 */
type AlertCondition int

const (
	GreaterThan AlertCondition = iota
	LessThan
	Equal
	NotEqual
	GreaterThanOrEqual
	LessThanOrEqual
)

/**
 * AlertSeverity - 告警严重程度
 */
type AlertSeverity int

const (
	Info AlertSeverity = iota
	Warning
	Error
	Critical
)

/**
 * Alert - 告警实例
 */
type Alert struct {
	ID          string
	RuleID      string
	Name        string
	Description string
	Severity    AlertSeverity
	Metric      string
	Value       interface{}
	Threshold   interface{}
	Condition   string
	Timestamp   time.Time
	Status      AlertStatus
	ResolvedAt  *time.Time
	Duration    *time.Duration
}

/**
 * AlertStatus - 告警状态
 */
type AlertStatus int

const (
	Active AlertStatus = iota
	Resolved
)

/**
 * AlertNotifier - 告警通知器接口
 */
type AlertNotifier interface {
	Notify(alert *Alert) error
	GetName() string
}

/**
 * 创建告警管理器
 */
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
		stopChan:       make(chan bool),
	}
}

/**
 * 添加告警规则
 */
func (am *AlertManager) AddAlertRule(rule AlertRule) {
	am.mu.Lock()
	defer am.mu.Unlock()

	// 检查规则ID是否已存在
	for _, existing := range am.alertRules {
		if existing.ID == rule.ID {
			LogWarn("告警规则ID已存在，将被替换: %s", rule.ID)
			am.RemoveAlertRule(rule.ID)
			break
		}
	}

	am.alertRules = append(am.alertRules, rule)
	LogInfo("告警规则已添加: %s (%s)", rule.Name, rule.ID)
}

/**
 * 移除告警规则
 */
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

/**
 * 添加通知器
 */
func (am *AlertManager) AddNotifier(notifier AlertNotifier) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.notifiers = append(am.notifiers, notifier)
	LogInfo("告警通知器已添加: %s -> %s", am.name, notifier.GetName())
}

/**
 * 设置最大历史记录大小
 */
func (am *AlertManager) SetMaxHistorySize(size int) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.maxHistorySize = size
}

/**
 * 设置冷却周期
 */
func (am *AlertManager) SetCooldownPeriod(period time.Duration) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.cooldownPeriod = period
}

/**
 * 启用告警管理器
 */
func (am *AlertManager) Enable() {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.enabled = true
	LogInfo("告警管理器已启用: %s", am.name)
}

/**
 * 禁用告警管理器
 */
func (am *AlertManager) Disable() {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.enabled = false
	LogInfo("告警管理器已禁用: %s", am.name)
}

/**
 * 检查指标并触发告警
 */
func (am *AlertManager) CheckMetric(metricName string, value interface{}) {
	if !am.enabled {
		return
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()

	for _, rule := range am.alertRules {
		if !rule.Enabled {
			continue
		}

		if rule.Metric != metricName {
			continue
		}

		// 检查是否在冷却期内
		alertID := fmt.Sprintf("%s_%s", rule.ID, metricName)
		if lastAlert, exists := am.activeAlerts[alertID]; exists {
			if now.Sub(lastAlert.Timestamp) < rule.Cooldown {
				continue // 在冷却期内，跳过
			}
		}

		// 评估条件
		if am.evaluateCondition(value, rule.Condition, rule.Threshold) {
			am.triggerAlert(&rule, metricName, value, now)
		} else {
			// 检查是否需要解决现有告警
			if activeAlert, exists := am.activeAlerts[alertID]; exists {
				am.resolveAlert(activeAlert, now)
			}
		}
	}
}

/**
 * 评估告警条件
 */
func (am *AlertManager) evaluateCondition(value interface{}, condition AlertCondition, threshold interface{}) bool {
	// 类型转换和比较
	switch condition {
	case GreaterThan:
		return am.compareValues(value, threshold) > 0
	case LessThan:
		return am.compareValues(value, threshold) < 0
	case Equal:
		return am.compareValues(value, threshold) == 0
	case NotEqual:
		return am.compareValues(value, threshold) != 0
	case GreaterThanOrEqual:
		return am.compareValues(value, threshold) >= 0
	case LessThanOrEqual:
		return am.compareValues(value, threshold) <= 0
	default:
		return false
	}
}

/**
 * 比较两个值
 */
func (am *AlertManager) compareValues(a, b interface{}) int {
	switch va := a.(type) {
	case int:
		if vb, ok := b.(int); ok {
			if va > vb {
				return 1
			} else if va < vb {
				return -1
			}
			return 0
		}
	case int64:
		if vb, ok := b.(int64); ok {
			if va > vb {
				return 1
			} else if va < vb {
				return -1
			}
			return 0
		}
	case float64:
		if vb, ok := b.(float64); ok {
			if va > vb {
				return 1
			} else if va < vb {
				return -1
			}
			return 0
		}
	case time.Duration:
		if vb, ok := b.(time.Duration); ok {
			if va > vb {
				return 1
			} else if va < vb {
				return -1
			}
			return 0
		}
	}
	return 0
}

/**
 * 触发告警
 */
func (am *AlertManager) triggerAlert(rule *AlertRule, metricName string, value interface{}, timestamp time.Time) {
	alertID := fmt.Sprintf("%s_%s", rule.ID, metricName)

	alert := &Alert{
		ID:          alertID,
		RuleID:      rule.ID,
		Name:        rule.Name,
		Description: rule.Description,
		Severity:    rule.Severity,
		Metric:      metricName,
		Value:       value,
		Threshold:   rule.Threshold,
		Condition:   am.conditionToString(rule.Condition),
		Timestamp:   timestamp,
		Status:      Active,
	}

	am.activeAlerts[alertID] = alert
	am.addToHistory(alert)

	// 发送通知
	for _, notifier := range am.notifiers {
		go func(notifier AlertNotifier, alert *Alert) {
			if err := notifier.Notify(alert); err != nil {
				LogError("告警通知失败 [%s]: %v", notifier.GetName(), err)
			}
		}(notifier, alert)
	}

	LogWarn("告警触发: %s - %s (值: %v, 阈值: %v)", alert.Name, alert.Metric, alert.Value, alert.Threshold)
}

/**
 * 解决告警
 */
func (am *AlertManager) resolveAlert(alert *Alert, resolvedAt time.Time) {
	alert.Status = Resolved
	alert.ResolvedAt = &resolvedAt

	duration := resolvedAt.Sub(alert.Timestamp)
	alert.Duration = &duration

	delete(am.activeAlerts, alert.ID)

	LogInfo("告警已解决: %s - 持续时间: %v", alert.Name, duration)
}

/**
 * 将告警添加到历史记录
 */
func (am *AlertManager) addToHistory(alert *Alert) {
	am.alertHistory = append(am.alertHistory, alert)

	// 限制历史记录大小
	if len(am.alertHistory) > am.maxHistorySize {
		am.alertHistory = am.alertHistory[len(am.alertHistory)-am.maxHistorySize:]
	}
}

/**
 * 条件转换为字符串
 */
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

/**
 * 获取活跃告警
 */
func (am *AlertManager) GetActiveAlerts() []*Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	alerts := make([]*Alert, 0, len(am.activeAlerts))
	for _, alert := range am.activeAlerts {
		alerts = append(alerts, alert)
	}

	return alerts
}

/**
 * 获取告警历史
 */
func (am *AlertManager) GetAlertHistory(limit int) []*Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if limit <= 0 || limit > len(am.alertHistory) {
		limit = len(am.alertHistory)
	}

	history := make([]*Alert, limit)
	copy(history, am.alertHistory[len(am.alertHistory)-limit:])

	return history
}

/**
 * 获取告警统计
 */
func (am *AlertManager) GetAlertStats() map[string]interface{} {
	am.mu.RLock()
	defer am.mu.RUnlock()

	stats := map[string]interface{}{
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

/**
 * 严重程度转换为字符串
 */
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

/**
 * 获取告警规则
 */
func (am *AlertManager) GetAlertRules() []AlertRule {
	am.mu.RLock()
	defer am.mu.RUnlock()

	rules := make([]AlertRule, len(am.alertRules))
	copy(rules, am.alertRules)

	return rules
}

/**
 * 停止告警管理器
 */
func (am *AlertManager) Stop() {
	am.stopChan <- true
}

/**
 * 获取管理器状态
 */
func (am *AlertManager) GetStatus() map[string]interface{} {
	am.mu.RLock()
	defer am.mu.RUnlock()

	return map[string]interface{}{
		"name":            am.name,
		"enabled":         am.enabled,
		"rules_count":     len(am.alertRules),
		"active_alerts":   len(am.activeAlerts),
		"history_size":    len(am.alertHistory),
		"max_history":     am.maxHistorySize,
		"cooldown_period": am.cooldownPeriod.String(),
		"notifiers":       len(am.notifiers),
	}
}

/**
 * 获取指标数据（实现MetricsDataSource接口）
 */
func (am *AlertManager) GetMetrics() map[string]interface{} {
	stats := am.GetAlertStats()

	metrics := make(map[string]interface{})

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

/**
 * 获取数据源名称
 */
func (am *AlertManager) GetName() string {
	return fmt.Sprintf("alert_manager_%s", am.name)
}

/**
 * 日志通知器 - 简单的日志通知器实现
 */
type LogAlertNotifier struct {
	name string
}

/**
 * 创建日志通知器
 */
func NewLogAlertNotifier(name string) *LogAlertNotifier {
	return &LogAlertNotifier{name: name}
}

/**
 * 发送通知
 */
func (n *LogAlertNotifier) Notify(alert *Alert) error {
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

	LogWarn("[%s] 告警通知 [%s]: %s - %s (值: %v)",
		n.name, severity, alert.Name, alert.Description, alert.Value)

	return nil
}

/**
 * 获取通知器名称
 */
func (n *LogAlertNotifier) GetName() string {
	return n.name
}
