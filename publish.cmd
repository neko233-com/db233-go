@echo off
REM db233-go 一键发布脚本 (Windows) - 调用PowerShell脚本

echo ========================================
echo    db233-go 发布脚本 (Windows)
echo ========================================
echo.
echo 正在启动PowerShell发布脚本...
echo.

powershell -ExecutionPolicy Bypass -File "%~dp0publish.ps1"

if %errorlevel% neq 0 (
    echo.
    echo 发布失败 (错误代码: %errorlevel%)
    pause
    exit /b 1
)

echo.
echo ========================================
echo 发布完成！
echo ========================================
echo.
echo 下一步操作：
echo   1. 访问仓库创建 Release
echo   2. 更新 CHANGELOG.md 添加版本说明
echo   3. 通知团队成员新版本发布
echo.
echo 脚本执行完毕，窗口将在 5 秒后自动关闭...
timeout /t 5 /nobreak >nul
