# db233-go 发版/压测一条龙：凭据检查 → pkg 单测 → 集成 → 框架对比 → 稳定性 → 刷盘对比
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

Step "[0/6] Secret leak check"
powershell -NoProfile -ExecutionPolicy Bypass -File "$Root/scripts/check-secrets.ps1"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "[1/6] pkg/db233 unit tests (flush + write buffer)"
go test ./pkg/db233/ -count=1 -timeout 2m
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "[2/6] Integration tests (./tests/, full)"
go test ./tests/ -count=1 -timeout 5m
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "[3/6] Perf + traffic + session flush (./tests/)"
go test ./tests/ -count=1 -timeout 5m -run "TestPerfStability|TestTrafficBurst|TestAllocPool|TestSessionFlush"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "[4/6] Framework compare (benchmarks/)"
Push-Location benchmarks
go test -count=1 -timeout 3m -run TestFrameworkCompare_Report -v
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Step "[5/6] Stability burst (benchmarks/)"
Push-Location benchmarks
go test -count=1 -timeout 5m -run TestStability -v
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Step "[6/6] Session flush compare + shutdown (benchmarks/)"
Push-Location benchmarks
go test -count=1 -timeout 5m -run "TestFlushCompare" -v
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host " ALL PASS — benchmark suite complete"
Write-Host "========================================"
