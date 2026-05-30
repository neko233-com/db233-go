# db233-go Benchmark 标准（v1.0.1+）

> 发版门禁 / 压测回归的唯一规范。与 `scripts/run-benchmark.*` 保持一致。

## 一键运行

```bash
# Windows (PowerShell)
./scripts/run-benchmark.ps1

# Linux / macOS
./scripts/run-benchmark.sh
```

环境：Go 1.25+、MySQL（优先 `config.local.json`，否则 `127.0.0.1:3306/db233_go`）。

## 流水线（4 阶段）

| 阶段 | 命令 | 通过标准 |
|------|------|----------|
| 1 单元回归 | `go test ./tests/ -count=1 -timeout 5m` | 0 FAIL |
| 2 主包压测 | `go test ./tests/ -run 'TestPerfStability\|TestTrafficBurst\|TestAllocPool' -count=1 -timeout 5m` | 0 FAIL |
| 3 框架对比 | `cd benchmarks && go test -run TestFrameworkCompare_Report -count=1 -timeout 3m` | 见下表 |
| 4 稳定性 | `cd benchmarks && go test -run TestStability -count=1 -timeout 5m` | 0 FAIL、无 Session 泄漏 |

## 框架对比阈值（Phase 3）

| 指标 | 阈值 |
|------|------|
| db233 单次 PK 读 vs GORM | ≤ **1.15×** GORM 中位数 |
| db233 单次 PK 读 vs raw SQL | ≤ **1.25×** database/sql |
| Session 读 ×1000 | ≤ GORM 估算 1000×读 / **10** |
| 批量 UPSERT ×50 | ≤ GORM × **1.5** |

## 稳定性（Phase 4）

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
- [ ] `version.txt` = `v1.0.1`
- [ ] `CHANGELOG.md` 含 v1.0.1
- [ ] `go test ./tests/ -short` CI 快速模式可过（跳过 MySQL 重测项）
