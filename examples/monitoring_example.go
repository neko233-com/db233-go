package main

import (
	"fmt"
	"log"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

/**
 * 监控系统完整示例
 *
 * 展示如何使用所有监控组件创建完整的监控系统
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
func main() {
	fmt.Println("=== db233-go 监控系统完整示例 ===")

	// 1. 初始化数据库管理器
	dbManager := db233.NewDbManager("example_db")

	// 配置数据库连接
	config := &db233.DbConfig{
		Host:         "localhost",
		Port:         3306,
		Database:     "test_db",
		Username:     "root",
		Password:     "password",
		MaxOpenConns: 10,
		MaxIdleConns: 5,
	}

	if err := dbManager.AddDataSource("main_db", config); err != nil {
		log.Fatalf("添加数据源失败: %v", err)
	}

	// 2. 创建监控组件
	fmt.Println("创建监控组件...")

	// 性能监控器
	perfMonitor := db233.NewPerformanceMonitor("main_db_perf", 1000)
	perfMonitor.SetSlowQueryThreshold(time.Second)

	// 连接池监控器
	connMonitor := db233.NewConnectionPoolMonitor("main_db_conn", dbManager.GetDataSource("main_db"))

	// 健康检查器
	healthChecker := db233.NewHealthChecker("main_db_health", dbManager.GetDataSource("main_db"))
	healthChecker.AddCheck("connectivity", db233.HealthCheckConnectivity)
	healthChecker.AddCheck("query_test", db233.HealthCheckQueryTest)

	// 告警管理器
	alertManager := db233.NewAlertManager("main_db_alerts")

	// 配置告警规则
	alertManager.AddRule(&db233.AlertRule{
		Name:        "high_error_rate",
		Description: "错误率过高",
		Severity:    db233.Warning,
		Condition: func(metrics map[string]interface{}) bool {
			if errorRate, ok := metrics["error_rate"].(float64); ok {
				return errorRate > 0.1 // 10%
			}
			return false
		},
		Cooldown: time.Minute * 5,
	})

	alertManager.AddRule(&db233.AlertRule{
		Name:        "slow_response",
		Description: "响应时间过慢",
		Severity:    db233.Error,
		Condition: func(metrics map[string]interface{}) bool {
			if avgTime, ok := metrics["avg_response_time"].(time.Duration); ok {
				return avgTime > time.Second*2
			}
			return false
		},
		Cooldown: time.Minute * 10,
	})

	// 指标收集器
	metricsCollector := db233.NewMetricsCollector("main_db_metrics", 30) // 30天保留期

	// 指标聚合器
	metricsAggregator := db233.NewMetricsAggregator("main_db_aggregator")

	// 3. 创建监控仪表板
	fmt.Println("创建监控仪表板...")
	dashboard := db233.NewMonitoringDashboard("main_dashboard")

	// 添加所有监控组件到仪表板
	dashboard.AddPerformanceMonitor("main_db", perfMonitor)
	dashboard.AddConnectionMonitor("main_db", connMonitor)
	dashboard.AddHealthChecker("main_db", healthChecker)
	dashboard.AddAlertManager("main_db", alertManager)
	dashboard.AddMetricsCollector("main_db", metricsCollector)
	dashboard.AddMetricsAggregator("main_db", metricsAggregator)

	// 配置仪表板
	dashboard.SetRefreshInterval(30 * time.Second)
	dashboard.EnableAutoRefresh()

	// 4. 启动监控系统
	fmt.Println("启动监控系统...")
	dashboard.Start()

	// 5. 模拟数据库操作并监控
	fmt.Println("开始模拟数据库操作...")

	// 模拟一些数据库操作
	for i := 0; i < 100; i++ {
		go func(id int) {
			// 执行查询
			start := time.Now()
			_, err := dbManager.GetDataSource("main_db").Query("SELECT 1")

			duration := time.Since(start)

			// 记录性能数据
			perfMonitor.RecordQuery("SELECT", duration, err == nil)

			// 收集指标
			metricsCollector.CollectMetric("query_duration", float64(duration.Milliseconds()))
			metricsCollector.CollectMetric("query_success", 1.0)

			if err != nil {
				metricsCollector.CollectMetric("query_error", 1.0)
			}

			time.Sleep(time.Millisecond * 100) // 模拟间隔
		}(i)
	}

	// 等待一段时间让操作完成
	time.Sleep(2 * time.Second)

	// 6. 检查监控数据
	fmt.Println("\n=== 监控数据检查 ===")

	// 获取仪表板快照
	snapshot := dashboard.GetCurrentSnapshot()

	fmt.Printf("仪表板摘要:\n")
	fmt.Printf("  数据库总数: %d\n", snapshot.Summary.TotalDatabases)
	fmt.Printf("  健康数据库: %d\n", snapshot.Summary.HealthyDatabases)
	fmt.Printf("  总查询数: %d\n", snapshot.Summary.TotalQueries)
	fmt.Printf("  活跃连接: %d\n", snapshot.Summary.ActiveConnections)
	fmt.Printf("  活跃告警: %d\n", snapshot.Summary.ActiveAlerts)
	fmt.Printf("  健康评分: %.2f\n", snapshot.Summary.HealthScore)
	fmt.Printf("  平均响应时间: %v\n", snapshot.Summary.ResponseTimeAvg)
	fmt.Printf("  错误率: %.2f%%\n", snapshot.Summary.ErrorRate*100)

	// 显示告警
	if len(snapshot.Alerts) > 0 {
		fmt.Printf("\n活跃告警:\n")
		for _, alert := range snapshot.Alerts {
			fmt.Printf("  %s [%s] - %s (%s)\n",
				alert.Name, alert.Severity, alert.Status, alert.Database)
		}
	}

	// 显示性能摘要
	if len(snapshot.Performance) > 0 {
		fmt.Printf("\n性能摘要:\n")
		for db, perf := range snapshot.Performance {
			fmt.Printf("  %s:\n", db)
			fmt.Printf("    总查询数: %d\n", perf.TotalQueries)
			fmt.Printf("    成功率: %.2f%%\n", perf.SuccessRate*100)
			fmt.Printf("    平均响应时间: %v\n", perf.AvgResponseTime)
			fmt.Printf("    慢查询率: %.2f%%\n", perf.SlowQueryRate*100)
			fmt.Printf("    QPS: %.2f\n", perf.QPS)
		}
	}

	// 7. 生成报告
	fmt.Println("\n=== 生成监控报告 ===")

	// 生成JSON报告
	if err := dashboard.GenerateReport("monitoring_report", "json"); err != nil {
		log.Printf("生成JSON报告失败: %v", err)
	} else {
		fmt.Println("JSON报告已生成: ./reports/monitoring_report.json")
	}

	// 生成文本报告
	if err := dashboard.GenerateReport("monitoring_report", "text"); err != nil {
		log.Printf("生成文本报告失败: %v", err)
	} else {
		fmt.Println("文本报告已生成: ./reports/monitoring_report.txt")
	}

	// 生成HTML报告
	if err := dashboard.GenerateReport("monitoring_report", "html"); err != nil {
		log.Printf("生成HTML报告失败: %v", err)
	} else {
		fmt.Println("HTML报告已生成: ./reports/monitoring_report.html")
	}

	// 8. 演示告警触发
	fmt.Println("\n=== 告警系统演示 ===")

	// 模拟高错误率
	fmt.Println("模拟高错误率场景...")
	for i := 0; i < 50; i++ {
		metricsCollector.CollectMetric("error_rate", 0.15) // 15% 错误率
		time.Sleep(100 * time.Millisecond)
	}

	// 检查告警
	alertManager.CheckRules(map[string]interface{}{
		"error_rate": 0.15,
	})

	time.Sleep(2 * time.Second)

	activeAlerts := alertManager.GetActiveAlerts()
	fmt.Printf("当前活跃告警数量: %d\n", len(activeAlerts))

	for _, alert := range activeAlerts {
		fmt.Printf("  告警: %s (%s) - %s\n", alert.Name, alert.Severity, alert.Description)
	}

	// 9. 指标聚合演示
	fmt.Println("\n=== 指标聚合演示 ===")

	// 添加数据源到聚合器
	metricsAggregator.AddDataSource(perfMonitor)
	metricsAggregator.AddDataSource(connMonitor)
	metricsAggregator.AddDataSource(healthChecker)
	metricsAggregator.AddDataSource(alertManager)
	metricsAggregator.AddDataSource(metricsCollector)

	// 刷新聚合数据
	metricsAggregator.RefreshMetrics()

	// 获取聚合统计
	aggregatedStats := metricsAggregator.GetAggregatedStats()
	fmt.Printf("聚合指标统计:\n")
	fmt.Printf("  总指标数量: %d\n", aggregatedStats.TotalMetrics)
	fmt.Printf("  数据点数量: %d\n", aggregatedStats.TotalDataPoints)
	fmt.Printf("  平均值: %.2f\n", aggregatedStats.AverageValue)
	fmt.Printf("  最大值: %.2f\n", aggregatedStats.MaxValue)
	fmt.Printf("  最小值: %.2f\n", aggregatedStats.MinValue)

	// 10. 清理和关闭
	fmt.Println("\n=== 清理资源 ===")

	dashboard.Stop()
	metricsCollector.Stop()
	alertManager.Stop()

	fmt.Println("监控系统演示完成！")
	fmt.Println("\n生成的报告文件:")
	fmt.Println("  - ./reports/monitoring_report.json")
	fmt.Println("  - ./reports/monitoring_report.txt")
	fmt.Println("  - ./reports/monitoring_report.html")
}
