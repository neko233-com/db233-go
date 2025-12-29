# db233-go

db233-go 是 db233 的 Go 语言版本，一个功能强大的数据库操作库，提供 ORM、分片、迁移和监控功能。

## 特性

- **ORM**: 基于反射的自动对象关系映射
- **分片策略**: 支持多种数据库和表分片策略
- **CRUD 操作**: 简化的数据访问接口
- **连接池**: 高效的数据库连接管理
- **插件系统**: 可扩展的钩子架构，支持监控和自定义逻辑
- **实体缓存**: 元数据缓存，提高运行时性能
- **包扫描**: 自动类型发现和注册
- **监控**: 内置性能监控、指标收集和日志记录
- **事务管理**: 支持复杂事务和保存点
- **数据迁移**: 版本控制的数据库模式迁移
- **健康检查**: 数据库连接和连接池健康监控
- **配置管理**: 灵活的配置加载和管理
- **日志系统**: 结构化日志记录

## 安装

```bash
go get github.com/neko233-com/db233-go
```

## 快速开始

### 1. 初始化数据库管理器

```go
package main

import (
    "github.com/neko233-com/db233-go/pkg/db233"
)

func main() {
    // 获取单例实例
    manager := db233.GetInstance()

    // 配置数据库组
    config := &db233.DbGroupConfig{
        GroupName: "myapp",
        DbConfigFetcher: &MyDbConfigFetcher{}, // 实现配置获取器
    }

    // 创建数据库组
    dbGroup, err := db233.NewDbGroup(config)
    if err != nil {
        panic(err)
    }

    // 添加到管理器
    err = manager.AddDbGroup(dbGroup)
    if err != nil {
        panic(err)
    }
}
```

### 2. 定义实体

```go
type User struct {
    ID       int    `db:"id,primary_key"`
    Username string `db:"username"`
    Email    string `db:"email"`
    Age      int    `db:"age"`
}
```

### 3. 使用 CRUD 操作

```go
// 初始化实体元数据
crudManager := db233.GetCrudManagerInstance()
crudManager.AutoInitEntity(&User{})

// 创建存储库
db, _ := manager.GetDb("myapp", 0) // 获取数据库实例
repo := &db233.BaseCrudRepository{Db: db}

// 保存用户
user := &User{
    Username: "john_doe",
    Email:    "john@example.com",
    Age:      30,
}

err := repo.Save(user)
if err != nil {
    log.Printf("保存失败: %v", err)
}

// 查找用户
found, err := repo.FindById(1, &User{})
if err != nil {
    log.Printf("查找失败: %v", err)
}
```

### 4. 使用事务管理

```go
// 编程式事务
tm := db233.NewTransactionManager(db)
err := tm.ExecuteInTransaction(func(tm *db233.TransactionManager) error {
    // 在事务中执行操作
    _, err := tm.Exec("UPDATE users SET age = age + 1 WHERE id = ?", 1)
    if err != nil {
        return err
    }

    // 创建保存点
    err = tm.Savepoint("update_age")
    if err != nil {
        return err
    }

    // 更多操作...
    return nil
})

// 声明式事务
err = db233.WithTransaction(db, func(tm *db233.TransactionManager) error {
    // 事务操作
    return nil
}, db233.TransactionOptions{
    Isolation: sql.LevelReadCommitted,
    ReadOnly:  false,
})
```

### 5. 使用数据迁移

```go
// 创建迁移管理器
mm := db233.NewMigrationManager(db, "./migrations")

// 初始化迁移表
err := mm.Init()
if err != nil {
    panic(err)
}

// 创建新迁移
err = mm.CreateMigration("add_user_table")
if err != nil {
    panic(err)
}

// 执行上迁
err = mm.Up(0) // 0 表示应用所有待迁移
if err != nil {
    panic(err)
}

// 查看迁移状态
migrations, err := mm.GetStatus()
if err != nil {
    panic(err)
}

for _, m := range migrations {
    fmt.Printf("Migration: %d_%s, Applied: %v\n", m.Version, m.Name, m.AppliedAt != nil)
}
```

### 6. 使用健康检查

```go
// 创建健康检查器
hc := db233.NewHealthChecker(db)

// 执行健康检查
result := hc.Check()
if result.Healthy {
    fmt.Printf("数据库健康: %s\n", result.Message)
} else {
    fmt.Printf("数据库不健康: %s\n", result.Message)
}

// 定期健康检查
scheduler := db233.NewHealthCheckScheduler(30 * time.Second)
scheduler.AddChecker("main_db", hc)
scheduler.Start()

// 稍后停止
defer scheduler.Stop()
```

### 7. 使用配置管理

```go
// 从文件加载配置
cm := db233.GetConfigManager()
err := cm.LoadFromFile("config.json")
if err != nil {
    panic(err)
}

// 获取配置值
dbHost := db233.GetConfigString("database.host", "localhost")
dbPort := db233.GetConfigInt("database.port", 3306)

// 从环境变量加载
cm.LoadFromEnv("DB233_")
```

### 8. 使用日志系统

```go
// 设置日志级别
logger := db233.GetLogger()
logger.SetLevel(db233.DEBUG)

// 记录日志
db233.LogInfo("应用启动完成")
db233.LogWarn("发现配置问题: %s", issue)
db233.LogError("数据库连接失败: %v", err)
```

### 9. 使用分片

```go
// 配置分片策略
strategy := &db233.ShardingDbStrategy100w{}

// 计算分片ID
dbId := strategy.CalculateDbId(12345) // 根据用户ID计算数据库分片
```

## 配置

### 数据库配置获取器

实现 `DbConfigFetcher` 接口来提供数据库配置：

```go
type MyDbConfigFetcher struct{}

func (f *MyDbConfigFetcher) Fetch(groupName string) ([]*db233.DbConfig, error) {
    return []*db233.DbConfig{
        {
            DbId:       0,
            Url:        "user:password@tcp(localhost:3306)/db0",
            DriverName: "mysql",
        },
        {
            DbId:       1,
            Url:        "user:password@tcp(localhost:3306)/db1",
            DriverName: "mysql",
        },
    }, nil
}
```

## 架构组件

- **DbManager**: 单例数据库管理器
- **DbGroup**: 数据库组，包含多个数据库实例
- **Db**: 单个数据库连接和操作
- **CrudRepository**: CRUD 操作接口
- **CrudManager**: 实体元数据管理
- **ShardingStrategy**: 分片策略接口
- **PluginManager**: 插件管理系统
- **EntityCacheManager**: 实体元数据缓存
- **PackageScanner**: 类型注册和扫描

## 插件系统

db233-go 提供了强大的插件系统，允许在数据库操作的关键节点插入自定义逻辑。

### 内置插件

#### 日志插件
记录所有 SQL 执行信息：

```go
loggingPlugin := db233.NewLoggingPlugin()
pluginManager := db233.GetPluginManagerInstance()
pluginManager.RegisterPlugin(loggingPlugin)
```

#### 性能监控插件
监控慢查询和性能指标：

```go
performancePlugin := db233.NewPerformanceMonitorPlugin()
performancePlugin.SetSlowQueryThreshold(100 * time.Millisecond)
pluginManager.RegisterPlugin(performancePlugin)
```

#### 指标收集插件
收集数据库操作统计信息：

```go
metricsPlugin := db233.NewMetricsPlugin()
pluginManager.RegisterPlugin(metricsPlugin)

// 获取指标数据
metrics := metricsPlugin.GetMetrics()
fmt.Printf("总查询数: %d\n", metrics["total_queries"])
fmt.Printf("总耗时: %v\n", metrics["total_duration"])

// 打印报告
metricsPlugin.PrintReport()
```

### 自定义插件

实现 `Db233Plugin` 接口创建自定义插件：

```go
type MyCustomPlugin struct {
    *db233.AbstractDb233Plugin
}

func NewMyCustomPlugin() *MyCustomPlugin {
    return &MyCustomPlugin{
        AbstractDb233Plugin: db233.NewAbstractDb233Plugin("my-plugin"),
    }
}

func (p *MyCustomPlugin) InitPlugin() {
    // 初始化逻辑
}

func (p *MyCustomPlugin) PreExecuteSql(context *db233.ExecuteSqlContext) {
    // SQL 执行前逻辑
}

func (p *MyCustomPlugin) PostExecuteSql(context *db233.ExecuteSqlContext) {
    // SQL 执行后逻辑
}

// 注册插件
pluginManager.RegisterPlugin(NewMyCustomPlugin())
```

### 插件生命周期

1. **InitPlugin()**: 插件初始化
2. **PreExecuteSql()**: SQL 执行前钩子
3. **PostExecuteSql()**: SQL 执行后钩子

所有插件都是线程安全的，支持并发操作。

### 完整示例

```go
package main

import (
    "reflect"
    "github.com/SolarisNeko/db233-go/pkg/db233"
)

// 定义实体
type User struct {
    ID   int    `db:"id,primary_key"`
    Name string `db:"name"`
    Age  int    `db:"age"`
}

type Product struct {
    ID    int     `db:"id,primary_key"`
    Name  string  `db:"name"`
    Price float64 `db:"price"`
}

// 定义仓库接口
type Repository interface {
    Save(entity interface{}) error
    FindById(id interface{}) interface{}
}

// 实现仓库
type UserRepository struct {
    db *db233.Db
}

func (r *UserRepository) Save(entity interface{}) error {
    // 实现保存逻辑
    return nil
}

func (r *UserRepository) FindById(id interface{}) interface{} {
    // 实现查找逻辑
    return nil
}

func init() {
    // 在init函数中注册类型
    scanner := db233.PackageScannerInstance
    scanner.RegisterType(reflect.TypeOf(User{}))
    scanner.RegisterType(reflect.TypeOf(Product{}))
    scanner.RegisterType(reflect.TypeOf(UserRepository{}))
}

func main() {
    // 初始化数据库管理器
    manager := db233.GetInstance()

    // 配置数据库组
    config := &db233.DbGroupConfig{
        GroupName: "app",
        DbConfigFetcher: &YourDbConfigFetcher{},
    }

    dbGroup, _ := db233.NewDbGroup(config)
    manager.AddDbGroup(dbGroup)

    // 使用包扫描器自动发现实体
    scanner := db233.PackageScannerInstance

    // 扫描所有实体
    entities := scanner.ScanStructTypes("main")
    for _, entityType := range entities {
        // 自动初始化实体元数据
        crudManager := db233.GetCrudManagerInstance()
        crudManager.AutoInitEntity(entityType)
    }

    // 扫描所有仓库
    repoInterface := reflect.TypeOf((*Repository)(nil)).Elem()
    repositories := scanner.ScanSubTypes("main", repoInterface)

    fmt.Printf("发现 %d 个实体和 %d 个仓库\n", len(entities), len(repositories))
}
```

## 高级监控系统

db233-go 提供了企业级的监控系统，包括性能监控、指标收集、告警管理和报告生成。所有监控组件都支持程序化访问，无需Web界面。

### 监控组件概述

- **PerformanceMonitor**: 详细的性能监控和统计
- **ConnectionPoolMonitor**: 连接池状态监控
- **HealthChecker**: 数据库健康检查
- **AlertManager**: 基于阈值的告警系统
- **MetricsCollector**: 历史指标收集和存储
- **MetricsAggregator**: 多源指标聚合
- **MonitoringDashboard**: 统一的监控仪表板
- **MonitoringReportGenerator**: 多格式报告生成

### 性能监控器

详细监控数据库操作性能：

```go
// 创建性能监控器
perfMonitor := db233.NewPerformanceMonitor("main_db", 1000)
perfMonitor.SetSlowQueryThreshold(time.Second)

// 记录查询性能
perfMonitor.RecordQuery("SELECT", 150*time.Millisecond, true)

// 获取详细报告
report := perfMonitor.GetDetailedReport()
fmt.Printf("总查询数: %d\n", report["total_queries"])
fmt.Printf("成功率: %.2f%%\n", report["success_rate"].(float64)*100)
fmt.Printf("平均响应时间: %s\n", report["avg_query_time"])
```

### 连接池监控器

监控连接池状态和利用率：

```go
// 创建连接池监控器
connMonitor := db233.NewConnectionPoolMonitor("main_db", dataSource)

// 获取连接池报告
report := connMonitor.GetReport()
fmt.Printf("活跃连接: %d\n", report["active_connections"])
fmt.Printf("空闲连接: %d\n", report["idle_connections"])
fmt.Printf("连接利用率: %.2f%%\n", report["connection_utilization"].(float64)*100)
```

### 健康检查器

全面的数据库健康检查：

```go
// 创建健康检查器
healthChecker := db233.NewHealthChecker("main_db", dataSource)

// 添加检查项
healthChecker.AddCheck("connectivity", db233.HealthCheckConnectivity)
healthChecker.AddCheck("query_test", db233.HealthCheckQueryTest)

// 执行检查
result := healthChecker.Check()
fmt.Printf("健康状态: %t\n", result.Healthy)
fmt.Printf("响应时间: %v\n", result.ResponseTime)
```

### 告警管理器

基于阈值的智能告警：

```go
// 创建告警管理器
alertManager := db233.NewAlertManager("main_db")

// 添加告警规则
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

// 检查规则并触发告警
alertManager.CheckRules(map[string]interface{}{
    "error_rate": 0.15,
})

// 获取活跃告警
activeAlerts := alertManager.GetActiveAlerts()
for _, alert := range activeAlerts {
    fmt.Printf("告警: %s (%s)\n", alert.Name, alert.Severity)
}
```

### 指标收集器

历史指标收集和趋势分析：

```go
// 创建指标收集器 (30天保留期)
collector := db233.NewMetricsCollector("main_db", 30)

// 收集指标
collector.CollectMetric("query_duration", 150.5)
collector.CollectMetric("connection_count", 25.0)

// 获取指标历史
history := collector.GetMetricHistory("query_duration", 24*time.Hour)
fmt.Printf("收集了 %d 个数据点\n", len(history))

// 导出数据
collector.ExportData("metrics_export.json")
```

### 指标聚合器

多源指标聚合和统计：

```go
// 创建指标聚合器
aggregator := db233.NewMetricsAggregator("main_db")

// 添加数据源
aggregator.AddDataSource(perfMonitor)
aggregator.AddDataSource(connMonitor)
aggregator.AddDataSource(healthChecker)

// 刷新聚合数据
aggregator.RefreshMetrics()

// 获取聚合统计
stats := aggregator.GetAggregatedStats()
fmt.Printf("总指标数: %d\n", stats.TotalMetrics)
fmt.Printf("平均值: %.2f\n", stats.AverageValue)
fmt.Printf("最大值: %.2f\n", stats.MaxValue)
```

### 监控仪表板

统一的监控数据展示：

```go
// 创建监控仪表板
dashboard := db233.NewMonitoringDashboard("main_dashboard")

// 添加监控组件
dashboard.AddPerformanceMonitor("main_db", perfMonitor)
dashboard.AddConnectionMonitor("main_db", connMonitor)
dashboard.AddHealthChecker("main_db", healthChecker)
dashboard.AddAlertManager("main_db", alertManager)

// 启动自动刷新
dashboard.SetRefreshInterval(30 * time.Second)
dashboard.EnableAutoRefresh()
dashboard.Start()

// 获取当前快照
snapshot := dashboard.GetCurrentSnapshot()
fmt.Printf("数据库总数: %d\n", snapshot.Summary.TotalDatabases)
fmt.Printf("健康数据库: %d\n", snapshot.Summary.HealthyDatabases)
fmt.Printf("活跃告警: %d\n", snapshot.Summary.ActiveAlerts)
```

### 监控报告生成

生成多格式监控报告：

```go
// 创建报告生成器
reportGenerator := db233.NewMonitoringReportGenerator("main_reports")

// 添加监控组件
reportGenerator.AddPerformanceMonitor("main_db", perfMonitor)
reportGenerator.AddConnectionMonitor("main_db", connMonitor)
reportGenerator.AddHealthChecker("main_db", healthChecker)

// 生成并导出报告
reportGenerator.ExportReport("daily_report", "json")  // JSON格式
reportGenerator.ExportReport("daily_report", "text")  // 文本格式
reportGenerator.ExportReport("daily_report", "html")  // HTML格式
```

### 完整监控系统示例

```go
package main

import (
    "fmt"
    "time"
    "github.com/SolarisNeko/db233-go/pkg/db233"
)

func main() {
    // 初始化数据库管理器
    dbManager := db233.NewDbManager("example_db")

    // 配置数据库连接
    config := &db233.DbConfig{
        Host: "localhost", Port: 3306,
        Database: "test_db", Username: "root", Password: "password",
        MaxOpenConns: 10, MaxIdleConns: 5,
    }
    dbManager.AddDataSource("main_db", config)

    // 创建监控组件
    perfMonitor := db233.NewPerformanceMonitor("main_db", 1000)
    connMonitor := db233.NewConnectionPoolMonitor("main_db", dbManager.GetDataSource("main_db"))
    healthChecker := db233.NewHealthChecker("main_db", dbManager.GetDataSource("main_db"))
    alertManager := db233.NewAlertManager("main_db")
    metricsCollector := db233.NewMetricsCollector("main_db", 30)
    metricsAggregator := db233.NewMetricsAggregator("main_db")

    // 创建监控仪表板
    dashboard := db233.NewMonitoringDashboard("main_dashboard")
    dashboard.AddPerformanceMonitor("main_db", perfMonitor)
    dashboard.AddConnectionMonitor("main_db", connMonitor)
    dashboard.AddHealthChecker("main_db", healthChecker)
    dashboard.AddAlertManager("main_db", alertManager)
    dashboard.AddMetricsCollector("main_db", metricsCollector)
    dashboard.AddMetricsAggregator("main_db", metricsAggregator)

    // 启动监控系统
    dashboard.Start()

    // 模拟数据库操作
    for i := 0; i < 100; i++ {
        start := time.Now()
        _, err := dbManager.GetDataSource("main_db").Query("SELECT 1")
        duration := time.Since(start)

        perfMonitor.RecordQuery("SELECT", duration, err == nil)
        metricsCollector.CollectMetric("query_duration", float64(duration.Milliseconds()))
    }

    // 检查监控数据
    snapshot := dashboard.GetCurrentSnapshot()
    fmt.Printf("监控摘要:\n")
    fmt.Printf("  数据库总数: %d\n", snapshot.Summary.TotalDatabases)
    fmt.Printf("  健康数据库: %d\n", snapshot.Summary.HealthyDatabases)
    fmt.Printf("  总查询数: %d\n", snapshot.Summary.TotalQueries)
    fmt.Printf("  活跃告警: %d\n", snapshot.Summary.ActiveAlerts)

    // 生成报告
    dashboard.GenerateReport("monitoring_report", "json")
    dashboard.GenerateReport("monitoring_report", "html")

    // 清理资源
    dashboard.Stop()
    metricsCollector.Stop()
}
```

### 监控最佳实践

1. **定期检查**: 设置自动刷新间隔，定期检查系统状态
2. **阈值告警**: 为关键指标设置合理的告警阈值
3. **历史数据**: 保留足够的历史数据用于趋势分析
4. **报告生成**: 定期生成报告用于审计和优化
5. **资源清理**: 及时清理过期数据和停止监控组件

### 监控指标说明

- **性能指标**: 查询响应时间、成功率、慢查询率、QPS
- **连接指标**: 活跃连接数、空闲连接数、利用率、等待连接数
- **健康指标**: 连接状态、响应时间、检查通过率
- **告警指标**: 活跃告警数、告警严重程度分布
- **系统指标**: CPU使用率、内存使用率、磁盘I/O

## 发布

运行一键发布脚本：

**Windows CMD:**
```cmd
publish.cmd
```

**PowerShell:**
```powershell
.\publish.ps1
```

脚本将自动：
1. 构建项目
2. 运行测试
3. 创建 Git 标签
4. 推送到远程仓库

## 许可证

本项目采用与原 Kotlin 版本相同的许可证。