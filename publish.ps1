# ========================================
# db233-go 自动发布脚本 (PowerShell)
# ========================================
# 功能：
# 1. 自动读取 version.txt 并自增版本号
# 2. 确保所有测试通过
# 3. 自动提交所有更改
# 4. 创建 Git Tag 并推送
# ========================================

param(
    [string]$VersionPart = "patch",  # patch | minor | major
    [switch]$DryRun = $false,         # 模拟运行，不实际提交
    [switch]$SkipTests = $false       # 跳过测试（不推荐）
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
    Write-ColoredHost "✓ $Message" "Green"
}

function Write-Error {
    param([string]$Message)
    Write-ColoredHost "✗ $Message" "Red"
}

function Write-Warning {
    param([string]$Message)
    Write-ColoredHost "⚠ $Message" "Yellow"
}

# 读取版本号
function Get-CurrentVersion {
    $versionFile = "version.txt"
    if (-not (Test-Path $versionFile)) {
        Write-Error "version.txt 文件不存在"
        exit 1
    }

    $version = Get-Content $versionFile -Raw
    $version = $version.Trim()

    if ($version -notmatch '^\d+\.\d+\.\d+$') {
        Write-Error "version.txt 中的版本号格式无效: $version (期望格式: X.Y.Z)"
        exit 1
    }

    return $version
}

# 自增版本号
function Get-NextVersion {
    param(
        [string]$CurrentVersion,
        [string]$Part = "patch"  # patch | minor | major
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
            Write-Error "无效的版本部分: $Part (支持: major, minor, patch)"
            exit 1
        }
    }

    return "$major.$minor.$patch"
}

# 保存版本号
function Set-Version {
    param([string]$Version)

    $versionFile = "version.txt"
    Set-Content -Path $versionFile -Value $Version -NoNewline
    Write-Success "版本号已更新到: $Version"
}

# ========================================
# 主流程
# ========================================

Write-ColoredHost "========================================" "Cyan"
Write-ColoredHost "   db233-go 自动发布脚本" "Cyan"
Write-ColoredHost "========================================" "Cyan"

if ($DryRun) {
    Write-Warning "模拟运行模式（DryRun）- 不会实际提交或推送"
}

# 1. 读取当前版本
Write-Step "读取当前版本号"
$currentVersion = Get-CurrentVersion
Write-ColoredHost "当前版本: $currentVersion" "White"

# 2. 计算下一个版本
Write-Step "计算下一个版本号"
$nextVersion = Get-NextVersion -CurrentVersion $currentVersion -Part $VersionPart
$tagName = "v$nextVersion"
Write-ColoredHost "下一个版本: $nextVersion (Tag: $tagName)" "Green"
Write-ColoredHost "版本类型: $VersionPart" "White"

# 3. 检查 Git 仓库状态
Write-Step "检查 Git 仓库状态"
$currentBranch = git branch --show-current
Write-ColoredHost "当前分支: $currentBranch" "White"

# 检查是否在正确的分支上
if ($currentBranch -ne "main" -and $currentBranch -ne "master") {
    Write-Warning "当前不在 main 或 master 分支上"
    $confirm = Read-Host "是否继续发布? (y/N)"
    if ($confirm -ne "y" -and $confirm -ne "Y") {
        Write-Error "发布已取消"
        exit 1
    }
}

# 检查标签是否已存在
$existingTag = git tag -l $tagName
if ($existingTag) {
    Write-Error "标签 $tagName 已存在，请先删除或使用不同的版本"
    exit 1
}

# 4. 拉取最新代码
Write-Step "拉取远程最新代码"
try {
    git fetch origin
    git pull origin $currentBranch
    Write-Success "代码已更新到最新"
} catch {
    Write-Warning "拉取代码时出现错误，继续执行..."
}

# 5. 清理和构建
Write-Step "清理并构建项目"
go clean -cache
go build ./...

if ($LASTEXITCODE -ne 0) {
    Write-Error "项目构建失败"
    exit 1
}
Write-Success "项目构建成功"

# 6. 运行测试
if (-not $SkipTests) {
    Write-Step "运行测试套件"
    Write-ColoredHost "运行所有测试（这可能需要一些时间）..." "White"

    # 运行测试并捕获输出
    $testOutput = go test ./... -v 2>&1

    if ($LASTEXITCODE -ne 0) {
        Write-Error "测试失败，发布已取消"
        Write-ColoredHost "测试输出:" "Yellow"
        Write-Host $testOutput
        exit 1
    }

    # 检查是否有测试跳过
    $skippedTests = $testOutput | Select-String "SKIP"
    if ($skippedTests) {
        Write-Warning "发现跳过的测试："
        $skippedTests | ForEach-Object { Write-Host $_.Line }
    }

    Write-Success "所有测试通过"
} else {
    Write-Warning "跳过测试（不推荐）"
}

# 7. 更新版本号文件
Write-Step "更新 version.txt"
if (-not $DryRun) {
    Set-Version -Version $nextVersion
} else {
    Write-Warning "[DryRun] 将会更新版本号到: $nextVersion"
}

# 8. 检查并提交所有更改
Write-Step "提交所有更改"
$status = git status --porcelain

if ($status) {
    Write-ColoredHost "发现未提交的更改：" "White"
    git status --short

    if (-not $DryRun) {
        # 添加所有更改
        git add .

        # 创建提交
        $commitMessage = "chore: release version $nextVersion"
        git commit -m $commitMessage

        Write-Success "更改已提交: $commitMessage"
    } else {
        Write-Warning "[DryRun] 将会提交所有更改"
    }
} else {
    Write-ColoredHost "没有需要提交的更改" "White"
}

# 9. 创建 Git Tag
Write-Step "创建 Git Tag"
$tagMessage = "Release version $nextVersion

自动发布脚本生成
发布时间: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')
发布分支: $currentBranch"

if (-not $DryRun) {
    git tag -a $tagName -m $tagMessage

    if ($LASTEXITCODE -ne 0) {
        Write-Error "创建标签失败"
        exit 1
    }

    Write-Success "标签 $tagName 创建成功"
} else {
    Write-Warning "[DryRun] 将会创建标签: $tagName"
}

# 10. 推送到远程仓库
Write-Step "推送到远程仓库"

if (-not $DryRun) {
    # 推送代码
    Write-ColoredHost "推送代码..." "White"
    git push origin $currentBranch

    if ($LASTEXITCODE -ne 0) {
        Write-Error "推送代码失败"
        exit 1
    }

    # 推送标签
    Write-ColoredHost "推送标签..." "White"
    git push origin $tagName

    if ($LASTEXITCODE -ne 0) {
        Write-Error "推送标签失败"
        Write-Warning "代码已推送，但标签推送失败"
        Write-Warning "请手动运行: git push origin $tagName"
        exit 1
    }

    Write-Success "代码和标签已成功推送到远程仓库"
} else {
    Write-Warning "[DryRun] 将会推送到远程仓库"
}

# 11. 生成发布摘要
Write-Step "发布摘要"
Write-ColoredHost "========================================" "Green"
Write-ColoredHost "✓ 发布成功！" "Green"
Write-ColoredHost "========================================" "Green"
Write-Host ""
Write-ColoredHost "版本信息：" "Cyan"
Write-ColoredHost "  旧版本: $currentVersion" "White"
Write-ColoredHost "  新版本: $nextVersion" "Green"
Write-ColoredHost "  Git Tag: $tagName" "Green"
Write-ColoredHost "  分支: $currentBranch" "White"
Write-Host ""
Write-ColoredHost "发布地址：" "Cyan"
$repoUrl = git config --get remote.origin.url
if ($repoUrl) {
    $repoUrl = $repoUrl -replace '\.git$', ''
    Write-ColoredHost "  仓库: $repoUrl" "White"
    Write-ColoredHost "  Tag: $repoUrl/releases/tag/$tagName" "White"
}
Write-Host ""

Write-ColoredHost "下一步操作：" "Cyan"
Write-ColoredHost "  1. 访问仓库创建 Release: $repoUrl/releases/new?tag=$tagName" "White"
Write-ColoredHost "  2. 更新 CHANGELOG.md 添加版本说明" "White"
Write-ColoredHost "  3. 通知团队成员新版本发布" "White"
Write-Host ""

if ($DryRun) {
    Write-Warning "这是模拟运行，实际未进行任何提交或推送"
    Write-Warning "如需实际发布，请移除 -DryRun 参数"
}

Write-Host ""
Write-ColoredHost "========================================" "Green"
Write-ColoredHost "发布脚本执行完毕！" "Green"
Write-ColoredHost "========================================" "Green"
