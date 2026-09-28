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
# Try Playwright bundled chromium first
INSTALL_OK=0
if uv run playwright install --with-deps chromium 2>/dev/null; then
    INSTALL_OK=1
elif uv run playwright install chromium 2>/dev/null; then
    INSTALL_OK=1
fi

if [ "$INSTALL_OK" -eq 1 ]; then
    echo "✅ Playwright bundled Chromium installed successfully!"
else
    echo "⚠️  Playwright bundled Chromium not supported on this OS (e.g. Debian 11 / older glibc)."
    echo "🔍 Checking for system chromium..."
    SYS_BROWSER=""
    for b in "/usr/bin/chromium" "/usr/bin/chromium-browser" "/usr/bin/google-chrome" "/usr/bin/google-chrome-stable"; do
        if [ -f "$b" ]; then
            SYS_BROWSER="$b"
            break
        fi
    done

    if [ -n "$SYS_BROWSER" ]; then
        echo "✅ System browser detected: $SYS_BROWSER"
        echo "   browser-use will automatically use this system browser!"
    else
        echo "💡 To run browser-use on Debian 11 / older distros, install system chromium:"
        echo "   sudo apt-get update && sudo apt-get install -y chromium"
        if command -v apt-get &>/dev/null; then
            if [ "$EUID" -eq 0 ]; then
                echo "📥 Auto-installing system chromium via apt-get..."
                apt-get update && apt-get install -y chromium || true
            else
                echo "👉 Please run with sudo: sudo apt-get update && sudo apt-get install -y chromium"
            fi
        fi
    fi
fi

echo "========================================================"
echo " Setup complete! Ready for Browser-Use on Linux.        "
echo "========================================================"
