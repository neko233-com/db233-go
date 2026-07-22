# 提交/发版前检查：扫描已跟踪与未跟踪（但未忽略）的文件。
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$script:failed = $false

function Fail([string]$Message) {
    Write-Host "FAIL: $Message" -ForegroundColor Red
    $script:failed = $true
}

function Ok([string]$Message) {
    Write-Host "OK: $Message" -ForegroundColor Green
}

$allFiles = @(
    git ls-files --cached --others --exclude-standard 2>$null |
        Where-Object { $_ }
)
if ($LASTEXITCODE -ne 0) {
    throw "无法读取 Git 文件列表"
}

function Get-MatchingFiles([string]$Pattern) {
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $paths = @(git grep --untracked --exclude-standard -I -l -E -e $Pattern -- . ':!scripts/check-secrets.*' 2>$null)
    $exitCode = $LASTEXITCODE
    $ErrorActionPreference = $previousPreference
    if ($exitCode -gt 1) {
        throw "Git 内容扫描失败"
    }
    return $paths
}

function Scan-Files([string]$Name, [string]$Pattern) {
    foreach ($path in @(Get-MatchingFiles $Pattern)) {
        # 禁止输出匹配内容，避免门禁日志二次泄密。
        Fail "疑似${Name}: ${path}"
    }
}

$forbiddenLocal = '(^|/)(config\.local\.(json|ya?ml)|[^/]+\.local\.(json|ya?ml))$'
foreach ($path in $allFiles) {
    if ($path -match $forbiddenLocal -and $path -notmatch '\.example$') {
        Fail "发现未忽略的本地凭据文件: $path"
    }
}

# 仅匹配高置信度格式。模式定义文件自身已从内容扫描排除。
Scan-Files "私钥" '-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----'
Scan-Files "AWS access key" '(AKIA|ASIA)[0-9A-Z]{16}'
Scan-Files "GitHub token" '(gh[pousr]_[A-Za-z0-9_]{30,}|github_pat_[A-Za-z0-9_]{20,})'
Scan-Files "GitLab token" 'glpat-[A-Za-z0-9_-]{20,}'
Scan-Files "OpenAI key" 'sk-(live|proj|svcacct|admin)-[A-Za-z0-9_-]{16,}'
Scan-Files "Slack token" 'xox[baprs]-[A-Za-z0-9-]{20,}'
Scan-Files "Google API key" 'AIza[0-9A-Za-z_-]{35}'
Scan-Files "带凭据 URL" 'https?://[^/@\s]+:[^/@\s]+@'
Scan-Files "真实 RDS 主机名" 'rm-[a-z0-9]+\.mysql\.rds\.aliyuncs\.com'
Scan-Files "业务仓库名称" ('server-project-' + 'sf-go')

$ipPattern = '([0-9]{1,3}\.){3}[0-9]{1,3}'
$ipRegex = [regex]::new($ipPattern)
foreach ($path in @(Get-MatchingFiles $ipPattern)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        continue
    }
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadLines((Resolve-Path -LiteralPath $path))) {
        $lineNumber++
        if ($line.IndexOf([char]0) -ge 0) {
            break
        }
        foreach ($match in $ipRegex.Matches($line)) {
            if ($match.Value -ne '127.0.0.1') {
                Fail "非 loopback IPv4 字面量: ${path}:${lineNumber}"
                break
            }
        }
    }
}

$dsnPattern = '[A-Za-z0-9_.%+-]+:[^@\s]+@tcp\(([^):]+)(:[0-9]+)?\)'
$dsnRegex = [regex]::new('[A-Za-z0-9_.%+-]+:[^@\s]+@tcp\((?<host>[^):]+)(?::[0-9]+)?\)')
foreach ($path in @(Get-MatchingFiles $dsnPattern)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        continue
    }
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadLines((Resolve-Path -LiteralPath $path))) {
        $lineNumber++
        foreach ($match in $dsnRegex.Matches($line)) {
            $hostName = $match.Groups['host'].Value.Trim('[', ']')
            if ($hostName -ne '127.0.0.1' -and $hostName -notmatch '^(?i:localhost)$') {
                Fail "带凭据的非本机数据库 DSN: ${path}:${lineNumber}"
                break
            }
        }
    }
}

if ($script:failed) {
    Write-Host ""
    Write-Host "凭据只能放在被 .gitignore 排除的本地配置或秘密管理系统中。" -ForegroundColor Yellow
    exit 1
}

Ok "未发现本地配置、私钥、高置信度令牌、远端凭据 URL/DSN 或非本机 IP"
exit 0
