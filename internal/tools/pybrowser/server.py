import asyncio
import os
import shutil
import socket
import sys
from datetime import datetime
from typing import Optional
from dotenv import load_dotenv

# Muat file .env dari folder project jika ada
load_dotenv()
project_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
load_dotenv(os.path.join(project_root, ".env"))

def is_cdp_available(host: str = "127.0.0.1", port: int = 9222, timeout: float = 0.5) -> bool:
    """Cek apakah headless Chromium di Docker/CDP aktif dan siap menerima koneksi"""
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except OSError:
        return False

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

def resolve_llm_and_vision(
    model_name: Optional[str] = None,
    provider_name: Optional[str] = None,
    requested_vision: Optional[bool] = None,
    api_base: Optional[str] = None,
    api_key: Optional[str] = None
):
    """
    Menyelesaikan LLM dan mode vision secara dinamis sesuai orchestrator GoAssistant.
    Jika model_name kosong atau 'auto', otomatis mewarisi model default provider aktif.
    """
    db_provs = get_db_providers()

    # 1. Resolusi nama model
    chosen_model = (model_name or "").strip()
    if not chosen_model or chosen_model.lower() == "auto":
        if db_provs:
            chosen_model = db_provs[0][3] or "deepseek-chat"
        else:
            chosen_model = "deepseek-chat"

    model_lower = chosen_model.lower()

    # 2. Tentukan apakah vision aktif secara dinamis
    if requested_vision is not None:
        vision_enabled = requested_vision
    elif any(k in model_lower for k in ["gemini", "gpt-4o", "gpt-5", "claude", "vl", "vision", "omni"]):
        vision_enabled = True
    else:
        # DeepSeek, GLM, Qwen text, Llama, Mistral, dll (Text-DOM mode hemat token)
        vision_enabled = False

    # 3. Jika pemanggil menyertakan api_base dan api_key langsung
    if is_valid_key(api_key) and api_base:
        return ChatOpenAI(
            model=chosen_model,
            base_url=api_base,
            api_key=api_key
        ), vision_enabled

    # 4. Cek kredensial dari Database SQLite GoAssistant (berdasarkan provider_name atau model)
    if db_provs:
        # Prioritaskan provider yang sesuai dengan provider_name
        if provider_name:
            p_low = provider_name.lower().strip()
            for p in db_provs:
                p_name, p_base, p_key, p_model = p
                if p_low in p_name.lower() or p_name.lower() in p_low:
                    if is_valid_key(p_key) and p_base:
                        return ChatOpenAI(
                            model=chosen_model,
                            base_url=p_base,
                            api_key=p_key
                        ), vision_enabled

        # Cari yang sesuai dengan model_name
        for p in db_provs:
            p_name, p_base, p_key, p_model = p
            if any(k in model_lower for k in [p_name.lower(), (p_model or "").lower()]):
                if is_valid_key(p_key) and p_base:
                    return ChatOpenAI(
                        model=chosen_model,
                        base_url=p_base,
                        api_key=p_key
                    ), vision_enabled

        # Fallback ke provider aktif pertama jika ada base & key
        first_name, first_base, first_key, first_model = db_provs[0]
        if is_valid_key(first_key) and first_base:
            return ChatOpenAI(
                model=chosen_model,
                base_url=first_base,
                api_key=first_key
            ), vision_enabled

    # 5. Cek API Key dari Environment Variable
    deepseek_key = os.getenv("DEEPSEEK_API_KEY")
    gemini_key = os.getenv("GEMINI_API_KEY") or os.getenv("GOOGLE_API_KEY")
    openai_key = os.getenv("OPENAI_API_KEY")

    if "gemini" in model_lower and is_valid_key(gemini_key):
        return ChatGoogle(model=chosen_model, api_key=gemini_key), vision_enabled

    if "deepseek" in model_lower and is_valid_key(deepseek_key):
        return ChatOpenAI(
            model=chosen_model,
            base_url="https://api.deepseek.com",
            api_key=deepseek_key
        ), vision_enabled

    if is_valid_key(openai_key):
        kwargs = {"model": chosen_model, "api_key": openai_key}
        if os.getenv("OPENAI_BASE_URL"):
            kwargs["base_url"] = os.getenv("OPENAI_BASE_URL")
        return ChatOpenAI(**kwargs), vision_enabled

    # 6. Fallback ke OmniRoute lokal (:20128)
    omni_base = os.getenv("OMNIROUTE_BASE_URL", "http://localhost:20128/v1")
    return ChatOpenAI(
        model=chosen_model,
        base_url=omni_base,
        api_key=os.getenv("OMNIROUTE_API_KEY", "sk-omniroute")
    ), vision_enabled


@mcp.tool(
    name="browser",
    description=(
        "Browser otonom (autonomous web agent) berbasis Python browser-use. "
        "AI dapat menjelajahi web secara mandiri untuk mencari informasi, membandingkan harga/tiket (Traveloka, Tokopedia, dll), "
        "membuka URL, mengisi form formulir, mengekstrak data dari berbagai halaman web, dan menavigasi situs interaktif. "
        "Mendukung model apa pun (DeepSeek, GLM, Gemini, GPT-4o, Claude) dan otomatis mewarisi model aktif orchestrator."
    )
)
async def browser(
    task: str = "",
    url: Optional[str] = None,
    action: Optional[str] = None,
    model: Optional[str] = None,
    provider: Optional[str] = None,
    api_base: Optional[str] = None,
    api_key: Optional[str] = None,
    headless: bool = True,
    max_steps: int = 15,
    use_vision: Optional[bool] = None,
    attach_screenshot: bool = True
) -> str:
    """
    Eksekusi tugas browser otonom dengan kontrol dinamis dan dukungan model multi-provider.
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

        llm, vision_enabled = resolve_llm_and_vision(model, provider, use_vision, api_base, api_key)
        
        # 1. Cek apakah ada Docker Chromium CDP aktif di 127.0.0.1:9222 atau env CDP_URL
        cdp_endpoint = os.getenv("CDP_URL")
        if not cdp_endpoint and is_cdp_available("127.0.0.1", 9222):
            cdp_endpoint = "http://127.0.0.1:9222"

        # 2. Jika tidak ada CDP Docker, baru fallback ke binary browser lokal
        executable_path = None
        if not cdp_endpoint:
            executable_path = os.getenv("CHROME_PATH")
            if not executable_path:
                for p in ["/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/snap/bin/chromium"]:
                    if os.path.exists(p):
                        executable_path = p
                        break

        default_ua = os.getenv(
            "BROWSER_USER_AGENT",
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
        )
        profile = BrowserProfile(
            headless=headless,
            disable_security=True,
            user_agent=default_ua,
            headers={
                "Accept-Language": "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7",
            },
            cdp_url=cdp_endpoint,
            executable_path=executable_path if not cdp_endpoint else None,
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
