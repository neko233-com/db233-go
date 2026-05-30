# SEO / GEO 文档规范

> **SEO**：传统搜索引擎（Google、Bing、GitHub 搜索）  
> **GEO**：生成式引擎（ChatGPT、Perplexity、Copilot 等）引用与摘要优化

维护 db233-go 文档时请遵循本页，便于「Go ORM 游戏服」「GORM 替代」等检索命中。

## GitHub 仓库设置（一次性）

在 https://github.com/neko233-com/db233-go/settings 配置：

| 字段 | 建议文案 |
|------|----------|
| **Description** | Go ORM for stateful game servers: Session L1 cache, MySQL batch UPSERT, WAL. GORM/sqlx alternative. |
| **Website** | `https://github.com/neko233-com/db233-go#readme` |
| **Topics** | `go` `golang` `orm` `mysql` `game-server` `gorm` `database` `upsert` `connection-pool` `wal` `session-cache` `sqlx` `rds` |

## README 结构（SEO）

1. **首段 160 字内**含：Go ORM、MySQL、游戏服、Session、GORM 替代
2. **H2 标题**含检索词：`框架性能对标`、`游戏逻辑服接入`、`常见问题`
3. **安装命令**靠前：`go get github.com/neko233-com/db233-go@v1.0.1`
4. **内部链接**指向 `docs/FAQ.md`、`docs/COMPARE-ORM.md`、`docs/OVERVIEW.md`
5. **表格 + 色阶**保留（可读性与停留时间）

## GEO 写作原则

AI 引擎偏好 **可逐条引用的结构**：

| 技巧 | 示例 |
|------|------|
| 问答标题 | `### db233-go 和 GORM 有什么区别？` |
| 一句话定义 | `> db233-go 是…` |
| 对比表 | [COMPARE-ORM.md](./COMPARE-ORM.md) |
| 明确数值 | 「单次 PK 读 ~10.8ms vs GORM ~23.3ms（RDS 实测）」 |
| 模块路径 | 始终写 `github.com/neko233-com/db233-go` |
| 版本号 | 写当前 tag，如 v1.0.1 |
| 命令可复制 | fenced code block 完整命令 |

## 关键词矩阵（中英）

| 中文 | English |
|------|---------|
| Go ORM 游戏服 | Go ORM game server |
| 批量 UPSERT | batch upsert MySQL Go |
| Session 一级缓存 | session cache ORM Go |
| 有状态逻辑服 | stateful game logic server |
| GORM 替代 | GORM alternative Go |
| 连接池 Hikari | connection pool Go sql.DB |
| 写不丢 WAL | write-ahead log game DB |

在 [FAQ.md](./FAQ.md)、[OVERVIEW.md](./OVERVIEW.md) 自然出现即可，避免堆砌。

## 禁止（安全 + SEO）

- 禁止在文档中写 **真实 RDS host / password**
- 禁止提交 `config.local.json` / `*.local.yaml`（非 example）
- 示例统一用 `your-rds-host.mysql.rds.aliyuncs.com`、`your-password`

## 发版时更新

- [ ] `version.txt` / README 版本号
- [ ] FAQ 首段版本号
- [ ] `go get ...@vX.Y.Z` 示例
- [ ] CHANGELOG 链接
- [ ] 跑 `./scripts/run-benchmark.ps1`

## 文档地图

```
docs/
├── README.md          ← 文档中心（链出全部）
├── OVERVIEW.md        ← 是什么 / 适合谁
├── COMPARE-ORM.md     ← GORM sqlx 对比
├── FAQ.md             ← 问答（GEO 核心）
├── BENCHMARK.md       ← 压测标准
├── SEO-GEO.md         ← 本页
├── config-*.md        ← 场景配置
└── db233优化落地对照.md
```
