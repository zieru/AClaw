import asyncio
import atexit
import os
import shutil
import socket
import sys
import time
from datetime import datetime
from typing import Optional
from dotenv import load_dotenv

# Muat file .env dari folder project jika ada
load_dotenv()
project_root = os.getenv("PROJECT_ROOT")
if not project_root:
    if os.path.exists("/app/data"):
        project_root = "/app"
    else:
        project_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
load_dotenv(os.path.join(project_root, ".env"))

def format_attachment_path(local_path: str) -> str:
    host_data_dir = os.getenv("HOST_DATA_DIR")
    if host_data_dir:
        data_root = os.path.join(project_root, "data")
        try:
            rel = os.path.relpath(local_path, data_root)
            return os.path.join(host_data_dir, rel).replace("\\", "/")
        except Exception:
            return local_path
    return local_path

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


# ---------------------------------------------------------------------------
# SESSION MANAGER — Camoufox Persistent Context (Lifecycle & Memory Control)
# ---------------------------------------------------------------------------
# Setiap `session_id` mendapat persistent context sendiri (user_data_dir di disk)
# sehingga cookie/login/localStorage/lokasi halaman dipertahankan antar panggilan tool.
# Manajemen Memori:
# - CAMOUFOX_MAX_SESSIONS: Batas maksimum sesi aktif di RAM (default: 1). Jika ada sesi baru,
#   sesi yang paling lama tidak digunakan (LRU) otomatis ditutup dan disimpan ke disk.
# - CAMOUFOX_SESSION_TTL: Waktu idle maksimum (detik) sebelum sesi ditutup otomatis (default: 300s = 5m).
# - Tab Reuse: Memanfaatkan tab awal bawaan Playwright (pages[0]) alih-alih membuka tab baru terus-menerus.
# - Explicit Close: Aksi action="close" menutup sesi seketika dan melepaskan seluruh memori RAM.
# ---------------------------------------------------------------------------

CAMOUFOX_MAX_SESSIONS = int(os.getenv("CAMOUFOX_MAX_SESSIONS", "1"))
CAMOUFOX_SESSION_TTL = float(os.getenv("CAMOUFOX_SESSION_TTL", "300"))  # 5 menit

_SESSION_LOCK = asyncio.Lock()
# session_id -> { "ctx": BrowserContext, "page": Page, "browser": AsyncCamoufox, "user_data_dir": str, "last_activity": float, "ignore_ssl": bool }
_ACTIVE_SESSIONS: dict = {}
_REAPER_TASK: Optional[asyncio.Task] = None


async def _ensure_reaper_started() -> None:
    global _REAPER_TASK
    if _REAPER_TASK is None or _REAPER_TASK.done():
        try:
            loop = asyncio.get_running_loop()
            _REAPER_TASK = loop.create_task(_session_reaper())
        except RuntimeError:
            pass


async def _session_reaper() -> None:
    """Background task untuk membersihkan sesi browser yang idle melebihi TTL."""
    while True:
        try:
            await asyncio.sleep(30)
            now = time.time()
            async with _SESSION_LOCK:
                expired = [
                    sid for sid, data in _ACTIVE_SESSIONS.items()
                    if (now - data.get("last_activity", now)) > CAMOUFOX_SESSION_TTL
                ]
                for sid in expired:
                    await _close_session_locked(sid)
        except asyncio.CancelledError:
            break
        except Exception:
            pass


async def _close_session_locked(session_id: str) -> bool:
    """Tutup sesi Camoufox, semua halamannya, dan hentikan proses binary agar memori RAM terbebas penuh."""
    entry = _ACTIVE_SESSIONS.pop(session_id, None)
    if not entry:
        return False

    ctx = entry.get("ctx")
    if ctx is not None:
        try:
            for p in list(getattr(ctx, "pages", [])):
                try:
                    await p.close()
                except Exception:
                    pass
        except Exception:
            pass
        try:
            await ctx.close()
        except Exception:
            pass

    browser = entry.get("browser")
    if browser is not None:
        try:
            await browser.__aexit__(None, None, None)
        except Exception:
            pass

    return True


async def _evict_oldest_session_if_needed() -> None:
    """Jika jumlah sesi mencapai batas maksimum, tutup sesi tertua (LRU) untuk menghindari OOM."""
    while len(_ACTIVE_SESSIONS) >= CAMOUFOX_MAX_SESSIONS and _ACTIVE_SESSIONS:
        oldest_sid = min(
            _ACTIVE_SESSIONS.keys(),
            key=lambda sid: _ACTIVE_SESSIONS[sid].get("last_activity", 0)
        )
        await _close_session_locked(oldest_sid)


async def _get_or_create_camoufox_session(
    session_id: str,
    headless: bool,
    ignore_ssl: bool = False,
) -> "tuple":
    """
    Ambil sesi Camoufox yang sudah ada untuk session_id, atau buat baru (persistent context).
    Mengembalikan (context, page).
    """
    import re
    from camoufox.async_api import AsyncCamoufox

    await _ensure_reaper_started()

    async with _SESSION_LOCK:
        entry = _ACTIVE_SESSIONS.get(session_id)
        # Jika opsi ignore_ssl berubah (misal retry SSL fallback), buat ulang sesi
        if entry is not None and entry.get("ignore_ssl") != ignore_ssl:
            await _close_session_locked(session_id)
            entry = None

        if entry is not None:
            ctx = entry.get("ctx")
            page = entry.get("page")
            # Validasi page masih responsif
            if page is not None:
                try:
                    await page.evaluate("() => 1")
                    entry["last_activity"] = time.time()
                    return ctx, page
                except Exception:
                    try:
                        await page.close()
                    except Exception:
                        pass
                    entry["page"] = None

            if ctx is not None:
                try:
                    pages = getattr(ctx, "pages", [])
                    if pages:
                        active_p = pages[0]
                    else:
                        active_p = await ctx.new_page()
                        await active_p.set_viewport_size({"width": 1920, "height": 1080})

                    # Tutup tab ekstra yang mungkin tertinggal
                    for extra in pages[1:]:
                        try:
                            await extra.close()
                        except Exception:
                            pass

                    entry["page"] = active_p
                    entry["last_activity"] = time.time()
                    return ctx, active_p
                except Exception:
                    pass

            # Sesi tidak valid -> tutup & buat baru
            await _close_session_locked(session_id)

        # ---- Buat sesi baru (persistent context) ----
        await _evict_oldest_session_if_needed()

        sess_root = os.path.abspath(os.path.join(project_root, "data", "browser", "sessions"))
        os.makedirs(sess_root, exist_ok=True)
        safe_id = re.sub(r'[^A-Za-z0-9_.-]', '_', session_id) if session_id else "default"
        user_data_dir = os.path.join(sess_root, f"camoufox_{safe_id}")

        # Konfigurasi efisiensi RAM Firefox
        firefox_user_prefs = {
            "browser.sessionhistory.max_total_viewers": 0,  # Bebaskan memori bfcache
            "browser.sessionhistory.max_entries": 3,
            "browser.cache.memory.enable": False,
            "dom.ipc.processCount": 1,                      # Batasi sub-proses content
        }

        launch_kwargs = {
            "headless": headless,
            "humanize": True,
            "geoip": True,
            "persistent_context": True,
            "user_data_dir": user_data_dir,
            "firefox_user_prefs": firefox_user_prefs,
        }
        if ignore_ssl:
            launch_kwargs["ignore_https_errors"] = True

        browser = AsyncCamoufox(**launch_kwargs)
        ctx = await browser.__aenter__()

        # Playwright persistent context otomatis membuka tab awal (ctx.pages[0])
        pages = getattr(ctx, "pages", [])
        if pages:
            active_page = pages[0]
        else:
            active_page = await ctx.new_page()
            await active_page.set_viewport_size({"width": 1920, "height": 1080})

        # Tutup tab ekstra bila ada
        for extra in pages[1:]:
            try:
                await extra.close()
            except Exception:
                pass

        entry = {
            "ctx": ctx,
            "page": active_page,
            "browser": browser,
            "user_data_dir": user_data_dir,
            "last_activity": time.time(),
            "ignore_ssl": ignore_ssl,
        }
        _ACTIVE_SESSIONS[session_id] = entry
        return ctx, active_page


async def run_camoufox_task(
    clean_task: str,
    target_url: Optional[str],
    llm,
    action: Optional[str] = None,
    model_name: Optional[str] = "Auto",
    headless: bool = True,
    attach_screenshot: bool = True,
    ignore_ssl: bool = False,
    session_id: Optional[str] = None,
    force_reload: bool = False,
) -> str:
    """
    Eksekusi penjelajahan web stealth tingkat tinggi menggunakan Camoufox (Firefox C++ engine-level spoofing)
    untuk membobol Cloudflare Turnstile, WAF, bot detection, dan proteksi fingerprinting (Pixelscan, KAI, dll).

    ignore_ssl (bool): Jika True, abaikan error sertifikat SSL (ignore_https_errors) pada context session ini saja.
    Hanya untuk akses read-only, bukan transaksi/pembayaran. Per-sesi, tidak global.

    session_id (str): Identitas sesi persisten. Jika sama dengan panggilan sebelumnya, browser & halaman
    yang sama dipertahankan (resume browsing) sehingga login/cookie/lokasi halaman tidak hilang.
    force_reload (bool): Jika True, paksa navigasi ulang ke target_url meski sesi sudah berada di halaman itu.
    """
    import re
    from urllib.parse import urljoin
    from camoufox.async_api import AsyncCamoufox

    if not session_id:
        session_id = "default"

    # Penanganan aksi tutup browser secara eksplisit
    if (action or "").lower() in ["close", "exit", "quit", "tutup"]:
        async with _SESSION_LOCK:
            await _close_session_locked(session_id)
        return f"🦊 Sesi Camoufox '{session_id}' berhasil ditutup dan memori sistem telah dibebaskan.", False

    # Resolusi URL awal dari task jika url tidak diberikan langsung
    url_to_open = target_url
    if not url_to_open:
        urls = re.findall(r'https?://[^\s<>"]+|www\.[^\s<>"]+', clean_task)
        if urls:
            url_to_open = urls[0]
            if not url_to_open.startswith("http"):
                url_to_open = f"https://{url_to_open}"
        elif "pixelscan" in clean_task.lower():
            url_to_open = "https://pixelscan.net/"
        elif "kai" in clean_task.lower():
            url_to_open = "https://booking.kai.id/"
        else:
            clean_q = clean_task.replace("cari", "").replace("buka", "").strip()
            url_to_open = f"https://duckduckgo.com/?q={clean_q}"

    screenshot_dir = os.path.abspath(os.path.join(project_root, "data", "browser", "screenshots"))
    os.makedirs(screenshot_dir, exist_ok=True)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    screenshot_path = os.path.join(screenshot_dir, f"camoufox_{timestamp}.png")

    ssl_error_detected = False

    try:
        ctx, page = await _get_or_create_camoufox_session(session_id, headless, ignore_ssl=ignore_ssl)

        # Resume navigation: hanya navigasi bila belum di URL target atau dipaksa reload.
        current_page_url = ""
        try:
            current_page_url = page.url
        except Exception:
            current_page_url = ""

        need_navigate = force_reload or not current_page_url or current_page_url == "about:blank"
        if url_to_open and current_page_url and current_page_url != "about:blank":
            try:
                from urllib.parse import urlparse
                if urlparse(current_page_url).netloc != urlparse(url_to_open).netloc:
                    need_navigate = True
                elif url_to_open.rstrip("/") != current_page_url.rstrip("/"):
                    need_navigate = True
            except Exception:
                need_navigate = True

        if need_navigate and url_to_open:
            try:
                await page.goto(url_to_open, wait_until="domcontentloaded", timeout=45000)
            except Exception:
                pass

        # Deteksi halaman peringatan sertifikat SSL Firefox (mis. sertifikat expired/invalid).
        # Ini menjadi pemicu auto-fallback untuk retry dengan ignore_ssl=True.
        try:
            _ssl_title = await page.title()
            _ssl_content = await page.content()
            ssl_error_detected = any(
                k in _ssl_title for k in [
                    "Warning: Security Risk", "Potential Security Issue",
                    "Your connection is not secure", "did not connect",
                ]
            ) or any(
                k in _ssl_content for k in [
                    "SEC_ERROR", "SSL_ERROR", "MOZILLA_PKIX_ERROR",
                    "Certificate is not trusted", "safety warning",
                ]
            )
        except Exception:
            ssl_error_detected = False

        # Berikan waktu jeda agar Cloudflare Turnstile / inisialisasi skrip selesai
        await asyncio.sleep(4)

        # Cek jika masih di halaman challenge Turnstile, tunggu tambahan beberapa detik
        title = await page.title()
        content = await page.content()
        if "Just a moment" in title or "Checking your browser" in content or "Attention Required" in title:
            await asyncio.sleep(5)
            title = await page.title()

        # -----------------------------------------------------------------
        # INTERACTIVE ACTION EXECUTOR (Form Filling, Typing, Clicking, Scan)
        # -----------------------------------------------------------------
        interactive_keywords = [
            "login", "masuk", "isi", "ketik", "klik", "submit", "daftar",
            "pesan", "booking", "cari", "username", "password", "email",
            "scan", "test", "cek", "check", "verifikasi", "fingerprint",
            "jalankan", "run", "pilih", "select", "tombol", "button", "tekan", "press"
        ]
        wants_interaction = (action in ["click", "type", "fill", "press_key", "eval_js"]) or any(kw in clean_task.lower() for kw in interactive_keywords)

        action_log = []
        if wants_interaction:
            import json
            # Kumpulkan seluruh elemen interaktif yang terlihat (button, link <a>, input, select, dll)
            elements = await page.evaluate("""
                () => {
                    const items = [];
                    window._camoufoxInteractive = [];
                    const selectors = 'button, a, input:not([type="hidden"]), select, textarea, [role="button"], [role="link"], [role="tab"], [role="checkbox"]';
                    document.querySelectorAll(selectors).forEach((el) => {
                        const rect = el.getBoundingClientRect();
                        const style = window.getComputedStyle(el);
                        if (rect.width > 0 && rect.height > 0 && style.visibility !== 'hidden' && style.display !== 'none' && style.opacity !== '0') {
                            const idx = items.length;
                            window._camoufoxInteractive.push(el);
                            el.setAttribute('data-camoufox-idx', idx.toString());
                                
                            let label = (el.innerText || el.value || el.getAttribute('aria-label') || el.title || el.placeholder || '').trim();
                            if (label.length > 80) label = label.slice(0, 80) + '...';
                                
                            let sel = '';
                            if (el.id) {
                                sel = '#' + CSS.escape(el.id);
                            } else if (el.name) {
                                sel = `${el.tagName.toLowerCase()}[name="${CSS.escape(el.name)}"]`;
                            } else if (el.tagName.toLowerCase() === 'a' && el.getAttribute('href')) {
                                const href = el.getAttribute('href');
                                if (href && !href.startsWith('javascript:')) {
                                    sel = `a[href="${CSS.escape(href)}"]`;
                                }
                            }
                            if (!sel) {
                                sel = `[data-camoufox-idx="${idx}"]`;
                            }

                            items.push({
                                idx: idx,
                                tag: el.tagName.toLowerCase(),
                                type: el.type || '',
                                id: el.id || '',
                                name: el.name || '',
                                selector: sel,
                                text: label,
                                href: el.getAttribute('href') || '',
                                placeholder: el.placeholder || '',
                            });
                        }
                    });
                    return items.slice(0, 100);
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
Halaman Saat Ini: {title} ({page.url})
Elemen Interaktif di Halaman (Top 100):
{json.dumps(elements, indent=2, ensure_ascii=False)}
Status Captcha: {captcha_info}

Berdasarkan tugas pengguna, tentukan urutan aksi interaksi web (click, fill, type, press, eval_js).
Balas HANYA dengan valid JSON array berisi daftar aksi, contoh:
[
  {{"action": "click", "idx": 5, "text": "Scan My Browser Now"}},
  {{"action": "fill", "idx": 2, "value": "user@example.com"}}
]
PENTING:
- Gunakan field `idx` (nomor indeks numerik dari daftar elemen di atas) agar browser dapat mengklik/mengisi target secara presisi tanpa salah sasaran.
- Untuk aksi click, sertakan juga "text" atau "selector" dan "href" jika ada.
- Jika tugas adalah melakukan scan fingerprint di pixelscan dan terdapat tombol/link seperti "Scan My Browser Now" atau "Fingerprint Check", pilih elemen tersebut untuk diklik.
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

                # Heuristic fallback jika AI planner tidak menghasilkan aksi tapi tugasnya jelas:
                if not actions:
                    if any(k in clean_task.lower() for k in ["login", "masuk"]):
                        user_match = re.search(r'([\w\.-]+@[\w\.-]+\.\w+)', clean_task)
                        pass_match = re.search(r'password\s+([^\s,]+)', clean_task, re.IGNORECASE)
                        if user_match:
                            actions.append({"action": "fill", "selector": "#username", "value": user_match.group(1)})
                        if pass_match:
                            actions.append({"action": "fill", "selector": "#password", "value": pass_match.group(1)})
                        if captcha_val:
                            actions.append({"action": "fill", "selector": "#captcha", "value": captcha_val})
                        actions.append({"action": "click", "selector": "#btnLogin"})
                    elif any(k in clean_task.lower() for k in ["pixelscan", "fingerprint", "scan"]):
                        for el in elements:
                            el_txt = el.get("text", "").lower()
                            if "scan my browser" in el_txt or "fingerprint check" in el_txt:
                                actions.append({
                                    "action": "click",
                                    "idx": el.get("idx"),
                                    "selector": el.get("selector"),
                                    "text": el.get("text"),
                                    "href": el.get("href")
                                })
                                break

                for act in actions:
                    action_type = act.get("action", "").lower()
                    idx = act.get("idx")
                    sel = act.get("selector", "")
                    text_val = act.get("text", "")
                    val = act.get("value", "")
                    href = act.get("href", "")
                    if not href and idx is not None:
                        for el in elements:
                            if el.get("idx") == idx:
                                href = el.get("href", "")
                                break

                    try:
                        if action_type in ["fill", "type"]:
                            filled = False
                            if idx is not None:
                                filled = await page.evaluate("""({i, v}) => {
                                    const el = (window._camoufoxInteractive && window._camoufoxInteractive[i]) || document.querySelector(`[data-camoufox-idx="${i}"]`);
                                    if (el) {
                                        el.focus();
                                        el.value = v;
                                        el.dispatchEvent(new Event('input', { bubbles: true }));
                                        el.dispatchEvent(new Event('change', { bubbles: true }));
                                        return true;
                                    }
                                    return false;
                                }""", {"i": idx, "v": str(val)})
                            if not filled and sel:
                                await page.fill(sel, str(val), timeout=5000)
                                filled = True

                            is_pwd = "pass" in str(sel).lower() or "pwd" in str(sel).lower()
                            display_val = "••••••••" if is_pwd else val
                            action_log.append(f"• Mengisi {sel or f'elemen [{idx}]'}: {display_val}")

                        elif action_type == "click":
                            clicked = False
                            target_desc = text_val or sel or (f"elemen [{idx}]" if idx is not None else "tombol")

                            # 1. Klik DOM via idx
                            if idx is not None:
                                clicked = await page.evaluate("""(i) => {
                                    const el = (window._camoufoxInteractive && window._camoufoxInteractive[i]) || document.querySelector(`[data-camoufox-idx="${i}"]`);
                                    if (el) {
                                        el.scrollIntoView({ behavior: 'instant', block: 'center' });
                                        el.click();
                                        return true;
                                    }
                                    return false;
                                }""", idx)

                            # 2. Klik via Playwright Locator Text
                            if not clicked and text_val:
                                try:
                                    loc = page.locator(f"text={text_val}").first
                                    if await loc.count() > 0:
                                        await loc.click(timeout=5000)
                                        clicked = True
                                except Exception:
                                    pass

                            # 3. Klik via CSS Selector
                            if not clicked and sel:
                                try:
                                    await page.click(sel, timeout=5000)
                                    clicked = True
                                except Exception:
                                    clicked = await page.evaluate("""(s) => {
                                        const el = document.querySelector(s);
                                        if (el) {
                                            el.scrollIntoView({ behavior: 'instant', block: 'center' });
                                            el.click();
                                            return true;
                                        }
                                        return false;
                                    }""", sel)

                            # 4. Navigasi langsung jika elemen bertipe tautan <a> dengan href
                            await asyncio.sleep(1)
                            if href and (not clicked or page.url == url_to_open):
                                full_target = urljoin(page.url, href)
                                if page.url != full_target:
                                    try:
                                        await page.goto(full_target, wait_until="domcontentloaded", timeout=30000)
                                        clicked = True
                                    except Exception:
                                        pass

                            if clicked:
                                action_log.append(f"• Berhasil mengklik: {target_desc}")
                            else:
                                action_log.append(f"• Gagal mengklik: {target_desc}")

                            await asyncio.sleep(3)

                        elif action_type == "eval_js":
                            script = act.get("script", "") or val
                            if script:
                                js_out = await page.evaluate(script)
                                action_log.append(f"• Eksekusi JS: {js_out}")

                        elif action_type == "press":
                            key = act.get("key") or val or "Enter"
                            await page.keyboard.press(str(key))
                            action_log.append(f"• Menekan tombol keyboard {key}")
                            await asyncio.sleep(2)

                    except Exception as act_err:
                        action_log.append(f"• Kendala aksi {action_type}: {act_err}")

                # Tunggu jeda setelah eksekusi seluruh aksi agar halaman baru termuat
                await asyncio.sleep(3)
                # Khusus pixelscan atau situs scanner fingerprint, beri waktu hingga analisis selesai
                if "pixelscan" in page.url or "pixelscan" in clean_task.lower():
                    await asyncio.sleep(7)

                title = await page.title()
            except Exception as plan_err:
                action_log.append(f"• Gagal mengeksekusi rencana aksi: {plan_err}")

        attachment_tag = ""
        if attach_screenshot:
            try:
                await page.screenshot(path=screenshot_path, full_page=False)
                if os.path.exists(screenshot_path):
                    reported_shot = format_attachment_path(screenshot_path)
                    attachment_tag = f"\n\n[ATTACH_FILE:{reported_shot}|CAPTION:Tangkapan Layar Camoufox Stealth ({page.url})]"
            except Exception:
                pass

        page_text = await page.inner_text("body")
        truncated_text = page_text[:8000] if page_text else ""

        action_log_str = "\n".join(action_log) if action_log else ""
        analysis_prompt = (
            f"Kamu telah berhasil membuka situs {page.url} menggunakan Camoufox Stealth Engine (Firefox anti-detect).\n"
            f"Judul Halaman Sekarang: {title}\n"
            f"Tugas Pengguna: {clean_task}\n\n"
            f"Aksi yang Baru Saja Dijalankan:\n{action_log_str if action_log_str else 'Hanya membaca halaman'}\n\n"
            f"Isi Konten Halaman Saat Ini (Setelah Aksi):\n{truncated_text}\n\n"
            f"Jelaskan apakah scan/aksi/tugas tersebut berhasil atau gagal berdasarkan isi halaman yang termuat, dan berikan laporan informatif yang rapi."
        )
        summary = await call_llm(llm, analysis_prompt)
        if not summary:
            summary = f"Berhasil memproses {page.url} (Judul: {title}).\nRingkasan isi halaman:\n{truncated_text[:800]}"

        actions_header = f"📋 <b>Aksi yang Dijalankan:</b>\n{action_log_str}\n\n" if action_log_str else ""
        report = f"🦊 <b>[Camoufox Stealth Engine - {model_name}]</b>\n\n{actions_header}{summary}{attachment_tag}"
        async with _SESSION_LOCK:
            if session_id in _ACTIVE_SESSIONS:
                _ACTIVE_SESSIONS[session_id]["last_activity"] = time.time()
        return report, ssl_error_detected
    except Exception as err:
        return f"❌ Gagal menjalankan Camoufox Stealth Engine: {err}", False


@mcp.tool(
    name="browser",
    description=(
        "Browser otonom (autonomous web agent) berbasis Python dengan Camoufox Stealth Engine sebagai PRIMARY browser. "
        "Camoufox menggunakan Firefox C++ spoofing engine level tanpa ketergantungan Docker/Zenika, mampu menembus Cloudflare Turnstile, "
        "WAF, anti-bot, serta anti-fingerprinting (Pixelscan, CreepJS, KAI, dll). "
        "AI dapat menjelajahi web secara mandiri, membuka URL, mengklik tombol, mengisi form formulir, mengekstrak data dari berbagai halaman web, "
        "atau menutup sesi browser untuk membebaskan memori RAM sistem (action='close'). "
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
    engine: str = "camoufox",
    ignore_ssl: bool = False,
    session_id: Optional[str] = None,
    force_reload: bool = False,
) -> str:
    """
    Eksekusi tugas browser otonom dengan kontrol dinamis dan dukungan model multi-provider.
    Camoufox Stealth adalah Primary Browser default (bebas Docker Zenika).
    Chromium CDP tersedia sebagai opsi sekunder jika engine='chromium'.

    action (str): Aksi khusus seperti 'open', 'click', 'type', atau 'close' untuk menutup
    sesi browser dan membebaskan RAM secara instan.

    ignore_ssl (bool): Jika True, abaikan error sertifikat SSL pada sesi Camoufox ini saja
    (ignore_https_errors). Tanpa Chromium — tetap Camoufox. Untuk akses read-only, bukan transaksi.
    Per-sesi, tidak global. Saat False dan halaman menampilkan peringatan sertifikat, otomatis
    di-retry sekali dengan bypass SSL.

    session_id (str): Identitas sesi persisten (resume browsing). Gunakan nilai yang sama untuk
    mempertahankan browser/login/cookie/lokasi halaman antar panggilan tool (misal setelah tanya
    kredensial lalu lanjut). Jika kosong, dianggap sesi 'default'.
    force_reload (bool): Jika True, paksa navigasi ulang ke URL meski sesi sudah di halaman itu.
    """
    try:
        act_lower = (action or "").strip().lower()
        if act_lower in ["close", "exit", "quit", "tutup"]:
            target_sess = session_id or "default"
            async with _SESSION_LOCK:
                if target_sess == "all":
                    count = len(_ACTIVE_SESSIONS)
                    for sid in list(_ACTIVE_SESSIONS.keys()):
                        await _close_session_locked(sid)
                    return f"✅ Berhasil menutup seluruh ({count}) sesi browser Camoufox dan membebaskan RAM sistem."
                else:
                    closed = await _close_session_locked(target_sess)
                    if closed:
                        return f"✅ Sesi browser '{target_sess}' berhasil ditutup dan memori sistem telah dibebaskan."
                    else:
                        return f"ℹ️ Sesi browser '{target_sess}' tidak sedang aktif di memori."
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
        # DUAL-ENGINE ROUTING: Camoufox is PRIMARY (Docker Zenika bypassed)
        # -------------------------------------------------------------
        is_chromium_forced = (engine.lower() == "chromium")
        use_camoufox = not is_chromium_forced and is_camoufox_ready()

        if use_camoufox:
            # Eksplisit: ignore_ssl=True langsung bypass sejak awal (domain apa pun).
            if ignore_ssl:
                result, _ = await run_camoufox_task(
                    clean_task,
                    url,
                    llm,
                    action=action,
                    model_name=model,
                    headless=headless,
                    attach_screenshot=attach_screenshot,
                    ignore_ssl=True,
                    session_id=session_id,
                    force_reload=force_reload,
                )
                return result

            # Normal: coba tanpa bypass SSL.
            result, ssl_error = await run_camoufox_task(
                clean_task,
                url,
                llm,
                action=action,
                model_name=model,
                headless=headless,
                attach_screenshot=attach_screenshot,
                ignore_ssl=False,
                session_id=session_id,
                force_reload=force_reload,
            )

            # Auto-fallback: halaman menampilkan peringatan sertifikat SSL -> retry sekali dgn bypass.
            if ssl_error:
                result, _ = await run_camoufox_task(
                    clean_task,
                    url,
                    llm,
                    action=action,
                    model_name=model,
                    headless=headless,
                    attach_screenshot=attach_screenshot,
                    ignore_ssl=True,
                    session_id=session_id,
                    force_reload=force_reload,
                )
                result += "\n\n⚠️ <i>Peringatan sertifikat SSL terdeteksi; akses di-retry dengan bypass SSL (per-sesi, read-only).</i>"
            return result
        elif engine.lower() == "camoufox" and not is_camoufox_ready():
            return "⚠️ Engine Camoufox belum terpasang binary-nya di sistem. Silakan jalankan 'uv run python -m camoufox fetch' di server."

        # -------------------------------------------------------------
        # ENGINE 2 (Fallback / Secondary): Chromium CDP / Local Chrome
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
            result, ssl_error = await run_camoufox_task(clean_task, url, llm, action=action, model_name=model, headless=headless, attach_screenshot=attach_screenshot, session_id=session_id, force_reload=force_reload)
            if ssl_error:
                result, _ = await run_camoufox_task(clean_task, url, llm, action=action, model_name=model, headless=headless, attach_screenshot=attach_screenshot, ignore_ssl=True, session_id=session_id, force_reload=force_reload)
                result += "\n\n⚠️ <i>Peringatan sertifikat SSL terdeteksi; akses di-retry dengan bypass SSL (per-sesi, read-only).</i>"
            return result

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
                        reported_dest = format_attachment_path(dest_path)
                        attachment_tag = f"\n\n[ATTACH_FILE:{reported_dest}|CAPTION:Tangkapan Layar Hasil Browser-Use ({model})]"
                    except Exception:
                        reported_shot = format_attachment_path(last_shot)
                        attachment_tag = f"\n\n[ATTACH_FILE:{reported_shot}|CAPTION:Tangkapan Layar Hasil Browser-Use ({model})]"
                
        mode_str = "Vision" if vision_enabled else "Text-DOM (DeepSeek Mode)"
        if is_blocked and not is_camoufox_ready():
            final_result += "\n\n💡 <i>Tips: Halaman ini terproteksi Cloudflare Bot Management. Anda dapat mengaktifkan Engine Camoufox di VPS dengan menjalankan: <code>uv run python -m camoufox fetch</code></i>"

        return f"🌐 <b>[Browser-Use Autonomous Agent - {model} ({mode_str})]</b>\n\n{final_result}{attachment_tag}"
        
    except Exception as err:
        return f"❌ Gagal menjalankan tugas browser-use: {err}"

if __name__ == "__main__":
    mcp.run(transport="stdio")
