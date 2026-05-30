# db233-go vs GORM vs sqlx vs database/sql

> 检索关键词：Go ORM comparison · GORM alternative · sqlx vs db233 · game server Go database

## 选型总表

| 维度 | db233-go | GORM | sqlx | database/sql |
|------|----------|------|------|--------------|
| 定位 | 游戏服 ORM + Session + WAL | 全功能 ORM | 轻量扩展 | 标准库 |
| 学习曲线 | 中（游戏服概念多） | 中 | 低 | 低 |
| 单次 PK 读（RDS 实测） | **~10ms** | ~23ms | ~19ms | ~22ms |
| 在线读（Session×1000） | **<0.001ms** | ~21s 量级 | — | — |
| 批量 UPSERT 50 行 | **~13ms** | ~55ms | ~974ms | ~581ms |
| Session 一级缓存 | ✅ | ❌ | ❌ | ❌ |
| 批量 UPSERT 单 SQL | ✅ | 部分 | ❌ | ❌ |
| 命名参数 `{name}` | ✅ | ✅ | ✅ | ❌ |
| 迁移 / 关联 / Hook 生态 | 基础 | **丰富** | 无 | 无 |
| WAL 写不丢 | ✅ | ❌ | ❌ | ❌ |

完整色阶表见根目录 [README.md](../README.md#框架性能对标阿里云-rds-mysql--同地域)。

## 什么时候选 db233-go？

1. **有状态游戏逻辑服** — 玩家在线期间读多写少，希望 **内存读、延迟落库**
2. **登录要拉多表** — `FindByIdConcurrent` 比循环 GORM `First` 少 RTT
3. **entitysave / 定时存档** — `UpdateBatchUpsert` 代替循环 Update
4. **云 RDS 抖动** — WAL + FaultTolerant 无限重试
5. **压测证明 GORM 单次读 / 批量写瓶颈** — db233 FastOrmScan + 真批量

## 什么时候继续用 GORM？

- 快速 CRUD 原型、Admin 后台、关联预加载 `Preload`
- 团队已深度绑定 GORM 迁移与插件
- 无 Session 概念、QPS 不高的内部系统

## 什么时候选 sqlx / raw SQL？

- 纯 SQL 微服务、ETL、报表
- 不需要 ORM 映射，只要 `StructScan`
- 极致控制每条 SQL

## 迁移提示（GORM → db233-go 游戏服）

| GORM | db233-go |
|------|----------|
| `db.First(&user, id)` | 在线：`session.Get(&UserEntity{})`；离线：`repo.FindById(id, &UserEntity{})` |
| `db.Save(&list)` 循环 | `repo.UpdateBatchUpsert(list)` |
| `db.Where("id IN ?", ids).Find(&users)` | `repo.FindByIds(ids, &UserEntity{})` 或 `FindByIdsMap` |
| 全局 DB 单例 | `InitGameDb` + `SessionRepository` |

## 压测复现

```bash
./scripts/run-benchmark.ps1
# 或
cd benchmarks && go test -run TestFrameworkCompare_Report -timeout 3m -v
```

见 [BENCHMARK.md](./BENCHMARK.md)。
