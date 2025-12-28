# db233-go 一键发布脚本 (PowerShell)

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "   db233-go 发布脚本 (PowerShell)" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan

Write-Host ""
Write-Host "检查Git状态..." -ForegroundColor Yellow
$status = git status --porcelain
if ($status) {
    Write-Host "警告: 工作目录有未提交的更改" -ForegroundColor Red
    Write-Host "请先提交或暂存所有更改" -ForegroundColor Red
    Write-Host ""
    git status
    Write-Host ""
    $confirm = Read-Host "是否继续? (y/N)"
    if ($confirm -ne "y" -and $confirm -ne "Y") {
        Write-Host "发布已取消" -ForegroundColor Red
        exit 1
    }
}

Write-Host ""
Write-Host "检查当前分支..." -ForegroundColor Yellow
$currentBranch = git branch --show-current
Write-Host "当前分支: $currentBranch" -ForegroundColor Green

if ($currentBranch -ne "main" -and $currentBranch -ne "master") {
    Write-Host "警告: 当前不在main或master分支上" -ForegroundColor Red
    $confirm = Read-Host "是否继续? (y/N)"
    if ($confirm -ne "y" -and $confirm -ne "Y") {
        Write-Host "发布已取消" -ForegroundColor Red
        exit 1
    }
}

Write-Host ""
Write-Host "正在构建项目..." -ForegroundColor Yellow
go build ./...

if ($LASTEXITCODE -ne 0) {
    Write-Host "构建失败" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "运行测试..." -ForegroundColor Yellow
go test ./...

if ($LASTEXITCODE -ne 0) {
    Write-Host "测试失败" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "获取最新标签..." -ForegroundColor Yellow
try {
    $latestTag = git describe --tags --abbrev=0
    Write-Host "当前最新标签: $latestTag" -ForegroundColor Green
} catch {
    Write-Host "没有找到现有标签" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
$newVersion = Read-Host "请输入新版本号 (例如 v1.0.0)"
Write-Host "========================================" -ForegroundColor Cyan

if (-not $newVersion) {
    Write-Host "错误: 版本号不能为空" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "验证版本号格式..." -ForegroundColor Yellow
if ($newVersion -notmatch "^v\d+\.\d+\.\d+$") {
    if ($newVersion -notmatch "^\d+\.\d+\.\d+$") {
        Write-Host "错误: 版本号格式无效 (支持格式: v1.0.0 或 1.0.0)" -ForegroundColor Red
        exit 1
    } else {
        Write-Host "检测到不带v前缀的版本号，将自动添加v前缀" -ForegroundColor Yellow
        $newVersion = "v$newVersion"
        Write-Host "版本号已更新为: $newVersion" -ForegroundColor Green
    }
}

Write-Host ""
Write-Host "检查标签是否已存在..." -ForegroundColor Yellow
$existingTag = git tag -l $newVersion
if ($existingTag) {
    Write-Host "错误: 标签 $newVersion 已存在" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "创建标签 $newVersion..." -ForegroundColor Yellow
git tag -a $newVersion -m "Release $newVersion"

if ($LASTEXITCODE -ne 0) {
    Write-Host "创建标签失败" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "推送标签到远程仓库..." -ForegroundColor Yellow
git push origin $newVersion

if ($LASTEXITCODE -ne 0) {
    Write-Host "推送标签失败" -ForegroundColor Red
    Write-Host "提示: 如果推送失败，可以手动运行: git push origin $newVersion" -ForegroundColor Yellow
    exit 1
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "发布完成！" -ForegroundColor Green
Write-Host "版本: $newVersion" -ForegroundColor Green
Write-Host "分支: $currentBranch" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green

Write-Host ""
Write-Host "接下来你可以:" -ForegroundColor Cyan
Write-Host "1. 在GitHub上创建Release" -ForegroundColor White
Write-Host "2. 上传构建的二进制文件" -ForegroundColor White
Write-Host "3. 更新CHANGELOG.md" -ForegroundColor White
Write-Host "4. 通知团队成员" -ForegroundColor White

Read-Host "按Enter键退出"