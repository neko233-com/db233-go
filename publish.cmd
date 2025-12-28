@echo off
REM db233-go 一键发布脚本 (Windows)

echo 正在构建项目...
go build ./...

if %errorlevel% neq 0 (
    echo 构建失败
    exit /b 1
)

echo 运行测试...
go test ./...

if %errorlevel% neq 0 (
    echo 测试失败
    exit /b 1
)

echo 获取最新标签...
for /f %%i in ('git describe --tags --abbrev=0') do set LATEST_TAG=%%i

echo 当前最新标签: %LATEST_TAG%

echo 请输入新版本号 (例如 v1.0.0):
set /p NEW_VERSION=

echo 创建标签 %NEW_VERSION%...
git tag %NEW_VERSION%

echo 推送标签...
git push origin %NEW_VERSION%

echo 发布完成！