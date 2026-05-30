# db233-go 配置建议 · Web Server（无状态）

> 适用：HTTP/API、管理后台、中心服只读接口等**无玩家 Session 常驻**进程。  
> 维护：db233 发版或 Web 服压测变更时更新。

---

## 1. 原则

- **不启用** `entityCache` / `SessionRepository` / `InitGameDb`
- 连接池 + `FindByIds` / `SaveBatchUpsert`
- 一般**不需要** WAL

---

## 2. `config/db233-performance.json`

```json
{
  "concurrentMaxWorkers": 8,
  "findByIdsChunkSize": 500,
  "batchUpsertChunkSize": 100,
  "enableSqlTemplateCache": true,
  "writeBufferEnabled": false,
  "maxOpenConns": 32,
  "maxIdleConns": 8,
  "connMaxLifetimeSec": 3600,
  "connMaxIdleTimeSec": 600,
  "entityCache": { "enabled": false }
}
```

---

## 3. 启动模板

```go
dbConfig := cfg.ToDbConnectionConfig()
dbConfig.MaxOpenConns = 32
db, _ := dbConfig.CreateDbWithoutFaultTolerance(0, nil)
db233.RegisterDbForConnectionPool(db)
_ = db233.WarmConnectionPool(db.DataSource, 2)
repo := db233.NewBaseCrudRepository(db)
```

勿调用 `InitGameDb`。

---

## 4. API 选用

| 场景 | API | 避免 |
|------|-----|------|
| 单条 PK 读 | `FindById` | — |
| 多 PK 读 | `FindByIds` / **`FindByIdsMap`** | 循环 `FindById` |
| 插入/更新 | `Save` | — |
| 批量更新 | `UpdateBatch` / `SaveBatchUpsert` | `SaveBatch`(仅 INSERT) |

---

## 5. 连接池

| 规模 | maxOpenConns | maxIdleConns |
|------|--------------|--------------|
| 小型 API | 16–32 | 4–8 |
| 中型 | 32–64 | 8–16 |

监控：`db.DataSource.Stats()`、`ConnectionPoolMonitor`。告警 `WaitCount` 持续上升。

---

## 6. 与游戏服差异

| 项 | Web | Game Stateful |
|----|-----|---------------|
| InitGameDb | 否 | 是 |
| entityCache | false | true |
| WAL | 通常否 | 是 |

见 [config-game-server-stateful.md](./config-game-server-stateful.md)。

---

## 7. 发版检查

- [ ] `entityCache.enabled = false`
- [ ] 批量写用 `SaveBatchUpsert`
- [ ] `config.local.json` 未提交
- [ ] `go test ./tests/ -short` 通过

---

## 8. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-05-30 | 初版 |
