#!/usr/bin/env bash
set -e
echo "========================================================"
echo " Installing Python browser-use dependencies with uv...  "
echo "========================================================"
cd "$(dirname "$0")"

# Check if uv is installed, if not install it
if ! command -v uv &> /dev/null; then
    if [ -f "$HOME/.local/bin/uv" ]; then
        export PATH="$HOME/.local/bin:$PATH"
    elif [ -f "/root/.local/bin/uv" ]; then
        export PATH="/root/.local/bin:$PATH"
    else
        echo "Installing uv..."
        curl -LsSf https://astral.sh/uv/install.sh | sh
        export PATH="$HOME/.local/bin:$PATH"
    fi
fi

uv sync
echo "========================================================"
echo " Installing Playwright Chromium browser & dependencies... "
echo "========================================================"
uv run playwright install --with-deps chromium || uv run playwright install chromium
echo "========================================================"
echo " Setup complete! Ready for Browser-Use on Linux.        "
echo "========================================================"
