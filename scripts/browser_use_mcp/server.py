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
from browser_use.browser.session import BrowserSession
from browser_use.llm import ChatOpenAI, ChatGoogle, ChatDeepSeek
try:
    from playwright_stealth import Stealth
    _STEALTH_AVAILABLE = True
except ImportError:
    _STEALTH_AVAILABLE = False

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


async def call_llm(llm, prompt_text: str, image_b64: Optional[str] = None) -> str:
    """Helper serbaguna untuk memanggil LLM baik dari browser_use.llm maupun langchain tanpa error Unknown message type"""
    try:
        from browser_use.llm.messages import UserMessage, ContentPartTextParam, ContentPartImageParam, ImageURL
        if image_b64:
            content = [
                ContentPartTextParam(type="text", text=prompt_text),
                ContentPartImageParam(type="image_url", image_url=ImageURL(url=f"data:image/png;base64,{image_b64}"))
            ]
            msg = UserMessage(content=content)
        else:
            msg = UserMessage(content=prompt_text)
        resp = await llm.ainvoke([msg])
        if hasattr(resp, "completion"):
            return str(resp.completion)
        if hasattr(resp, "content"):
            return str(resp.content)
        return str(resp)
    except Exception as e1:
        try:
            from langchain_core.messages import HumanMessage
            if image_b64:
                resp = await llm.ainvoke([HumanMessage(content=[
                    {"type": "text", "text": prompt_text},
                    {"type": "image_url", "image_url": {"url": f"data:image/png;base64,{image_b64}"}}
                ])])
            else:
                resp = await llm.ainvoke([HumanMessage(content=prompt_text)])
            if hasattr(resp, "content"):
                return str(resp.content)
            return str(resp)
        except Exception:
            return ""


def is_camoufox_ready() -> bool:
    """Cek apakah binary browser Camoufox sudah terpasang dan siap digunakan"""
    try:
        from camoufox.pkgman import installed_verstr
        return installed_verstr() is not None
    except Exception:
        return False

async def run_camoufox_task(
    clean_task: str,
    target_url: Optional[str],
    llm,
    model_name: Optional[str] = "Auto",
    headless: bool = True,
    attach_screenshot: bool = True,
) -> str:
    """
    Eksekusi penjelajahan web stealth tingkat tinggi menggunakan Camoufox (Firefox C++ engine-level spoofing)
    untuk membobol Cloudflare Turnstile, WAF, dan anti-bot pada situs seperti booking.kai.id.
    """
    import re
    from camoufox.async_api import AsyncCamoufox

    # Resolusi URL awal dari task jika url tidak diberikan langsung
    url_to_open = target_url
    if not url_to_open:
        urls = re.findall(r'https?://[^\s<>"]+|www\.[^\s<>"]+', clean_task)
        if urls:
            url_to_open = urls[0]
            if not url_to_open.startswith("http"):
                url_to_open = f"https://{url_to_open}"
        elif "kai" in clean_task.lower():
            url_to_open = "https://booking.kai.id/"
        else:
            clean_q = clean_task.replace("cari", "").replace("buka", "").strip()
            url_to_open = f"https://duckduckgo.com/?q={clean_q}"

    screenshot_dir = os.path.abspath(os.path.join(project_root, "data", "browser", "screenshots"))
    os.makedirs(screenshot_dir, exist_ok=True)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    screenshot_path = os.path.join(screenshot_dir, f"camoufox_{timestamp}.png")

    try:
        async with AsyncCamoufox(
            headless=headless,
            humanize=True,
            geoip=True,
        ) as browser:
            page = await browser.new_page()
            await page.set_viewport_size({"width": 1920, "height": 1080})

            try:
                await page.goto(url_to_open, wait_until="domcontentloaded", timeout=45000)
            except Exception:
                pass

            # Berikan waktu jeda agar Cloudflare Turnstile menyelesaikan verifikasi otomatis
            await asyncio.sleep(4)

            # Cek jika masih di halaman challenge Turnstile, tunggu tambahan beberapa detik
            title = await page.title()
            content = await page.content()
            if "Just a moment" in title or "Checking your browser" in content or "Attention Required" in title:
                await asyncio.sleep(5)
                title = await page.title()

            # -----------------------------------------------------------------
            # INTERACTIVE ACTION EXECUTOR (Form Filling, Typing, Clicking)
            # -----------------------------------------------------------------
            interactive_keywords = ["login", "masuk", "isi", "ketik", "klik", "submit", "daftar", "pesan", "booking", "cari", "username", "password", "email"]
            wants_interaction = any(kw in clean_task.lower() for kw in interactive_keywords)

            action_log = []
            if wants_interaction:
                import json
                # Kumpulkan elemen form & interaktif yang terlihat di halaman
                elements = await page.evaluate("""
                    () => {
                        const items = [];
                        document.querySelectorAll('input:not([type="hidden"]), select, textarea, button, input[type="submit"]').forEach((el) => {
                            const rect = el.getBoundingClientRect();
                            if (rect.width > 0 && rect.height > 0) {
                                items.push({
                                    tag: el.tagName.toLowerCase(),
                                    type: el.type || '',
                                    id: el.id || '',
                                    name: el.name || '',
                                    placeholder: el.placeholder || '',
                                    text: el.innerText ? el.innerText.trim().slice(0, 50) : (el.value || ''),
                                });
                            }
                        });
                        return items;
                    }
                """)

                # Cek jika ada gambar captcha di halaman
                captcha_detected = False
                captcha_val = ""
                try:
                    captcha_elem = await page.query_selector('img[src*="captcha"], #captchaImg, img[alt*="captcha"]')
                    if captcha_elem:
                        captcha_detected = True
                        captcha_bytes = await captcha_elem.screenshot()
                        import base64
                        b64_captcha = base64.b64encode(captcha_bytes).decode('utf-8')
                        ocr_prompt = "Baca teks atau angka pada gambar captcha ini dengan persis. Balas HANYA dengan karakter captchanya saja tanpa spasi atau kata pengantar:"
                        raw_c = await call_llm(llm, ocr_prompt, image_b64=b64_captcha)
                        captcha_val = re.sub(r'[^a-zA-Z0-9]', '', raw_c.strip())
                except Exception:
                    pass

                captcha_info = f"Teks Captcha yang berhasil di-OCR: '{captcha_val}'" if captcha_val else "Tidak ada atau gagal membaca captcha"
                planner_prompt = f"""Kamu adalah browser automation controller.
Tugas Pengguna: {clean_task}
Halaman Saat Ini: {title} ({url_to_open})
Elemen Interaktif di Halaman:
{json.dumps(elements, indent=2, ensure_ascii=False)}
Status Captcha: {captcha_info}

Berdasarkan tugas pengguna, tentukan urutan aksi interaksi form (fill, type, click).
Balas HANYA dengan valid JSON array berisi daftar aksi, contoh:
[
  {{"action": "fill", "selector": "#username", "value": "user@example.com"}},
  {{"action": "fill", "selector": "#password", "value": "rahasia123"}},
  {{"action": "fill", "selector": "#captcha", "value": "{captcha_val}"}},
  {{"action": "click", "selector": "#btnLogin"}}
]
PENTING:
- Gunakan selector CSS spesifik (utamakan '#id' atau '[name=...]').
- Jika tugas adalah login dan ada captcha, pastikan masukkan nilai captcha ke field captcha.
- Jika tidak ada aksi yang diperlukan, balas dengan `[]`.
"""
                try:
                    raw_plan = await call_llm(llm, planner_prompt)
                    actions = []
                    m = re.search(r'\[\s*\{.*\}\s*\]', raw_plan, re.DOTALL)
                    if m:
                        try:
                            actions = json.loads(m.group(0))
                        except Exception:
                            actions = []

                    # Heuristic fallback jika AI planner tidak menghasilkan aksi tapi tugasnya login
                    if not actions and any(k in clean_task.lower() for k in ["login", "masuk"]):
                        user_match = re.search(r'([\w\.-]+@[\w\.-]+\.\w+)', clean_task)
                        pass_match = re.search(r'password\s+([^\s,]+)', clean_task, re.IGNORECASE)
                        if user_match:
                            actions.append({"action": "fill", "selector": "#username", "value": user_match.group(1)})
                        if pass_match:
                            actions.append({"action": "fill", "selector": "#password", "value": pass_match.group(1)})
                        if captcha_val:
                            actions.append({"action": "fill", "selector": "#captcha", "value": captcha_val})
                        actions.append({"action": "click", "selector": "#btnLogin"})

                    for act in actions:
                        action_type = act.get("action", "").lower()
                        sel = act.get("selector", "")
                        val = act.get("value", "")
                        if action_type in ["fill", "type"] and sel:
                            await page.fill(sel, str(val))
                            is_pwd = "pass" in sel.lower() or "pwd" in sel.lower()
                            action_log.append(f"• Mengisi {sel}: {'••••••••' if is_pwd else val}")
                        elif action_type == "click" and sel:
                            await page.click(sel)
                            action_log.append(f"• Mengklik {sel}")
                            await asyncio.sleep(4)
                        elif action_type == "press" and val:
                            await page.keyboard.press(str(val))
                            action_log.append(f"• Menekan tombol keyboard {val}")

                    # Tunggu jeda setelah eksekusi seluruh aksi agar halaman baru termuat
                    await asyncio.sleep(4)
                    title = await page.title()
                except Exception as plan_err:
                    action_log.append(f"• Gagal mengeksekusi rencana aksi: {plan_err}")

            attachment_tag = ""
            if attach_screenshot:
                try:
                    await page.screenshot(path=screenshot_path, full_page=False)
                    if os.path.exists(screenshot_path):
                        attachment_tag = f"\n\n[ATTACH_FILE:{screenshot_path}|CAPTION:Tangkapan Layar Camoufox Stealth ({url_to_open})]"
                except Exception:
                    pass

            page_text = await page.inner_text("body")
            truncated_text = page_text[:8000] if page_text else ""

            action_log_str = "\n".join(action_log) if action_log else ""
            analysis_prompt = (
                f"Kamu telah berhasil membuka situs {url_to_open} menggunakan Camoufox Stealth Engine (Firefox anti-detect).\n"
                f"Judul Halaman Sekarang: {title}\n"
                f"Tugas Pengguna: {clean_task}\n\n"
                f"Aksi yang Baru Saja Dijalankan:\n{action_log_str if action_log_str else 'Hanya membaca halaman'}\n\n"
                f"Isi Konten Halaman Saat Ini (Setelah Aksi):\n{truncated_text}\n\n"
                f"Jelaskan apakah login atau aksi tersebut berhasil/gagal berdasarkan isi halaman yang termuat, dan berikan laporan informatif yang rapi."
            )
            summary = await call_llm(llm, analysis_prompt)
            if not summary:
                summary = f"Berhasil memproses {url_to_open} (Judul: {title}).\nRingkasan isi halaman:\n{truncated_text[:800]}"

            actions_header = f"📋 <b>Aksi yang Dijalankan:</b>\n{action_log_str}\n\n" if action_log_str else ""
            return f"🦊 <b>[Camoufox Stealth Engine - {model_name}]</b>\n\n{actions_header}{summary}{attachment_tag}"
    except Exception as err:
        return f"❌ Gagal menjalankan Camoufox Stealth Engine: {err}"


@mcp.tool(
    name="browser",
    description=(
        "Browser otonom (autonomous web agent) berbasis Python dengan Dual-Engine (Chromium + Camoufox Stealth). "
        "AI dapat menjelajahi web secara mandiri untuk mencari informasi, membandingkan harga/tiket (Traveloka, KAI, Tokopedia, dll), "
        "membuka URL, mengisi form formulir, mengekstrak data dari berbagai halaman web, dan menembus proteksi Cloudflare/WAF. "
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
    attach_screenshot: bool = True,
    engine: str = "auto"
) -> str:
    """
    Eksekusi tugas browser otonom dengan kontrol dinamis dan dukungan model multi-provider.
    Mendukung Dual-Engine: Engine 1 (Chromium CDP default) & Engine 2 (Camoufox Stealth).
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

        # -------------------------------------------------------------
        # DUAL-ENGINE ROUTING
        # -------------------------------------------------------------
        # Deteksi awal: Jika target adalah domain Cloudflare ketat (seperti KAI / Turnstile)
        is_kai_or_cloudflare = bool(
            (url and ("kai.id" in url.lower() or "cloudflare" in url.lower()))
            or ("kai.id" in clean_task.lower() or "kai access" in clean_task.lower())
        )
        should_use_camoufox = (engine.lower() == "camoufox") or (engine.lower() == "auto" and is_kai_or_cloudflare and is_camoufox_ready())

        if should_use_camoufox:
            if is_camoufox_ready():
                return await run_camoufox_task(clean_task, url, llm, model_name=model, headless=headless, attach_screenshot=attach_screenshot)
            elif engine.lower() == "camoufox":
                return "⚠️ Engine Camoufox belum terpasang binary-nya di VPS. Silakan jalankan 'uv run python -m camoufox fetch' di server."

        # -------------------------------------------------------------
        # ENGINE 1: Standard Chromium / Playwright / Docker CDP
        # -------------------------------------------------------------
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

        # Terapkan playwright-stealth pada BrowserContext jika tersedia
        # Stealth patches: navigator.webdriver, chrome runtime, plugins, dll agar tidak terdeteksi Cloudflare/WAF
        browser_session = BrowserSession(browser_profile=profile)
        if _STEALTH_AVAILABLE:
            try:
                await browser_session.start()
                ctx = getattr(browser_session, "context", None)
                if ctx is not None:
                    stealth = Stealth(
                        navigator_languages_override=("id-ID", "id"),
                        navigator_platform_override="Win32",
                        navigator_user_agent_override=default_ua,
                    )
                    await stealth.apply_stealth_async(ctx)
            except Exception:
                pass

        agent = Agent(
            task=clean_task,
            llm=llm,
            browser_session=browser_session,
            max_actions_per_step=3,
            use_vision=vision_enabled
        )
        
        history = await agent.run(max_steps=max_steps)
        final_result = history.final_result() or "Tugas penjelajahan web selesai tanpa kesimpulan teks khusus."
        
        # Auto-fallback: Jika Engine 1 terblokir Cloudflare WAF, otomatis oper ke Engine 2 (Camoufox)!
        is_blocked = (
            "you have been blocked" in final_result.lower()
            or "unable to access kai.id" in final_result.lower()
            or "attention required! | cloudflare" in final_result.lower()
            or "just a moment..." in final_result.lower()
        )
        if is_blocked and is_camoufox_ready() and engine.lower() != "chromium":
            return await run_camoufox_task(clean_task, url, llm, model_name=model, headless=headless, attach_screenshot=attach_screenshot)

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
        if is_blocked and not is_camoufox_ready():
            final_result += "\n\n💡 <i>Tips: Halaman ini terproteksi Cloudflare Bot Management. Anda dapat mengaktifkan Engine Camoufox di VPS dengan menjalankan: <code>uv run python -m camoufox fetch</code></i>"

        return f"🌐 <b>[Browser-Use Autonomous Agent - {model} ({mode_str})]</b>\n\n{final_result}{attachment_tag}"
        
    except Exception as err:
        return f"❌ Gagal menjalankan tugas browser-use: {err}"

if __name__ == "__main__":
    mcp.run(transport="stdio")
