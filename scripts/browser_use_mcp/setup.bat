@echo off
echo ========================================================
echo  Installing Python browser-use dependencies with uv...
echo ========================================================
cd /d "%~dp0"
uv sync
echo ========================================================
echo  Installing Playwright Chromium browser...
echo ========================================================
uv run playwright install chromium
echo ========================================================
echo  Setup complete! Ready for DeepSeek ^& Browser-Use.
echo ========================================================
