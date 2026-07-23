# db233-go 文档中心

> **db233-go** 是面向有状态游戏逻辑服与高性能 MySQL 场景的 **Go ORM / 数据访问库**（`github.com/neko233-com/db233-go`）。  
> 关键词：Go ORM · MySQL batch UPSERT · Session cache · WAL · GORM alternative · game server database

## 按场景选文档

| 我要… | 文档 |
|--------|------|
| 5 分钟了解是什么、适不适合 | [OVERVIEW.md](./OVERVIEW.md) |
| 和 GORM / sqlx / raw SQL 怎么选 | [COMPARE-ORM.md](./COMPARE-ORM.md) |
| 有状态游戏逻辑服怎么配 | [config-game-server-stateful.md](./config-game-server-stateful.md) |
| 安全清库、重建后防旧数据复活 | [DATABASE_GENERATION.md](./DATABASE_GENERATION.md) |
| 游戏英雄/背包等复杂 map 如何存一列 | [game-complex-json-columns.md](./game-complex-json-columns.md) |
| Web / API 无状态服怎么配 | [config-web-server.md](./config-web-server.md) |
| 压测怎么跑、通过标准 | [BENCHMARK.md](./BENCHMARK.md) |
| 优化建议落地对照 | [db233优化落地对照.md](./db233优化落地对照.md) |
| 常见问题 | [FAQ.md](./FAQ.md) |
| 发版说明 | [../ChangeLog.md](../ChangeLog.md) |

## 核心 API 速查

| 能力 | API / 配置 |
|------|------------|
| 一站式游戏服初始化 | `InitGameDb(db, dbConfig, opts)` |
| 玩家 Session + L1 缓存 | `SessionRepository.OpenSession` → `session.Get` |
| 登录多表并发加载 | `FindByIdConcurrent(playerId, entityTypes, nil)` |
| 批量写（单 SQL UPSERT） | `UpdateBatchUpsert` / `SaveBatchUpsert` |
| 批量读 | `FindByIds` / `FindByIdsMap` |
| 本地凭据（勿提交 Git） | `config.local.json` / `config.local.yaml` |
| 数据库代次屏障 | `DatabaseGeneration` / `BeginDatabaseGenerationTransition` |
| 统一建表与迁移 | [SCHEMA_MIGRATION.md](SCHEMA_MIGRATION.md) |
| Entity 版本化数据升级 | [ENTITY_DATA_MIGRATION.md](ENTITY_DATA_MIGRATION.md) |
| 性能 JSON | `config/db233-performance.*.example.json` |
| 私密原子监控导出 | `MonitoringReportGenerator.ExportReport` / `MetricsCollector.ExportToFile` |

## 本地凭据安全

真实 `host` / `password` **只能**写在 gitignore 的本地文件中：

- `config.local.json` 或 `config.local.yaml`（见根目录 `*.example` 模板）
- 推送前：`./scripts/check-secrets.ps1`

监控报告与指标导出可能包含数据库名、指标名、标签和告警等业务标识。db233-go 使用 owner-only 文件权限与同目录原子替换；生产仍须选择受控目录，不得提交 Git 或公开分发。

## 维护者

- GitHub 仓库 **Description / Topics** 建议：`go` `golang` `orm` `mysql` `game-server` `gorm` `database` `upsert` `session-cache` `wal`
- 发版前：`version.txt`、FAQ/`go get` 示例版本号、 `./scripts/run-benchmark.ps1`
- 文档示例统一用占位符 `your-rds-host.mysql.rds.aliyuncs.com`，禁止写入真实凭据
