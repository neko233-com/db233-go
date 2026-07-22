# OLAP 查询使用指南

## 概述

db233-go 支持 OLAP（在线分析处理）查询，可以方便地执行聚合函数（COUNT、SUM、AVG、MAX、MIN 等）并自动进行类型转换。

## 核心特性

1. **自动类型转换**：指定基础类型作为 `returnType`，自动将查询结果转换为指定类型
2. **忽略别名**：无论 SQL 中是否使用别名，都会直接取第一个返回值
3. **支持所有基础类型**：int、int64、float64、string、bool 等
4. **指针返回**：实体查询返回指针引用，避免值传递

## 使用方法

### 1. 基础类型查询（推荐）

指定基础类型作为 `returnType`，会自动取第一个返回值并转换类型：

```go
// COUNT 查询返回 int64
var countType int64
results := db.ExecuteQuery("SELECT COUNT(*) as cnt FROM users", [][]any{}, countType)
count := results[0].(int64)
fmt.Printf("用户总数: %d\n", count)

// SUM 查询返回 float64
var sumType float64
results = db.ExecuteQuery("SELECT SUM(age) as total_age FROM users", [][]any{}, sumType)
sum := results[0].(float64)
fmt.Printf("年龄总和: %.2f\n", sum)

// AVG 查询返回 float32
var avgType float32
results = db.ExecuteQuery("SELECT AVG(age) as avg_age FROM users", [][]any{}, avgType)
avg := results[0].(float32)
fmt.Printf("平均年龄: %.2f\n", avg)

// MAX 查询返回 int
var maxType int
results = db.ExecuteQuery("SELECT MAX(age) as max_age FROM users", [][]any{}, maxType)
maxAge := results[0].(int)
fmt.Printf("最大年龄: %d\n", maxAge)

// MIN 查询返回 int64
var minType int64
results = db.ExecuteQuery("SELECT MIN(age) as min_age FROM users", [][]any{}, minType)
minAge := results[0].(int64)
fmt.Printf("最小年龄: %d\n", minAge)
```

### 2. 支持的基础类型

以下基础类型都可以作为 `returnType`：

- **整数类型**：`int`, `int8`, `int16`, `int32`, `int64`
- **无符号整数**：`uint`, `uint8`, `uint16`, `uint32`, `uint64`
- **浮点数**：`float32`, `float64`
- **字符串**：`string`
- **布尔值**：`bool`

### 3. 忽略 SQL 别名

无论 SQL 中是否使用别名，都会直接取第一个返回值：

```go
// 即使 SQL 中有别名，也会忽略，直接取第一个值（COUNT）
var countType int64
results := db.ExecuteQuery("SELECT COUNT(*) as total_records, MAX(age) as max_age FROM users", [][]any{}, countType)
count := results[0].(int64) // 返回 COUNT 的值，忽略 MAX(age)
```

### 4. 带参数的 OLAP 查询

```go
// 带 WHERE 条件的 COUNT
var countType int64
results := db.ExecuteQuery("SELECT COUNT(*) FROM users WHERE age > ?", [][]any{{25}}, countType)
count := results[0].(int64)
fmt.Printf("年龄大于 25 的用户数: %d\n", count)

// 带多个条件的 SUM
var sumType float64
results = db.ExecuteQuery("SELECT SUM(age) FROM users WHERE age BETWEEN ? AND ?", [][]any{{20, 40}}, sumType)
sum := results[0].(float64)
fmt.Printf("年龄在 20-40 之间的用户年龄总和: %.2f\n", sum)
```

### 5. 空表查询

即使表中没有数据，COUNT 等聚合函数也会返回 0：

```go
var countType int64
results := db.ExecuteQuery("SELECT COUNT(*) FROM users WHERE username = 'non_existent'", [][]any{}, countType)
count := results[0].(int64) // 返回 0
```

### 6. 原始值查询（返回 map）

如果 `returnType` 为 `nil`，会返回原始值或 map：

```go
// 单列查询返回原始值
results := db.ExecuteQuery("SELECT COUNT(*) FROM users", [][]any{}, nil)
count := results[0] // 原始值

// 多列查询返回 map[string]any
results = db.ExecuteQuery("SELECT COUNT(*) as cnt, MAX(age) as max_age FROM users", [][]any{}, nil)
if len(results) > 0 {
    if rowMap, ok := results[0].(map[string]any); ok {
        cnt := rowMap["cnt"]
        maxAge := rowMap["max_age"]
        fmt.Printf("总数: %v, 最大年龄: %v\n", cnt, maxAge)
    }
}
```

### 7. 实体查询（返回指针）

对于实体查询，返回的是指针类型，避免值传递：

```go
// 实体查询返回指针
results := db.ExecuteQuery("SELECT * FROM users WHERE age > ?", [][]any{{18}}, &User{})
for _, result := range results {
    user := result.(*User) // 返回的是指针类型
    fmt.Printf("User: %+v\n", user)
}
```

## 完整示例

```go
package main

import (
	    "fmt"
	    "log"
	    "github.com/neko233-com/db233-go/pkg/db233"
)

func main() {
    // 创建数据库连接
    config := &db233.DbConnectionConfig{
        DatabaseType: db233.EnumDatabaseTypeMySQL,
        Host:         "127.0.0.1",
        Port:         3306,
        Database:     "test_db",
        Username:     "root",
        Password:     "<password>",
    }
    
	    db, err := config.CreateDb(0, nil)
	    if err != nil {
	        log.Fatal(db233.SafeErrorSummary(err))
	    }
	    defer func() {
	        if closeErr := db.Close(); closeErr != nil {
	            log.Printf("关闭数据库失败: %s", db233.SafeErrorSummary(closeErr))
	        }
	    }()

    // 1. COUNT 查询
    var countType int64
    results := db.ExecuteQuery("SELECT COUNT(*) as cnt FROM users", [][]any{}, countType)
    if len(results) > 0 {
        count := results[0].(int64)
        fmt.Printf("用户总数: %d\n", count)
    }

    // 2. SUM 查询
    var sumType float64
    results = db.ExecuteQuery("SELECT SUM(age) as total FROM users", [][]any{}, sumType)
    if len(results) > 0 {
        sum := results[0].(float64)
        fmt.Printf("年龄总和: %.2f\n", sum)
    }

    // 3. AVG 查询
    var avgType float32
    results = db.ExecuteQuery("SELECT AVG(age) as avg FROM users", [][]any{}, avgType)
    if len(results) > 0 {
        avg := results[0].(float32)
        fmt.Printf("平均年龄: %.2f\n", avg)
    }

    // 4. 带参数的查询
    var countType2 int64
    results = db.ExecuteQuery("SELECT COUNT(*) FROM users WHERE age > ?", [][]any{{25}}, countType2)
    if len(results) > 0 {
        count := results[0].(int64)
        fmt.Printf("年龄大于 25 的用户数: %d\n", count)
    }

    // 5. 实体查询（返回指针）
    results = db.ExecuteQuery("SELECT * FROM users WHERE age > ?", [][]any{{18}}, &User{})
    for _, result := range results {
        user := result.(*User)
        fmt.Printf("User: %+v\n", user)
    }
}
```

## 注意事项

1. **类型转换**：如果数据库返回的值无法转换为目标类型，会返回错误
2. **多列查询**：当使用基础类型作为 `returnType` 时，只会取第一列的值，忽略其他列
3. **空结果**：如果查询没有结果，`results` 会是空切片，需要检查长度
4. **别名忽略**：无论 SQL 中是否使用别名，都会直接取第一个返回值

## 测试

所有功能都有完整的单元测试，位于 `tests/olap_query_test.go`：

- `TestOLAPQuery_CountInt64` - COUNT 返回 int64
- `TestOLAPQuery_CountInt` - COUNT 返回 int
- `TestOLAPQuery_SumFloat64` - SUM 返回 float64
- `TestOLAPQuery_AvgFloat32` - AVG 返回 float32
- `TestOLAPQuery_MaxInt` - MAX 返回 int
- `TestOLAPQuery_MinInt64` - MIN 返回 int64
- `TestOLAPQuery_IgnoreAlias` - 忽略别名测试
- `TestOLAPQuery_EmptyTable` - 空表查询测试
- `TestOLAPQuery_WithParams` - 带参数查询测试

运行测试：

```bash
go test ./tests -run TestOLAPQuery -v
```
