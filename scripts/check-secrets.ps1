# 提交/发版前检查：禁止将本地数据库凭据纳入 Git
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$fail = $false

function Fail([string]$Msg) {
    Write-Host "FAIL: $Msg" -ForegroundColor Red
    $script:fail = $true
}

function Ok([string]$Msg) {
    Write-Host "OK: $Msg" -ForegroundColor Green
}

$forbiddenTracked = @(
    "config.local.json",
    "config.local.yaml",
    "config.local.yml"
)

foreach ($f in $forbiddenTracked) {
    $listed = git ls-files $f 2>$null
    if ($listed) {
        Fail "已纳入 Git 跟踪: $f — 请 git rm --cached $f 并仅保留 *.example"
    }
}

$staged = @(git diff --cached --name-only 2>$null)
foreach ($f in $forbiddenTracked) {
    if ($staged -contains $f) {
        Fail "暂存区含凭据文件: $f"
    }
}
foreach ($line in $staged) {
    if ($line -match '\.local\.(json|ya?ml)$' -and $line -notmatch '\.example$') {
        Fail "暂存区含本地配置: $line"
    }
}

# 已跟踪文件列表中不得出现非 example 的 local 配置
$allTracked = @(git ls-files 2>$null)
foreach ($line in $allTracked) {
    if ($line -match '(^|/)config\.local\.(json|ya?ml)$') {
        Fail "已跟踪 config.local.*: $line"
    }
    if ($line -match '\.local\.(json|ya?ml)$' -and $line -notmatch '\.example$') {
        Fail "已跟踪本地凭据文件: $line"
    }
}

if ($fail) {
    Write-Host ""
    Write-Host "凭据只能放在 gitignore 的文件中:" -ForegroundColor Yellow
    Write-Host "  config.local.json / config.local.yaml / *.local.json / *.local.yaml"
    exit 1
}

Ok "未发现数据库凭据泄露风险（config.local.* 未纳入 Git）"
exit 0
