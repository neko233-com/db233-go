# db233-go 是什么？

**db233-go**（模块路径 `github.com/neko233-com/db233-go`）是一个 **Go 语言数据库访问库 / 轻量 ORM**，专为 **有状态游戏逻辑服** 与 **MySQL 高 QPS** 场景设计，也可用于无状态 Web/API 服务。

## 一句话定义

> 登录时把玩家数据加载进 **Session 内存（L1）**，在线读 **零查库**；写操作 **延迟批量 UPSERT** + **本地 WAL**，云数据库故障也不丢数据。

## 解决什么问题？

| 痛点 | db233-go 做法 |
|------|----------------|
| 在线每次读都 hit DB（GORM `First`） | `OpenSession` + `session.Get`，命中 L1 |
| 循环 `Save` / `Update` RTT 爆炸 | `UpdateBatchUpsert` 单条 SQL 多行 |
| 登录串行查 30+ 张表 | `FindByIdConcurrent` 并发多表 |
| 高峰写丢数据 | `LocalWriteJournal`（WAL）+ 无限重试 |
| ORM 反射 + map 中转 GC 高 | `FastOrmScan` 直扫字段 + `EnableAllocPool` |
| 冷启动首包慢 | `WarmGameDb` 预热连接池 / Stmt / 元数据 |

## 适合谁？

**推荐使用：**

- MMORPG / 卡牌 / SLG **有状态逻辑服**（单库单写、按 playerId 分数据）
- 需要 **Session 级缓存** 等价于 Java Hibernate 一级缓存 的 Go 项目
- 需要 **批量 UPSERT**、**FindByIds IN 查询** 的游戏后端

**不强制使用 Session 也可以：**

- 无状态 API 服 — 仅用 CRUD + 命名参数 SQL + 连接池（见 [config-web-server.md](./config-web-server.md)）

## 不适合谁？

- 需要复杂关系映射 / 自动迁移全家桶 → 考虑 GORM + 工具链
- 分库分表中间件已接管 SQL → 可能只需 database/sql
- 强依赖 PostgreSQL 特有 ORM 生态 → 当前 MySQL 路径最成熟

## 30 秒上手

```go
import "github.com/neko233-com/db233-go/pkg/db233"

db, cfg, _ := db233.OpenDbFromLocalConfig("config.local.json")
opts := db233.DefaultGameDbOptions()
opts.EntityTypes = []db233.IDbEntity{&PlayerBaseEntity{}, &PlayerBagEntity{}}
sessionRepo, _ := db233.InitGameDb(db, cfg, opts)

session, _ := sessionRepo.OpenSession(playerId, loginTypes)
bag := session.Get(&PlayerBagEntity{}).(*PlayerBagEntity) // 内存读，不查库
```

## 版本与许可

- 当前稳定版：**v1.0.1**（见 [CHANGELOG.md](../CHANGELOG.md)）
- 许可：**Apache-2.0**

## 相关链接

- [与 GORM / sqlx 对比](./COMPARE-ORM.md)
- [FAQ](./FAQ.md)
- [GitHub 仓库](https://github.com/neko233-com/db233-go)
