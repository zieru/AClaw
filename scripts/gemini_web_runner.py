#!/usr/bin/env python3
"""
gemini_web_runner.py
Bridge script for GoAssistant to communicate with gemini-web2api via File IPC.
Supports single-prompt, streaming chunk generation (JSONL), model selection,
and proxy/cookie configuration. Compatible with Windows and Linux (Alpine).
"""

import argparse
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
        # Create a temp cookie file or configure directly
        temp_cookie_path = str(out_path.parent / f"cookie_{os.getpid()}.txt")
        try:
            with open(temp_cookie_path, "w", encoding="utf-8") as cf:
                cf.write(req["cookie_str"].strip())
            CONFIG["cookie_file"] = temp_cookie_path
        except Exception:
            pass

    is_stream = req.get("stream", True)
    start_time = time.time()
    full_text = []

    try:
        if is_stream and HAS_HTTPX:
            for delta in generate_stream(prompt, model_id, resolved_think):
                if delta:
                    full_text.append(delta)
                    write_line({"type": "chunk", "delta": delta})
            final_content = "".join(full_text).strip()
        else:
            raw = generate(prompt, model_id, resolved_think)
            final_content = extract_response_text(raw).strip()

        latency_ms = int((time.time() - start_time) * 1000)
        approx_tokens = (len(prompt) + len(final_content)) // 4

        write_line({
            "type": "done",
            "content": final_content,
            "total_tokens": approx_tokens,
            "latency_ms": latency_ms,
            "model": model_name,
        })
    except Exception as e:
        write_line({
            "type": "error",
            "error": f"Eksekusi Gemini Web gagal: {e}",
            "latency_ms": int((time.time() - start_time) * 1000),
        })
        sys.exit(1)


if __name__ == "__main__":
    main()
