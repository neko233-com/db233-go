#!/usr/bin/env bash
# db233-go 发版/压测一条龙
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "========================================"
echo " db233-go benchmark suite"
echo " root: $ROOT"
echo "========================================"

step() { echo ""; echo ">>> $1"; echo "----------------------------------------"; }

step "[0/6] Secret leak check"
bash "$ROOT/scripts/check-secrets.sh"

step "[1/6] pkg/db233 unit tests"
go test ./pkg/db233/ -count=1 -timeout 2m

step "[2/6] Integration tests (./tests/, full)"
go test ./tests/ -count=1 -timeout 5m

step "[3/6] Perf + traffic + session flush"
go test ./tests/ -count=1 -timeout 5m \
  -run 'TestPerfStability|TestTrafficBurst|TestAllocPool|TestSessionFlush'

step "[4/6] Framework compare"
(cd benchmarks && go test -count=1 -timeout 3m -run TestFrameworkCompare_Report -v)

step "[5/6] Stability burst"
(cd benchmarks && go test -count=1 -timeout 5m -run TestStability -v)

step "[6/6] Session flush compare"
(cd benchmarks && go test -count=1 -timeout 5m -run TestFlushCompare -v)

echo ""
echo "========================================"
echo " ALL PASS — benchmark suite complete"
echo "========================================"
