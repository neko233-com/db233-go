# Changelog

All notable changes to **db233-go** are documented here.

## [v1.2.5] - 2026-07-23

**清理事务未知结果 fail-closed** — `PrimaryKeyResetBarrier.FailClosed` 在提交结果不确定时保持 managed write 全局阻断，防止旧状态重新写入。

## [v1.2.4] - 2026-07-23

**单主键安全清理屏障** — 新增 `BeginPrimaryKeyReset`；在线清理玩家前丢弃对应 Session、WriteBuffer、WAL 和失败队列，防止事务删除后旧快照复活。

## [v1.2.3] - 2026-07-23

**严格单表恢复版本** — 当前 Entity 表已绑定版本时，无版本旧 WAL/失败操作也视为不一致，禁止升级后静默解释旧快照。

## [v1.2.2] - 2026-07-23

**单表结构版本与有界恢复** — 自动维护每张 Entity 表的结构版本；WAL/失败队列版本不一致直接失败，默认 2 次后写入 durable dead-letter 并逐条 ERROR。

## [v1.2.1] - 2026-07-23

**破坏性 DDL 精确授权** — 改列、删列、替换索引和删索引除操作开关外，还必须显式点名表与对象，防止一次配置误处理全库历史漂移。

## [v1.2.0] - 2026-07-23

**Entity 生产升级生命周期** — 在自动建表基础上增加不可变的 Go 数据迁移、迁移后校验、显式收缩和数据库版本查询。

- 新增 `AutoMigrateEntityLifecycle`，统一编排 PreSchema、DataMigration、FinalizeSchema 和最终验证。
- 新增 `EntityDataMigration`，每个 `Up + Verify + 审计记录` 在同一事务提交。
- 使用 MySQL advisory lock 防止多个实例并发迁移；未知历史版本和已应用定义变更默认 fail-fast。
- 新增 `GetEntityMigrationState`，提供全局版本、各 Entity 版本、应用数量和最后迁移时间。
- 数据迁移回调拒绝 DDL/事务控制；删列、删索引必须通过显式 Finalize 权限。

## [v1.1.0] - 2026-07-22

**生产一致性与生命周期加固** — 完善严格错误传播和事务能力，并用数据库代次屏障阻止清库后的旧 Session、缓冲与恢复队列重新写回历史数据。

### v1.0.9 → v1.1.0 量化结论

结论：v1.1.0 是**稳定性优先**版本，不应宣传为纯吞吐升级。

- 业务目标“高频内存改状态，延迟后只刷最终状态”在 v1.0.9 已具备跨 Session 按表合并；v1.1.0 相对 v1.0.9 的 SQL 数降幅约为 **0%**。
- 若和“每次状态变化直接写库”相比，100 玩家各更新 100 次、同表且单批容量不小于 100 时，可从 10,000 次 SQL 合并为 1 次，数据库写压力约降 **99.99%**。合并 SQL 按 1 次计数；实际分块时按 chunk 数计数。
- v1.1.0 为每次内存态写入制作不可变快照，避免调用方继续修改同一指针导致刷盘竞态。代价是 PlayerSession.Put CPU/分配增加；本机仍约 **190 万次状态更新/秒**。
- 100 玩家最终状态合并 Flush 与 50 Entity 批量 UPSERT 基本持平；关服 Flush 因严格排空、快照和错误传播变慢，但本地 MySQL 的 100 Session 中位数仍低于 3 ms。

#### 同机实测

测试日期：2026-07-23。Windows amd64、Go 1.26.5、本地 loopback MySQL。精确标签 v1.0.9、v1.1.0；原生基准各 5 轮，完全相同的“100 玩家 × 100 状态覆盖 + 最终 Flush”测试各 3 轮。表内为各轮中位数；数值只用于估值，不承诺其它硬件、远程数据库或不同实体大小得到相同比例。

| 场景 | v1.0.9 | v1.1.0 | 变化 | 解释 |
|---|---:|---:|---:|---|
| 50 Entity 批量 UPSERT | 0.518 ms | 0.523 ms | 延迟 +1.0% | 基本持平 |
| 100 玩家 × 100 次内存状态覆盖 | 1.578 ms | 5.248 ms | 延迟 3.33×；吞吐 -69.9% | 新版每次写入制作不可变快照 |
| 上述 100 玩家最终状态 Flush | 3.093 ms | 3.151 ms | 延迟 +1.9% | 基本持平 |
| 原生跨 Session 合并 Flush | 0.534 ms | 0.699 ms | 延迟 +30.9% | 测试小于 1 ms，容易受调度抖动影响 |
| 关服 FlushAll 100 Session | 0.635 ms | 2.724 ms | 延迟 4.29× | 新版执行严格排空、快照和错误检查 |

原生 v1.0.9 基准会忽略部分操作错误，v1.1.0 基准会验证并立即失败，因此旧版数字偏乐观。综合估值：

- **数据库吞吐：约 -2%～+2%，可视为持平。**
- **状态写入 CPU 吞吐：约下降 70%，绝对吞吐仍约 190 万次/秒。**
- **数据库 SQL 压力：相对 v1.0.9 基本不变；相对逐状态直写可下降最高约 99.99%。**
- **生产收益：不是更多 QPS，而是最终状态正确、故障时可恢复、清库后旧数据不回灌、关闭时可证明已排空。**

#### 稳定性修复统计

v1.0.9..v1.1.0 可归为 **20 个可独立验证的稳定性/一致性故障域**。这是故障域数量，不是 GitHub issue 数，也不伪造“稳定性提升百分比”：

1. 数据库 generation 清库屏障。
2. 旧 Session、WriteBuffer 与恢复回调跨 generation 拒写。
3. WAL manifest 与逐条 generation 隔离。
4. 旧版、错代、损坏恢复文件隔离。
5. Db.Close() 后台停止、dirty 数据和恢复队列严格排空。
6. 同一玩家 Session 并发打开、关闭合并。
7. Entity 不可变快照，阻止指针后续修改污染最终刷盘。
8. WriteBuffer 失败后的 pending 状态恢复。
9. 失败重试队列的持久化、回放与终态错误传播。
10. Prepared Statement 并发 Prepare、淘汰、关闭竞态。
11. 监控、健康检查、插件、数据源与 Session 后台任务重复启停和资源泄漏。
12. Query、Columns、Scan、rows.Err()、Close 全链路严格错误传播。
13. ORM 整数溢出、符号回绕、浮点精度丢失和隐式截断拒绝。
14. 事务 context、Commit、Rollback、panic 与 cancel cause 收敛。
15. 保存点、auto-increment 提交后回填和 Entity hook 重入死锁。
16. Schema 编排锁、并发幂等、DryRun 和最终数据库真实复核。
17. 本地配置未知字段、多文档、超限、符号链接与权限拒绝。
18. 监控/恢复文件 owner-only、fsync、原子替换和旧文件保留。
19. MySQL connector 特殊字符凭据、连接失败与资源关闭。
20. Flush 指标、监控采样有界化及 SQL/错误/配置日志隐私。

回归覆盖从 **218 个测试函数 / 45 个测试文件** 增至 **415 个测试函数 / 81 个测试文件**：新增 **197 个测试函数（+90.4%）**、**36 个测试文件**。另增加 MySQL 集成、100 玩家并发、race detector、随机顺序重复、Windows 和 benchmark gate。

### Added

- `GameDbOptions.DatabaseGeneration`、`BeginDatabaseGenerationTransition` 与 `FailClosed`：以独占屏障覆盖“清理玩家数据 + 更新持久化 epoch”的同一数据库事务；commit-unknown、回滚失败或本地切代失败时保持拒写。
- WAL 与失败重试队列写入代次 manifest 和逐条代次；旧版、错代或损坏的恢复文件会被隔离，无法证明属于当前数据库时禁止自动回放。
- `Db.FlushWriteMetrics()`、`AverageFlushWritesPerSecond()` 与快照 `RateSince`：以低开销原子计数暴露实际 flush SQL/实体、成功/失败和来源；合并 SQL 只计一次，每个 chunk 各计一次，恢复回放计入总压力。
- `Db.AutoMigrateSchema` / `VerifySchema`：统一普通实体批量建表与迁移，提供安全默认权限、DryRun、最终严格验证、代次租约、可取消编排锁、跨实例幂等复核及稳定报告。
- `config.local.json` / `config.local.yaml` 统一严格加载：支持 JSON/YAML、未知字段与多文档拒绝、1 MiB 上限、符号链接防护，并在 Unix 强制 `0600` 或更严格权限。
- 真实 MySQL 事务回归和 100 玩家并发生产路径测试，覆盖每玩家 100 次内存态更新、跨 Session 合并为单条 SQL、WAL、最终状态、指标与完整关闭。
- GitHub Actions 门禁：Ubuntu + MySQL、随机顺序重复测试、race detector、benchmark gate 和 Windows 测试。

### Changed

- 严格 ORM 拒绝整数溢出、符号回绕、浮点精度丢失和隐式截断，支持 `sql.Scanner` / `sql.Null*`，并完整传播 Query、Columns、Scan、`rows.Err()` 与 Close 错误。
- 事务 Repository、保存点、context 与 auto-increment 回填统一使用 fail-closed 语义；操作始终绑定当前 `sql.Tx`，错误链保留原始 cause，回填只在确认提交后生效。
- Session 同一玩家并发打开/关闭合并，缓存、SQL 模板、ORM 计划与监控样本保持有界；`Db.Close()` 会停止后台任务、刷写数据并汇总资源关闭错误。
- MySQL 驱动升级到 v1.10.0；连接配置通过 driver connector 构造，用户名或密码含特殊字符时不再依赖手工拼接 DSN。

### Fixed

- 修复清库与正在执行的 Session、WriteBuffer、WAL 回放或失败重试并发时，旧代数据可能穿越清库边界的问题。
- 修复批量/事务写入中的标识符校验、混合 Entity 契约、RowsAffected、WAL 删除和失败记录错误传播缺口。
- 修复 Prepared Statement 缓存并发准备、淘汰与关闭之间的竞态，并避免慢速 `Prepare` 长时间占用全局锁。
- 修复监控、健康检查、配置热加载、插件、数据源和 Session 后台任务的重复启动/停止、锁内外部调用及资源泄漏风险。
- 修复监控 JSON/text 与指标导出使用宽权限、非原子覆盖的问题；现统一以 owner-only 临时文件、fsync 和原子替换写入，失败保留旧文件。

### Security

- SQL 日志与慢查询默认只记录有限 SQL 操作类别；包级运行时日志的裸字符串参数统一变为类型摘要；错误摘要仅含动态类型与有限分类，不记录 SQL hash、文本长度、参数、表名或玩家标识。完整 SQL 仅在显式 opt-in 后记录，参数始终不记录。
- 配置日志不再输出秘密值；MySQL 凭据不经字符串 DSN 拼接；Repository 动态表名、列名和保存点名称均执行严格标识符校验。
- 凭据门禁覆盖 tracked/untracked 文件、常见高置信度令牌、带凭据 URL/远端 DSN 与非本机 IP；发布脚本只允许从同步且 CI 全绿的 `main` 创建不可变精确标签。

### Upgrade Notes

- 有状态生产服升级前必须优雅停服并排空旧 WAL/失败队列；启动时先从数据库读取持久化 epoch，再把非空值传给 `DatabaseGeneration`。安全清库流程见 [docs/DATABASE_GENERATION.md](docs/DATABASE_GENERATION.md)。
- 玩家表删除与 epoch 更新必须在同一事务内完成；数据库身份、租约和 epoch 元数据表不得加入玩家清档集合。
- 既有方法签名保持兼容，但严格路径与关键写入现在会返回过去可能被忽略的错误；调用方必须检查并传播返回值。若调用方对 `GameDbOptions`、`JournalEntry` 或 `FailedOperation` 使用未命名字段复合字面量，需改为命名字段，以接收本版本新增的 generation 字段。
- Unix 上的本地凭据文件必须先执行 `chmod 600 config.local.json`（YAML 同理）；权限更宽、符号链接或超过 1 MiB 的配置将 fail closed。
- 本版本不要求修改既有玩家表 schema；性能热路径继续受现有 benchmark gate 约束。

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

- 集成测试仅接受 `DB233_TEST_DSN` 或未跟踪的 `config.local.json`，并强制连接 loopback/本机 Unix socket；CI 凭据按运行动态生成。
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
