# db233-go 常见问题（FAQ）

> 本文采用 **问答格式**，便于搜索引擎与传统 SEO、以及 AI / GEO（Generative Engine Optimization）检索引用。  
> 模块：`github.com/neko233-com/db233-go` · 当前版本：**v1.0.1**

---

## 基础

### db233-go 是什么？

db233-go 是 Go 语言的 **ORM / 数据库访问库**，面向 **有状态游戏逻辑服** 与 **MySQL 高并发** 场景。核心能力包括：**Session 一级缓存**（在线读零查库）、**批量 UPSERT**、**FindByIdConcurrent 登录加载**、**本地 WAL 写不丢**、连接池与冷启动预热。

### db233-go 和 GORM 有什么区别？

GORM 是通用全功能 ORM；db233-go 侧重 **游戏服 Session 内存读 + 真批量写 + WAL**。RDS 实测单次 PK 读 db233 约 **10ms**、GORM 约 **23ms**；在线 Session 读 1000 次 db233 **亚毫秒**、GORM 需循环查库约 **21 秒** 量级。详见 [COMPARE-ORM.md](./COMPARE-ORM.md)。

### db233-go 能替代 database/sql 吗？

可以。db233-go 底层仍使用 `database/sql` 连接池，在其上提供 ORM 映射、批量 SQL 生成、Session 缓存与 WAL。无 Session 场景可当作 **带 UPSERT 的 ORM + 命名参数 SQL** 使用。

### 支持哪些数据库？

**MySQL** 路径最成熟（阿里云 RDS 压测验证）。`DbConnectionConfig` 支持 PostgreSQL 连接串，游戏服特性以 MySQL 为主。

### 如何安装？

```bash
go get github.com/neko233-com/db233-go@v1.0.1
```

```go
import "github.com/neko233-com/db233-go/pkg/db233"
```

---

## 游戏服 / Session

### 什么是有状态游戏逻辑服？db233-go 怎么匹配？

有状态逻辑服在内存中持有玩家 Session，在线期间业务读写在内存完成，定时或下线再落库。db233-go 的 `OpenSession` 登录加载实体到 L1，`session.Get` **不访问数据库**，`CloseSession` / 定时任务刷 dirty。

### Session 缓存和 Redis 缓存有什么区别？

Session L1 是 **进程内、按玩家、带 dirty 写回** 的 ORM 一级缓存，延迟低、与实体生命周期绑定。Redis 适合跨进程共享；db233-go Session 适合 **单逻辑服单写、playerId 亲和** 架构。

### 负缓存（negative cache）是什么？默认开吗？

确认某表对某玩家 **无记录** 后，可选记住「 absent」，避免重复 `SELECT`。默认 **关闭**（`negativeCacheEnabled: false`），避免误用；可按 Session 动态开启。

### 玩家下线数据会丢吗？

不会。下线 `CloseSession` 强制刷 dirty；关服 `FlushAll` / `db.Close()`；写路径可走 **WAL 先落盘** 再写 RDS，失败无限重试。

---

## 性能

### 为什么 db233-go 单次读比 GORM 快？

**FastOrmScan** 用元数据 **直扫结构体字段**，跳过 `map[string]any` 中转；**PreparedStmtCache** 复用 `*sql.Stmt`；**WarmGameDb** 冷启动预热。GORM 还有 schema/clause 链开销。

### UpdateBatch 和 UpdateBatchUpsert 用哪个？

**一律用 `UpdateBatchUpsert` 或 `UpdateBatch`**（二者等价真批量，内部单 SQL UPSERT）。**不要**循环 `Update`。

### FindByIds 返回 slice 还是 map？

两者都有：`FindByIds` 返回 slice；`FindByIdsMap` 返回 `map[primaryKey]IDbEntity`。

### 如何跑性能对比压测？

```bash
./scripts/run-benchmark.ps1
```

规范见 [BENCHMARK.md](./BENCHMARK.md)。

---

## 配置与安全

### 数据库密码放哪里？会进 Git 吗？

**只能**放在 gitignore 的本地文件：

- `config.local.json`
- `config.local.yaml`

仓库内仅有 `*.example` 占位模板。推送前运行 `./scripts/check-secrets.ps1`。

### 游戏服 performance JSON 放哪？

复制 [config/db233-performance.game-server-stateful.example.json](./config/db233-performance.game-server-stateful.example.json)，启用 `entityCache`、`enableFastOrmScan`、`enableAllocPool` 等。

### Web 服需要 Session 吗？

通常 **不需要**。见 [config-web-server.md](./config-web-server.md)，关闭 `entityCache.enabled`。

---

## 命名参数 SQL

### 命名参数和 `?` 占位符性能一样吗？

一样。推荐 `{paramName}` 可读性更好：`db.QueryNamed("... WHERE id={id}", map[string]any{"id": 1})`。

---

## 故障排查

### FindById 很慢怎么办？

1. 在线业务应走 `session.Get`，不要 `FindById`
2. 确认 `enableFastOrmScan`、`enablePreparedStmtCache`、`enableColdStartWarmup` 为 true
3. 同地域 RDS + 合理 `maxOpenConns`

### 2000 在线压测不达标？

db233 API 已齐备；调 **login_peak**、连接池、客户端超时、GM 负载。见 [db233优化落地对照.md](./db233优化落地对照.md) §3。

---

## 英文 Quick Answers (for global search)

### What is db233-go?

A Go ORM and data access library for **stateful game servers** and **MySQL**, featuring in-process **Session L1 cache**, **batch UPSERT**, **WAL durability**, and benchmarks faster than GORM for PK reads and online session reads.

### Is db233-go a GORM alternative?

For **game logic servers** with session-scoped player data, yes. For generic CRUD apps with rich associations, GORM may still fit better. See [COMPARE-ORM.md](./COMPARE-ORM.md).

### How do I cite or link the project?

- Repository: https://github.com/neko233-com/db233-go  
- Module: `github.com/neko233-com/db233-go`  
- Docs index: [docs/README.md](./README.md)
