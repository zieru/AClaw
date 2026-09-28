import asyncio
import os
import shutil
import sys
from datetime import datetime
from typing import Optional
from dotenv import load_dotenv

# Muat file .env dari folder project jika ada
load_dotenv()
project_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
load_dotenv(os.path.join(project_root, ".env"))

from mcp.server.fastmcp import FastMCP
from browser_use.agent.service import Agent
from browser_use.browser.profile import BrowserProfile
from browser_use.llm import ChatOpenAI, ChatGoogle, ChatDeepSeek

mcp = FastMCP("browser-use-server")

def get_db_providers():
    """Mengambil provider aktif dari database SQLite goassistant.db"""
    db_path = os.path.join(project_root, "data", "goassistant.db")
    if not os.path.exists(db_path):
        return []
    try:
        import sqlite3
        conn = sqlite3.connect(db_path)
        c = conn.cursor()
        c.execute("SELECT name, base_url, api_key, default_model FROM providers WHERE is_active = 1 ORDER BY priority ASC")
        rows = c.fetchall()
        conn.close()
        return rows
    except Exception:
        return []

def is_valid_key(val: Optional[str]) -> bool:
    return bool(val and not val.startswith("${") and val.strip() != "")

def resolve_llm_and_vision(model_name: str, requested_vision: Optional[bool] = None):
    """
    Menyelesaikan LLM dan mode vision.
    Untuk DeepSeek / model text-only, use_vision otomatis diatur ke False (hemat token, cepat, anti-error).
    Mendukung auto-fallback ke database GoAssistant (dahl, HCNSEC, dll).
    """
    model_lower = model_name.lower().strip()
    
    # 1. Tentukan apakah vision aktif
    if requested_vision is not None:
        use_vision = requested_vision
    elif any(k in model_lower for k in ["deepseek", "qwen", "llama", "mistral", "gemma"]):
        use_vision = False
    else:
        use_vision = True

    # 2. Cek API Key dari Environment Variable
    deepseek_key = os.getenv("DEEPSEEK_API_KEY")
    gemini_key = os.getenv("GEMINI_API_KEY") or os.getenv("GOOGLE_API_KEY")
    openai_key = os.getenv("OPENAI_API_KEY")

    if "deepseek" in model_lower and is_valid_key(deepseek_key):
        return ChatOpenAI(
            model=model_name,
            base_url="https://api.deepseek.com",
            api_key=deepseek_key
        ), use_vision

    if "gemini" in model_lower and is_valid_key(gemini_key):
        return ChatGoogle(model=model_name, api_key=gemini_key), use_vision

    if is_valid_key(openai_key):
        kwargs = {"model": model_name, "api_key": openai_key}
        if os.getenv("OPENAI_BASE_URL"):
            kwargs["base_url"] = os.getenv("OPENAI_BASE_URL")
        return ChatOpenAI(**kwargs), use_vision

    # 3. Cari dari Database SQLite GoAssistant (misal: dahl, HCNSEC)
    db_provs = get_db_providers()
    if db_provs:
        # Prioritaskan provider yang sesuai dengan model atau provider pertama
        target_prov = db_provs[0]
        for p in db_provs:
            p_name, p_base, p_key, p_model = p
            if "deepseek" in model_lower and ("dahl" in p_name.lower() or "deepseek" in p_name.lower() or "hcnsec" in p_name.lower()):
                target_prov = p
                break

        p_name, p_base, p_key, p_model = target_prov
        actual_model = model_name
        # Jika model_name adalah generic "deepseek-chat" tapi DB memiliki model spesifik
        if model_name in ["deepseek-chat", "default", ""] and p_model and p_model != "auto":
            actual_model = p_model

        if is_valid_key(p_key) and p_base:
            return ChatOpenAI(
                model=actual_model,
                base_url=p_base,
                api_key=p_key
            ), use_vision

    # 4. Fallback ke OmniRoute lokal (:20128)
    omni_base = os.getenv("OMNIROUTE_BASE_URL", "http://localhost:20128/v1")
    return ChatOpenAI(
        model=model_name,
        base_url=omni_base,
        api_key=os.getenv("OMNIROUTE_API_KEY", "sk-omniroute")
    ), use_vision


@mcp.tool(
    name="browser",
    description=(
        "Browser otonom (autonomous web agent) berbasis Python browser-use. "
        "AI dapat menjelajahi web secara mandiri untuk mencari informasi, membandingkan harga/tiket (Traveloka, Tokopedia, dll), "
        "membuka URL, mengisi form formulir, mengekstrak data dari berbagai halaman web, dan menavigasi situs interaktif. "
        "Mendukung model DeepSeek (Text-DOM mode hemat token), Gemini, dan OpenAI."
    )
)
async def browser(
    task: str = "",
    url: Optional[str] = None,
    action: Optional[str] = None,
    model: str = "deepseek-chat",
    headless: bool = True,
    max_steps: int = 15,
    use_vision: Optional[bool] = None,
    attach_screenshot: bool = True
) -> str:
    """
    Eksekusi tugas browser otonom dengan kontrol dinamis dan dukungan DeepSeek.
    """
    try:
        clean_task = (task or "").strip()
        if not clean_task:
            if url:
                clean_task = f"Kunjungi situs {url} dan baca informasi atau selesaikan kebutuhan halaman tersebut."
            else:
                return "❌ Parameter 'task' atau 'url' wajib diisi untuk menjalankan browser."
        elif url and url not in clean_task:
            clean_task = f"Buka {url} dan selesaikan tugas berikut: {clean_task}"

        llm, vision_enabled = resolve_llm_and_vision(model, use_vision)
        
        # Auto-detect system Chromium/Chrome (especially useful on Debian 11 / older distros)
        executable_path = os.getenv("CHROME_PATH")
        if not executable_path:
            for p in ["/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/snap/bin/chromium"]:
                if os.path.exists(p):
                    executable_path = p
                    break

        profile = BrowserProfile(
            headless=headless,
            disable_security=True,
            executable_path=executable_path,
        )
        
        # Ensure provider attribute exists for Agent telemetry and logging
        if not hasattr(llm, "provider") or not getattr(llm, "provider", None):
            try:
                setattr(llm, "provider", "openai")
            except Exception:
                pass

        agent = Agent(
            task=clean_task,
            llm=llm,
            browser_profile=profile,
            max_actions_per_step=3,
            use_vision=vision_enabled
        )
        
        history = await agent.run(max_steps=max_steps)
        final_result = history.final_result() or "Tugas penjelajahan web selesai tanpa kesimpulan teks khusus."
        
        # Ambil screenshot halaman terakhir jika diminta dan tersedia
        attachment_tag = ""
        if attach_screenshot:
            screenshot_paths = history.screenshot_paths(return_none_if_not_screenshot=False)
            if screenshot_paths:
                last_shot = screenshot_paths[-1]
                if last_shot and os.path.exists(last_shot):
                    screenshot_dir = os.path.abspath(os.path.join(project_root, "data", "browser", "screenshots"))
                    os.makedirs(screenshot_dir, exist_ok=True)
                    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
                    dest_path = os.path.join(screenshot_dir, f"browser_use_{timestamp}.png")
                    try:
                        shutil.copy2(last_shot, dest_path)
                        attachment_tag = f"\n\n[ATTACH_FILE:{dest_path}|CAPTION:Tangkapan Layar Hasil Browser-Use ({model})]"
                    except Exception:
                        attachment_tag = f"\n\n[ATTACH_FILE:{last_shot}|CAPTION:Tangkapan Layar Hasil Browser-Use ({model})]"
                
        mode_str = "Vision" if vision_enabled else "Text-DOM (DeepSeek Mode)"
        return f"🌐 <b>[Browser-Use Autonomous Agent - {model} ({mode_str})]</b>\n\n{final_result}{attachment_tag}"
        
    except Exception as err:
        return f"❌ Gagal menjalankan tugas browser-use: {err}"

if __name__ == "__main__":
    mcp.run(transport="stdio")
