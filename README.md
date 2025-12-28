# db233-go

db233-go 是 db233 的 Go 语言版本，一个功能强大的数据库操作库，提供 ORM、分片、迁移和监控功能。

## 特性

- **ORM**: 基于反射的自动对象关系映射
- **分片策略**: 支持多种数据库和表分片策略
- **CRUD 操作**: 简化的数据访问接口
- **连接池**: 高效的数据库连接管理
- **监控**: 数据库操作指标收集

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

## 测试

运行所有测试：

```bash
go test ./...
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