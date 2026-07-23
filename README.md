# db233-go

<!-- keywords: Go ORM, MySQL, game server, batch UPSERT, Session cache, WAL, GORM alternative, sqlx, stateful logic server, 游戏服, 批量写入, 连接池 -->

**Go 语言 ORM / 数据库库**（[`github.com/neko233-com/db233-go`](https://github.com/neko233-com/db233-go)）— 面向 **有状态游戏逻辑服** 与 **MySQL 高 QPS**：**Session 一级缓存**（在线读零查库）、**批量 UPSERT**、**WAL 写不丢**、连接池与冷启动预热。可作为 **GORM / sqlx** 的性能向替代方案。

| | |
|:--|:--|
| **版本** | v1.1.0 · Go 1.25.12+ |
| **数据库** | MySQL（主），PostgreSQL（连接层） |
| **典型场景** | MMORPG 逻辑服、单库单写、登录多表加载、entitysave 批量存档 |
| **文档** | [docs/README.md](docs/README.md) · [FAQ](docs/FAQ.md) · [对比 GORM](docs/COMPARE-ORM.md) · [是什么](docs/OVERVIEW.md) |

```bash
go get github.com/neko233-com/db233-go@v1.1.0
```

**发版压测**：`./scripts/run-benchmark.ps1`（[BENCHMARK.md](docs/BENCHMARK.md)）  
**推送前**：`./scripts/check-secrets.ps1`（凭据仅 `config.local.json` / `*.local.yaml`）

## 框架性能对标（本地 MySQL）

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

## 文档与 FAQ

| 文档 | 说明 |
|------|------|
| [docs/README.md](docs/README.md) | **文档中心**（场景导航） |
| [docs/OVERVIEW.md](docs/OVERVIEW.md) | 是什么、适合谁、30 秒上手 |
| [docs/COMPARE-ORM.md](docs/COMPARE-ORM.md) | **db233 vs GORM vs sqlx** 选型 |
| [docs/FAQ.md](docs/FAQ.md) | **常见问题** |
| [docs/BENCHMARK.md](docs/BENCHMARK.md) | 压测标准与一键脚本 |
| [config-game-server-stateful.md](docs/config-game-server-stateful.md) | 有状态游戏服配置 |
| [config-web-server.md](docs/config-web-server.md) | Web/API 服配置 |

---

> 支持 **命名参数查询** `{paramName}` 与批量命名更新。

## 📋 目录

- [框架性能对标](#框架性能对标本地-mysql)
- [文档与 FAQ](#文档与-faq)
- [游戏逻辑服接入](#游戏逻辑服接入-v102)
- [命名参数查询](#命名参数查询)
- [核心特性](#核心特性)
- [快速开始](#快速开始)
- [命名参数完整指南](#命名参数完整指南)
- [API 文档](#api-文档)
- [测试](#测试)
- [常见问题 FAQ](#常见问题-faq)
- [更新日志](#更新日志)
- [许可证](#许可证)

---

## 命名参数查询

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

### 严格 Entity 查询与事务 Repository

正确性关键读取应显式使用 `StrictCrudRepository`。它采用 all-or-error 语义：Query、Scan、字段转换、结果遍历或 Rows 关闭任一失败时都返回非 `nil` error，不会把空集或部分结果伪装为成功；成功加载的 Entity 会调用一次 `DeserializeAfterLoadDb()`。

```go
ctx := context.Background()
repo := db233.NewStrictCrudRepository(db)

user, err := repo.FindByIdContext(ctx, userID, &User{})
if err != nil {
    return err // 数据库或映射失败
}
if user == nil {
    return ErrUserNotFound // 查询成功，但没有记录
}
```

需要跨删除、分块 UPSERT 和严格回读保持原子性时，先手工开启事务，再从 manager 获取绑定当前 `*sql.Tx` 的窄 Repository：

```go
tm := db233.NewTransactionManager(db)
if err := tm.BeginContext(ctx); err != nil {
    return err
}
defer func() {
    if tm.IsActive() {
        _ = tm.Rollback()
    }
}()

txRepo, err := tm.CrudRepository()
if err != nil {
    return err
}
if _, err := txRepo.DeleteByConditionContext(
    ctx,
    "season_id = ?",
    []any{seasonID},
    &PvpPlan{},
); err != nil {
    return err
}
if err := txRepo.SaveBatchUpsertContext(ctx, plans); err != nil {
    return err
}
loaded, err := txRepo.FindByConditionContext(ctx, "season_id = ?", []any{seasonID}, &PvpPlan{})
if err != nil {
    return err
}
if err := validatePlans(loaded); err != nil {
    return err
}
if err := tm.Commit(); err != nil {
    return err // 业务侧按自身规则处理 commit-unknown
}
```

事务 Repository 只执行同步、串行的事务性 DML，不接入 WAL、WriteBuffer 或 DB 级 Prepared Statement 缓存。目标表必须使用 InnoDB 等事务引擎，同一 Unit of Work 内不得混入会隐式提交的 DDL。Commit/Rollback 后，已取得的 Repository 句柄永久失效。事务内 auto-increment 主键只在 Commit 成功后回填 Entity；回滚到保存点会同步丢弃保存点后的待回填 ID，Commit 返回 error 时仍须按 commit-unknown 规则回读并协调内存状态。若直接使用 `TransactionManager.Query`/`QueryContext`，在返回的 `Rows.Close` 前不得与同一 manager 的 Repository、Exec、保存点或 Commit/Rollback 并发混用。

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

> `MonitoringReportGenerator.ExportReport` 与 `MetricsCollector.ExportToFile` 的产物可能包含数据库、指标、标签、告警等业务监控标识。导出采用同目录临时文件原子替换，文件权限固定为 Unix `0600` / Windows 当前进程身份专用 DACL；由库新建的目录会设为私有。仍应写入受控目录，禁止提交仓库、公开下载或由不受信任进程共享。

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
	    fmt.Printf("更新失败: %s\n", db233.SafeErrorSummary(err))
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
	    fmt.Printf("保存失败: %s\n", db233.SafeErrorSummary(err))
}
```

### CRUD 操作

```go
// 创建
user := &User{Name: "Bob", Email: "bob@example.com", Age: 30}
if err := db.Save(user); err != nil {
	    fmt.Printf("创建失败: %s\n", db233.SafeErrorSummary(err))
}

// 查询
var savedUser User
if err := db.FindById(user.ID, &savedUser); err != nil {
	    fmt.Printf("查询失败: %s\n", db233.SafeErrorSummary(err))
}

// 更新
user.Name = "Bob Smith"
if err := db.Update(user); err != nil {
	    fmt.Printf("更新失败: %s\n", db233.SafeErrorSummary(err))
}

// 删除
if err := db.Delete(user); err != nil {
	    fmt.Printf("删除失败: %s\n", db233.SafeErrorSummary(err))
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
	    fmt.Printf("库存扣减失败: %s\n", db233.SafeErrorSummary(err))
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

## 游戏逻辑服接入（v1.0.2+）

> **配置最佳实践（持续维护）**  
> - 有状态逻辑服：[docs/config-game-server-stateful.md](docs/config-game-server-stateful.md)  
> - 无状态 Web/API：[docs/config-web-server.md](docs/config-web-server.md)  
> - 压测优化建议落地对照：[docs/db233优化落地对照.md](docs/db233优化落地对照.md)  
> - **选型 / FAQ**：[docs/COMPARE-ORM.md](docs/COMPARE-ORM.md) · [docs/FAQ.md](docs/FAQ.md)

### 升级依赖

```bash
go get github.com/neko233-com/db233-go@v1.1.0
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
// 生产必填：先从数据库中的持久化 epoch 元数据读取；清库/重建后必须更换。
opts.DatabaseGeneration = dataEpoch.EpochID
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
if err != nil { return err }
// db.SessionRepo 已绑定；关服必须检查 db.Close() 返回值。
```

`DatabaseGeneration` 会同时保护 Session、WriteBuffer、WAL 和失败重试队列。启动时必须先从数据库读取 epoch，再初始化 db233；运行中清库必须使用两阶段屏障，详见 [数据库代次与安全清库](docs/DATABASE_GENERATION.md)。

### 登录 / 在线 / 下线

```go
// 登录：并发加载可缓存实体到 L1（未注册类型不加载）
session, _ := sessionRepo.OpenSession(playerId, loginEntityTypes)

// 读：走内存（未命中可 GetOrLoad）
bag := session.Get(&PlayerBagEntity{}).(*PlayerBagEntity)

// 写：L1 + dirty；缓存开启时不立即写库，由定时刷写或下线/关服落库
bag.Gold += 100
if err := session.MarkDirty(bag); err != nil { return err }

// 下线：强制刷写 dirty 到 DB（同一 playerId PK，WAL 保护）
if err := sessionRepo.CloseSession(playerId); err != nil { return err }

// 关服：先停止业务/raw SQL 写入，再由 Close 严格刷写并关闭全部组件。
if err := db.Close(); err != nil { return err }
```

`MarkDirty`/`Put` 会立即捕获与业务对象隔离的不可变持久化快照；同一表和主键在下次 flush 前只保留最后一版。快照完成后继续修改内存对象不会改变已经排队的版本，下一次修改后必须再次 `MarkDirty`。调用快照时业务必须自行保证源对象没有并发写；含锁、channel、func、非导出可变状态的复杂实体应实现 `EntitySnapshotter` 提供受控深拷贝。

`Db.Close()` 会先拒绝新的 managed write，再依次排空 Session、WriteBuffer、WAL 和失败队列，最后停止后台任务并关闭连接。数据库不可用时，可恢复实体会先保留在本地 WAL，但 `Close` 仍返回聚合错误供进程告警；禁止忽略该错误。`Close` 幂等，重复调用返回同一结果。直接使用 `Db.DataSource` 的写入不受自动准入控制，必须由业务侧 shutdown writer gate 先行停止并排空。

### Flush 写库压力指标

```go
// lifetime：从本 Db 第一次真正 flush Exec 开始计算。
metrics := db.FlushWriteMetrics()
log.Printf("flush SQL/s=%.2f attempted=%d succeeded=%d failed=%d",
    db.AverageFlushWritesPerSecond(),
    metrics.AttemptedSQL,
    metrics.SucceededSQL,
    metrics.FailedSQL,
)

// 自选窗口：例如每 10 秒采样一次。
previous := metrics
time.Sleep(10 * time.Second)
rates := db.FlushWriteMetrics().RateSince(previous)
log.Printf("10s flush SQL/s=%.2f entities/s=%.2f",
    rates.AttemptedSQLPerSecond,
    rates.AttemptedEntitiesPerSecond,
)
```

`AttemptedSQL` 只统计 db233 管理的状态 flush：Session（含定时、下线、关服、代次排空）、WriteBuffer、显式 `UpdateBatchUpsert` 与恢复回放真正发给 `database/sql` 的 `Exec`；不统计调用方直接使用 `DataSource` / `GetDataSource`、raw SQL 或事务 Repository 发出的写入。合并后的 SQL 只算 1 次，每个 chunk 各算 1 次；失败请求也会形成数据库压力，因此计入 attempted。SQL 构建、序列化、WAL 追加或 Prepare 在 Exec 前失败不计；Exec 成功后即计 succeeded，即使后续 `RowsAffected` 或 WAL 清理失败。`BySource` 可区分 `session`、`write_buffer`、`manual`、`wal_replay`、`fault_tolerance_replay`；指标不保存 SQL、参数、错误文本、表名或玩家标识。

### 数据不丢保证

| 机制 | 说明 |
|------|------|
| WAL 先落盘 | `fsync` 后写库，失败保留 `pending.ndjson` |
| 无限重试 | FaultTolerantManager 默认永不丢弃 |
| UPSERT 幂等 | 使用稳定业务主键时，实体 Save/Update/UPSERT 可安全重放 |
| 数据库代次 | 清库后隔离旧 Session、缓冲、WAL 与失败重试，防止历史数据复活 |

恢复语义是 **at-least-once**，不是跨数据库的 exactly-once。任意 `ExecuteUpdate` 在“服务端已提交、客户端收到连接错误”时可能被重复执行；这类 SQL 必须自行满足幂等性，或携带数据库唯一约束保护的业务幂等键。任何写 API 返回错误时，即使数据已进入 WAL/失败队列，业务层也不得向上游确认成功。

WAL/失败队列默认最多执行 2 次。达到上限后不会丢弃：完整恢复条目写入私有
`dead-letter/`，每条输出独立 ERROR，正常恢复队列停止无限重试。Entity 生命周期还会
维护 `db233_entity_schema_versions`；恢复条目固化单表结构版本，版本不一致直接失败，
禁止 ORM 猜测业务字段转换。

### SQL 日志隐私

默认只记录 SQL 动词；不记录 SQL 文本、长度、可字典猜测的稳定哈希、绑定参数或驱动错误原文。包级 `Log*` 运行时日志还会把所有裸字符串参数变为仅含类型的摘要，防止表名、列名、路径、配置键或玩家标识意外外泄。临时诊断可调用 `db233.SetLogFullSQL(true)`，完整 SQL 会以安全引用形式输出（控制字符会转义），参数仍不记录。完整 SQL 可能包含调用方自行拼入的字面量，只能在受控环境短时开启，诊断结束后必须恢复为 `false`。公开日志或 HTTP 错误使用 `db233.SafeErrorSummary(err)`；应用若通过 `GetLogger()` 主动记录业务字符串，应自行承担脱敏责任。

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

复制模板并填入真实连接（Unix 必须是 `0600` 或更严格）：

```bash
install -m 600 config.local.json.example config.local.json
# 或
install -m 600 config.local.yaml.example config.local.yaml
```

Windows 使用仅当前用户/服务账号可访问的 NTFS 目录：

```powershell
Copy-Item config.local.json.example config.local.json
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

`*.local.json` / `config.local.json` / `*.local.yaml` 已在 `.gitignore` 中忽略。加载器拒绝符号链接、非普通文件、超过 1 MiB 的配置，以及 Unix 下 group/other 可读写的文件；生产凭据优先由秘密管理系统注入。

```go
// 本地开发 / 测试
db, dbConfig, err := db233.OpenDbFromLocalConfig("config.local.json")
sessionRepo, err := db233.InitGameDb(db, dbConfig, opts)
```

集成测试仅从 `DB233_TEST_DSN` 或未纳入 Git 的 `config.local.json` 读取连接，并强制要求 loopback/本机 Unix socket，避免误连远程数据库。常用本地环境可使用 `127.0.0.1:3306 / root / root / db233_go`。

### Go 框架性能对比（实测）

环境：本地 MySQL `127.0.0.1:3306/db233_go`，Go 1.25.12+。
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

## 常见问题 FAQ

<details>
<summary><strong>db233-go 是什么？和 GORM 有什么区别？</strong></summary>

db233-go 是面向 **有状态游戏逻辑服** 的 Go ORM：登录后玩家数据在 **Session 内存（L1）**，在线 `session.Get` **不查库**；写用 **批量 UPSERT + WAL**。  
与 GORM 相比：RDS 实测 **单次 PK 读更快**、**Session 读 1000 次亚毫秒级**（GORM 需循环查库）。通用 CRUD / 关联预加载仍可选 GORM。  
→ 完整对比 [docs/COMPARE-ORM.md](docs/COMPARE-ORM.md) · [docs/FAQ.md](docs/FAQ.md)

</details>

<details>
<summary><strong>如何安装与初始化游戏服？</strong></summary>

```bash
go get github.com/neko233-com/db233-go@v1.1.0
install -m 600 config.local.json.example config.local.json   # Unix 本地凭据，勿提交 Git
```

```go
sessionRepo, _ := db233.InitGameDb(db, dbConfig, opts) // 见上文「游戏逻辑服接入」
```

</details>

<details>
<summary><strong>数据库密码会进 Git 吗？</strong></summary>

不会（若遵循规范）。真实连接仅写在 **gitignore** 的 `config.local.json` / `config.local.yaml`；推送前运行 `./scripts/check-secrets.ps1`。

</details>

<details>
<summary><strong>在线读慢怎么办？</strong></summary>

业务读应走 **`session.Get`**，不要循环 `repo.FindById`。FindById 仅用于未 OpenSession 或运维查询。见 [docs/FAQ.md](docs/FAQ.md#性能)。

</details>

更多问题 → **[docs/FAQ.md](docs/FAQ.md)**（含英文 Quick Answers）

---

## 普通实体统一建表与迁移

业务只需提供实体原型清单，db233-go 统一完成批量建表、补列、建索引、并发幂等和最终严格验证。生产安全默认不修改类型、不删除列、不替换或删除索引：

```go
report, err := db.AutoMigrateSchema(ctx, []any{
    &PlayerEntity{},
    &InventoryEntity{},
}, nil)
if err != nil {
    return err
}
_ = report
```

启动前可先用 `DryRun` 审阅计划，或调用 `VerifySchema` 做纯只读检查。完整权限、报告与 MySQL DDL 边界见 [统一 Schema 建表与迁移](docs/SCHEMA_MIGRATION.md)。需要在增列和删旧列之间执行版本化 Go 数据升级时，使用 [Entity 版本化数据迁移](docs/ENTITY_DATA_MIGRATION.md)。

## 埋点描述文件自动建表

适合埋点接收服：客户端新增行为埋点时，只改 JSON 描述文件；db233-go 根据描述自动建表、补列、建索引，并暴露元数据给上层做 `map[string]any` 上报校验。

此机制与现有 `IDbEntity` / `CrudManager.AutoCreateTable` 是两套独立机制，可同时存在，互不影响。

描述文件只支持 JSON；文件内允许 `//` 和 `/* */` 注释。完整样板见 [docs/config/tracking-schema.example.json](docs/config/tracking-schema.example.json)。

```json
{
  // JSONC: .json 文件允许注释
  "version": "1",
  "tables": [
    {
      "name": "player_behavior_events",
      "comment": "player behavior tracking",
      "columns": [
        {"name": "player_id", "type": "string", "size": 64, "primaryKey": true, "required": true},
        {"name": "event_time", "type": "timestamp", "required": true, "default": "CURRENT_TIMESTAMP"},
        {"name": "action", "type": "string", "size": 64, "required": true, "enum": ["login", "level_up"]},
        {"name": "level", "type": "int"},
        {"name": "extra", "type": "json"}
      ],
      "indexes": [
        {"name": "idx_player_behavior_action", "columns": ["action"]}
      ]
    }
  ]
}
```

```go
schema, plan, err := db233.ApplyTrackingSchemaFile(db, "tracking-schema.json", nil)
if err != nil {
	    log.Fatal(db233.SafeErrorSummary(err))
}
_ = plan

table, ok := schema.GetTable("player_behavior_events")
if !ok {
    log.Fatal("tracking table 未定义")
}
payload := map[string]any{
    "player_id":  "p001",
    "event_time": "2026-06-12T10:00:00Z",
    "action":     "login",
    "level":      10,
    "extra":      map[string]any{"device": "android"},
}
if errs := table.ValidatePayload(payload, false); len(errs) > 0 {
    panic(errs[0])
}
_, err = db233.InsertTrackingPayload(db, table, payload)
```

热重载：

```go
reloader := db233.NewTrackingSchemaReloader(db, "tracking-schema.json", 5*time.Second, db233.TrackingSchemaApplyOptions{})
reloader.OnReload(func(schema *db233.TrackingSchema, plan db233.TrackingSchemaPlan) {
    // schema 可缓存给上报层做 KV 校验
})
reloader.Start()
defer reloader.Stop()
```

本地记录与增量同步：

```go
opts := &db233.TrackingSchemaApplyOptions{
    CachePath: "runtime/tracking-schema.cache.json", // 可选；不填则使用 tracking-schema.json.cache.json
}
schema, plan, err := db233.ApplyTrackingSchemaFile(db, "configs/tracking-schema.json", opts)
```

机制说明：

- 本地 cache 记录上次成功同步的描述文件 hash、版本、时间、SQL 数量。
- 文件 hash 未变化时直接跳过 SQL 规划和执行。
- 文件 hash 变化时读取数据库真实结构，只做增量改动：缺表建表、缺列补列、缺索引建索引。
- `ForceApply: true` 可强制重新检查数据库结构。
- `DisableLocalCache: true` 可关闭本地记录。

默认安全策略：只建表、补列、建索引，不删列。当前自动执行 SQL 路径支持 MySQL。

---

## 更新日志

完整记录见 [ChangeLog.md](ChangeLog.md)。

### v1.2.3 (2026-07-23) — 严格单表恢复版本

- 当前表已绑定版本时，旧版无版本恢复条目也按不一致处理，禁止静默接管

### v1.2.2 (2026-07-23) — 单表结构版本与有界恢复

- 自动维护每张 Entity 表的 `schema_version + schema_fingerprint`
- WAL/失败队列按单表版本硬校验，默认最多执行 2 次
- 终态失败写入 durable dead-letter，并逐条输出可人工追踪的 ERROR

### v1.1.0 (2026-07-22) — 生产一致性与生命周期加固

- 新增数据库代次屏障，清库后旧 Session、WriteBuffer、WAL 和失败重试数据不会跨代写回
- 补齐严格 ORM、事务、保存点、错误链、资源关闭与并发缓存的 fail-closed 契约
- 新增真实 MySQL 100 玩家并发回归，以及 Linux/MySQL、race、benchmark 和 Windows CI 门禁
- SQL 与配置日志默认脱敏；仅 `db233.SetLogFullSQL(true)` 显式 opt-in 可记录安全引用的完整 SQL，参数值与驱动错误原文不进入日志；公开边界统一使用 `SafeErrorSummary`

### v1.0.10 (2026-07-22) — 严格错误传播与事务能力

- 新增 Strict Query、Strict Entity Repository 和事务绑定 Repository
- 修复事务 context 生命周期、终态 reset、rollback error 与 panic 回滚语义
- 保持旧 Query、CrudRepository、Begin 和 WithTransaction API 编译兼容

### v1.0.2 (2026-05-30) — 文档完善

- 文档中心、FAQ、GORM 对比、项目概览
- README 版本号与发版说明修正

### v1.0.1 (2026-05-30) — 性能正式化

- FastOrmScan、对象池、冷启动预热、发版 benchmark 门禁
- 详见 [ChangeLog.md](ChangeLog.md)

### v1.0.0 (2026-05-30) — 生产就绪：游戏服 Session 缓存 + WAL + 连接池

首个稳定版，面向有状态游戏逻辑服。

压测：`go test ./tests/ -run TestPerfStability_Short -timeout 90s -v`

### v0.1.2 (2026-05-30) — Session 实体缓存 + 延迟刷写
- EntityCache 配置（LRU / 按类型上限 / 定时刷写）
- CacheableEntity 白名单 + InitGameDb 返回 SessionRepository

### v0.1.0 (2026-05-30) — 游戏服高性能 + 数据不丢
- FindByIds / SaveBatchUpsert / UpdateBatchUpsert / FindByIdConcurrent
- Session L1 + WriteBuffer + LocalWriteJournal (WAL)
- InitGameDb 一站式初始化

---

## 许可证

Apache License 2.0 - 详见 [LICENSE](LICENSE) 文件

---

## 贡献

欢迎提交 Issue 和 Pull Request！

---

**文档最后更新：** 2026-07-22 · v1.1.0 · [文档中心](docs/README.md) · [FAQ](docs/FAQ.md)
