# 容错连接管理使用指南

## 概述

容错连接管理器（FaultTolerantManager）提供了以下功能：

1. **自动重连机制**：当数据库连接断开时，自动尝试重连
2. **失败操作持久化**：将失败的数据库操作保存到文件，防止数据丢失
3. **自动重试**：连接恢复后，自动重试之前失败的操作
4. **边界恢复**：把组件边界内的 panic 转为错误并保留恢复数据

恢复采用 **at-least-once** 语义。实体 Save/Update/UPSERT 必须使用稳定业务主键；任意 `ExecuteUpdate` 必须是幂等 SQL，或使用数据库唯一约束保护的业务幂等键。组件不提供分布式 exactly-once。

## 启用容错功能

### 方式一：创建数据库时自动启用

使用 `CreateDb` 方法创建数据库实例时，默认会自动启用容错管理器：

```go
import "github.com/neko233-com/db233-go/pkg/db233"

// 创建数据库配置
config := &db233.DbConnectionConfig{
    DatabaseType: db233.EnumDatabaseTypeMySQL,
    Host:         "127.0.0.1",
    Port:         3306,
    Database:     "test_db",
    Username:     "root",
    Password:     "<password>",
}

// 创建数据库实例（自动启用容错）
db, err := config.CreateDb(0, nil)
if err != nil {
	    log.Fatal(db233.SafeErrorSummary(err))
}
defer func() {
    if closeErr := db.Close(); closeErr != nil {
        log.Printf("关闭数据库失败: %s", db233.SafeErrorSummary(closeErr))
    }
}()
```

有状态游戏服推荐使用 `InitGameDb`，并从数据库持久化元数据读取非空 `DatabaseGeneration`。全量或局部清库必须在同一事务内轮换 generation，详见 [数据库代次与安全清库](./DATABASE_GENERATION.md)。

### 方式二：手动启用

如果使用 `CreateDbWithoutFaultTolerance` 创建数据库，可以稍后手动启用：

```go
// 创建数据库实例（不启用容错）
db, err := config.CreateDbWithoutFaultTolerance(0, nil)
if err != nil {
	    log.Fatal(db233.SafeErrorSummary(err))
}

// 严格 API 会传播路径独占、恢复文件损坏和启动错误。
if err := db.EnableFaultToleranceStrict(config); err != nil {
    if closeErr := db.Close(); closeErr != nil {
        log.Printf("回滚关闭数据库失败: %s", db233.SafeErrorSummary(closeErr))
    }
    log.Fatal(db233.SafeErrorSummary(err))
}
defer func() {
    if closeErr := db.Close(); closeErr != nil {
        log.Printf("关闭数据库失败: %s", db233.SafeErrorSummary(closeErr))
    }
}() // Close 会停止容错管理器
```

## 配置选项

容错管理器支持以下配置选项：

```go
opts := db233.DefaultGameDbOptions()
opts.DatabaseGeneration = epochFromDatabase
opts.LocalJournalPath = "/path/to/private/recovery-dir"
opts.EntityTypes = []db233.IDbEntity{&PlayerEntity{}}

sessionRepo, err := db233.InitGameDb(db, config, opts)
if err != nil {
	    log.Fatal(db233.SafeErrorSummary(err))
}
_ = sessionRepo
```

`LocalJournalPath` 必须在初始化前设置；同一恢复文件只允许一个实例/进程持有。游戏服默认永不丢弃失败操作。运行中不得直接切换恢复路径。

## 工作原理

### 1. 健康检查

容错管理器会定期检查数据库连接的健康状态（默认每30秒）：

- 如果连接不健康，自动触发重连
- 重连成功后，立即尝试重试失败的操作

### 2. 失败操作记录

当数据库操作因连接错误失败时：

- 操作会被记录到失败队列
- 操作信息会被持久化到文件（`failed_operations.json`）
- 即使进程重启，失败的操作也会被保留

### 3. 自动重试

- 定期检查失败队列（默认每10秒）
- 如果连接健康，尝试执行失败的操作
- 成功执行的操作会从队列中移除
- 游戏服默认无限重试，不会因次数达到上限而丢弃

### 4. 边界恢复

组件在关键后台循环和用户 hook 边界把意外 panic 转成带因果链的错误，避免恢复队列被静默丢弃。它不能拦截应用其他 goroutine 的 panic，也不能替代服务进程自己的顶层恢复、告警与受控重启。

## 失败操作持久化

失败的操作会被保存到 JSON 文件：

**文件位置**：`./.db233_failed_ops/failed_operations.json`

**文件格式**：
```json
[
  {
	    "id": "ftm_00000000000000000000000000000000",
    "operation": "Save",
    "sql": "INSERT INTO test_user (username, email, age) VALUES (?, ?, ?)",
    "params": ["user1", "user1@example.com", 25],
    "table_name": "test_user",
    "primary_key": null,
    "timestamp": "2026-01-19T12:00:00Z",
    "retry_count": 0,
    "last_error": "",
    "database_generation": "epoch-20260722-001"
  }
]
```

恢复文件包含执行所需的 SQL 参数或实体 JSON。目录和文件会使用最小权限创建；生产环境仍应放在仅服务账号可访问的加密磁盘/卷中，禁止放入共享目录、源码仓库或日志采集目录。仓库默认忽略 `.db233_journal/` 与 `.db233_failed_ops/`；自定义路径也必须加入业务仓库忽略规则。

## 监控和管理

### 查看失败操作数量

```go
count := db.FaultTolerantMgr.GetFailedOperationCount()
fmt.Printf("当前有 %d 个失败的操作等待重试\n", count)
```

### 清除失败操作（谨慎使用）

```go
// 清除所有失败的操作（仅在确认不需要重试时使用）
if err := db.FaultTolerantMgr.ClearFailedOperationsStrict(); err != nil {
	    log.Printf("清理失败队列失败: %s", db233.SafeErrorSummary(err))
}
```

## 示例：完整的容错使用

```go
package main

import (
    "log"
    "time"
    
    "github.com/neko233-com/db233-go/pkg/db233"
)

func main() {
    // 1. 创建数据库配置
    config := &db233.DbConnectionConfig{
        DatabaseType: db233.EnumDatabaseTypeMySQL,
        Host:         "127.0.0.1",
        Port:         3306,
        Database:     "test_db",
        Username:     "root",
        Password:     "<password>",
    }
    
    // 2. 创建数据库实例（自动启用容错）
	    db, err := config.CreateDb(0, nil)
	    if err != nil {
	        log.Fatal(db233.SafeErrorSummary(err))
    }
    defer func() {
        if closeErr := db.Close(); closeErr != nil {
            log.Printf("关闭数据库失败: %s", db233.SafeErrorSummary(closeErr))
        }
    }()
    
    // 3. 创建 Repository
    repo := db233.NewBaseCrudRepository(db)
    
    // 4. 正常使用，容错会自动处理连接问题
    user := &TestUser{
        Username: "testuser",
        Email:    "test@example.com",
        Age:      25,
    }
    
    // 即使此时连接断开，操作也会被记录并在重连后重试
    err = repo.Save(user)
	    if err != nil {
	        // 返回错误时不得向上游确认成功；数据可能已进入本地恢复队列。
	        log.Printf("保存未确认成功: %s", db233.SafeErrorSummary(err))
    }
    
    // 5. 程序继续运行，容错管理器在后台工作
    time.Sleep(1 * time.Hour)
}
```

## 注意事项

1. **持久化路径**：确保应用有权限写入持久化目录
2. **磁盘空间**：失败操作会占用磁盘空间，定期检查并清理
3. **路径独占**：每个数据库实例使用唯一恢复目录；启动时无法独占路径应直接失败
4. **代次隔离**：清库前使用 generation 屏障；不得手工删除恢复文件冒充安全清库
5. **重放语义**：at-least-once；任意 SQL 由应用层保证幂等，事务原子性不能跨重放边界

## 故障排查

### 问题：连接一直无法重连

**检查**：
1. 数据库服务是否正常运行
2. 网络连接是否正常
3. 连接配置是否正确
4. 查看日志中的重连错误信息

### 问题：失败操作一直无法重试

**检查**：
1. 连接是否已恢复（查看健康检查日志）
2. 数据库 generation 是否匹配，恢复目录是否被另一实例占用
3. SQL 语句和参数是否正确
4. 仅在受控主机本地查看 `failed_operations.json`；不得复制到工单、聊天或普通日志

### 问题：进程仍然崩溃

**检查**：
1. 是否有组件边界外的未处理 panic
2. 用户 hook 是否阻塞或 panic
3. 查看脱敏后的错误链和堆栈

## 最佳实践

1. **始终启用容错**：在生产环境中始终启用容错管理器
2. **监控失败操作**：定期检查失败操作数量，及时处理异常
3. **保持幂等**：实体使用稳定主键；任意更新使用幂等键或唯一约束
4. **日志监控**：监控容错管理器的日志，及时发现问题
5. **受控清理**：仅在明确接受数据丢失且完成 generation 轮换后清理失败队列
