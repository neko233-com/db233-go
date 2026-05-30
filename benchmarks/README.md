# benchmarks

Go ORM 框架对比 + 稳定性压测 + **发版门禁**（需 MySQL）。

## 一键（推荐）

```bash
# 项目根目录
./scripts/run-benchmark.sh      # Linux / macOS
./scripts/run-benchmark.ps1     # Windows PowerShell
```

完整规范见 [docs/BENCHMARK.md](../docs/BENCHMARK.md)。

## 依赖

```bash
cd benchmarks && go mod tidy
```

连接：项目根 `config.local.json`（或 `127.0.0.1` 本地 MySQL）。

## 分项命令

```bash
# 框架对比（database/sql / sqlx / GORM / db233-go）
cd benchmarks && go test -run TestFrameworkCompare_Report -timeout 3m -v

# 稳定性（突发 / 连接池 / LRU / WAL）
cd benchmarks && go test -run TestStability -timeout 5m -v

# 发版门禁（对比 + 全部稳定性）
cd benchmarks && go test -run TestReleaseGate -timeout 8m -v
```

主包压测（脚本 Phase 1–2）：

```bash
go test ./tests/ -timeout 5m
go test ./tests/ -run 'TestPerfStability|TestTrafficBurst|TestAllocPool' -timeout 5m -v
```

## 通过标准（v1.0.2+）

| 类别 | 标准 |
|------|------|
| 单次 PK 读 | db233 ≤ **1.15×** GORM，≤ **1.25×** raw SQL |
| 登录 3 表 | db233 并发加载显著优于串行框架 |
| 批量写 50 | db233 ≤ GORM × **1.5** |
| Session 读 1k | 显著优于 GORM 循环 First |
| 稳定性 | 0 致命错误、Session 无泄漏、WAL pending=0 |
