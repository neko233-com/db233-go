# db233-go 发版/压测一条龙：单元 → 主包压测 → 框架对比 → 稳定性
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Step([string]$Msg) {
    Write-Host ""
    Write-Host ">>> $Msg" -ForegroundColor Cyan
    Write-Host "----------------------------------------"
}

Write-Host "========================================"
Write-Host " db233-go benchmark suite"
Write-Host " root: $Root"
Write-Host "========================================"

Step "[1/4] Unit tests (./tests/, full)"
go test ./tests/ -count=1 -timeout 5m
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "[2/4] Perf + traffic + alloc pool (./tests/)"
go test ./tests/ -count=1 -timeout 5m -run "TestPerfStability|TestTrafficBurst|TestAllocPool"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "[3/4] Framework compare (benchmarks/)"
Push-Location benchmarks
go test -count=1 -timeout 3m -run TestFrameworkCompare_Report -v
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Step "[4/4] Stability burst (benchmarks/)"
Push-Location benchmarks
go test -count=1 -timeout 5m -run TestStability -v
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host " ALL PASS — benchmark suite complete"
Write-Host "========================================"
