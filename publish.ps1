# ========================================
# db233-go Auto Publish Script (PowerShell)
# ========================================
# 功能：
# 1. 自动读取 version.txt 并自增版本号
# 2. 确保所有测试通过
# 3. 自动提交所有更改
# 4. 创建 Git Tag 并推送
# ========================================
param(
    [string]$VersionPart = "patch",  # patch | minor | major
    [switch]$DryRun = $false,         # 模拟运行
    [switch]$SkipTests = $false       # 跳过测试
)
# 设置错误处理
$ErrorActionPreference = "Stop"
# ========================================
# 辅助函数
# ========================================
function Write-ColoredHost {
    param(
        [string]$Message,
        [string]$Color = "White"
    )
    Write-Host $Message -ForegroundColor $Color
}
function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-ColoredHost "===> $Message" "Cyan"
}
function Write-Success {
    param([string]$Message)
    Write-ColoredHost "OK: $Message" "Green"
}
function Write-ErrorMsg {
    param([string]$Message)
    Write-ColoredHost "ERROR: $Message" "Red"
}
function Write-WarningMsg {
    param([string]$Message)
    Write-ColoredHost "WARNING: $Message" "Yellow"
}
# 读取版本号
function Get-CurrentVersion {
    $versionFile = "version.txt"
    if (-not (Test-Path $versionFile)) {
        Write-ErrorMsg "version.txt not found"
        exit 1
    }
    $version = Get-Content $versionFile -Raw
    $version = $version.Trim()
    if ($version -notmatch '^\d+\.\d+\.\d+$') {
        Write-ErrorMsg "Invalid version format in version.txt: $version (Expected: X.Y.Z)"
        exit 1
    }
    return $version
}
# 自增版本号
function Get-NextVersion {
    param(
        [string]$CurrentVersion,
        [string]$Part = "patch"
    )
    $parts = $CurrentVersion -split '\.'
    $major = [int]$parts[0]
    $minor = [int]$parts[1]
    $patch = [int]$parts[2]
    switch ($Part.ToLower()) {
        "major" {
            $major++
            $minor = 0
            $patch = 0
        }
        "minor" {
            $minor++
            $patch = 0
        }
        "patch" {
            $patch++
        }
        default {
            Write-ErrorMsg "Invalid version part: $Part (Support: major, minor, patch)"
            exit 1
        }
    }
    return "{0}.{1}.{2}" -f $major, $minor, $patch
}
# 保存版本号
function Set-Version {
    param([string]$Version)
    $versionFile = "version.txt"
    Set-Content -Path $versionFile -Value $Version -NoNewline
    Write-Success "Version updated to: $Version"
}
# ========================================
# 主流程
# ========================================
Write-ColoredHost "========================================" "Cyan"
Write-ColoredHost "   db233-go Auto Publish Script" "Cyan"
Write-ColoredHost "========================================" "Cyan"
if ($DryRun) {
    Write-WarningMsg "Dry Run Mode - No changes will be committed or pushed"
}
# 1. 读取当前版本
Write-Step "Reading current version"
$currentVersion = Get-CurrentVersion
Write-ColoredHost "Current Version: $currentVersion" "White"
# 2. 计算下一个版本
Write-Step "Calculating next version"
$nextVersion = Get-NextVersion -CurrentVersion $currentVersion -Part $VersionPart
$tagName = "v$nextVersion"
Write-ColoredHost "Next Version: $nextVersion (Tag: $tagName)" "Green"
Write-ColoredHost "Version Part: $VersionPart" "White"
# 3. 检查 Git 仓库状态
Write-Step "Checking Git status"
$currentBranch = git branch --show-current
Write-ColoredHost "Current Branch: $currentBranch" "White"
# 检查分支
if ($currentBranch -ne "main" -and $currentBranch -ne "master") {
    Write-WarningMsg "Current branch is not main or master"
    $confirm = Read-Host "Continue anyway? (y/N)"
    if ($confirm -ne "y" -and $confirm -ne "Y") {
        Write-ErrorMsg "Publish cancelled"
        exit 1
    }
}
# 检查标签
$existingTag = git tag -l $tagName
if ($existingTag) {
    Write-ErrorMsg "Tag $tagName already exists"
    exit 1
}
# 4. 拉取最新代码
Write-Step "Pulling latest code"
try {
    git fetch origin
    git pull origin $currentBranch
    Write-Success "Code is up to date"
}
catch {
    Write-WarningMsg "Failed to pull code, continuing..."
}
# 5. 清理和构建
Write-Step "Cleaning and building project"
go clean -cache
go build ./...
if ($LASTEXITCODE -ne 0) {
    Write-ErrorMsg "Build failed"
    exit 1
}
Write-Success "Build successful"
# 6. 运行测试
if (-not $SkipTests) {
    Write-Step "Running tests"
    Write-ColoredHost "Executing all tests..." "White"
    $testOutput = go test ./... -v 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-ErrorMsg "Tests failed, publish cancelled"
        Write-ColoredHost "Test Output:" "Yellow"
        Write-Host $testOutput
        exit 1
    }
    $skippedTests = $testOutput | Select-String "SKIP"
    if ($skippedTests) {
        Write-WarningMsg "Found skipped tests:"
        $skippedTests | ForEach-Object { Write-Host $_.Line }
    }
    Write-Success "All tests passed"
}
else {
    Write-WarningMsg "Skipping tests (Not recommended)"
}
# 7. 更新版本号文件
Write-Step "Updating version.txt"
if (-not $DryRun) {
    Set-Version -Version $nextVersion
}
else {
    Write-WarningMsg "[DryRun] Would update version to: $nextVersion"
}
# 8. 检查并提交更改
Write-Step "Committing changes"
$status = git status --porcelain
if ($status) {
    Write-ColoredHost "Uncommitted changes found:" "White"
    git status --short
    if (-not $DryRun) {
        git add .
        $commitMessage = "chore: release version $nextVersion"
        git commit -m $commitMessage
        Write-Success "Changes committed: $commitMessage"
    }
    else {
        Write-WarningMsg "[DryRun] Would commit all changes"
    }
}
else {
    Write-ColoredHost "No changes to commit" "White"
}
# 9. 创建 Git Tag
Write-Step "Creating Git Tag"
$tagDate = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
$tagMessage = "Release $nextVersion`n`nAuto-published`nTime: $tagDate`nBranch: $currentBranch"
if (-not $DryRun) {
    git tag -a $tagName -m $tagMessage
    if ($LASTEXITCODE -ne 0) {
        Write-ErrorMsg "Failed to create tag"
        exit 1
    }
    Write-Success "Tag $tagName created"
}
else {
    Write-WarningMsg "[DryRun] Would create tag: $tagName"
}
# 10. 推送到远程
Write-Step "Pushing to remote"
if (-not $DryRun) {
    Write-ColoredHost "Pushing code..." "White"
    git push origin $currentBranch
    if ($LASTEXITCODE -ne 0) {
        Write-ErrorMsg "Failed to push code"
        exit 1
    }
    Write-ColoredHost "Pushing tags..." "White"
    git push origin $tagName
    if ($LASTEXITCODE -ne 0) {
        Write-ErrorMsg "Failed to push tags"
        Write-WarningMsg "Code pushed but tag failed. Run: git push origin $tagName"
        exit 1
    }
    Write-Success "Successfully pushed to remote"
}
else {
    Write-WarningMsg "[DryRun] Would push code and tags to origin"
}
# 11. 发布摘要
Write-Step "Publish Summary"
Write-ColoredHost "========================================" "Green"
Write-ColoredHost "Completed Successfully!" "Green"
Write-ColoredHost "========================================" "Green"
Write-Host ""
Write-ColoredHost "Version Info:" "Cyan"
Write-ColoredHost "  Old: $currentVersion" "White"
Write-ColoredHost "  New: $nextVersion" "Green"
Write-ColoredHost "  Tag: $tagName" "Green"
Write-ColoredHost "  Branch: $currentBranch" "White"
Write-Host ""
$repoUrl = git config --get remote.origin.url
if ($repoUrl) {
    $repoUrl = $repoUrl -replace '\.git$', ''
    Write-ColoredHost "Repo: $repoUrl" "White"
    Write-ColoredHost "Tags: $repoUrl/tags" "White"
}
Write-Host ""
if ($DryRun) {
    Write-WarningMsg "Dry run completed. No actual publish performed."
}
Write-Host ""
Write-ColoredHost "========================================" "Green"
Write-Host ""
