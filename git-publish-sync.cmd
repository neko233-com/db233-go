@echo off
chcp 65001 >nul
echo.
echo ======================================
echo   db233-go Git Publish Sync
echo ======================================
echo.

REM Ensure GitHub uses the configured username
echo [1/6] Configuring Git credential...
git config credential.https://github.com.username neko233-com
echo OK: GitHub username set to neko233-com
echo.

REM Push to origin
echo [2/6] Pushing to origin...
git push origin main
if errorlevel 1 (
    echo FAILED: push to origin main
    pause
    exit /b 1
)
echo OK: pushed to origin main
echo.

echo [3/6] Pushing tags to origin...
git push origin --tags
if errorlevel 1 (
    echo FAILED: push tags to origin
    pause
    exit /b 1
)
echo OK: pushed tags to origin
echo.

REM Configure GitHub remote
echo [4/6] Configuring GitHub remote...
git remote rm github 2>nul
git remote add github https://github.com/neko233-com/db233-go.git
echo OK: GitHub remote configured
echo.

REM Push to GitHub
echo [5/6] Pushing to GitHub...
git push github main
if errorlevel 1 (
    echo FAILED: push to GitHub main
    pause
    exit /b 1
)
echo OK: pushed to GitHub main
echo.

echo [6/6] Pushing tags to GitHub...
git push github --tags
if errorlevel 1 (
    echo FAILED: push tags to GitHub
    pause
    exit /b 1
)
echo OK: pushed tags to GitHub
echo.

echo ======================================
echo   All done.
echo ======================================
echo.
pause
