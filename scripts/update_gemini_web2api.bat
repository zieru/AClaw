@echo off
setlocal enabledelayedexpansion

echo ========================================================
echo   GoAssistant: Update gemini-web2api Dependency
echo ========================================================

cd /d "%~dp0.."

if exist "scripts\gemini-web2api\.git" (
    echo [*] Updating via git pull in scripts\gemini-web2api...
    git -C scripts\gemini-web2api pull
) else (
    echo [*] Updating via pip...
    python -m pip install --upgrade "git+https://github.com/Sophomoresty/gemini-web2api.git" "httpx>=0.25"
)

echo.
echo [*] Testing runner diagnostic...
python scripts\gemini_web_runner.py --test

echo.
echo [*] Update completed.
