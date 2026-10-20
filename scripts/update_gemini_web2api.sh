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
echo "  GoAssistant: Update gemini-web2api Dependency"
echo "========================================================"

TARGET_DIR="scripts/gemini-web2api"

# 1. Update gemini-web2api via Git (avoids upstream setuptools pyproject.toml bug)
if [ -d "$TARGET_DIR/.git" ]; then
    echo "[*] Updating gemini-web2api via git pull..."
    git -C "$TARGET_DIR" pull
else
    echo "[*] Cloning gemini-web2api from GitHub..."
    mkdir -p scripts
    rm -rf "$TARGET_DIR"
    git clone --depth 1 https://github.com/Sophomoresty/gemini-web2api.git "$TARGET_DIR"
fi

# 2. Ensure httpx is installed for streaming support
if ! $PYTHON_CMD -c "import httpx" >/dev/null 2>&1; then
    echo "[*] Installing httpx dependency..."
    if command -v apk >/dev/null 2>&1; then
        apk add py3-httpx || $PYTHON_CMD -m pip install --break-system-packages "httpx>=0.25" || true
    elif $PYTHON_CMD -m pip --version >/dev/null 2>&1; then
        $PYTHON_CMD -m pip install --upgrade --break-system-packages "httpx>=0.25" || true
    fi
fi

echo ""
echo "[*] Testing runner diagnostic..."
$PYTHON_CMD scripts/gemini_web_runner.py --test

echo ""
echo "[*] Update completed successfully!"
