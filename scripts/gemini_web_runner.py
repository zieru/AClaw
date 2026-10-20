#!/usr/bin/env python3
"""
gemini_web_runner.py
Bridge script for GoAssistant to communicate with gemini-web2api via File IPC.
Supports single-prompt, streaming chunk generation (JSONL), model selection,
multimodal image input (Scotty resumable upload), and proxy/cookie configuration.
Compatible with Windows and Linux (Alpine).
"""

import argparse
import base64
import json
import os
import sys
import time
from pathlib import Path

# Setup import path for gemini_web2api
current_dir = Path(__file__).resolve().parent
local_pkg = current_dir / "gemini-web2api"
if local_pkg.exists() and str(local_pkg) not in sys.path:
    sys.path.insert(0, str(local_pkg))

local_pkg_alt = current_dir / "gemini_web2api"
if local_pkg_alt.exists() and str(local_pkg_alt) not in sys.path:
    sys.path.insert(0, str(local_pkg_alt))

try:
    from gemini_web2api.config import CONFIG
    from gemini_web2api.gemini import generate, generate_stream, extract_response_text, HAS_HTTPX
    from gemini_web2api.models import MODELS
    from gemini_web2api.multimodal import upload_image, detect_image_mime, fetch_image_bytes
    from gemini_web2api.tools import _decode_data_url
    IMPORT_SUCCESS = True
    IMPORT_ERR = ""
except Exception as e:
    IMPORT_SUCCESS = False
    IMPORT_ERR = str(e)


def run_test():
    """Runs a quick diagnostic check of the gemini-web2api environment."""
    result = {
        "status": "ok" if IMPORT_SUCCESS else "error",
        "python_version": sys.version,
        "import_success": IMPORT_SUCCESS,
        "error": IMPORT_ERR if not IMPORT_SUCCESS else None,
        "has_httpx": HAS_HTTPX if IMPORT_SUCCESS else False,
        "available_models": list(MODELS.keys()) if IMPORT_SUCCESS else [],
        "search_paths": sys.path[:3],
    }
    print(json.dumps(result, indent=2))
    return 0 if IMPORT_SUCCESS else 1


def resolve_model(model_name: str, think_override=None):
    """Resolve model string to mode_id and think_mode according to gemini-web2api."""
    m_name = (model_name or "gemini-3.5-flash-thinking").strip().lower()
    think_mode = 0

    if "@think=" in m_name:
        parts = m_name.split("@think=", 1)
        m_name = parts[0]
        try:
            think_mode = int(parts[1])
        except ValueError:
            think_mode = 0
    elif think_override is not None:
        try:
            think_mode = int(think_override)
        except (ValueError, TypeError):
            think_mode = 0

    info = MODELS.get(m_name)
    if not info:
        # Fallback to flash-thinking or flash
        if "pro" in m_name:
            info = MODELS.get("gemini-3.1-pro", {"mode": 3, "think": 4})
        elif "thinking" in m_name:
            info = MODELS.get("gemini-3.5-flash-thinking", {"mode": 2, "think": 0})
        else:
            info = MODELS.get("gemini-3.6-flash", {"mode": 1, "think": 4})

    model_id = info["mode"]
    if "@think=" not in (model_name or "") and think_override is None:
        think_mode = info.get("think", 0)

    return model_id, think_mode


def process_image(img_input):
    """Extract raw image bytes and MIME type from data-URI, URL, local path, or raw base64."""
    if not img_input:
        return None
    if not isinstance(img_input, str):
        return None

    # Case 1: Data URI (e.g. data:image/png;base64,...)
    if img_input.startswith("data:"):
        try:
            decoded = _decode_data_url(img_input)
            if decoded:
                data, mime = decoded
                return data, detect_image_mime(data, mime or "image/png")
        except Exception:
            pass

    # Case 2: Web URL
    if img_input.startswith("http://") or img_input.startswith("https://"):
        try:
            data = fetch_image_bytes(img_input)
            if data:
                return data, detect_image_mime(data, "image/png")
        except Exception:
            pass

    # Case 3: Local file on disk
    if os.path.isfile(img_input):
        try:
            data = Path(img_input).read_bytes()
            return data, detect_image_mime(data, "image/png")
        except Exception:
            pass

    # Case 4: Raw base64 string
    try:
        data = base64.b64decode(img_input, validate=True)
        if data:
            return data, detect_image_mime(data, "image/png")
    except Exception:
        pass

    return None


def main():
    parser = argparse.ArgumentParser(description="GoAssistant gemini-web2api File IPC Runner")
    parser.add_argument("--req", type=str, help="Path to request JSON file")
    parser.add_argument("--out", type=str, help="Path to output JSONL file")
    parser.add_argument("--test", action="store_true", help="Run self-test diagnostic")
    args = parser.parse_args()

    if args.test:
        sys.exit(run_test())

    if not args.req or not args.out:
        parser.print_help()
        sys.exit(1)

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)

    def write_line(data: dict):
        with open(out_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(data, ensure_ascii=False) + "\n")
            f.flush()

    if not IMPORT_SUCCESS:
        write_line({
            "type": "error",
            "error": f"Modul gemini-web2api gagal diimpor: {IMPORT_ERR}. Pastikan dependensi sudah terpasang.",
        })
        sys.exit(1)

    try:
        with open(args.req, "r", encoding="utf-8") as f:
            req = json.load(f)
    except Exception as e:
        write_line({"type": "error", "error": f"Gagal membaca request file: {e}"})
        sys.exit(1)

    prompt = req.get("prompt", "")
    if not prompt:
        write_line({"type": "error", "error": "Prompt tidak boleh kosong"})
        sys.exit(1)

    model_name = req.get("model", "gemini-3.5-flash-thinking")
    think_mode = req.get("think_mode", None)
    model_id, resolved_think = resolve_model(model_name, think_mode)

    # Configure proxy & timeout if provided
    proxy = req.get("proxy")
    if proxy:
        CONFIG["proxy"] = proxy
    timeout = req.get("timeout_sec")
    if timeout:
        CONFIG["request_timeout_sec"] = int(timeout)

    # Configure cookie if provided
    cookie_file = req.get("cookie_file")
    if cookie_file and os.path.exists(cookie_file):
        CONFIG["cookie_file"] = cookie_file
    elif req.get("cookie_str"):
        temp_cookie_path = str(out_path.parent / f"cookie_{os.getpid()}.txt")
        try:
            with open(temp_cookie_path, "w", encoding="utf-8") as cf:
                cf.write(req["cookie_str"].strip())
            CONFIG["cookie_file"] = temp_cookie_path
        except Exception:
            pass

    # Process multimodal images (if any)
    raw_images = req.get("images", [])
    file_refs = []
    if raw_images:
        for idx, img_in in enumerate(raw_images):
            processed = process_image(img_in)
            if not processed:
                continue
            data_bytes, mime_type = processed
            try:
                ref = upload_image(data_bytes, filename=f"image_{idx}.png", mime_type=mime_type)
                if ref:
                    file_refs.append(ref)
            except Exception as e:
                write_line({
                    "type": "error",
                    "error": f"Gagal mengunggah gambar ke Google Scotty: {e}. Catatan: Multimodal Gemini Web memerlukan cookie session Google aktif.",
                })
                sys.exit(1)

        if file_refs and "[Image attached]" not in prompt:
            prompt = f"{prompt}\n\n[Image attached]"

    is_stream = req.get("stream", True)
    start_time = time.time()
    full_text = []

    try:
        if HAS_HTTPX:
            for delta in generate_stream(prompt, model_id, resolved_think, file_refs=file_refs if file_refs else None):
                if delta:
                    full_text.append(delta)
                    if is_stream:
                        write_line({"type": "chunk", "delta": delta})
            final_content = "".join(full_text).strip()
        else:
            raw = generate(prompt, model_id, resolved_think, file_refs=file_refs if file_refs else None)
            final_content = extract_response_text(raw).strip()

        latency_ms = int((time.time() - start_time) * 1000)
        approx_tokens = (len(prompt) + len(final_content)) // 4

        if not final_content and file_refs:
            write_line({
                "type": "error",
                "error": "Google Gemini Web tidak mengembalikan teks untuk input gambar. Fitur multimodal vision pada Gemini Web membutuhkan session cookie akun Google aktif. Silakan masukkan cookie di GoAssistant.",
                "latency_ms": latency_ms,
            })
            sys.exit(1)

        write_line({
            "type": "done",
            "content": final_content,
            "total_tokens": approx_tokens,
            "latency_ms": latency_ms,
            "model": model_name,
        })
    except Exception as e:
        err_str = str(e)
        if "BardErrorInfo" in err_str and "1003" in err_str:
            err_str = "Google Gemini Web menolak pemrosesan gambar (BardErrorInfo [1003]). Multimodal vision pada Gemini Web membutuhkan session cookie akun Google. Silakan atur cookie akun Google di GoAssistant."
        write_line({
            "type": "error",
            "error": f"Eksekusi Gemini Web gagal: {err_str}",
            "latency_ms": int((time.time() - start_time) * 1000),
        })
        sys.exit(1)


if __name__ == "__main__":
    main()
