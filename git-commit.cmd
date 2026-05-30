@echo off
chcp 65001 >nul
echo.
echo ======================================
echo   db233-go Git Commit and Push
echo ======================================
echo.

git add .
if errorlevel 1 (
    echo FAILED: git add
    pause
    exit /b 1
)

git commit -m "auto commit this version code"
if errorlevel 1 (
    echo WARN: nothing to commit or commit failed
    pause
    exit /b 1
)

git push origin main
if errorlevel 1 (
    echo FAILED: push to origin main
    pause
    exit /b 1
)

echo.
echo OK: committed and pushed to origin main
echo.
pause
