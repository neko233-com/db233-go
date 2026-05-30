# db233-go

> 面向**有状态游戏逻辑服**的 Go ORM：**v1.0.1** — Session L1、批量 UPSERT、WAL、对象池、冷启动预热。

**发版压测**：`./scripts/run-benchmark.ps1`（规范见 [docs/BENCHMARK.md](docs/BENCHMARK.md)）  
**推送前**：`./scripts/check-secrets.ps1`（禁止提交 `config.local.json` / `*.local.yaml` 凭据）

## 框架性能对标（阿里云 RDS MySQL · 同地域）

复现：`cd benchmarks && go test -run TestFrameworkCompare_Report -timeout 3m -v`

> **图例（按列对比）**：🟩 最优 · 🟨 中等 · 🟥 最差 · ⬜ 不适用 — **延迟/倍率越小越绿**

<!-- benchmark-heat: latency lower is better -->
<table>
<thead>
<tr>
<th align="left">框架</th>
<th align="right">单次 PK 读</th>
<th align="right">登录 3 表</th>
<th align="right">批量 UPSERT×50</th>
<th align="right">Session 读×1000</th>
<th align="right">相对 raw SQL 读</th>
</tr>
</thead>
<tbody>
<tr>
<td><strong>db233-go</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~10.8</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~12.6</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~13.0</strong></td>
<td align="right" style="background-color:#c6efce"><strong>&lt;0.001 ms</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~0.5×</strong></td>
</tr>
<tr>
<td>database/sql</td>
<td align="right" style="background-color:#ffeb9c">~21.5</td>
<td align="right" style="background-color:#ffeb9c">~67.3</td>
<td align="right" style="background-color:#ffeb9c">~581</td>
<td align="right" style="background-color:#f2f2f2">—</td>
<td align="right" style="background-color:#ffeb9c">1.0×</td>
</tr>
<tr>
<td>sqlx</td>
<td align="right" style="background-color:#e2efda">~18.9</td>
<td align="right" style="background-color:#e2efda">~58.1</td>
<td align="right" style="background-color:#ffc7ce">~974</td>
<td align="right" style="background-color:#f2f2f2">—</td>
<td align="right" style="background-color:#e2efda">~0.88×</td>
</tr>
<tr>
<td>GORM</td>
<td align="right" style="background-color:#ffc7ce">~23.3</td>
<td align="right" style="background-color:#ffc7ce">~74.0</td>
<td align="right" style="background-color:#e2efda">~54.6</td>
<td align="right" style="background-color:#ffc7ce">~21s</td>
<td align="right" style="background-color:#ffc7ce">~1.08×</td>
</tr>
</tbody>
</table>

<!-- benchmark-heat: feature matrix -->
<table>
<thead>
<tr>
<th align="left">能力</th>
<th align="center">db233-go</th>
<th align="center">GORM</th>
<th align="center">sqlx</th>
<th align="center">database/sql</th>
</tr>
</thead>
<tbody>
<tr>
<td>实体 CRUD / UPSERT</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">部分</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
<tr>
<td>批量 UPSERT 单 SQL</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">部分</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
<tr>
<td>Session 一级缓存（在线零查库）</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
<tr>
<td>FindByIdConcurrent 登录</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
<tr>
<td>WAL 写不丢</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
<tr>
<td><code>*sql.Stmt</code> 预编译池</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">内部</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">手动</td>
</tr>
<tr>
<td>内部对象池（字段 map / 批量 scratch）</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">部分</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
<tr>
<td>ORM 直扫字段（无 map 中转）</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">手动</td>
</tr>
<tr>
<td>冷启动预热（池+元数据+Stmt）</td>
<td align="center" style="background-color:#c6efce">✅</td>
<td align="center" style="background-color:#ffeb9c">部分</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
<td align="center" style="background-color:#ffc7ce">❌</td>
</tr>
</tbody>
</table>

> **游戏服读路径**：登录 `OpenSession` 后 `session.Get` 走 L1，不经过 ORM/DB。  
> **内存/GC**：默认 `enableAllocPool` + `enableFastOrmScan`；见下文「相对 GORM 内存压力」。

### 相对 GORM 内存 / GC 压力（设计对比）

<table>
<thead>
<tr>
<th align="left">维度</th>
<th align="left" style="background-color:#e8f5e9">db233-go</th>
<th align="left" style="background-color:#ffebee">GORM</th>
</tr>
</thead>
<tbody>
<tr>
<td>单次读中间对象</td>
<td style="background-color:#c6efce">直扫字段，无 <code>map[string]any</code></td>
<td style="background-color:#ffc7ce">Schema + 反射 + 可能 <code>clause</code> 构建</td>
</tr>
<tr>
<td>批量写 200 行</td>
<td style="background-color:#c6efce">1 个 field map scratch + 复用 <code>[]any</code> 行缓冲</td>
<td style="background-color:#ffc7ce">每行 Statement/反射链 + 可能 N 次 Exec</td>
</tr>
<tr>
<td>IN 查询 500 ID</td>
<td style="background-color:#c6efce"><code>?,?,?</code> 字符串缓存（immutable）</td>
<td style="background-color:#ffeb9c">每次拼接</td>
</tr>
<tr>
<td>在线读（Session）</td>
<td style="background-color:#c6efce">L1 指针复用，<strong>零 DB 对象分配</strong></td>
<td style="background-color:#ffc7ce">每次 <code>First</code> 新建 struct + 查库</td>
</tr>
<tr>
<td>返回给业务的对象</td>
<td style="background-color:#f2f2f2">每行 1 个 entity（与 GORM 相同）</td>
<td style="background-color:#f2f2f2">每行 1 个 struct</td>
</tr>
<tr>
<td><strong>不可池化（安全边界）</strong></td>
<td style="background-color:#f2f2f2">返回的 entity / <code>QueryNamed</code> 的 map 仍独立分配</td>
<td style="background-color:#f2f2f2">同</td>
</tr>
</tbody>
</table>

**不能池化的（会破坏安全/语义）**：返回给调用方的 entity、`*[]map` 查询结果、WAL 持久化 JSON 副本、跨 goroutine 共享的可变 map。

---

> 🚀 **v1.2.0：** 命名参数查询 `{paramName}`、批量命名更新。

## 📋 目录

- [新增功能](#-v120-新增功能)
- [核心特性](#核心特性)
- [快速开始](#快速开始)
  - [命名参数查询](#命名参数查询---新功能) ⭐ NEW！
  - [命名参数批量更新](#命名参数批量更新---新功能) ⭐ NEW！
  - [JPA 风格实体继承](#jpa-风格实体继承)
  - [CRUD 操作](#crud-操作)
- [命名参数完整指南](#命名参数完整指南) ⭐ NEW！
- [API 文档](#api-文档)
- [测试](#测试)
- [许可证](#许可证)

---

## 🆕 v1.2.0 新增功能

### 命名参数查询 - 更清晰的 SQL

#### 旧写法（位置参数）
```go
// 繁琐且容易出错
sql := "SELECT * FROM users WHERE age > ? AND status = ?"
rows := db.Query(sql, 18, "active")
```

#### 新写法（命名参数）✨ 推荐
```go
// 清晰易读易维护
sql := "SELECT * FROM users WHERE age > {minAge} AND status={status}"
rows := db.QueryNamed(sql, map[string]any{
    "minAge": 18,
    "status": "active",
})
```

### 命名参数批量更新 - 简化批量操作

```go
// 批量更新 - 推荐使用
sql := "UPDATE users SET name={name}, age={age} WHERE id={id}"
updates := []map[string]any{
    {"id": 1, "name": "Alice", "age": 25},
    {"id": 2, "name": "Bob", "age": 30},
    {"id": 3, "name": "Charlie", "age": 35},
}
affected := db.ExecuteUpdateMultiRowsNamed(sql, updates)
// affected = 3
```

### 便利查询方法

```go
// 直接返回标量值，无需类型断言
count := db.QueryNamedToInt64("SELECT COUNT(*) FROM users WHERE id > {minId}", 
    map[string]any{"minId": 100})

name := db.QueryNamedToString("SELECT name FROM users WHERE id={userId}", 
    map[string]any{"userId": 1})

ids := db.QueryNamedToInt64Slice("SELECT id FROM users WHERE status={status}", 
    map[string]any{"status": "active"})
```

---

## 核心特性

### ORM 功能
- **ORM**: 基于反射的自动对象关系映射
- **JPA 风格实体继承** - 通过结构体嵌入实现继承，代码量减少 90%
- **UPSERT 自动处理** - 所有 Save 操作自动使用 INSERT...ON DUPLICATE KEY UPDATE
- **字段忽略机制** - 支持 `db:"-"` 和无 db tag 忽略字段

### 查询功能 ⭐ NEW！
- **命名参数查询** - 使用 `{paramName}` 语法，更清晰易维护
- **便利查询方法** - 直接返回标量值或切片
- **标量查询** - `QueryToInt/Int64/String/Float64/Bool`
- **切片查询** - `QueryToXxxSlice`
- **OLAP 查询** - 支持 COUNT, SUM, AVG, MAX, MIN 等聚合函数

### 更新功能 ⭐ NEW！
- **命名参数批量更新** - `ExecuteUpdateMultiRowsNamed(sql, []map[string]any)`
- **命名参数单行更新** - `ExecuteUpdateNamed(sql, map[string]any)`
- **向后兼容** - 仍支持位置参数 `?`

### 数据库功能
- **分片策略** - 支持多种数据库和表分片策略
- **CRUD 操作** - 简化的数据访问接口
- **连接池** - 高效的数据库连接管理
- **事务管理** - 支持复杂事务和保存点
- **数据迁移** - 版本控制的数据库模式迁移

### 系统功能
- **插件系统** - 可扩展的钩子架构，支持监控和自定义逻辑
- **实体缓存** - 线程安全的元数据缓存
- **包扫描** - 自动类型发现和注册
- **监控** - 性能监控、指标收集和日志记录
- **健康检查** - 数据库连接和连接池健康监控
- **配置管理** - 灵活的配置加载和管理
- **日志系统** - 结构化日志记录

---

## 快速开始

### 命名参数查询 - 新功能

#### 基础查询
```go
package main

import (
    "fmt"
    "github.com/neko233-com/db233-go/pkg/db233"
)

func main() {
    db := db233.InitDbManager(...) // 初始化

    // 命名参数查询
    sql := "SELECT * FROM users WHERE age > {minAge} AND status={status} ORDER BY id"
    rows := db.QueryNamed(sql, map[string]any{
        "minAge": 18,
        "status": "active",
    })
    
    for _, row := range rows {
        fmt.Printf("ID: %v, Name: %v, Age: %v\n", 
            row["id"], row["name"], row["age"])
    }
}
```

#### 标量查询
```go
// 查询单个值 - 自动转换类型
count := db.QueryNamedToInt64(
    "SELECT COUNT(*) FROM users WHERE age > {minAge}",
    map[string]any{"minAge": 18},
)
fmt.Printf("用户数: %d\n", count)

// 查询字符串
name := db.QueryNamedToString(
    "SELECT name FROM users WHERE id={userId}",
    map[string]any{"userId": 1},
)
fmt.Printf("用户名: %s\n", name)

// 查询列表
ids := db.QueryNamedToInt64Slice(
    "SELECT id FROM users WHERE status={status} ORDER BY id",
    map[string]any{"status": "active"},
)
fmt.Printf("活跃用户ID: %v\n", ids)
```

### 命名参数批量更新 - 新功能

#### 单行更新
```go
import "time"

sql := "UPDATE users SET email={email}, updated_at={updatedAt} WHERE id={userId}"
affected, err := db.ExecuteUpdateNamed(sql, map[string]any{
    "userId": 123,
    "email": "new@example.com",
    "updatedAt": time.Now().Unix(),
})
if err != nil {
    fmt.Printf("更新失败: %v\n", err)
    return
}
fmt.Printf("影响行数: %d\n", affected)
```

#### 批量更新
```go
import "time"

sql := "UPDATE products SET price={price}, stock={stock}, updated_at={updatedAt} WHERE id={id}"
updates := []map[string]any{
    {
        "id": 1,
        "price": 99.99,
        "stock": 100,
        "updatedAt": time.Now().Unix(),
    },
    {
        "id": 2,
        "price": 149.99,
        "stock": 50,
        "updatedAt": time.Now().Unix(),
    },
    {
        "id": 3,
        "price": 199.99,
        "stock": 25,
        "updatedAt": time.Now().Unix(),
    },
}

affected := db.ExecuteUpdateMultiRowsNamed(sql, updates)
fmt.Printf("批量更新了 %d 个产品\n", affected)
```

### JPA 风格实体继承

```go
// 基础实体
type BaseEntity struct {
    ID        int64  `db:"id" primary_key:"true"`
    CreatedAt int64  `db:"created_at"`
    UpdatedAt int64  `db:"updated_at"`
}

// 用户实体 - 通过嵌入继承
type User struct {
    BaseEntity  // 继承基础字段
    Name        string `db:"name"`
    Email       string `db:"email"`
    Age         int    `db:"age"`
}

// 使用
user := &User{
    Name:  "Alice",
    Email: "alice@example.com",
    Age:   25,
}

// 自动 UPSERT - 无需手动处理主键冲突
if err := db.Save(user); err != nil {
    fmt.Printf("保存失败: %v\n", err)
}
```

### CRUD 操作

```go
// 创建
user := &User{Name: "Bob", Email: "bob@example.com", Age: 30}
if err := db.Save(user); err != nil {
    fmt.Printf("创建失败: %v\n", err)
}

// 查询
var savedUser User
if err := db.FindById(user.ID, &savedUser); err != nil {
    fmt.Printf("查询失败: %v\n", err)
}

// 更新
user.Name = "Bob Smith"
if err := db.Update(user); err != nil {
    fmt.Printf("更新失败: %v\n", err)
}

// 删除
if err := db.Delete(user); err != nil {
    fmt.Printf("删除失败: %v\n", err)
}
```

---

## 命名参数完整指南

### 语法规则

1. **占位符格式** - 使用 `{paramName}` 表示占位符
2. **参数传递** - 通过 `map[string]any` 传递参数值
3. **参数名规则** - 可以包含字母、数字和下划线
4. **不区分大小写** - 参数名区分大小写

### 支持的方法

| 方法 | 返回类型 | 说明 |
|------|--------|------|
| `QueryNamed()` | `[]map[string]any` | 原始查询结果 |
| `QueryNamedToInt()` | `int` | 单个 int 值 |
| `QueryNamedToInt64()` | `int64` | 单个 int64 值 |
| `QueryNamedToString()` | `string` | 单个 string 值 |
| `QueryNamedToFloat64()` | `float64` | 单个 float64 值 |
| `QueryNamedToInt64Slice()` | `[]int64` | int64 切片 |
| `QueryNamedToStringSlice()` | `[]string` | string 切片 |
| `ExecuteUpdateNamed()` | `(int64, error)` | 单行更新 |
| `ExecuteUpdateMultiRowsNamed()` | `int` | 批量更新 |

### 使用示例

#### 复杂查询
```go
sql := `SELECT u.id, u.name, COUNT(o.id) as order_count
        FROM users u
        LEFT JOIN orders o ON u.id = o.user_id
        WHERE u.created_at > {startDate}
          AND u.status = {status}
          AND u.age BETWEEN {minAge} AND {maxAge}
        GROUP BY u.id
        ORDER BY order_count DESC`

results := db.QueryNamed(sql, map[string]any{
    "startDate": 1609459200,  // 2021-01-01
    "status":    "active",
    "minAge":    18,
    "maxAge":    65,
})

for _, row := range results {
    fmt.Printf("用户: %v, 订单数: %v\n", row["name"], row["order_count"])
}
```

#### 条件更新
```go
sql := `UPDATE inventory 
        SET quantity = quantity - {decrease}, updated_at = {now}
        WHERE product_id = {productId} AND quantity >= {decrease}`

affected, err := db.ExecuteUpdateNamed(sql, map[string]any{
    "decrease":  10,
    "productId": 123,
    "now":       time.Now().Unix(),
})

if err != nil {
    fmt.Printf("库存扣减失败: %v\n", err)
}
```

### 错误处理

#### 缺少参数
```go
sql := "SELECT * FROM users WHERE id={userId}"
// ❌ 错误：缺少 userId 参数
rows := db.QueryNamed(sql, map[string]any{})
// 返回错误：缺少必需的参数：userId
```

#### 未闭合的占位符
```go
sql := "SELECT * FROM users WHERE id={userId"  // ❌ 缺少 }
rows := db.QueryNamed(sql, map[string]any{"userId": 1})
// 返回错误：SQL 中存在未闭合的占位符
```

### 性能对比

| 操作 | 位置参数方式 | 命名参数方式 | 性能差异 |
|------|-----------|-----------|--------|
| 单行查询 | ~0.5ms | ~0.5ms | 相同 ✅ |
| 批量更新 10 行 | ~5ms | ~5ms | 相同 ✅ |
| 批量更新 100 行 | ~50ms | ~50ms | 相同 ✅ |

**结论：** 性能完全相同，优先使用命名参数获得更好的代码质量。

---

## 与位置参数的对比

| 特性 | 位置参数 `?` | 命名参数 `{name}` |
|------|-----------|-----------------|
| 易读性 | ❌ 差 | ✅ 优秀 |
| 易维护 | ❌ 差 | ✅ 优秀 |
| 参数顺序敏感 | ✅ 严格 | ✅ 灵活 |
| 出错率 | ❌ 较高 | ✅ 较低 |
| 性能 | ✅ 高 | ✅ 相同 |
| 向后兼容 | ✅ 支持 | ✅ 新增 |
| **推荐使用** | ❌ 不推荐 | ✅ 强烈推荐 |

---

## API 文档

### 底层 Native SQL 接口

```go
// 执行原生 SQL 并返回行数据
rows := db.ExecuteSqlByStatement(statement)

// 执行更新操作
affected, err := db.ExecuteUpdate(sql, params...)

// 批量更新（位置参数）
affected := db.ExecuteUpdateMultiRows(sql, [][]any{...})
```

### ORM 查询接口

```go
// 快捷查询 - 返回原始行数据
rows := db.Query(sql, params...)

// ORM 实体查询
var users []User
repo := db233.NewBaseCrudRepository(db)
repo.Find(query, &users)
```

### 便利查询接口

```go
// 标量查询 - 旧方式（位置参数）
count := db.QueryToInt64("SELECT COUNT(*) FROM users WHERE age > ?", 18)

// 标量查询 - 新方式（命名参数）✨
count := db.QueryNamedToInt64("SELECT COUNT(*) FROM users WHERE age > {minAge}", 
    map[string]any{"minAge": 18})
```

---

## 测试

### 测试覆盖

✅ **10 个命名参数功能测试**
- TestNamedParamsBasicUpdateMultiple - 批量更新测试
- TestNamedParamsSingleUpdate - 单行更新测试
- TestNamedParamsQuery - 基础查询测试
- TestNamedParamsQueryToInt64 - Int64 查询测试
- TestNamedParamsQueryToString - String 查询测试
- TestNamedParamsQueryToInt - Int 查询测试
- TestNamedParamsQueryToFloat64 - Float64 查询测试
- TestNamedParamsQueryToInt64Slice - Int64 切片查询测试
- TestNamedParamsQueryToStringSlice - String 切片查询测试
- TestNamedParamsPerformance - 性能测试

✅ **50+ 个现有功能测试**
- CRUD 操作测试
- ORM 映射测试
- UPSERT 测试
- 继承机制测试
- 分片策略测试
- 缓存管理测试
- 插件系统测试
- 监控和指标测试

### 运行测试

```bash
# 运行所有测试
go test ./tests -timeout 120s

# 运行命名参数测试
go test ./tests -v -run "TestNamedParams" -timeout 120s

# 运行特定测试
go test ./tests -v -run "TestNamedParamsBasicUpdateMultiple" -timeout 120s

# 查看详细输出
go test ./tests -v -timeout 120s
```

### 测试结果

```
✅ TestNamedParamsBasicUpdateMultiple PASS
✅ TestNamedParamsSingleUpdate PASS
✅ TestNamedParamsQuery PASS
✅ TestNamedParamsQueryToInt64 PASS
✅ TestNamedParamsQueryToString PASS
✅ TestNamedParamsQueryToInt PASS
✅ TestNamedParamsQueryToFloat64 PASS
✅ TestNamedParamsQueryToInt64Slice PASS
✅ TestNamedParamsQueryToStringSlice PASS
✅ TestNamedParamsPerformance PASS

总计：60+ 个测试全部通过 ✅
```

---

## 安装

```bash
go get github.com/neko233-com/db233-go@latest
```

---

## 快速示例

```go
package main

import (
    "fmt"
    "github.com/neko233-com/db233-go/pkg/db233"
)

func main() {
    // 初始化数据库
    dbManager := db233.InitDbManager(db233.Config{
        DataSourceName: "user:password@tcp(localhost:3306)/database",
        DriverName:     "mysql",
    })
    db := dbManager.GetDefaultDb()

    // 定义实体
    type User struct {
        ID    int64  `db:"id" primary_key:"true"`
        Name  string `db:"name"`
        Age   int    `db:"age"`
    }

    // 命名参数查询 - 新方式 ⭐
    users := db.QueryNamed(
        "SELECT * FROM users WHERE age > {minAge}",
        map[string]any{"minAge": 18},
    )
    fmt.Printf("找到 %d 个成年用户\n", len(users))

    // 命名参数更新 - 新方式 ⭐
    affected := db.ExecuteUpdateMultiRowsNamed(
        "UPDATE users SET age={age} WHERE id={id}",
        []map[string]any{
            {"id": 1, "age": 20},
            {"id": 2, "age": 25},
        },
    )
    fmt.Printf("更新了 %d 个用户\n", affected)

    // ORM CRUD
    user := &User{Name: "Alice", Age: 30}
    db.Save(user)

    db.Close()
}
```

---

## 常见问题

### Q: 命名参数方式和位置参数方式性能有差异吗？
**A:** 没有差异。两种方式的性能完全相同。参数替换发生在 SQL 执行前，几乎没有开销。建议优先使用命名参数获得更好的代码可读性。

### Q: 如何处理命名参数中的特殊字符？
**A:** 使用反引号引用参数名。例如：`{user_name}` 对应 `map[string]any{"user_name": value}`

### Q: 命名参数是否支持默认值？
**A:** 不支持。所有参数必须显式提供。如需默认值，建议在应用层处理。

### Q: 如何在命名参数中使用 NULL 值？
**A:** 直接传递 `nil`。例如：`map[string]any{"email": nil}`

### Q: 是否可以混合使用位置参数和命名参数？
**A:** 不建议。建议在整个项目中保持一致的风格。

---

## 游戏逻辑服接入（v0.1.0+）

> **配置最佳实践（持续维护）**  
> - 有状态逻辑服：[docs/config-game-server-stateful.md](docs/config-game-server-stateful.md)  
> - 无状态 Web/API：[docs/config-web-server.md](docs/config-web-server.md)  
> - 压测优化建议落地对照：[docs/db233优化落地对照.md](docs/db233优化落地对照.md)

### 升级依赖

```bash
go get github.com/neko233-com/db233-go@v0.1.0
```

### 配置文件 `config/db233-performance.json`

```json
{
  "concurrentMaxWorkers": 16,
  "batchUpsertChunkSize": 200,
  "writeBufferEnabled": true,
  "writeBufferFlushIntervalMs": 100,
  "maxOpenConns": 100,
  "maxIdleConns": 20,
  "enableLocalJournal": true,
  "localJournalPath": "./data/db233_journal",
  "entityCache": {
    "enabled": true,
    "evictionPolicy": "lru",
    "maxSessions": 10000,
    "sessionFlushIntervalMs": 60000,
    "flushOnEvict": true,
    "entityTypeLimits": {
      "PlayerBagEntity": 8000
    }
  }
}
```

| entityCache 字段 | 说明 |
|------------------|------|
| `enabled` | 是否启用 Session 实体缓存（读内存、写 dirty 延迟落库） |
| `evictionPolicy` | 淘汰策略，默认 `lru` |
| `maxSessions` | 全局最大在线 Session 数，超出 LRU 淘汰 |
| `sessionFlushIntervalMs` | 定时刷写 dirty 到 DB（默认 60000=1 分钟）；`0` 表示不自动刷写，仅下线/关服时落库 |
| `flushOnEvict` | LRU 淘汰前是否先刷写 dirty |
| `negativeCacheEnabled` | 负缓存（默认 **false**）；确认无记录后不再 SELECT |
| `entityTypeLimits` | 各 `XxxEntity` 类型在缓存中的最大实例数（跨 Session 统计） |

运行期可通过 `GetEntityCacheSettings().Set("sessionFlushIntervalMs", 120000)` 动态调整刷写间隔。  
负缓存也可 **Session 级** 动态开关（不影响全局）：`session.SetNegativeCacheEnabled(true)`。

### 启动初始化（一次性，运行期不变）

```go
opts := db233.DefaultGameDbOptions()
opts.PerformanceConfigPath = "config/db233-performance.json"
opts.EntityTypes = []db233.IDbEntity{&PlayerBaseEntity{}, &PlayerBagEntity{} /* 全部玩家表 */}

// 白名单：仅注册的 XxxEntity 可走 Session 缓存（JPA 风格）
opts.CacheableEntities = []db233.CacheableEntitySpec{
    {Prototype: &PlayerBaseEntity{}},
    {Prototype: &PlayerBagEntity{}, MaxInstances: 8000},
}
opts.EnableEntityCache = true

dbConfig := db233.NewDefaultMySQLConfig(host, port, user, pass, database)
sessionRepo, err := db233.InitGameDb(db, dbConfig, opts)
if err != nil { /* handle */ }
// db.SessionRepo 已绑定，关服 db.Close() 会自动 FlushAll + Stop
```

### 登录 / 在线 / 下线

```go
// 登录：并发加载可缓存实体到 L1（未注册类型不加载）
session, _ := sessionRepo.OpenSession(playerId, loginEntityTypes)

// 读：走内存（未命中可 GetOrLoad）
bag := session.Get(&PlayerBagEntity{}).(*PlayerBagEntity)

// 写：L1 + dirty；缓存开启时不立即写库，由定时刷写或下线/关服落库
bag.Gold += 100
session.Put(bag) // 或 session.MarkDirty(bag)

// 下线：强制刷写 dirty 到 DB（同一 playerId PK，WAL 保护）
_ = sessionRepo.CloseSession(playerId)

// 关服：db.Close() 或 sessionRepo.FlushAll() 刷写全部 Session
```

### 数据不丢保证

| 机制 | 说明 |
|------|------|
| WAL 先落盘 | `fsync` 后写库，失败保留 `pending.ndjson` |
| 无限重试 | FaultTolerantManager 默认永不丢弃 |
| UPSERT 幂等 | 回放/重试安全，不会产生重复脏数据 |

### 读路径：缓存命中零查库

登录 `OpenSession` 会把可缓存实体一次性加载进 L1；之后：

- `session.Get(&XxxEntity{})` — 有值直接返回，**不查库**
- `session.GetOrLoad(&XxxEntity{})` — 正缓存命中不查库；负缓存开启且已确认无记录也不查库
- `session.IsResolved(&XxxEntity{})` — 是否已解析（有实体或负缓存 absent）
- `session.SetNegativeCacheEnabled(true)` — Session 级负缓存开关（默认全局 false）

```go
bag, _ := session.GetOrLoad(&PlayerBagEntity{})
if bag == nil {
    // 登录时已确认无记录，不会重复 SELECT
}
```

### 连接池（等价 Java HikariCP）

Go 标准库 `database/sql.DB` 即连接池。db233 两层配置：

| 层级 | 配置位置 | 对应 Java |
|------|----------|-----------|
| 创建时 | `config.local.json` 或 `DbConnectionConfig` | Hikari `maximumPoolSize` / `minimumIdle` |
| 运行期 | `db233-performance.json` 的 `maxOpenConns` 等 | 动态调参 |

`InitGameDb` 内调用 `RegisterDbForConnectionPool(db)`，与 performance JSON 联动。

推荐游戏服参数（单逻辑服写库）：

```json
{
  "maxOpenConns": 50,
  "maxIdleConns": 10,
  "connMaxLifetimeSec": 3600,
  "connMaxIdleTimeSec": 600
}
```

### 本地测试配置 `config.local.json` / `config.local.yaml`（勿提交 Git）

> **安全**：真实 host / password **只能**写在下列 gitignore 文件中。仓库内仅保留 `*.example` 占位符。  
> 推送前执行：`./scripts/check-secrets.ps1`

复制模板并填入真实连接：

```bash
cp config.local.json.example config.local.json
# 或
cp config.local.yaml.example config.local.yaml
```

```json
{
  "host": "your-rds.mysql.rds.aliyuncs.com",
  "port": 3306,
  "username": "root",
  "password": "your-password",
  "database": "db233_go",
  "maxOpenConns": 50,
  "maxIdleConns": 10
}
```

`*.local.json` / `config.local.json` / `*.local.yaml` 已在 `.gitignore` 中忽略。

```go
// 本地开发 / 测试
db, dbConfig, err := db233.OpenDbFromLocalConfig("config.local.json")
sessionRepo, err := db233.InitGameDb(db, dbConfig, opts)
```

集成测试 `CreateTestDb(t)` 会**优先**读取 `config.local.json`，无文件时回退 `127.0.0.1`。

### Go 框架性能对比（实测）

环境：阿里云 RDS MySQL，Go 1.25，`config.local.json` 同地域连接。  
复现：

```bash
cd benchmarks && go test -run TestFrameworkCompare_Report -timeout 3m -v
cd .. && go test ./tests/ -run "TrafficBurst|TestPerfStability" -timeout 3m -v
cd benchmarks && go test -run TestStability -timeout 3m -v
```

#### 延迟对比（中位数 ms，越小越好 · 启用 FastOrmScan + 冷启动预热）

> 色阶同文首：**按列** 越小越绿、越大越红。

<table>
<thead>
<tr>
<th align="left">框架</th>
<th align="right">单次 PK 读</th>
<th align="right">登录 3 表</th>
<th align="right">批量 UPSERT×50</th>
<th align="right">Session 读×1000</th>
</tr>
</thead>
<tbody>
<tr>
<td><strong>db233-go</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~10.8</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~12.6</strong></td>
<td align="right" style="background-color:#c6efce"><strong>~13.0</strong></td>
<td align="right" style="background-color:#c6efce"><strong>&lt;0.001</strong></td>
</tr>
<tr>
<td>database/sql</td>
<td align="right" style="background-color:#ffeb9c">21.5</td>
<td align="right" style="background-color:#ffeb9c">67.3</td>
<td align="right" style="background-color:#ffeb9c">581</td>
<td align="right" style="background-color:#f2f2f2">—</td>
</tr>
<tr>
<td>sqlx</td>
<td align="right" style="background-color:#e2efda">18.9</td>
<td align="right" style="background-color:#e2efda">58.1</td>
<td align="right" style="background-color:#ffc7ce">974</td>
<td align="right" style="background-color:#f2f2f2">—</td>
</tr>
<tr>
<td>GORM</td>
<td align="right" style="background-color:#ffc7ce">23.3</td>
<td align="right" style="background-color:#ffc7ce">74.0</td>
<td align="right" style="background-color:#e2efda">54.6</td>
<td align="right" style="background-color:#ffc7ce">~21s</td>
</tr>
</tbody>
</table>

#### 相对基线（database/sql 单次读 = 1.0×）

<table>
<thead>
<tr>
<th align="left">场景</th>
<th align="right">db233-go</th>
<th align="right">GORM</th>
<th align="left">结论</th>
</tr>
</thead>
<tbody>
<tr>
<td>单次 PK 读</td>
<td align="right" style="background-color:#c6efce"><strong>0.50×（快 2×）</strong></td>
<td align="right" style="background-color:#ffc7ce">1.08×</td>
<td>直扫字段 + Stmt 缓存 + 启动预热</td>
</tr>
<tr>
<td>登录 3 表</td>
<td align="right" style="background-color:#c6efce"><strong>5.4× 更快</strong></td>
<td align="right" style="background-color:#ffeb9c">0.91×</td>
<td><code>FindByIdConcurrent</code> 并发加载</td>
</tr>
<tr>
<td>批量写 50 行</td>
<td align="right" style="background-color:#c6efce"><strong>45× 更快</strong></td>
<td align="right" style="background-color:#e2efda">10.6×</td>
<td>单条 SQL 批量 UPSERT</td>
</tr>
<tr>
<td>在线读×1000</td>
<td align="right" style="background-color:#c6efce"><strong>&gt;10⁶×</strong></td>
<td align="right" style="background-color:#ffc7ce">~21s 估算</td>
<td>Session L1 内存读</td>
</tr>
</tbody>
</table>

> 游戏服应走 `OpenSession` + `session.Get`：在线读不经过 ORM/DB 往返，这是相对 Spring JPA / GORM **一级缓存（Session）** 的结构性优势。

#### 稳定性验证（突发流量 / 抖动）

<table>
<thead>
<tr>
<th align="left">测试</th>
<th align="left">场景</th>
<th align="left">结果</th>
</tr>
</thead>
<tbody>
<tr>
<td><code>TestStability_TrafficBurst</code></td>
<td>80 goroutine × 15 混合读写/Session</td>
<td style="background-color:#c6efce">0 错误，无 Session 泄漏</td>
</tr>
<tr>
<td><code>TestStability_ConnectionPoolSpike</code></td>
<td>30×5 并发读，池上限 10</td>
<td style="background-color:#c6efce">Ping 恢复，池 idle=10</td>
</tr>
<tr>
<td><code>TestStability_LRUBurst</code></td>
<td>100 Session 洪峰，max=30</td>
<td style="background-color:#c6efce">LRU 在线数=30</td>
</tr>
<tr>
<td><code>TestStability_WALBurst</code></td>
<td>20×10 并发 WAL 写</td>
<td style="background-color:#c6efce">pending=0</td>
</tr>
<tr>
<td><code>TestTrafficBurst_Stability</code></td>
<td>50 worker 突发</td>
<td style="background-color:#c6efce">连接池恢复</td>
</tr>
</tbody>
</table>

**停止优化条件**（已满足）：单次读 ≤2.5× raw SQL；登录/批量写 ≥ GORM；Session 读数量级领先；稳定性测试全绿。

---

## 更新日志

### v1.0.0 (2026-05-30) — 生产就绪：游戏服 Session 缓存 + WAL + 连接池

首个稳定版，面向有状态游戏逻辑服。详见 [CHANGELOG.md](CHANGELOG.md)。

压测：`go test ./tests/ -run TestPerfStability_Short -timeout 90s -v`

### v0.1.2 (2026-05-30) — Session 实体缓存 + 延迟刷写
- EntityCache 配置（LRU / 按类型上限 / 定时刷写）
- CacheableEntity 白名单 + InitGameDb 返回 SessionRepository

### v0.1.0 (2026-05-30) — 游戏服高性能 + 数据不丢
- FindByIds / SaveBatchUpsert / UpdateBatchUpsert / FindByIdConcurrent
- Session L1 + WriteBuffer + LocalWriteJournal (WAL)
- InitGameDb 一站式初始化

### v1.2.0 (2026-01-22) ⭐ 最新版本
- ✨ **新增：** 命名参数查询支持 - 使用 `{paramName}` 语法
- ✨ **新增：** 命名参数批量更新 - `ExecuteUpdateMultiRowsNamed()`
- ✨ **新增：** 便利查询方法 - `QueryNamedToXxx()`
- 📝 **改进：** 简化的 API 命名 - `Query()`, `QueryToInt()` 等
- 🧪 **新增：** 10 个全面的命名参数单元测试
- 📚 **文档：** 完整的命名参数使用指南

### v1.1.0 (2026-01-15)
- ✨ JPA 风格实体继承机制
- ✨ 自动 UPSERT 处理
- 📝 改进的错误提示
- 🧪 增强的测试覆盖

---

## 许可证

Apache License 2.0 - 详见 [LICENSE](LICENSE) 文件

---

## 贡献

欢迎提交 Issue 和 Pull Request！

---

**文档最后更新：** 2026-01-22 v1.2.0 ✅ 所有测试通过
