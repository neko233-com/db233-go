# Changelog

All notable changes to **db233-go** are documented here.

## [v1.0.10] - 2026-07-22

**严格错误传播与事务能力** — 为正确性关键 Entity 读取和跨分块原子写入补齐 fail-closed 契约。

### Added

- `StrictQueryer`、`ExecuteQueryStrictContext` 与 `ExecuteQueryTypedStrict`：Query、Columns、Scan、字段转换、`rows.Err()` 和 Close 失败统一返回 error，禁止交付部分结果。
- `StrictEntityRepository` / `StrictCrudRepository`：提供 context-aware 严格 Entity 读取，并在整批成功后调用加载钩子。
- `BeginContext`、`ExecuteInTransactionContext`、`WithTransactionContext`：继承调用方 context，保留标准错误链。
- `TransactionCrudRepository`：串行地在同一 `sql.Tx` 上执行严格读取、Save、分组分块 UPSERT 与条件删除；auto-increment 主键在 Commit 成功后回填，回滚到保存点会丢弃其后的待回填动作。
- 不依赖真实 MySQL 的 scripted driver 回归测试与 ORM mapping microbenchmark。

### Fixed

- 修复 `Begin` 返回后立即取消事务 context、导致事务被自动回滚的问题。
- Commit/Rollback 无论成功失败都会清理 manager 本地状态；终态错误会保留 cancel/deadline cause。
- callback error 与 rollback error 通过 `errors.Join` 同时保留；callback panic 会先尝试回滚，再原样 re-panic。
- `Db233Exception` 新增 `Unwrap()`，支持 `errors.Is/As` 检查底层 context、驱动和事务错误。
- auto-increment `SaveBatchUpsert` 的 `SerializeBeforeSaveDb()` 每个 Entity 只调用一次。
- Entity 序列化/反序列化 hook 在事务非重入锁外执行，避免 hook 重入同一 manager 或 Repository 时自死锁；SQL 执行仍保持串行。

### Compatibility

- 不修改 schema、主键、字段或持久化格式，现有数据无需重置。
- 旧 `DbApi`、`CrudRepository`、`ExecuteQuery*`、`Begin` 和 `WithTransaction` 保持编译兼容；严格语义由新窄接口显式启用。
- 事务 Repository 不使用 WAL、WriteBuffer 或 DB Statement 缓存；目标表须使用事务引擎，Unit of Work 仅允许事务性 DML。
- legacy `TransactionManager.Query*` 为保持 `*sql.Rows` 返回类型，只串行保护 Rows 创建阶段；Rows 关闭前不得与同一 manager 的其他事务操作并发混用。
- 本版本门禁不连接真实 MySQL；MySQL/InnoDB、DDL 隐式提交、驱动错误码和连接池 cancel 后复用行为仍属于未验证边界。

## [v1.0.7] - 2026-06-03

**测试连接固定本地 MySQL** — 普通集成测试不再优先读取 `config.local.json`，避免误连远程数据库。

### Changed

- `CreateTestDb(t)` 固定使用 `127.0.0.1:3306 / root / root / db233_go`，并启用 `parseTime=true`。
- `game_integration_test` 的 `InitGameDb` 配置固定使用本地 MySQL。
- 文档同步说明普通测试只用本地 MySQL；仅显式调用 `CreateTestDbFromLocalConfig(t)` 时读取 `config.local.json`。

---

## [v1.0.6] - 2026-06-03

**复杂 JSON 列容量提示** — 游戏英雄、背包等大 JSON 字段推荐使用 `MEDIUMTEXT`，减少 2MB+ 文本存储风险。

### Changed

- MySQL 自动建表中，`map` / `slice` / `array` / 普通 `struct` 等复杂字段默认 SQL 类型为 `MEDIUMTEXT`。
- 显式 `db_type` 不做隐式转换：写 `TEXT` 就建 `TEXT`，写 `MEDIUMTEXT` 就建 `MEDIUMTEXT`；普通 `string` 未指定 `db_type` 时仍默认 `VARCHAR(255)`。
- 文档强调 `TEXT` 容量较小，英雄等可能达到几 MB 的 JSON 字段应显式使用 `db_type:"MEDIUMTEXT"` 或 `LONGTEXT`。

---

## [v1.0.4] - 2026-06-03

**复杂 JSON 字段回读修复** — 支持游戏英雄、背包等 `map[string]*HeroBo` 复杂字段按单列 JSON 保存和查询回读。

### Added

- **复杂字段回归测试**：`TestHeroCollectionJSONRoundTrip` 覆盖 `map[int]HeroDataBo`、`map[string]*HeroDataBo`、`[]HeroDataBo` 的 `Save -> FindById` round-trip。
- **JSON 默认值辅助方法**：`GetOrCreateDefault` / `ToJSONStringOrDefault` 用于已有 string JSON 列的业务字段同步。
- **游戏接入规范**：新增 [docs/game-complex-json-columns.md](docs/game-complex-json-columns.md)，说明复杂 map/slice 直接存一列，以及 string 影子字段兼容写法。

### Fixed

- ORM 查询映射从 `TEXT` / `[]byte` 读到 `map`、`slice`、`array`、普通 `struct` 字段时，现在会自动 JSON 反序列化，再调用 `DeserializeAfterLoadDb()`。
- 复杂 JSON 列为空字符串时，`map` / `slice` 自动初始化为空容器，避免首次创建玩家数据后回读为 nil。

---

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
