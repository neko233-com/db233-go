@echo off
chcp 65001 >nul
echo.
echo ======================================
echo   DB233-GO 发布同步脚本
echo ======================================
echo.

REM 确保 GitHub 使用指定的用户名
echo [1/6] 配置 Git Credential...
git config credential.https://github.com.username neko233-com
echo ✓ GitHub 用户名设置为: neko233-com
echo.

REM 推送到 origin (Gitee)
echo [2/6] 推送到 origin (Gitee)...
git push origin main
if errorlevel 1 (
    echo ✗ 推送到 origin main 失败
    pause
    exit /b 1
)
echo ✓ 成功推送到 origin main
echo.

echo [3/6] 推送标签到 origin...
git push origin --tags
if errorlevel 1 (
    echo ✗ 推送标签到 origin 失败
    pause
    exit /b 1
)
echo ✓ 成功推送标签到 origin
echo.

REM 配置 GitHub 远程仓库
echo [4/6] 配置 GitHub 远程仓库...
git remote rm github 2>nul
git remote add github https://github.com/neko233-com/db233-go.git
echo ✓ GitHub 远程仓库已配置
echo.

REM 推送到 GitHub
echo [5/6] 推送到 GitHub...
git push github main
if errorlevel 1 (
    echo ✗ 推送到 GitHub main 失败
    pause
    exit /b 1
)
echo ✓ 成功推送到 GitHub main
echo.

echo [6/6] 推送标签到 GitHub...
git push github --tags
if errorlevel 1 (
    echo ✗ 推送标签到 GitHub 失败
    pause
    exit /b 1
)
echo ✓ 成功推送标签到 GitHub
echo.

echo ======================================
echo   ✓ 所有操作完成！
echo ======================================
echo.
pause

