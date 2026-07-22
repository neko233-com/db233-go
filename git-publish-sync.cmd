@echo off
setlocal
echo This legacy entry point now uses the production release gate.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0publish.ps1" %*
exit /b %errorlevel%
