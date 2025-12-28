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
echo 发布完成！
pause