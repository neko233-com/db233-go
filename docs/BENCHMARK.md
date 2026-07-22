# db233-go Benchmark 标准（v1.1.0+）

> 发版门禁 / 压测回归的唯一规范。与 `scripts/run-benchmark.*` 保持一致。  
> 文档索引：[README.md](./README.md) · [FAQ](./FAQ.md) · [COMPARE-ORM](./COMPARE-ORM.md)

## 一键运行

```bash
# Windows (PowerShell)
./scripts/run-benchmark.ps1

# Linux / macOS
bash ./scripts/run-benchmark.sh
```

环境：Go 1.25.12+、本地 MySQL `127.0.0.1:3306/db233_go`（可用 root/root）。连接从 `DB233_TEST_DSN` 或未跟踪的 `config.local.json` 读取，并强制限制为本机。

## 流水线（6 阶段）

| 阶段 | 命令 | 通过标准 |
|------|------|----------|
| 0 凭据 | `scripts/check-secrets.*` | 无 config.local.* 进 Git |
| 1 pkg 单测 | `go test ./pkg/db233/ -count=1 -timeout 2m` | flush / WriteBuffer 全绿 |
| 2 集成回归 | `go test ./tests/ -count=1 -timeout 5m` | 0 FAIL |
| 3 主包压测 | `go test ./tests/ -run 'TestPerfStability\|TestTrafficBurst\|TestAllocPool\|TestSessionFlush'` | 0 FAIL |
| 4 框架对比 | `cd benchmarks && go test -run TestFrameworkCompare_Report` | 见下表 |
| 5 稳定性 | `cd benchmarks && go test -run TestStability` | 0 FAIL |
| 6 刷盘对比 | `cd benchmarks && go test -run TestFlushCompare` | 合并 ≤ 逐 Session ×1.05 |

## 刷盘压测（Phase 6）

| 测试 | 场景 |
|------|------|
| `TestFlushCompare_MergedVsPerSession` | 100 Session dirty：跨表合并 vs 逐 Session |
| `TestFlushCompare_Shutdown100Sessions` | 关服 `FlushAll` 分波 + 数据落库 |

## 框架对比阈值（Phase 4）

| 指标 | 阈值 |
|------|------|
| db233 单次 PK 读 vs GORM | ≤ **1.15×** GORM 中位数 |
| db233 单次 PK 读 vs raw SQL | ≤ **1.25×** database/sql |
| Session 读 ×1000 | ≤ GORM 估算 1000×读 / **10** |
| 批量 UPSERT ×50 | ≤ GORM × **1.5** |

## 稳定性（Phase 5）

| 测试 | 场景 |
|------|------|
| `TestStability_TrafficBurst` | 80×15 混合读写/Session |
| `TestStability_ConnectionPoolSpike` | 池上限 10 尖峰恢复 |
| `TestStability_LRUBurst` | 100 Session，max=30 |
| `TestStability_WALBurst` | 20×10 并发 WAL 写，pending=0 |

## 性能配置（压测默认）

`benchmarks/setup_test.go` 加载 `DefaultCrudPerformanceSettings()` 并 `WarmGameDb`：

- `enableFastOrmScan` / `enableAllocPool` / `enablePreparedStmtCache` / `enableColdStartWarmup`

## 发版检查清单

- [ ] `scripts/run-benchmark.ps1` 全绿
- [ ] `version.txt` = `v1.1.0`
- [ ] `CHANGELOG.md` 含 v1.1.0
- [ ] `go test ./tests/ -short` CI 快速模式可过（跳过 MySQL 重测项）
