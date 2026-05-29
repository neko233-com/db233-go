# ========================================
# db233-go Auto Publish Script (PowerShell)
# ========================================
# 功能：
# 1. 自动读取 version.txt 并自增版本号
# 2. 确保所有测试通过
# 3. 自动提交 version.txt 变更
# 4. 创建并推送 Git Tag 与当前分支
# ========================================

param(
    [string]$VersionPart = "patch",  # patch | minor | major
    [switch]$DryRun = $false,          # 模拟运行，不写入、不推送
    [switch]$SkipTests = $false        # 跳过测试
)


# 始终切换到脚本所在目录
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

# 全局错误立刻退出
$ErrorActionPreference = "Stop"


# ========================================
# 输出工具
# ========================================
function Write-ColoredHost {
    param(
        [string]$Message,
        [string]$Color = "White"
    )
    Write-Host $Message -ForegroundColor $Color
}

function Write-Section {
    param([string]$Message)
    Write-Host ""
    Write-ColoredHost "===> $Message" "Cyan"
}

function Write-Ok {
    param([string]$Message)
    Write-ColoredHost "OK: $Message" "Green"
}

function Write-Err {
    param([string]$Message)
    Write-ColoredHost "ERROR: $Message" "Red"
}

function Write-Warn {
    param([string]$Message)
    Write-ColoredHost "WARN: $Message" "Yellow"
}

# Git 辅助：PowerShell 下 git  stderr 不应触发 Stop
function Invoke-Git {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args)
    $prev = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    & git @Args
    $exit = $LASTEXITCODE
    $ErrorActionPreference = $prev
    return $exit
}

function Get-GitRemoteUrl {
    param([string]$Name)
    $prev = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $url = git remote get-url $Name 2>$null
    $exit = $LASTEXITCODE
    $ErrorActionPreference = $prev
    if ($exit -ne 0 -or [string]::IsNullOrWhiteSpace($url)) {
        return $null
    }
    return $url.Trim()
}

function Ensure-GithubRemote {
    param([string]$OriginUrl)
    if ([string]::IsNullOrWhiteSpace($OriginUrl)) {
        Write-Warn "origin URL 为空，跳过 github remote 配置"
        return $false
    }

    $githubUrl = Get-GitRemoteUrl "github"
    if (-not $githubUrl) {
        Write-ColoredHost "自动添加 github remote（与 origin 同步）..." "Yellow"
        $exit = Invoke-Git remote add github $OriginUrl
        if ($exit -ne 0) {
            Write-Err "添加 github remote 失败"
            return $false
        }
        Write-Ok "github remote 已添加: $OriginUrl"
        return $true
    }

    if ($githubUrl -ne $OriginUrl) {
        Write-ColoredHost "同步 github remote URL..." "Yellow"
        $exit = Invoke-Git remote set-url github $OriginUrl
        if ($exit -ne 0) {
            Write-Err "更新 github remote 失败"
            return $false
        }
        Write-Ok "github remote 已更新: $OriginUrl"
    } else {
        Write-Ok "github remote 已就绪: $githubUrl"
    }
    return $true
}


# ========================================
# 版本工具
# ========================================
function Get-CurrentVersion {
    $versionFile = "version.txt"
    if (-not (Test-Path $versionFile)) {
        Write-Err "version.txt not found"
        exit 1
    }

    $raw = (Get-Content $versionFile -Raw).Trim()
    if ($raw -notmatch '^v?(\d+)\.(\d+)\.(\d+)$') {
        Write-Err "Invalid version format in version.txt. Expected vX.Y.Z"
        exit 1
    }

    return $raw
}

function Bump-Version {
    param(
        [string]$CurrentVersion,
        [string]$Part
    )

    if ($CurrentVersion -match '^v?(\d+)\.(\d+)\.(\d+)$') {
        $major = [int]$Matches[1]
        $minor = [int]$Matches[2]
        $patch = [int]$Matches[3]
    } else {
        Write-Err "Cannot parse version: $CurrentVersion"
        exit 1
    }

    switch ($Part.ToLower()) {
        "major" { $major++; $minor = 0; $patch = 0 }
        "minor" { $minor++; $patch = 0 }
        "patch" { $patch++ }
        default { Write-Err "Invalid version part: $Part"; exit 1 }
    }

    return "v{0}.{1}.{2}" -f $major, $minor, $patch
}

function Set-Version {
    param([string]$Version)
    "${Version}" | Out-File "version.txt" -Encoding UTF8 -NoNewline
    Write-Ok "version.txt updated to $Version"
}


# ========================================
# 主流程
# ========================================
Write-ColoredHost "========================================" "Cyan"
Write-ColoredHost "   db233-go Auto Publish Script" "Cyan"
Write-ColoredHost "========================================" "Cyan"

if ($DryRun) {
    Write-Warn "DryRun mode: no files will be modified or pushed"
}


# 0. 确保工作区干净（DryRun 时仅告警）
Write-Section "Checking git status"
$gitStatus = git status --porcelain
if ($LASTEXITCODE -ne 0) {
    Write-Err "Git command failed"
    exit 1
}
if ($gitStatus) {
    if ($DryRun) {
        Write-Warn "Working tree not clean (DryRun continues)."
        Write-Host $gitStatus
    } else {
        Write-Err "Working tree not clean. Please commit or stash changes."
        Write-Host $gitStatus
        exit 1
    }
}


# 1. 运行测试
if (-not $SkipTests) {
    Write-Section "Running tests"
    go test ./tests
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Tests failed"
        exit 1
    }
    Write-Ok "Tests passed"
} else {
    Write-Warn "Skip tests requested"
}


# 2. 构建
Write-Section "Building"
go build ./...
if ($LASTEXITCODE -ne 0) {
    Write-Err "Build failed"
    exit 1
}
Write-Ok "Build succeeded"


# 3. 读取与递增版本
Write-Section "Bumping version"
$currentVersion = Get-CurrentVersion
Write-ColoredHost "Current version: $currentVersion" "White"
$newVersion = Bump-Version -CurrentVersion $currentVersion -Part $VersionPart
Write-ColoredHost "New version: $newVersion" "Green"


# 4. 更新 version.txt（非 DryRun）
if (-not $DryRun) {
    Set-Version -Version $newVersion
} else {
    Write-Warn "[DryRun] Would update version.txt to $newVersion"
}


# 5. 提交 version.txt（非 DryRun）
if (-not $DryRun) {
    Write-Section "Committing version.txt"
    git add version.txt
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Failed to stage version.txt"
        exit 1
    }

    git commit -m "chore: bump version to $newVersion"
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Failed to commit version.txt"
        exit 1
    }
    Write-Ok "Commit created"
} else {
    Write-Warn "[DryRun] Would commit version.txt"
}


# 6. 创建标签
Write-Section "Creating git tag"
if (-not $DryRun) {
    git tag -a $newVersion -m "Release $newVersion"
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Failed to create git tag"
        exit 1
    }
    Write-Ok "Tag $newVersion created"
} else {
    Write-Warn "[DryRun] Would create tag $newVersion"
}


# 7. 推送当前分支与标签
Write-Section "Pushing to remote"

# 规范化 origin URL（GitHub 推荐带 .git 后缀，避免部分环境 push/release 异常）
$originUrl = git remote get-url origin 2>$null
if ($originUrl -match '^https://github\.com/[^/]+/[^/.]+$') {
    $fixedUrl = "$originUrl.git"
    Write-Warn "origin URL 缺少 .git 后缀，自动修正: $fixedUrl"
    git remote set-url origin $fixedUrl
    if ($LASTEXITCODE -eq 0) {
        Write-Ok "origin URL 已更新"
    }
}

$currentBranch = git branch --show-current
if (-not $DryRun) {
    Write-ColoredHost "Pushing branch $currentBranch..." "White"
    git push origin $currentBranch
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Failed to push branch $currentBranch"
        exit 1
    }

    Write-ColoredHost "Pushing tag $newVersion..." "White"
    git push origin $newVersion
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Failed to push tag $newVersion"
        exit 1
    }

    # 自动配置 github 远程（与 origin 同 URL，兼容双 remote 推送习惯）
    $originUrl = Get-GitRemoteUrl "origin"
    if (Ensure-GithubRemote -OriginUrl $originUrl) {
        Write-ColoredHost "Pushing to github remote..." "White"
        $branchExit = Invoke-Git push github $currentBranch
        $tagExit = Invoke-Git push github $newVersion
        if ($branchExit -eq 0 -and $tagExit -eq 0) {
            Write-Ok "Pushed to github remote"
        } else {
            Write-Warn "github remote 推送部分失败（origin 已成功时可忽略）"
        }
    }

    Write-Ok "Pushed branch and tag"
} else {
    Write-Warn "[DryRun] Would push branch $currentBranch and tag $newVersion"
}


# 8. 摘要
Write-Section "Release summary"
Write-ColoredHost "Old version: $currentVersion" "White"
Write-ColoredHost "New version: $newVersion" "Green"
Write-ColoredHost "Branch: $currentBranch" "White"
Write-ColoredHost "Tag: $newVersion" "Green"

$repoUrl = git config --get remote.origin.url
if ($repoUrl) {
    $repoUrl = $repoUrl -replace '\.git$', ''
    Write-ColoredHost "Repo: $repoUrl" "White"
    Write-ColoredHost "Tag URL: $repoUrl/releases/tag/$newVersion" "White"
}

if ($DryRun) {
    Write-Warn "DryRun completed. No changes were pushed."
}

Write-Host ""
Write-ColoredHost "========================================" "Green"
Write-ColoredHost "Release pipeline finished" "Green"
Write-ColoredHost "========================================" "Green"
