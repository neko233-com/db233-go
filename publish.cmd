@echo off
REM db233-go 一键发布脚本 (Windows)

echo ========================================
echo    db233-go 发布脚本 (Windows)
echo ========================================

echo 检查Git状态...
git status --porcelain > nul 2>&1
if %errorlevel% equ 0 (
    for /f %%i in ('git status --porcelain 2^>nul') do (
        echo 警告: 工作目录有未提交的更改
        echo 请先提交或暂存所有更改
        echo.
        git status
        echo.
        set /p CONFIRM="是否继续? (y/N): "
        if /i not "!CONFIRM!"=="y" (
            echo 发布已取消
            exit /b 1
        )
    )
)

echo 检查当前分支...
for /f %%i in ('git branch --show-current') do set CURRENT_BRANCH=%%i
echo 当前分支: %CURRENT_BRANCH%

if not "%CURRENT_BRANCH%"=="main" (
    if not "%CURRENT_BRANCH%"=="master" (
        echo 警告: 当前不在main或master分支上
        set /p CONFIRM="是否继续? (y/N): "
        if /i not "!CONFIRM!"=="y" (
            echo 发布已取消
            exit /b 1
        )
    )
)

echo.
echo 正在构建项目...
go build ./...

if %errorlevel% neq 0 (
    echo 构建失败
    exit /b 1
)

echo.
echo 运行测试...
go test ./...

if %errorlevel% neq 0 (
    echo 测试失败
    exit /b 1
)

echo.
echo 获取最新标签...
git describe --tags --abbrev=0 > nul 2>&1
if %errorlevel% equ 0 (
    for /f %%i in ('git describe --tags --abbrev=0') do set LATEST_TAG=%%i
    echo 当前最新标签: %LATEST_TAG%
) else (
    echo 没有找到现有标签
)

echo.
echo ========================================
echo 请输入新版本号 (例如 v1.0.0):
set /p NEW_VERSION=
echo ========================================

if "%NEW_VERSION%"=="" (
    echo 错误: 版本号不能为空
    exit /b 1
)

echo.
echo 验证版本号格式...
echo %NEW_VERSION% | findstr /r "^v[0-9]\+\.[0-9]\+\.[0-9]\+$" > nul
if %errorlevel% neq 0 (
    echo 警告: 版本号格式不标准 (建议使用 v1.0.0 格式)
    set /p CONFIRM="是否继续? (y/N): "
    if /i not "!CONFIRM!"=="y" (
        echo 发布已取消
        exit /b 1
    )
)

echo.
echo 检查标签是否已存在...
git tag -l "%NEW_VERSION%" | findstr "%NEW_VERSION%" > nul
if %errorlevel% equ 0 (
    echo 错误: 标签 %NEW_VERSION% 已存在
    exit /b 1
)

echo.
echo 创建标签 %NEW_VERSION%...
git tag -a %NEW_VERSION% -m "Release %NEW_VERSION%"

if %errorlevel% neq 0 (
    echo 创建标签失败
    exit /b 1
)

echo.
echo 推送标签到远程仓库...
git push origin %NEW_VERSION%

if %errorlevel% neq 0 (
    echo 推送标签失败
    echo 提示: 如果推送失败，可以手动运行: git push origin %NEW_VERSION%
    exit /b 1
)

echo.
echo ========================================
echo 发布完成！
echo 版本: %NEW_VERSION%
echo 分支: %CURRENT_BRANCH%
echo ========================================

echo.
echo 接下来你可以:
echo 1. 在GitHub上创建Release
echo 2. 上传构建的二进制文件
echo 3. 更新CHANGELOG.md
echo 4. 通知团队成员

pause