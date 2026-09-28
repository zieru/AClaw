# Browser-Use Autonomous Agent MCP Server

Server MCP berbasis Python untuk menjalankan tugas web otonom menggunakan library [browser-use](https://github.com/browser-use/browser-use) dan Playwright.

## 🚀 Fitur Utama
1. **Dynamic Model Inheritance**: Otomatis menggunakan model AI aktif dari Orchestrator GoAssistant (GLM-5.3, Gemini, GPT-4o, DeepSeek, Claude, dll).
2. **Vision & Text-DOM Auto Switching**: 
   - Model Vision (Gemini, GPT-4o, Claude) -> `use_vision=True` (screenshot web parsing).
   - Model Text/Reasoning (DeepSeek, GLM, Qwen, Llama) -> `use_vision=False` (Text-DOM parsing hemat token).
3. **Docker Alpine Chrome Support**: Auto-detect port CDP `9222` di `127.0.0.1`.

## 🐳 Menjalankan Docker Alpine Chrome (VPS Deployment)
Agar VPS tidak terbebani instalasi binary Chromium besar di OS host, jalankan container ultra-ringan (~180MB):

```bash
docker run -d \
  --name goassistant-chrome \
  -p 127.0.0.1:9222:9222 \
  --restart=unless-stopped \
  --shm-size=512m \
  --memory=1g \
  zenika/alpine-chrome \
  --no-sandbox \
  --disable-blink-features=AutomationControlled \
  --user-agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36" \
  --window-size=1920,1080 \
  --remote-debugging-address=0.0.0.0 \
  --remote-debugging-port=9222
```

## 🛠️ Setup Manual (Jika tanpa Docker)
Jalankan setup script untuk instalasi environment virtual `uv`:
```bash
./setup.sh    # Linux / macOS
setup.bat     # Windows
```
