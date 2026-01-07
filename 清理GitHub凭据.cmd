@echo off
chcp 65001 >nul
echo.
echo ======================================
echo   清理 GitHub 凭据脚本
echo ======================================
echo.
echo 此脚本将清理所有旧的 GitHub 凭据，
echo 然后在下次推送时会使用配置的用户名：neko233-com
echo.
pause
echo.

echo [1/7] 删除旧的 GitHub 凭据...
cmdkey /delete:LegacyGeneric:target=git:https://github.com 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据（已删除或不存在）
) else (
    echo   ✓ 已删除
)

echo [2/7] 删除带用户名的凭据...
cmdkey /delete:"LegacyGeneric:target=git:https://neko233-com@github.com" 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据
) else (
    echo   ✓ 已删除
)

echo [3/7] 删除 SolarisNeko 的凭据...
cmdkey /delete:"LegacyGeneric:target=git:https://SolarisNeko@github.com" 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据
) else (
    echo   ✓ 已删除
)

echo [4/7] 删除通用 GitHub 凭据...
cmdkey /delete:"LegacyGeneric:target=https://github.com/" 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据
) else (
    echo   ✓ 已删除
)

echo [5/7] 删除 Visual Studio GitHub 凭据...
cmdkey /delete:"LegacyGeneric:target=GitHub for Visual Studio - https://github.com/" 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据
) else (
    echo   ✓ 已删除
)

cmdkey /delete:"LegacyGeneric:target=GitHub for Visual Studio - https://neko233-com@github.com/" 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据
) else (
    echo   ✓ 已删除
)

cmdkey /delete:"LegacyGeneric:target=GitHub for Visual Studio - https://SolarisNeko@github.com/" 2>nul
if errorlevel 1 (
    echo   - 未找到该凭据
) else (
    echo   ✓ 已删除
)

echo.
echo [6/7] 确认 Git 配置...
git config --global credential.https://github.com.username neko233-com
git config credential.https://github.com.username neko233-com
echo ✓ GitHub 用户名已设置为: neko233-com
echo.

echo [7/7] 验证配置...
echo.
echo 全局配置：
git config --global --get credential.https://github.com.username
echo.
echo 仓库配置：
git config --get credential.https://github.com.username
echo.

echo ======================================
echo   ✓ 清理完成！
echo ======================================
echo.
echo 📝 下次推送到 GitHub 时：
echo    1. 会弹出 Git Credential Manager 登录窗口
echo    2. 用户名会自动填充为: neko233-com
echo    3. 输入您的密码或 Personal Access Token
echo    4. 凭据会被安全存储，之后不再弹窗
echo.
pause

