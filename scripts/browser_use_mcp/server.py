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
from langchain_openai import ChatOpenAI
from langchain_google_genai import ChatGoogleGenerativeAI

mcp = FastMCP("browser-use-server")

def resolve_llm_and_vision(model_name: str, requested_vision: Optional[bool] = None):
    """
    Menyelesaikan LLM dan mode vision.
    Untuk DeepSeek / model text-only, use_vision otomatis diatur ke False (hemat token, cepat, anti-error).
    """
    model_lower = model_name.lower().strip()
    
    # 1. Tentukan apakah vision aktif
    if requested_vision is not None:
        use_vision = requested_vision
    elif any(k in model_lower for k in ["deepseek", "qwen", "llama", "mistral", "gemma"]):
        # Model teks murni tidak membutuhkan vision
        use_vision = False
    else:
        # Default True untuk model multimodal (gemini, gpt-4o, claude)
        use_vision = True

    # 2. Khusus DeepSeek Model
    if "deepseek" in model_lower:
        deepseek_key = os.getenv("DEEPSEEK_API_KEY")
        if deepseek_key:
            llm = ChatOpenAI(
                model=model_name,
                base_url="https://api.deepseek.com",
                api_key=deepseek_key
            )
            return llm, use_vision

        # Fallback ke OmniRoute lokal (:20128)
        omni_base = os.getenv("OMNIROUTE_BASE_URL", "http://localhost:20128/v1")
        omni_key = os.getenv("OMNIROUTE_API_KEY") or os.getenv("OPENAI_API_KEY", "sk-omniroute")
        llm = ChatOpenAI(
            model=model_name,
            base_url=omni_base,
            api_key=omni_key
        )
        return llm, use_vision

    # 3. Model Gemini (Official Google AI)
    if "gemini" in model_lower:
        gemini_key = os.getenv("GEMINI_API_KEY") or os.getenv("GOOGLE_API_KEY")
        if gemini_key:
            return ChatGoogleGenerativeAI(model=model_name, google_api_key=gemini_key), use_vision

    # 4. Model OpenAI (Official atau OmniRoute)
    openai_key = os.getenv("OPENAI_API_KEY")
    openai_base = os.getenv("OPENAI_BASE_URL")
    if openai_key:
        kwargs = {"model": model_name, "api_key": openai_key}
        if openai_base:
            kwargs["base_url"] = openai_base
        return ChatOpenAI(**kwargs), use_vision

    # 5. Default Fallback ke Gateway OmniRoute lokal GoAssistant (:20128)
    omni_base = os.getenv("OMNIROUTE_BASE_URL", "http://localhost:20128/v1")
    return ChatOpenAI(
        model=model_name,
        base_url=omni_base,
        api_key=os.getenv("OMNIROUTE_API_KEY", "sk-omniroute")
    ), use_vision

@mcp.tool(
    name="run_browser_task",
    description=(
        "Jalankan tugas penjelajahan web otonom menggunakan Python browser-use. "
        "Mendukung model teks murni seperti DeepSeek (use_vision=False otomatis, sangat hemat token) "
        "maupun model multimodal (Gemini/GPT-4o). Mampu menyelesaikan alur multi-langkah seperti "
        "pencarian tiket, pembandingan harga toko online, pengisian form bertingkat, dan scraping interaktif."
    )
)
async def run_browser_task(
    task: str,
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
        llm, vision_enabled = resolve_llm_and_vision(model, use_vision)
        
        profile = BrowserProfile(
            headless=headless,
            disable_security=True,
        )
        
        agent = Agent(
            task=task,
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
