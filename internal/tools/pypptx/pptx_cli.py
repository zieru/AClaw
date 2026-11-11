#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
GoAssistant PPTX CLI Engine
Script backend untuk manipulasi berkas PowerPoint (.pptx) menggunakan python-pptx.
Mendukung aksi: inspect, replace_text, create, dan add_slide dengan format input/output JSON.
"""

import sys
import os
import json
import argparse
from typing import Dict, Any, List, Optional
from datetime import datetime

import pptx
from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.dml.color import RGBColor
from pptx.enum.text import PP_ALIGN, MSO_ANCHOR
from pptx.enum.shapes import MSO_SHAPE


THEMES = {
    "modern_blue": {
        "primary": RGBColor(0, 82, 204),      # Deep Blue
        "secondary": RGBColor(23, 43, 77),    # Dark Navy
        "accent": RGBColor(0, 184, 217),      # Cyan Accent
        "bg": RGBColor(248, 249, 250),        # Light Gray
        "card_bg": RGBColor(255, 255, 255),   # White
        "card_border": RGBColor(222, 226, 230),
        "text_dark": RGBColor(23, 43, 77),
        "text_muted": RGBColor(107, 119, 140),
        "white": RGBColor(255, 255, 255),
    },
    "telkomsel_red": {
        "primary": RGBColor(238, 46, 36),     # Telkomsel Red
        "secondary": RGBColor(0, 26, 65),     # Deep Dark Navy
        "accent": RGBColor(255, 122, 0),      # Orange
        "bg": RGBColor(248, 249, 250),
        "card_bg": RGBColor(255, 255, 255),
        "card_border": RGBColor(225, 228, 232),
        "text_dark": RGBColor(0, 26, 65),
        "text_muted": RGBColor(90, 100, 110),
        "white": RGBColor(255, 255, 255),
    },
    "corporate_dark": {
        "primary": RGBColor(56, 189, 248),    # Sky Blue
        "secondary": RGBColor(15, 23, 42),    # Slate 900
        "accent": RGBColor(148, 163, 184),    # Slate 400
        "bg": RGBColor(15, 23, 42),
        "card_bg": RGBColor(30, 41, 59),      # Slate 800
        "card_border": RGBColor(51, 65, 85),
        "text_dark": RGBColor(248, 250, 252),
        "text_muted": RGBColor(148, 163, 184),
        "white": RGBColor(255, 255, 255),
    },
    "minimal_clean": {
        "primary": RGBColor(17, 24, 39),      # Charcoal
        "secondary": RGBColor(55, 65, 81),    # Gray 700
        "accent": RGBColor(79, 70, 229),      # Indigo
        "bg": RGBColor(255, 255, 255),
        "card_bg": RGBColor(249, 250, 251),   # Gray 50
        "card_border": RGBColor(229, 231, 235),
        "text_dark": RGBColor(17, 24, 39),
        "text_muted": RGBColor(107, 114, 128),
        "white": RGBColor(255, 255, 255),
    }
}


def get_theme(name: Optional[str]) -> Dict[str, RGBColor]:
    if not name:
        return THEMES["modern_blue"]
    key = name.strip().lower()
    return THEMES.get(key, THEMES["modern_blue"])


def cmd_inspect(file_path: str) -> Dict[str, Any]:
    if not os.path.exists(file_path):
        raise FileNotFoundError(f"File tidak ditemukan: {file_path}")

    prs = Presentation(file_path)
    slides_info = []

    for idx, slide in enumerate(prs.slides, 1):
        slide_title = ""
        texts = []
        tables = []
        notes = ""

        # Extract notes if any
        if slide.has_notes_slide:
            notes_text = slide.notes_slide.notes_text_frame.text.strip()
            if notes_text:
                notes = notes_text

        # Inspect shapes
        for shape in slide.shapes:
            if shape.has_text_frame:
                shape_text = shape.text_frame.text.strip()
                if shape_text:
                    texts.append(shape_text)
                    if shape == slide.shapes.title or (not slide_title and len(shape_text) < 100):
                        if not slide_title:
                            slide_title = shape_text.split("\n")[0]

            if shape.has_table:
                table_data = []
                for row in shape.table.rows:
                    row_cells = [cell.text.strip() for cell in row.cells]
                    table_data.append(row_cells)
                if table_data:
                    tables.append({
                        "rows_count": len(shape.table.rows),
                        "cols_count": len(shape.table.columns),
                        "data": table_data
                    })

        layout_name = slide.slide_layout.name if slide.slide_layout else "Unknown"

        slides_info.append({
            "slide_number": idx,
            "layout": layout_name,
            "title": slide_title,
            "texts": texts,
            "tables": tables,
            "notes": notes
        })

    return {
        "success": True,
        "file_path": file_path,
        "slide_count": len(prs.slides),
        "slide_width_in": round(prs.slide_width.inches, 2),
        "slide_height_in": round(prs.slide_height.inches, 2),
        "slides": slides_info
    }


def replace_in_text_frame(tf, replacements: Dict[str, str]) -> int:
    """Ganti teks di text frame dengan dukungan multi-run."""
    count = 0
    for p in tf.paragraphs:
        # Check if any replacement target exists in the full paragraph text
        for old_txt, new_txt in replacements.items():
            if old_txt in p.text:
                # Try single-run replacement first
                replaced_in_run = False
                for run in p.runs:
                    if old_txt in run.text:
                        run.text = run.text.replace(old_txt, new_txt)
                        replaced_in_run = True
                        count += 1

                # If text spanned across multiple runs
                if not replaced_in_run and old_txt in p.text:
                    full_text = p.text.replace(old_txt, new_txt)
                    if p.runs:
                        p.runs[0].text = full_text
                        for r in p.runs[1:]:
                            r.text = ""
                    else:
                        p.text = full_text
                    count += 1
    return count


def cmd_replace_text(file_path: str, output_path: str, replacements: Dict[str, str], target_slide: Optional[int] = None) -> Dict[str, Any]:
    if not os.path.exists(file_path):
        raise FileNotFoundError(f"File sumber tidak ditemukan: {file_path}")

    prs = Presentation(file_path)
    total_replaced = 0

    slides_to_process = prs.slides
    if target_slide is not None and 1 <= target_slide <= len(prs.slides):
        slides_to_process = [prs.slides[target_slide - 1]]

    for slide in slides_to_process:
        for shape in slide.shapes:
            if shape.has_text_frame:
                total_replaced += replace_in_text_frame(shape.text_frame, replacements)

            if shape.has_table:
                for row in shape.table.rows:
                    for cell in row.cells:
                        total_replaced += replace_in_text_frame(cell.text_frame, replacements)

        if slide.has_notes_slide:
            total_replaced += replace_in_text_frame(slide.notes_slide.notes_text_frame, replacements)

    os.makedirs(os.path.dirname(os.path.abspath(output_path)), exist_ok=True)
    prs.save(output_path)

    return {
        "success": True,
        "input_path": file_path,
        "output_path": output_path,
        "replacements_count": total_replaced,
        "message": f"Berhasil mengganti {total_replaced} teks/placeholder pada berkas presentasi."
    }


def add_title_slide(prs: Presentation, title: str, subtitle: str, theme: Dict[str, RGBColor]):
    """Membuat slide cover judul dengan visual modern 16:9."""
    blank_layout = prs.slide_layouts[6]  # Blank
    slide = prs.slides.add_slide(blank_layout)

    # Accent top border bar
    top_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0), Inches(0), prs.slide_width, Inches(0.15))
    top_bar.fill.solid()
    top_bar.fill.fore_color.rgb = theme["primary"]
    top_bar.line.color.rgb = theme["primary"]

    # Background subtle card
    bg_shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0.8), Inches(1.2), Inches(11.733), Inches(5.2))
    bg_shape.fill.solid()
    bg_shape.fill.fore_color.rgb = theme["card_bg"]
    bg_shape.line.color.rgb = theme["card_border"]

    # Left decorative vertical accent line
    accent_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(1.4), Inches(2.0), Inches(0.12), Inches(3.4))
    accent_bar.fill.solid()
    accent_bar.fill.fore_color.rgb = theme["primary"]
    accent_bar.line.color.rgb = theme["primary"]

    # Title & Subtitle text box
    tb = slide.shapes.add_textbox(Inches(1.8), Inches(2.0), Inches(10.0), Inches(3.4))
    tf = tb.text_frame
    tf.word_wrap = True

    p_title = tf.paragraphs[0]
    p_title.text = title
    p_title.font.name = "Segoe UI"
    p_title.font.size = Pt(40)
    p_title.font.bold = True
    p_title.font.color.rgb = theme["primary"]

    if subtitle:
        p_sub = tf.add_paragraph()
        p_sub.text = subtitle
        p_sub.font.name = "Segoe UI"
        p_sub.font.size = Pt(20)
        p_sub.font.color.rgb = theme["text_muted"]
        p_sub.space_before = Pt(16)

    # Date / Footer info
    p_date = tf.add_paragraph()
    p_date.text = f"Dibuat oleh GoAssistant • {datetime.now().strftime('%d %B %Y')}"
    p_date.font.name = "Segoe UI"
    p_date.font.size = Pt(12)
    p_date.font.italic = True
    p_date.font.color.rgb = theme["text_muted"]
    p_date.space_before = Pt(24)

    return slide


def add_content_slide(prs: Presentation, slide_data: Dict[str, Any], theme: Dict[str, RGBColor]):
    """Membuat slide konten terstruktur (bullets, cards/kpis, dan table)."""
    blank_layout = prs.slide_layouts[6]
    slide = prs.slides.add_slide(blank_layout)

    title_text = slide_data.get("title", "Slide Title")
    subtitle_text = slide_data.get("subtitle", "")
    bullets = slide_data.get("bullets", [])
    cards = slide_data.get("cards", [])
    table_data = slide_data.get("table", None)
    notes_text = slide_data.get("notes", "")

    # Top accent bar
    top_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0), Inches(0), prs.slide_width, Inches(0.10))
    top_bar.fill.solid()
    top_bar.fill.fore_color.rgb = theme["primary"]
    top_bar.line.color.rgb = theme["primary"]

    # Header Box (Title & Subtitle)
    header_box = slide.shapes.add_textbox(Inches(0.8), Inches(0.4), Inches(11.733), Inches(1.1))
    htf = header_box.text_frame
    htf.word_wrap = True

    hp = htf.paragraphs[0]
    hp.text = title_text
    hp.font.name = "Segoe UI"
    hp.font.size = Pt(26)
    hp.font.bold = True
    hp.font.color.rgb = theme["primary"]

    if subtitle_text:
        hsub = htf.add_paragraph()
        hsub.text = subtitle_text
        hsub.font.name = "Segoe UI"
        hsub.font.size = Pt(14)
        hsub.font.color.rgb = theme["text_muted"]
        hsub.space_before = Pt(4)

    # Dividing line under header
    div_line = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0.8), Inches(1.5), Inches(11.733), Inches(0.02))
    div_line.fill.solid()
    div_line.fill.fore_color.rgb = theme["card_border"]
    div_line.line.color.rgb = theme["card_border"]

    current_top = 1.7

    # Render Cards / KPIs if present
    if cards:
        card_count = min(len(cards), 4)
        card_gap = 0.3
        total_width = 11.733
        card_w = (total_width - (card_gap * (card_count - 1))) / card_count
        card_h = 1.4

        for c_idx, card in enumerate(cards[:card_count]):
            c_left = 0.8 + c_idx * (card_w + card_gap)
            c_shape = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(c_left), Inches(current_top), Inches(card_w), Inches(card_h))
            c_shape.fill.solid()
            c_shape.fill.fore_color.rgb = theme["card_bg"]
            c_shape.line.color.rgb = theme["primary"]
            c_shape.line.width = Pt(1.5)

            ctf = c_shape.text_frame
            ctf.word_wrap = True

            # Card Value
            cp_val = ctf.paragraphs[0]
            cp_val.text = str(card.get("value", ""))
            cp_val.font.name = "Segoe UI"
            cp_val.font.size = Pt(22)
            cp_val.font.bold = True
            cp_val.font.color.rgb = theme["primary"]
            cp_val.alignment = PP_ALIGN.CENTER

            # Card Title / Label
            cp_label = ctf.add_paragraph()
            cp_label.text = str(card.get("title", ""))
            cp_label.font.name = "Segoe UI"
            cp_label.font.size = Pt(11)
            cp_label.font.bold = True
            cp_label.font.color.rgb = theme["text_dark"]
            cp_label.alignment = PP_ALIGN.CENTER
            cp_label.space_before = Pt(2)

            # Card Description
            if "desc" in card and card["desc"]:
                cp_desc = ctf.add_paragraph()
                cp_desc.text = str(card["desc"])
                cp_desc.font.name = "Segoe UI"
                cp_desc.font.size = Pt(9)
                cp_desc.font.color.rgb = theme["text_muted"]
                cp_desc.alignment = PP_ALIGN.CENTER

        current_top += card_h + 0.3

    # Render Table if present
    if table_data and "rows" in table_data:
        headers = table_data.get("headers", [])
        rows = table_data.get("rows", [])
        all_rows = []
        if headers:
            all_rows.append(headers)
        all_rows.extend(rows)

        if all_rows:
            num_rows = len(all_rows)
            num_cols = len(all_rows[0])
            table_w = Inches(11.733)
            row_h = Inches(0.4)
            table_h = Inches(min(num_rows * 0.4, 4.0))

            t_shape = slide.shapes.add_table(num_rows, num_cols, Inches(0.8), Inches(current_top), table_w, table_h)
            tbl = t_shape.table

            for r_idx, row in enumerate(all_rows):
                is_header = (r_idx == 0 and bool(headers))
                for c_idx, cell_value in enumerate(row):
                    if c_idx < num_cols:
                        cell = tbl.cell(r_idx, c_idx)
                        cell.text = str(cell_value)
                        cell.vertical_anchor = MSO_ANCHOR.MIDDLE

                        # Styling
                        p = cell.text_frame.paragraphs[0]
                        p.font.name = "Segoe UI"
                        if is_header:
                            p.font.bold = True
                            p.font.size = Pt(13)
                            p.font.color.rgb = theme["white"]
                            cell.fill.solid()
                            cell.fill.fore_color.rgb = theme["primary"]
                        else:
                            p.font.size = Pt(11)
                            p.font.color.rgb = theme["text_dark"]
                            cell.fill.solid()
                            if r_idx % 2 == 1:
                                cell.fill.fore_color.rgb = theme["card_bg"]
                            else:
                                cell.fill.fore_color.rgb = theme["bg"]

    # Render Bullet Points if present (and no table, or alongside)
    elif bullets:
        content_box = slide.shapes.add_textbox(Inches(0.8), Inches(current_top), Inches(11.733), Inches(5.2 - current_top))
        ctf = content_box.text_frame
        ctf.word_wrap = True

        for b_idx, bullet in enumerate(bullets):
            p = ctf.paragraphs[0] if b_idx == 0 else ctf.add_paragraph()
            p.text = f"•  {bullet}"
            p.font.name = "Segoe UI"
            p.font.size = Pt(16)
            p.font.color.rgb = theme["text_dark"]
            p.space_before = Pt(12)
            p.space_after = Pt(4)

    # Attach Speaker Notes if any
    if notes_text:
        notes_slide = slide.notes_slide
        notes_slide.notes_text_frame.text = notes_text

    return slide


def cmd_create(spec: Dict[str, Any]) -> Dict[str, Any]:
    output_path = spec.get("output_path", "")
    if not output_path:
        os.makedirs("data/output", exist_ok=True)
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        output_path = f"data/output/presentation_{timestamp}.pptx"

    prs = Presentation()
    # 16:9 Widescreen standard
    prs.slide_width = Inches(13.333)
    prs.slide_height = Inches(7.5)

    theme = get_theme(spec.get("theme", "modern_blue"))

    # Title slide
    title = spec.get("title", "Presentasi Baru")
    subtitle = spec.get("subtitle", "")
    add_title_slide(prs, title, subtitle, theme)

    # Content slides
    slides_data = spec.get("slides", [])
    for s_data in slides_data:
        add_content_slide(prs, s_data, theme)

    os.makedirs(os.path.dirname(os.path.abspath(output_path)), exist_ok=True)
    prs.save(output_path)

    return {
        "success": True,
        "output_path": output_path,
        "slide_count": len(prs.slides),
        "theme": spec.get("theme", "modern_blue"),
        "message": f"Presentasi berhasil dibuat dengan {len(prs.slides)} slide."
    }


def cmd_add_slide(file_path: str, output_path: str, slide_data: Dict[str, Any], theme_name: Optional[str] = None) -> Dict[str, Any]:
    if not os.path.exists(file_path):
        raise FileNotFoundError(f"File sumber tidak ditemukan: {file_path}")

    prs = Presentation(file_path)
    theme = get_theme(theme_name)

    add_content_slide(prs, slide_data, theme)

    if not output_path:
        output_path = file_path

    os.makedirs(os.path.dirname(os.path.abspath(output_path)), exist_ok=True)
    prs.save(output_path)

    return {
        "success": True,
        "input_path": file_path,
        "output_path": output_path,
        "total_slides": len(prs.slides),
        "message": f"Berhasil menambahkan 1 slide baru. Total slide sekarang: {len(prs.slides)}."
    }


def main():
    parser = argparse.ArgumentParser(description="GoAssistant PPTX CLI Engine")
    subparsers = parser.add_subparsers(dest="action", required=True)

    # Inspect
    inspect_parser = subparsers.add_parser("inspect", help="Membaca dan menginspeksi struktur slide")
    inspect_parser.add_argument("--file", required=True, help="Path berkas PPTX")

    # Replace Text
    replace_parser = subparsers.add_parser("replace_text", help="Mengganti teks/placeholder template")
    replace_parser.add_argument("--file", required=True, help="Path berkas PPTX sumber")
    replace_parser.add_argument("--output", required=True, help="Path berkas PPTX output")
    replace_parser.add_argument("--replacements", required=True, help="JSON string atau path file JSON berisi dict {old: new}")
    replace_parser.add_argument("--slide", type=int, default=None, help="Nomor slide tertentu (opsional)")

    # Create
    create_parser = subparsers.add_parser("create", help="Membuat presentasi baru")
    create_parser.add_argument("--json", required=True, help="JSON string atau path file JSON spesifikasi presentasi")

    # Add Slide
    add_parser = subparsers.add_parser("add_slide", help="Menambahkan slide ke presentasi")
    add_parser.add_argument("--file", required=True, help="Path berkas PPTX sumber")
    add_parser.add_argument("--output", default=None, help="Path berkas PPTX output")
    add_parser.add_argument("--slide-json", required=True, help="JSON string atau path file JSON spesifikasi slide baru")
    add_parser.add_argument("--theme", default="modern_blue", help="Nama tema")

    args = parser.parse_args()

    try:
        if args.action == "inspect":
            result = cmd_inspect(args.file)
        elif args.action == "replace_text":
            repl_raw = args.replacements
            if os.path.exists(repl_raw):
                with open(repl_raw, "r", encoding="utf-8") as f:
                    replacements = json.load(f)
            else:
                replacements = json.loads(repl_raw)
            result = cmd_replace_text(args.file, args.output, replacements, args.slide)
        elif args.action == "create":
            spec_raw = args.json
            if os.path.exists(spec_raw):
                with open(spec_raw, "r", encoding="utf-8") as f:
                    spec = json.load(f)
            else:
                spec = json.loads(spec_raw)
            result = cmd_create(spec)
        elif args.action == "add_slide":
            slide_raw = args.slide_json
            if os.path.exists(slide_raw):
                with open(slide_raw, "r", encoding="utf-8") as f:
                    slide_data = json.load(f)
            else:
                slide_data = json.loads(slide_raw)
            out_p = args.output if args.output else args.file
            result = cmd_add_slide(args.file, out_p, slide_data, args.theme)
        else:
            result = {"success": False, "error": f"Aksi '{args.action}' tidak dikenali"}

        print(json.dumps(result, ensure_ascii=False))
        sys.exit(0)
    except Exception as e:
        err_res = {"success": False, "error": str(e)}
        print(json.dumps(err_res, ensure_ascii=False))
        sys.exit(1)


if __name__ == "__main__":
    main()
