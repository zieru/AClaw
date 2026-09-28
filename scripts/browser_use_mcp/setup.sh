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
echo " Checking Browser Engine / Chromium...                  "
echo "========================================================"

# 1. Check if Docker CDP port 9222 is already running
if nc -z 127.0.0.1 9222 2>/dev/null || curl -s http://127.0.0.1:9222/json/version &>/dev/null; then
    echo "✅ Docker Chromium (CDP port 9222) is ACTIVE and READY!"
    echo "   browser-use will automatically connect via cdp_url='http://127.0.0.1:9222'"
else
    # 2. Try Playwright bundled chromium
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
            echo "💡 Pilihan 1: Pasang Chromium Native di Debian 11:"
            echo "   sudo apt-get update && sudo apt-get install -y chromium"
            echo ""
            echo "💡 Pilihan 2: Jalankan Docker Chromium (zenika/alpine-chrome, ~180MB):"
            echo "   docker run -d --name goassistant-chrome -p 127.0.0.1:9222:9222 --restart=unless-stopped --shm-size=256m --memory=512m zenika/alpine-chrome --no-sandbox --remote-debugging-address=0.0.0.0 --remote-debugging-port=9222"
        fi
    fi
fi
echo "========================================================"
echo " Checking Camoufox Stealth Engine (Firefox Anti-Detect).."
echo "========================================================"
if uv run python -c "from camoufox.pkgman import installed_verstr; installed_verstr()" 2>/dev/null; then
    echo "✅ Camoufox browser binary is installed and ready!"
else
    echo "📥 Fetching Camoufox browser binary for Cloudflare/WAF bypass..."
    uv run python -m camoufox fetch || echo "⚠️ Camoufox fetch skipped or failed. Run 'uv run python -m camoufox fetch' manually."
fi

echo "========================================================"
echo " Setup complete! Ready for Dual-Engine Browser on Linux. "
echo "========================================================"
