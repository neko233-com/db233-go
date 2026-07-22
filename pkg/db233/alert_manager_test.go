package db233

import (
	"sync"
	"testing"
	"time"
)

type blockingAlertNotifier struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (n *blockingAlertNotifier) Notify(*Alert) error {
	n.once.Do(func() { close(n.started) })
	<-n.release
	return nil
}

func (*blockingAlertNotifier) GetName() string { return "blocking" }

func TestAlertManagerReplaceRuleAndStop(t *testing.T) {
	manager := NewAlertManager("test")
	rule := AlertRule{ID: "load", Metric: "cpu", Condition: GreaterThan, Threshold: 10, Enabled: true}
	manager.AddAlertRule(rule)
	rule.Threshold = 20
	manager.AddAlertRule(rule)
	if rules := manager.GetAlertRules(); len(rules) != 1 || rules[0].Threshold != 20 {
		t.Fatalf("规则替换失败: %#v", rules)
	}

	notifier := &blockingAlertNotifier{started: make(chan struct{}), release: make(chan struct{})}
	manager.AddNotifier(notifier)
	manager.CheckMetric("cpu", 30)
	select {
	case <-notifier.started:
	case <-time.After(time.Second):
		t.Fatal("通知器未启动")
	}

	stopped := make(chan struct{})
	go func() {
		manager.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop 未等待在途通知")
	case <-time.After(20 * time.Millisecond):
	}
	close(notifier.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop 未完成")
	}

	manager.Stop()
	manager.CheckMetric("cpu", 40)
}

func TestAlertManagerUsesDefaultCooldownAndDefensiveCopies(t *testing.T) {
	manager := NewAlertManager("production")
	manager.SetCooldownPeriod(time.Hour)
	threshold := map[string]any{"limit": 10}
	manager.AddAlertRule(AlertRule{ID: "copy", Metric: "copy", Threshold: threshold, Enabled: true})
	threshold["limit"] = 99
	if got := manager.GetAlertRules()[0].Threshold.(map[string]any)["limit"]; got != 10 {
		t.Fatalf("规则阈值被调用方修改: %v", got)
	}

	manager.AddAlertRule(AlertRule{
		ID:        "cpu",
		Name:      "cpu high",
		Metric:    "cpu",
		Condition: GreaterThan,
		Threshold: float64(0.5),
		Enabled:   true,
	})
	manager.CheckMetric("cpu", float64(0.9))
	manager.CheckMetric("cpu", float64(0.9))
	if got := len(manager.GetAlertHistory(0)); got != 1 {
		t.Fatalf("默认冷却期未抑制重复告警: history=%d", got)
	}

	value := map[string]any{"secret": "original"}
	manager.mu.Lock()
	manager.triggerAlert(&AlertRule{ID: "alias", Name: "alias", Threshold: map[string]any{"n": 1}}, "alias", value, time.Now())
	manager.mu.Unlock()
	value["secret"] = "mutated"
	alerts := manager.GetActiveAlerts()
	var stored *Alert
	for _, alert := range alerts {
		if alert.ID == "alias_alias" {
			stored = alert
			break
		}
	}
	if stored == nil || stored.Value.(map[string]any)["secret"] != "original" {
		t.Fatalf("告警值保留外部别名: %+v", stored)
	}
}

func TestAlertManagerMixedNumericTypesAndImmediateResolution(t *testing.T) {
	manager := NewAlertManager("numeric")
	manager.SetCooldownPeriod(time.Hour)
	manager.AddAlertRule(AlertRule{
		ID: "mixed", Metric: "load", Condition: GreaterThan, Threshold: float64(10.5), Enabled: true,
	})
	manager.CheckMetric("load", int64(11))
	if got := len(manager.GetActiveAlerts()); got != 1 {
		t.Fatalf("混合数值类型未触发告警: active=%d", got)
	}
	manager.CheckMetric("load", int64(1))
	if got := len(manager.GetActiveAlerts()); got != 0 {
		t.Fatalf("冷却期内恢复未立即解决告警: active=%d", got)
	}
	history := manager.GetAlertHistory(0)
	if len(history) != 1 || history[0].Status != Resolved || history[0].ResolvedAt == nil {
		t.Fatalf("解决历史错误: %+v", history)
	}

	manager.AddAlertRule(AlertRule{
		ID: "mismatch", Metric: "mismatch", Condition: Equal, Threshold: "10", Enabled: true,
	})
	manager.CheckMetric("mismatch", 10)
	if got := len(manager.GetActiveAlerts()); got != 0 {
		t.Fatalf("不可比较类型不应被视为相等: active=%d", got)
	}
}
