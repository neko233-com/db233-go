# db233-go 配置建议 · Game Server Stateful（有状态逻辑服）

> 适用：单库单写、登录全量加载、在线内存驻留、延迟刷写的游戏逻辑服。  
> 维护：db233 发版或压测（2000 在线）结论变更时更新。

---

## 1. 原则

| 原则 | 说明 |
|------|------|
| 登录 | `OpenSession` + `FindByIdConcurrent` |
| 在线读 | `session.Get` / `GetOrLoad`，禁止热路径 `FindById` |
| 在线写 | `session.Put` → dirty；定时/下线 `Flush` |
| 落盘 | `UpdateBatchUpsert`（非 `UpdateBatch`） |
| 不丢 | WAL + FaultTolerant |
| 削峰 | db233 提效 + 游戏服 `login_peak` / `entitysave` |

---

## 2. `config/db233-performance.json`

```json
{
  "concurrentMaxWorkers": 16,
  "findByIdsChunkSize": 500,
  "batchUpsertChunkSize": 200,
  "enableSqlTemplateCache": true,
  "writeBufferEnabled": true,
  "writeBufferFlushIntervalMs": 100,
  "writeBufferMaxBatchSize": 200,
  "maxOpenConns": 50,
  "maxIdleConns": 10,
  "connMaxLifetimeSec": 3600,
  "connMaxIdleTimeSec": 600,
  "enableLocalJournal": true,
  "localJournalPath": "./data/db233_journal",
  "entityCache": {
    "enabled": true,
    "evictionPolicy": "lru",
    "maxSessions": 10000,
    "sessionFlushIntervalMs": 60000,
    "flushOnEvict": true,
    "negativeCacheEnabled": false,
    "entityTypeLimits": { "PlayerBagEntity": 8000 }
  }
}
```

### 与游戏服 YAML 对齐

| 游戏服 | db233 | 建议 |
|--------|-------|------|
| `database.max_open_conns` | `maxOpenConns` | 与 `login_peak.worker_count` 同量级 |
| `entitysave` debounce | `writeBuffer` + 按表 Upsert | 避免双写同一表 |

---

## 3. 启动

```go
opts := db233.DefaultGameDbOptions()
opts.PerformanceConfigPath = "config/db233-performance.json"
opts.EnableLocalJournal = true
opts.EnableWriteBuffer = true
opts.EnableEntityCache = true
opts.EntityTypes = []db233.IDbEntity{ /* 全表 */ }
opts.CacheableEntities = []db233.CacheableEntitySpec{
    {Prototype: &PlayerBaseEntity{}},
    {Prototype: &PlayerBagEntity{}, MaxInstances: 8000},
}
sessionRepo, err := db233.InitGameDb(db, dbConfig, opts)
defer db.Close()
```

---

## 4. 运行时

```go
// 登录
session, _ := sessionRepo.OpenSession(playerId, loginTypes)
// 读
bag := session.Get(&PlayerBagEntity{}).(*PlayerBagEntity)
// 写
bag.Gold += 100; session.Put(bag)
// 下线
_ = sessionRepo.CloseSession(playerId)
// 全局 entitysave
repo.UpdateBatchUpsert(pendingSameTable) // 或 UpdateBatch（等价真批量）
// 多 PK 读
m, _ := repo.FindByIdsMap(ids, &PlayerBagEntity{})
```

---

## 5. entityCache

| 字段 | 推荐 |
|------|------|
| `sessionFlushIntervalMs` | 60000（0=仅下线刷） |
| `negativeCacheEnabled` | false（按需 Session 级开） |
| `maxSessions` | 在线上限 × 1.2 |

---

## 6. 连接池（2000 在线目标）

| 阶段 | maxOpenConns |
|------|--------------|
| 当前压测调优 | 32–50 |
| 目标 2000 | 50–100 + RDS 规格 |

暴露 `Stats().WaitCount` / `WaitDuration` 到 `/api/monitor`。

---

## 7. 压测指标（文档基线）

| 指标 | 目标 | 参考 |
|------|------|------|
| 峰值在线 | ≥2000 | ~376 |
| 登录 p99 | <2s | ~123s |
| 稳定期写 | ms 级 | Fishing ~0.005ms ✅ |

回归：`benchmarks/` + 游戏服 `一键压测游戏逻辑服.ps1`。

---

## 8. 优化建议落地

见 [db233优化落地对照.md](./db233优化落地对照.md)。

---

## 9. 发版检查

- [ ] InitGameDb + CacheableEntities 完整
- [ ] 在线读走 Session
- [ ] 不用 UpdateBatch 落盘
- [ ] WAL 可写、关服 FlushAll
- [ ] 池与 login_peak 匹配

---

## 10. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-05-30 | 初版 v1.0.0 |
