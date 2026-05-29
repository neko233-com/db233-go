# Changelog

## [v0.1.0] - 2026-05-30

### Added（游戏逻辑服高性能 + 数据不丢）

- **FindByIds** — 单表 IN 批量查询，可配置分块
- **SaveBatchUpsert / UpdateBatchUpsert** — 批量 UPSERT，可配置分块
- **FindByIdConcurrent** — 登录多表并发加载（35 表 → ~1 RTT）
- **SaveBuffered + WriteBuffer** — 高频写异步合并刷盘
- **PlayerSession + SessionRepository** — Session L1 缓存（读内存 / 写 dirty）
- **LocalWriteJournal (WAL)** — 先落盘 fsync 再写库，云 DB 故障本地恢复
- **EntityTypeRegistry** — WAL 回放实体类型注册
- **CrudPerformanceSettings** — 启动时外部 JSON 配置（连接池/分块/并发/WAL）
- **InitGameDb** — 游戏服一站式初始化
- **RegisterDbForConnectionPool** — 连接池参数应用

### Changed

- **FaultTolerantManager** — 默认无限重试，实体 JSON 回放，永不丢弃失败写
- **Save** — 绑定 WAL 时走耐久 UPSERT 路径
- **NewBaseCrudRepository** — 自动继承 `db.WriteJournal`

### Fixed

- WAL 同主键重复追加 → 合并为最新版本
- WAL 回放 / UPSERT 在无 DB 连接时安全降级，不 panic
