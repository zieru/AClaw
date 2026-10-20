#!/bin/sh
# GoAssistant: Update gemini-web2api Dependency (Linux / Alpine compatible)

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$ROOT_DIR"

PYTHON_CMD="python3"
if ! command -v python3 >/dev/null 2>&1; then
    PYTHON_CMD="python"
fi

echo "========================================================"
echo "  GoAssistant: Update gemini-web2api Dependency (Alpine)"
echo "========================================================"

if [ -d "scripts/gemini-web2api/.git" ]; then
    echo "[*] Updating via git pull in scripts/gemini-web2api..."
    git -C scripts/gemini-web2api pull
else
    echo "[*] Updating via pip..."
    # Include --break-system-packages for Alpine / PEP 668 compliance
    $PYTHON_CMD -m pip install --upgrade --break-system-packages "git+https://github.com/Sophomoresty/gemini-web2api.git" "httpx>=0.25" || true
fi

echo ""
echo "[*] Testing runner diagnostic..."
$PYTHON_CMD scripts/gemini_web_runner.py --test

echo ""
echo "[*] Update completed."
