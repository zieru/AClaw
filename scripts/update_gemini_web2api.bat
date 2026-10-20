@echo off
setlocal enabledelayedexpansion

echo ========================================================
echo   GoAssistant: Update gemini-web2api Dependency
echo ========================================================

cd /d "%~dp0.."

set TARGET_DIR=scripts\gemini-web2api

if exist "%TARGET_DIR%\.git" (
    echo [*] Updating via git pull in %TARGET_DIR%...
    git -C "%TARGET_DIR%" pull
) else (
    where git >nul 2>&1
    if !ERRORLEVEL! equ 0 (
        echo [*] Cloning gemini-web2api to %TARGET_DIR%...
        git clone --depth 1 https://github.com/Sophomoresty/gemini-web2api.git "%TARGET_DIR%"
    ) else (
        echo [*] Updating via pip...
        python -m pip install --upgrade "git+https://github.com/Sophomoresty/gemini-web2api.git" "httpx>=0.25"
    )
)

echo.
echo [*] Testing runner diagnostic...
python scripts\gemini_web_runner.py --test

echo.
echo [*] Update completed.
