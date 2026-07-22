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
    "sessionFlushIntervalJitterPct": 10,
    "sessionFlushMaxWorkers": 8,
    "sessionFlushMergeByTable": true,
    "shutdownFlushMaxWorkers": 8,
    "shutdownFlushWaveIntervalMs": 20,
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
opts.DatabaseGeneration = dataEpoch.EpochID // 先从数据库持久化元数据读取；生产必填
opts.EntityTypes = []db233.IDbEntity{ /* 全表 */ }
opts.CacheableEntities = []db233.CacheableEntitySpec{
    {Prototype: &PlayerBaseEntity{}},
    {Prototype: &PlayerBagEntity{}, MaxInstances: 8000},
}
sessionRepo, err := db233.InitGameDb(db, dbConfig, opts)
if err != nil {
    return err
}
// shutdown 路径停止业务/raw SQL 准入后执行：
// return db.Close() // 必须把聚合错误传播给进程监督器
```

启动必须先完成 schema/数据库身份校验，再读取或创建持久化 epoch，最后调用 `InitGameDb`。epoch 读取失败必须退出。清库/重建时必须更换 epoch，并使用 `BeginDatabaseGenerationTransition` 覆盖“删除玩家表 + 更新 epoch”的同一事务；完整流程见 [数据库代次与安全清库](./DATABASE_GENERATION.md)。

---

## 4. 运行时

```go
// 登录
session, err := sessionRepo.OpenSession(playerId, loginTypes)
if err != nil { return err }
// 读
bag := session.Get(&PlayerBagEntity{}).(*PlayerBagEntity)
// 写
bag.Gold += 100
if err := session.MarkDirty(bag); err != nil { return err }
// 下线
if err := sessionRepo.CloseSession(playerId); err != nil { return err }
// 全局 entitysave
if err := repo.UpdateBatchUpsert(pendingSameTable); err != nil { return err }
// 多 PK 读
m, _ := repo.FindByIdsMap(ids, &PlayerBagEntity{})
```

---

## 5. entityCache

| 字段 | 推荐 |
|------|------|
| `sessionFlushIntervalMs` | 60000（0=仅下线刷） |
| `sessionFlushIntervalJitterPct` | 10（±10% 抖动，避免整点齐刷） |
| `sessionFlushMaxWorkers` | 8（定时刷 / CloseSession 并发写库上限） |
| `sessionFlushMergeByTable` | true（定时 tick 跨玩家按表合并 UPSERT） |
| `shutdownFlushMaxWorkers` | 8–16（关服 `FlushAll` 每波并发数） |
| `shutdownFlushWaveIntervalMs` | 20–50（关服波次间隔，削峰 DB） |
| `negativeCacheEnabled` | false（按需 Session 级开） |
| `maxSessions` | 在线上限 × 1.2 |

### 刷盘行为摘要

| 场景 | 行为 |
|------|------|
| 定时刷写 | 收集全部 dirty → **按表合并** → 有界 worker 批量 UPSERT |
| 玩家下线 `CloseSession` | 单 Session 刷盘，受 `sessionFlushMaxWorkers` 限流 |
| 关服 `db.Close()` / `FlushAll` | 全量 dirty 合并 → **分波**刷盘（波内并发 + 波间 sleep）→ 再刷 WriteBuffer |
| WriteBuffer | 全局队列合并；`writeBufferMaxBatchSize` 限制单次 Flush 行数 |

---

## 6. 连接池（2000 在线目标）

| 阶段 | maxOpenConns |
|------|--------------|
| 当前压测调优 | 32–50 |
| 目标 2000 | 50–100 + RDS 规格 |

暴露 `Stats().WaitCount` / `WaitDuration` 到 `/api/monitor`。

### Flush 写库压力

```go
previous := db.FlushWriteMetrics()
// 按业务监控周期再次采样；不要求库内启动额外统计协程。
time.Sleep(10 * time.Second)
current := db.FlushWriteMetrics()
window := current.RateSince(previous)

actualFlushSQLPerSecond := window.AttemptedSQLPerSecond
lifetimeAverage := db.AverageFlushWritesPerSecond()
sessionSQL := current.BySource[db233.FlushWriteSourceSession].AttemptedSQL
```

- `AttemptedSQL` = 真正进入 `database/sql Exec` 的 flush 次数；失败也计入 DB 压力。
- 同表合并 SQL 计 1 次；每个 `batchUpsertChunkSize` chunk 各计 1 次，不按玩家/实体数重复计。
- 总量包含 Session、WriteBuffer、显式状态 flush、WAL 回放和失败队列回放；`BySource` 可拆分。
- 只统计 db233 管理的状态 flush；调用方直接使用 `DataSource` / `GetDataSource`、raw SQL 或事务 Repository 的写入不在此指标内。
- SQL/WAL 构建、序列化和 Prepare 在 Exec 前失败不计。指标不保存 SQL、参数、错误或玩家 ID。

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
- [ ] DatabaseGeneration 来自数据库持久化 epoch；清库使用两阶段屏障
- [ ] 池与 login_peak 匹配

---

## 10. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-07-22 | v1.1.0：持久化数据库代次与安全清库屏障 |
| 2026-05-30 | 初版 v1.0.0 |
