# db233-go production release gate. Version changes must already be reviewed
# and merged through a pull request before this script is run.
[CmdletBinding()]
param(
    [switch]$DryRun,
    [switch]$Resume
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root
$Repository = 'neko233-com/db233-go'

function Write-Step([string]$Message) {
    Write-Host ""
    Write-Host "===> $Message" -ForegroundColor Cyan
}

function Fail([string]$Message) {
    throw $Message
}

function Invoke-Checked([string]$Command, [string[]]$Arguments, [string]$Description) {
    Write-Step $Description
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        Fail "$Description 失败"
    }
}

function Get-CheckedOutput([string]$Command, [string[]]$Arguments, [string]$Description) {
    $output = @(& $Command @Arguments)
    if ($LASTEXITCODE -ne 0) {
        Fail "$Description 失败"
    }
    return $output
}

function Assert-ExpectedOriginUrl([string]$Url, [string]$Kind) {
    if ($Url -match '(?i)^https?://[^/@\s]+:[^/@\s]+@') {
        Fail "origin $Kind URL 内嵌凭据；请改用 Git Credential Manager 或 SSH"
    }
    if ($Url -notmatch '^(https://github\.com/neko233-com/db233-go(?:\.git)?|git@github\.com:neko233-com/db233-go(?:\.git)?)$') {
        Fail "origin $Kind URL 不是预期的 GitHub 仓库"
    }
}

function Get-RemoteTagCommit([string]$Version) {
    $lines = @(Get-CheckedOutput git @(
        'ls-remote', '--tags', 'origin', "refs/tags/$Version", "refs/tags/$Version^{}"
    ) '检查远端标签')
    if ($lines.Count -eq 0) {
        return $null
    }
    $records = @($lines | ForEach-Object {
        $parts = $_ -split '\s+', 2
        if ($parts.Count -eq 2) {
            [pscustomobject]@{ Commit = $parts[0]; Ref = $parts[1] }
        }
    })
    $selected = $records | Where-Object { $_.Ref -eq "refs/tags/$Version^{}" } | Select-Object -First 1
    if (-not $selected) {
        $selected = $records | Where-Object { $_.Ref -eq "refs/tags/$Version" } | Select-Object -First 1
    }
    if (-not $selected -or $selected.Commit -notmatch '^[0-9a-fA-F]{40,64}$') {
        Fail "无法解析远端标签 $Version"
    }
    return $selected.Commit.ToLowerInvariant()
}

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    Fail '未找到 git'
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail '未找到 go'
}
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    Fail '未找到 gh；发布必须通过 GitHub CLI 完成'
}

Write-Step '验证工作树、分支与远端'
$status = @(Get-CheckedOutput git @('status', '--porcelain=v1', '--untracked-files=all') '读取 Git 状态')
if ($status.Count -ne 0) {
    Fail '工作树不干净；版本与发布说明必须先经 PR 合并'
}
$branch = (Get-CheckedOutput git @('branch', '--show-current') '读取当前分支' | Select-Object -First 1).Trim()
if ($branch -ne 'main') {
    Fail '只能从 main 发布'
}
$originUrl = (Get-CheckedOutput git @('remote', 'get-url', 'origin') '读取 origin') | Select-Object -First 1
Assert-ExpectedOriginUrl $originUrl 'fetch'
$pushUrls = @(Get-CheckedOutput git @('remote', 'get-url', '--push', '--all', 'origin') '读取 origin push URL')
if ($pushUrls.Count -ne 1) {
    Fail 'origin 必须且只能配置一个 push URL'
}
Assert-ExpectedOriginUrl $pushUrls[0] 'push'

Invoke-Checked git @('fetch', '--prune', 'origin', 'main', '--tags') '更新 origin/main 与 tags'
$head = (Get-CheckedOutput git @('rev-parse', 'HEAD') '读取 HEAD' | Select-Object -First 1).Trim()
$originHead = (Get-CheckedOutput git @('rev-parse', 'origin/main') '读取 origin/main' | Select-Object -First 1).Trim()
if ($head -ne $originHead) {
    Fail 'HEAD 与 origin/main 不一致；拒绝从未同步提交发布'
}

$version = (Get-Content version.txt -Raw).Trim()
if ($version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
    Fail 'version.txt 必须是 vX.Y.Z'
}
foreach ($releaseDocument in @('README.md', 'CHANGELOG.md')) {
    if (-not (Select-String -LiteralPath $releaseDocument -SimpleMatch $version -Quiet)) {
        Fail "$releaseDocument 未包含版本 $version"
    }
}

Invoke-Checked gh @('auth', 'status', '--hostname', 'github.com') '验证 GitHub 登录'
$repoName = (Get-CheckedOutput gh @('repo', 'view', $Repository, '--json', 'nameWithOwner', '--jq', '.nameWithOwner') '验证 GitHub 仓库' | Select-Object -First 1).Trim()
if ($repoName -ne $Repository) {
    Fail 'GitHub 仓库身份不匹配'
}

Write-Step '运行本地发布门禁'
& "$Root/scripts/check-secrets.ps1"
if ($LASTEXITCODE -ne 0) { Fail '凭据门禁失败' }
$unformatted = @(& gofmt -l .)
if ($LASTEXITCODE -ne 0) { Fail 'gofmt 检查失败' }
if ($unformatted.Count -ne 0) {
    $unformatted | ForEach-Object { Write-Host $_ -ForegroundColor Red }
    Fail '存在未格式化 Go 文件'
}
Invoke-Checked git @('diff', '--check') '检查补丁格式'
Invoke-Checked go @('mod', 'verify') '验证根模块依赖'
Invoke-Checked go @('build', './...') '构建根模块'
Invoke-Checked go @('vet', './...') '运行 go vet'
Invoke-Checked go @('run', 'github.com/kisielk/errcheck@v1.20.0', '-ignoretests', './...') '检查生产代码未处理错误'
Invoke-Checked go @('run', 'honnef.co/go/tools/cmd/staticcheck@v0.7.0', '-checks=SA*,S*,-ST*', './...') '运行 staticcheck'
Invoke-Checked go @('run', 'golang.org/x/vuln/cmd/govulncheck@v1.6.0', './...') '运行漏洞扫描'
Invoke-Checked go @('test', './...', '-shuffle=on', '-count=3', '-timeout=10m') '运行根模块重复测试'

Push-Location benchmarks
try {
    Invoke-Checked go @('mod', 'verify') '验证 benchmark 模块依赖'
    Invoke-Checked go @('vet', './...') '运行 benchmark go vet'
    Invoke-Checked go @('run', 'github.com/kisielk/errcheck@v1.20.0', '-ignoretests', './...') '检查 benchmark 支撑代码未处理错误'
    Invoke-Checked go @('run', 'honnef.co/go/tools/cmd/staticcheck@v0.7.0', '-checks=SA*,S*,-ST*', './...') '运行 benchmark staticcheck'
    Invoke-Checked go @('test', '-run', '^TestReleaseGate$', '-count=1', '-timeout=12m') '运行本机 benchmark release gate'
} finally {
    Pop-Location
}
Invoke-Checked go @('run', 'github.com/goreleaser/goreleaser/v2@v2.17.0', 'check') '验证 GoReleaser 配置'

Write-Step '验证 main 对应 GitHub CI'
$runsJson = Get-CheckedOutput gh @(
    'run', 'list', '--repo', $Repository, '--workflow', 'ci.yml', '--branch', 'main',
    '--commit', $head, '--limit', '20', '--json', 'headSha,status,conclusion,databaseId'
) '读取 GitHub Actions 状态'
$runs = @($runsJson -join "`n" | ConvertFrom-Json)
$successful = @($runs | Where-Object { $_.headSha -eq $head -and $_.status -eq 'completed' -and $_.conclusion -eq 'success' })
if ($successful.Count -eq 0) {
    Fail '当前 main 没有成功完成的 CI（含 Linux、race、benchmark、Windows）'
}

$previousPreference = $ErrorActionPreference
$localTagCommit = $null
$ErrorActionPreference = 'Continue'
$localTagOutput = @(& git rev-parse --verify "refs/tags/$version^{commit}" 2>$null)
$localTagExit = $LASTEXITCODE
$ErrorActionPreference = $previousPreference
if ($localTagExit -eq 0) {
    $localTagCommit = ($localTagOutput | Select-Object -First 1).Trim()
    if ($localTagCommit -ne $head) {
        Fail "本地标签 $version 指向其他提交"
    }
}

$remoteTagCommit = Get-RemoteTagCommit $version
$remoteTagExists = -not [string]::IsNullOrWhiteSpace($remoteTagCommit)
if ($remoteTagExists -and $remoteTagCommit -ne $head) {
    Fail "远端标签 $version 指向其他提交；请在 PR 中提升 version.txt"
}

$releaseExists = $false
$ErrorActionPreference = 'Continue'
& gh release view $version --repo $Repository *> $null
$releaseExists = $LASTEXITCODE -eq 0
$ErrorActionPreference = $previousPreference
if ($releaseExists) {
    if (-not $remoteTagExists -or $remoteTagCommit -ne $head) {
        Fail "版本 $version 已发布但不指向当前 HEAD；请在 PR 中提升 version.txt"
    }
    Write-Host "Release $version 已存在且指向当前 HEAD，无需重复发布。" -ForegroundColor Green
    exit 0
}

if (($localTagCommit -or $remoteTagExists) -and -not $Resume) {
    Fail "标签 $version 已存在但 Release 不存在；人工确认后使用 -Resume"
}

if ($DryRun) {
    Write-Host "DryRun 通过：将发布 $version（HEAD=$head）。" -ForegroundColor Green
    exit 0
}

if (-not $localTagCommit) {
    Invoke-Checked git @('tag', '-a', $version, '-m', "Release $version") "创建标签 $version"
}
if (-not $remoteTagExists) {
    Invoke-Checked git @('push', 'origin', "refs/tags/$version") "推送标签 $version"
}
$publishedTagCommit = Get-RemoteTagCommit $version
if ($publishedTagCommit -ne $head) {
    Fail "远端标签 $version 未稳定指向当前 HEAD；拒绝创建 Release"
}
Invoke-Checked gh @(
    'release', 'create', $version, '--repo', $Repository, '--verify-tag',
    '--title', $version, '--generate-notes'
) "创建 GitHub Release $version"

Write-Host "发布完成：$version" -ForegroundColor Green
