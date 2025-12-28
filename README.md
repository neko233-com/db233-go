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

## 安装

```bash
go get github.com/SolarisNeko/db233-go
```

## 快速开始

### 1. 初始化数据库管理器

```go
package main

import (
    "github.com/SolarisNeko/db233-go/pkg/db233"
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

### 4. 使用分片

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