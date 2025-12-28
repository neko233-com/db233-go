# db233-go 一键发布脚本 (PowerShell)

Write-Host "正在构建项目..."
go build ./...

if ($LASTEXITCODE -ne 0) {
    Write-Host "构建失败"
    exit 1
}

Write-Host "运行测试..."
go test ./...

if ($LASTEXITCODE -ne 0) {
    Write-Host "测试失败"
    exit 1
}

Write-Host "获取最新标签..."
$latestTag = git describe --tags --abbrev=0

Write-Host "当前最新标签: $latestTag"

$newVersion = Read-Host "请输入新版本号 (例如 v1.0.0)"

Write-Host "创建标签 $newVersion..."
git tag $newVersion

Write-Host "推送标签..."
git push origin $newVersion

Write-Host "发布完成！"