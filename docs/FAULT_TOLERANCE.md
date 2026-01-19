# 容错连接管理使用指南

## 概述

容错连接管理器（FaultTolerantManager）提供了以下功能：

1. **自动重连机制**：当数据库连接断开时，自动尝试重连
2. **失败操作持久化**：将失败的数据库操作保存到文件，防止数据丢失
3. **自动重试**：连接恢复后，自动重试之前失败的操作
4. **Panic Recovery**：防止数据库错误导致整个进程退出

## 启用容错功能

### 方式一：创建数据库时自动启用（推荐）

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
    Password:     "root",
}

// 创建数据库实例（自动启用容错）
db, err := config.CreateDb(0, nil)
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

### 方式二：手动启用

如果使用 `CreateDbWithoutFaultTolerance` 创建数据库，可以稍后手动启用：

```go
// 创建数据库实例（不启用容错）
db, err := config.CreateDbWithoutFaultTolerance(0, nil)
if err != nil {
    log.Fatal(err)
}

// 手动启用容错管理器
db.EnableFaultTolerance(config)
defer db.DisableFaultTolerance()
```

## 配置选项

容错管理器支持以下配置选项：

```go
// 获取容错管理器
ftm := db.FaultTolerantMgr

// 设置持久化路径（默认：./.db233_failed_ops）
ftm.SetPersistPath("/path/to/persist/dir")

// 配置重连参数（在创建后立即配置）
ftm.maxReconnectAttempts = 10        // 最大重连次数
ftm.reconnectInterval = 5 * time.Second  // 重连间隔
ftm.healthCheckInterval = 30 * time.Second  // 健康检查间隔
ftm.maxRetryAttempts = 3             // 最大重试次数
ftm.retryInterval = 10 * time.Second  // 重试间隔
```

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
- 达到最大重试次数后，操作会被放弃

### 4. Panic Recovery

所有数据库操作都包含 panic recovery，确保：

- 数据库错误不会导致进程崩溃
- 错误会被记录到日志
- 连接错误会被自动处理

## 失败操作持久化

失败的操作会被保存到 JSON 文件：

**文件位置**：`./.db233_failed_ops/failed_operations.json`

**文件格式**：
```json
[
  {
    "id": "1640000000000_Save",
    "operation": "Save",
    "sql": "INSERT INTO test_user (username, email, age) VALUES (?, ?, ?)",
    "params": ["user1", "user1@example.com", 25],
    "table_name": "test_user",
    "primary_key": null,
    "timestamp": "2026-01-19T12:00:00Z",
    "retry_count": 0,
    "last_error": ""
  }
]
```

## 监控和管理

### 查看失败操作数量

```go
count := db.FaultTolerantMgr.GetFailedOperationCount()
fmt.Printf("当前有 %d 个失败的操作等待重试\n", count)
```

### 清除失败操作（谨慎使用）

```go
// 清除所有失败的操作（仅在确认不需要重试时使用）
db.FaultTolerantMgr.ClearFailedOperations()
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
        Password:     "root",
    }
    
    // 2. 创建数据库实例（自动启用容错）
    db, err := config.CreateDb(0, nil)
    if err != nil {
        log.Fatal(err)
    }
    defer func() {
        db.DisableFaultTolerance()
        db.Close()
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
        log.Printf("保存失败（已记录，将在重连后重试）: %v", err)
    }
    
    // 5. 程序继续运行，容错管理器在后台工作
    time.Sleep(1 * time.Hour)
}
```

## 注意事项

1. **持久化路径**：确保应用有权限写入持久化目录
2. **磁盘空间**：失败操作会占用磁盘空间，定期检查并清理
3. **重试次数**：默认最多重试3次，可根据需要调整
4. **性能影响**：健康检查和重试机制会占用少量资源，但影响很小
5. **数据一致性**：重试机制不保证事务一致性，需要应用层处理

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
2. 失败操作是否达到最大重试次数
3. SQL 语句和参数是否正确
4. 查看 `failed_operations.json` 文件内容

### 问题：进程仍然崩溃

**检查**：
1. 是否在所有数据库操作中添加了 panic recovery（已自动添加）
2. 是否有其他未处理的 panic
3. 查看完整的错误堆栈

## 最佳实践

1. **始终启用容错**：在生产环境中始终启用容错管理器
2. **监控失败操作**：定期检查失败操作数量，及时处理异常
3. **配置合理的重试次数**：根据业务需求设置合适的重试次数和间隔
4. **日志监控**：监控容错管理器的日志，及时发现问题
5. **定期清理**：定期清理过期的失败操作（如果确认不需要重试）
