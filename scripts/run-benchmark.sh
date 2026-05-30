#!/usr/bin/env bash
# db233-go 发版/压测一条龙：单元 → 主包压测 → 框架对比 → 稳定性
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "========================================"
echo " db233-go benchmark suite"
echo " root: $ROOT"
echo "========================================"

step() { echo ""; echo ">>> $1"; echo "----------------------------------------"; }

step "[0/4] Secret leak check"
"$ROOT/scripts/check-secrets.sh"

step "[1/4] Unit tests (./tests/, full)"
go test ./tests/ -count=1 -timeout 5m

step "[2/4] Perf + traffic + alloc pool (./tests/)"
go test ./tests/ -count=1 -timeout 5m \
  -run 'TestPerfStability|TestTrafficBurst|TestAllocPool'

step "[3/4] Framework compare (benchmarks/)"
(cd benchmarks && go test -count=1 -timeout 3m -run TestFrameworkCompare_Report -v)

step "[4/4] Stability burst (benchmarks/)"
(cd benchmarks && go test -count=1 -timeout 5m -run TestStability -v)

echo ""
echo "========================================"
echo " ALL PASS — benchmark suite complete"
echo "========================================"
