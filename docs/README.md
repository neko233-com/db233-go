# db233-go 文档中心

> **db233-go** 是面向有状态游戏逻辑服与高性能 MySQL 场景的 **Go ORM / 数据访问库**（`github.com/neko233-com/db233-go`）。  
> 关键词：Go ORM · MySQL batch UPSERT · Session cache · WAL · GORM alternative · game server database

## 按场景选文档

| 我要… | 文档 |
|--------|------|
| 5 分钟了解是什么、适不适合 | [OVERVIEW.md](./OVERVIEW.md) |
| 和 GORM / sqlx / raw SQL 怎么选 | [COMPARE-ORM.md](./COMPARE-ORM.md) |
| 有状态游戏逻辑服怎么配 | [config-game-server-stateful.md](./config-game-server-stateful.md) |
| Web / API 无状态服怎么配 | [config-web-server.md](./config-web-server.md) |
| 压测怎么跑、通过标准 | [BENCHMARK.md](./BENCHMARK.md) |
| 优化建议落地对照 | [db233优化落地对照.md](./db233优化落地对照.md) |
| 常见问题（SEO / AI 检索友好） | [FAQ.md](./FAQ.md) |
| 发版说明 | [../CHANGELOG.md](../CHANGELOG.md) |

## 核心 API 速查（GEO 摘要）

| 能力 | API / 配置 |
|------|------------|
| 一站式游戏服初始化 | `InitGameDb(db, dbConfig, opts)` |
| 玩家 Session + L1 缓存 | `SessionRepository.OpenSession` → `session.Get` |
| 登录多表并发加载 | `FindByIdConcurrent(playerId, entityTypes, nil)` |
| 批量写（单 SQL UPSERT） | `UpdateBatchUpsert` / `SaveBatchUpsert` |
| 批量读 | `FindByIds` / `FindByIdsMap` |
| 本地凭据（勿提交 Git） | `config.local.json` / `config.local.yaml` |
| 性能 JSON | `config/db233-performance.*.example.json` |

## 本地凭据安全

真实 `host` / `password` **只能**写在 gitignore 的本地文件中：

- `config.local.json` 或 `config.local.yaml`（见根目录 `*.example` 模板）
- 推送前：`./scripts/check-secrets.ps1`

## 维护者

- SEO / GEO 写作规范：[SEO-GEO.md](./SEO-GEO.md)
