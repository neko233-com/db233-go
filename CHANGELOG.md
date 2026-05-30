# Changelog

All notable changes to **db233-go** are documented here.

## [v1.0.3] - 2026-05-30

**Session 刷盘与压测** — 有界 worker、跨玩家合并 UPSERT、关服分波、完整测试与刷盘对比。

### Added

- **Session 刷盘**：有界 worker 池、跨 Session 按表合并、定时抖动、关服分波 `FlushAll`
- **WriteBuffer**：`writeBufferMaxBatchSize` 分片刷盘；失败回滚 pending
- **测试**：`pkg/db233` flush/WriteBuffer 单测 + `tests/session_flush_*` 集成 + `benchmarks/flush_compare_test.go`
- **压测脚本**：6 阶段（含 pkg 单测 + 刷盘对比）

### Changed

- 定时 `FlushAllDirty` 重叠 tick 跳过（DB 慢时不堆叠）

---

## [v1.0.2] - 2026-05-30

**文档完善** — 修正版本号、补充 FAQ / 对比 / 概览，优化检索可读性。

### Added

- **docs/** — 文档中心、FAQ、COMPARE-ORM、OVERVIEW
- README 折叠式 FAQ、文档内链

### Fixed

- README 中错误的 v1.2.0 / v1.1.0 版本表述（实际发版为 v1.0.x）

---

## [v1.0.1] - 2026-05-30

**性能正式化** — ORM 直扫、对象池、冷启动预热、发版 benchmark 一条龙。

### Added

- **FastOrmScan / OrmScanPlan** — 元数据直扫字段，单次 PK 读对标/优于 GORM
- **EnableAllocPool** — 字段 map、批量 UPSERT scratch、IN 占位符、Builder/JSON Buffer、WriteBuffer 切片池
- **EnableRowMapPool** — Query 路径 Scan 中间缓冲池化（返回 map 仍独立拷贝）
- **WarmGameDb** — InitGameDb 冷启动：连接池 + 元数据 + Stmt + 扫描计划
- **PreparedStmtCache / UpdateBatch 真批量 / FindByIdsMap** — 缺口补齐
- **scripts/run-benchmark.ps1 / .sh** — 四阶段发版压测一键脚本
- **benchmarks/TestReleaseGate** — 发版门禁（框架对比 + 稳定性）
- **docs/BENCHMARK.md** — 压测标准与阈值

### Changed

- 默认性能配置开启 `enableFastOrmScan`、`enableAllocPool`、`enableColdStartWarmup`
- README 框架对标表与内存/GC 对比正式化

### Fixed

- 嵌入结构体 `ColumnToFieldPath` 索引路径
- 复杂字段（slice/map/TEXT）快速 ORM 间接 Scan + convertValue

---

## [v1.0.0] - 2026-05-30

**首个生产就绪版本** — 面向有状态游戏逻辑服：单库单写、登录全量加载、Session 内存读、延迟刷写、WAL 数据不丢。

### Added

#### 游戏服核心
- **InitGameDb** — 一站式初始化（WAL、写缓冲、连接池、Session 缓存、实体注册）
- **FindByIdConcurrent** — 登录多表并发加载（35 表 → 约 1 个 RTT 量级）
- **FindByIds / SaveBatchUpsert / UpdateBatchUpsert** — 批量读写，可配置分块
- **SaveBuffered + WriteBuffer** — 高频写异步合并

#### Session 实体缓存（L1）
- **EntityCacheSettings** — 开关、LRU、`maxSessions`、按类型实例上限、定时刷写间隔
- **负缓存** — 默认 **关闭**；`negativeCacheEnabled` / `session.SetNegativeCacheEnabled()` 动态开关
- **CacheableEntityRegistry** — `XxxEntity` 白名单（JPA 风格）
- **延迟写库** — 默认 1 分钟刷 dirty；`sessionFlushIntervalMs=0` 仅下线/关服落库
- **FlushAll / CloseSession / db.Close()** — 强制按 playerId PK 刷写

#### 数据不丢
- **LocalWriteJournal (WAL)** — fsync 后写库，云 DB 故障本地恢复
- **EntityTypeRegistry** — WAL 回放实体类型
- **FaultTolerantManager** — 无限重试，实体 JSON 回放，不丢弃失败写

#### 连接与配置
- **连接池** — `database/sql` + `RegisterDbForConnectionPool`（等价 HikariCP 参数）
- **WarmConnectionPool** — 启动预热，降低首条 SQL 冷延迟
- **config.local.json** — 本地 RDS 连接（`*.local.json` 已 gitignore）
- **OpenDbFromLocalConfig / LoadLocalDbConfigFromFile** — 本地开发一键连库
- **CrudPerformanceSettings** — 外部 JSON（连接池/分块/并发/WAL/entityCache）

#### 性能（对标 / 超越 Spring JPA）
- **SaveBatchUpsert 按表自动分组** — 混合实体类型一次调用，按表批量 UPSERT（减少 RTT）
- **UpdateBatch 真批量** — 内部走 `SaveBatchUpsert`（跨表自动分组），勿再循环 `Update`
- **FindByIdsMap** — `map[primaryKey]IDbEntity` 一行封装，基于 `FindByIds`
- **PreparedStmtCache** — `*sql.Stmt` LRU + TTL；`enablePreparedStmtCache` / `stmtCacheSize` / `stmtCacheTTLSeconds`
- **FastOrmScan + RowMapPool** — 实体读直扫字段（无 map 中转）；Query 路径 Scan 缓冲池化
- **WarmGameDb 冷启动** — `enableColdStartWarmup` / `poolWarmupRounds`；InitGameDb 自动预热
- **EnableAllocPool** — 字段 map / 批量 UPSERT scratch / IN 占位符 / JSON Buffer / WriteBuffer 切片池
- **ResolveEntityTableName** — 表名缓存，Session 读路径零反射
- **EntityCacheSettings 无锁 Snapshot** — 热路径读配置无 mutex
- **Session 正/负缓存分离** — `entities` + `absentTables`，语义清晰、易维护
- **测试辅助** — `SaveEntityCacheSettings` / `SetEntityCacheKey` / `EnableSessionNegativeCache`（压测不 ApplyFull）

#### 测试
- **TestPerfStability_Short** — 短时压测（Ping 延迟、缓存加速、批量写、Session 开闭循环）
- **benchmarks/** — 与 database/sql、sqlx、GORM 对比 + 突发流量稳定性
- **TestTrafficBurst_*** — 主包连接池抖动 / 混合读写稳定性

### Changed

- **InitGameDb** 返回 `*SessionRepository`，绑定 `db.SessionRepo`
- **CreateTestDb** 优先使用 `config.local.json`，无文件回退本地 MySQL

### Fixed

- **CreateDataSource** Ping 失败时错误信息被 shadow 覆盖（`%!w(<nil>)`）
- **LocalWriteJournal** 首次写入前自动 `MkdirAll`，修复 WAL 目录不存在
- **PlayerSession.Load** 持锁时调用负缓存判断导致死锁
- **EntityCacheSettings.Set** 回调在锁内执行导致潜在死锁
- **SessionRepository.Stop()** 未启动定时刷写时不再永久阻塞
- WAL 同主键重复追加 → 合并为最新版本
- WAL 回放 / UPSERT 无 DB 连接时安全降级，不 panic

### 文档

- [docs/db233优化落地对照.md](docs/db233优化落地对照.md) — 压测优化建议 vs v1.0.0 落地状态
- [docs/config-game-server-stateful.md](docs/config-game-server-stateful.md) — 有状态游戏服配置
- [docs/config-web-server.md](docs/config-web-server.md) — Web/API 服配置
- [docs/FAULT_TOLERANCE.md](docs/FAULT_TOLERANCE.md) — WAL / 容错

### 性能压测（如何运行）

```bash
go test ./tests/ -run TestPerfStability_Short -timeout 90s -v
```

| 场景 | 建议 |
|------|------|
| 在线读 | 走 `session.Get` / `GetOrLoad`，勿 `repo.FindById` |
| 登录 | `OpenSession` + `FindByIdConcurrent` |
| 高频写 | `entityCache` 延迟写 + 定时/下线 `Flush` |
| 批量落库 | `UpdateBatchUpsert` 代替循环 `Save` |
| MySQL 延迟 | 同地域 RDS + `WarmConnectionPool` + 合理 `maxOpenConns` |

---

## [v0.1.2] - 2026-05-30

### Added（Session 实体缓存 + 延迟刷写）

- EntityCacheSettings、CacheableEntityRegistry、Session LRU、延迟刷写、FlushAll

### Fixed

- SessionRepository.Stop 阻塞问题

## [v0.1.0] - 2026-05-30

### Added（游戏逻辑服高性能 + 数据不丢）

- FindByIds、SaveBatchUpsert、FindByIdConcurrent、PlayerSession、LocalWriteJournal、InitGameDb
