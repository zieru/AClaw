#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
GoAssistant PPTX CLI Engine
Script backend untuk manipulasi berkas PowerPoint (.pptx) menggunakan python-pptx.
Mendukung aksi: inspect, replace_text, create, add_slide, dan edit_slide (in-place rich styling).
"""

import sys
import os
import re
import json
import argparse
from typing import Dict, Any, List, Optional, Tuple, Union
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

STATUS_COLORS = {
    "success": RGBColor(16, 185, 129),   # Emerald Green #10B981
    "green": RGBColor(16, 185, 129),
    "danger": RGBColor(239, 68, 68),     # Crimson Red #EF4444
    "red": RGBColor(239, 68, 68),
    "warning": RGBColor(245, 158, 11),   # Amber/Orange #F59E0B
    "amber": RGBColor(245, 158, 11),
    "orange": RGBColor(245, 158, 11),
    "info": RGBColor(6, 182, 212),       # Cyan #06B6D4
    "cyan": RGBColor(6, 182, 212),
    "blue": RGBColor(59, 130, 246),      # Blue #3B82F6
    "neutral": RGBColor(100, 116, 139),  # Slate #64748B
    "gray": RGBColor(100, 116, 139),
}


def expand_path(p: str) -> str:
    """Expand tilde dan resolve absolute path."""
    if not p:
        return ""
    expanded = os.path.expanduser(p.strip())
    return os.path.abspath(expanded)


def get_theme(name: Optional[str]) -> Dict[str, RGBColor]:
    if not name:
        return THEMES["modern_blue"]
    key = name.strip().lower()
    return THEMES.get(key, THEMES["modern_blue"])


def parse_color(val: Any, default_color: RGBColor, theme: Optional[Dict[str, RGBColor]] = None) -> RGBColor:
    """Mengonversi nilai warna (hex, nama status, theme key) menjadi RGBColor."""
    if not val:
        return default_color
    if isinstance(val, RGBColor):
        return val
    if isinstance(val, (list, tuple)) and len(val) >= 3:
        return RGBColor(int(val[0]), int(val[1]), int(val[2]))

    s = str(val).strip().lower()
    if s in STATUS_COLORS:
        return STATUS_COLORS[s]

    if theme and s in theme:
        return theme[s]

    # Hex format #RRGGBB or RRGGBB
    if s.startswith("#"):
        s = s[1:]
    if len(s) == 6:
        try:
            r = int(s[0:2], 16)
            g = int(s[2:4], 16)
            b = int(s[4:6], 16)
            return RGBColor(r, g, b)
        except ValueError:
            pass

    return default_color


def add_formatted_runs(paragraph, text: str, font_name: str = "Segoe UI", font_size: float = 12,
                       default_color: Optional[RGBColor] = None, default_bold: bool = False,
                       default_italic: bool = False):
    """
    Menambahkan run teks dengan mem-parse penanda HTML/Markdown sederhana:
    <b>teks</b> atau **teks** -> bold
    <i>teks</i> atau *teks* -> italic
    """
    if not text:
        return

    # Normalisasi format **teks** menjadi <b>teks</b>
    normalized = re.sub(r'\*\*(.*?)\*\*', r'<b>\1</b>', text)
    tokens = re.split(r'(<b>.*?</b>|<i>.*?</i>)', normalized)

    for tok in tokens:
        if not tok:
            continue
        is_bold = default_bold
        is_italic = default_italic
        run_text = tok

        if tok.startswith("<b>") and tok.endswith("</b>"):
            run_text = tok[3:-4]
            is_bold = True
        elif tok.startswith("<i>") and tok.endswith("</i>"):
            run_text = tok[3:-4]
            is_italic = True

        run = paragraph.add_run()
        run.text = run_text
        run.font.name = font_name
        run.font.size = Pt(font_size)
        run.font.bold = is_bold
        run.font.italic = is_italic
        if default_color:
            run.font.color.rgb = default_color


def cmd_inspect(file_path: str) -> Dict[str, Any]:
    file_path = expand_path(file_path)
    if not os.path.exists(file_path):
        raise FileNotFoundError(f"File tidak ditemukan: {file_path}")

    prs = Presentation(file_path)
    slides_info = []

    for idx, slide in enumerate(prs.slides, 1):
        slide_title = ""
        texts = []
        tables = []
        notes = ""

        if slide.has_notes_slide:
            notes_text = slide.notes_slide.notes_text_frame.text.strip()
            if notes_text:
                notes = notes_text

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
    count = 0
    for p in tf.paragraphs:
        for old_txt, new_txt in replacements.items():
            if old_txt in p.text:
                replaced_in_run = False
                for run in p.runs:
                    if old_txt in run.text:
                        run.text = run.text.replace(old_txt, new_txt)
                        replaced_in_run = True
                        count += 1
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
    file_path = expand_path(file_path)
    output_path = expand_path(output_path)
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

    os.makedirs(os.path.dirname(output_path), exist_ok=True)
    prs.save(output_path)

    return {
        "success": True,
        "input_path": file_path,
        "output_path": output_path,
        "replacements_count": total_replaced,
        "message": f"Berhasil mengganti {total_replaced} teks/placeholder pada berkas presentasi."
    }


def render_rich_slide(slide, slide_data: Dict[str, Any], theme: Dict[str, RGBColor], prs: Presentation):
    """
    Engine render slide visual kaya dan eksekutif.
    Mendukung: Header ber-badge, KPI Cards ber-status badge, Multi-Kolom Side-by-Side,
    Callout Alert Boxes, Tabel Modern Zebra-Striped, dan Freeform Canvas Elements.
    """
    total_w = prs.slide_width.inches if hasattr(prs, 'slide_width') else 13.333
    total_h = prs.slide_height.inches if hasattr(prs, 'slide_height') else 7.5

    margin_left = 0.8
    usable_width = total_w - (margin_left * 2)

    # 1. Slide Background (jika ada permintaan kustom)
    bg_color_req = slide_data.get("background")
    if bg_color_req:
        bg_color = parse_color(bg_color_req, theme["bg"], theme)
        bg_shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0), Inches(0), Inches(total_w), Inches(total_h))
        bg_shape.fill.solid()
        bg_shape.fill.fore_color.rgb = bg_color
        bg_shape.line.fill.background()

    # 2. Top Accent Bar
    top_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0), Inches(0), Inches(total_w), Inches(0.12))
    top_bar.fill.solid()
    top_bar.fill.fore_color.rgb = theme["primary"]
    top_bar.line.fill.background()

    # 3. Header Box (Title, Subtitle, Category Badge)
    title_text = slide_data.get("title", "")
    subtitle_text = slide_data.get("subtitle", "")
    category_badge = slide_data.get("category_badge", "")

    current_top = 0.4
    if title_text:
        # Check for Category Badge at Top
        if category_badge:
            badge_shape = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(margin_left), Inches(current_top), Inches(2.2), Inches(0.28))
            badge_shape.fill.solid()
            badge_shape.fill.fore_color.rgb = theme["primary"]
            badge_shape.line.fill.background()
            btf = badge_shape.text_frame
            btf.margin_top = Pt(2)
            btf.margin_bottom = Pt(2)
            bp = btf.paragraphs[0]
            bp.text = str(category_badge).upper()
            bp.font.name = "Segoe UI"
            bp.font.size = Pt(8.5)
            bp.font.bold = True
            bp.font.color.rgb = theme["white"]
            bp.alignment = PP_ALIGN.CENTER
            current_top += 0.35

        header_box = slide.shapes.add_textbox(Inches(margin_left), Inches(current_top), Inches(usable_width), Inches(1.1))
        htf = header_box.text_frame
        htf.word_wrap = True
        htf.margin_left = Pt(0)
        htf.margin_top = Pt(0)

        hp = htf.paragraphs[0]
        hp.text = title_text
        hp.font.name = "Segoe UI"
        hp.font.size = Pt(24)
        hp.font.bold = True
        hp.font.color.rgb = theme["primary"]

        if subtitle_text:
            hsub = htf.add_paragraph()
            hsub.text = subtitle_text
            hsub.font.name = "Segoe UI"
            hsub.font.size = Pt(13)
            hsub.font.color.rgb = theme["text_muted"]
            hsub.space_before = Pt(3)

        current_top += 0.95

        # Horizontal Divider
        div_line = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(margin_left), Inches(current_top), Inches(usable_width), Inches(0.015))
        div_line.fill.solid()
        div_line.fill.fore_color.rgb = theme["card_border"]
        div_line.line.fill.background()
        current_top += 0.18

    # 4. Render KPI Cards Row (jika ada)
    kpis = slide_data.get("kpis") or slide_data.get("cards", [])
    if kpis and isinstance(kpis, list):
        card_count = min(len(kpis), 6)
        card_gap = 0.22
        card_w = (usable_width - (card_gap * (card_count - 1))) / card_count
        card_h = 1.35

        for c_idx, card in enumerate(kpis[:card_count]):
            c_left = margin_left + c_idx * (card_w + card_gap)
            c_shape = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(c_left), Inches(current_top), Inches(card_w), Inches(card_h))
            c_shape.fill.solid()
            c_shape.fill.fore_color.rgb = theme["card_bg"]
            c_shape.line.color.rgb = theme["card_border"]
            c_shape.line.width = Pt(1)

            ctf = c_shape.text_frame
            ctf.word_wrap = True
            ctf.margin_left = Pt(10)
            ctf.margin_right = Pt(10)
            ctf.margin_top = Pt(10)
            ctf.margin_bottom = Pt(8)

            # Baris Metrik Utama
            cp_val = ctf.paragraphs[0]
            val_text = str(card.get("value", ""))
            cp_val.text = val_text
            cp_val.font.name = "Segoe UI"
            cp_val.font.size = Pt(22)
            cp_val.font.bold = True
            card_accent = parse_color(card.get("color"), theme["primary"], theme)
            cp_val.font.color.rgb = card_accent

            # Badge Status jika disertakan (misal: "93.8% Obsolete" atau "+147")
            badge_text = card.get("badge", "")
            if badge_text:
                b_run = cp_val.add_run()
                b_run.text = f"  [{badge_text}]"
                b_run.font.size = Pt(10)
                b_run.font.bold = True
                b_color = parse_color(card.get("badge_type") or card.get("badge_color"), STATUS_COLORS["neutral"], theme)
                b_run.font.color.rgb = b_color

            # Judul / Label Metrik
            cp_title = ctf.add_paragraph()
            cp_title.text = str(card.get("title", ""))
            cp_title.font.name = "Segoe UI"
            cp_title.font.size = Pt(10.5)
            cp_title.font.bold = True
            cp_title.font.color.rgb = theme["text_dark"]
            cp_title.space_before = Pt(3)

            # Subtitle / Keterangan Ringkas
            desc_text = card.get("desc") or card.get("subtitle", "")
            if desc_text:
                cp_desc = ctf.add_paragraph()
                cp_desc.text = str(desc_text)
                cp_desc.font.name = "Segoe UI"
                cp_desc.font.size = Pt(8.5)
                cp_desc.font.color.rgb = theme["text_muted"]
                cp_desc.space_before = Pt(1)

        current_top += card_h + 0.22

    # 5. Render Multi-Kolom Side-by-Side (jika ada)
    columns_data = slide_data.get("columns", [])
    if columns_data and isinstance(columns_data, list):
        col_count = len(columns_data)
        col_gap = 0.28
        col_w = (usable_width - (col_gap * (col_count - 1))) / col_count
        avail_h = max(2.0, (total_h - 0.5) - current_top)

        for col_idx, col in enumerate(columns_data):
            c_left = margin_left + col_idx * (col_w + col_gap)

            # Card Container untuk Kolom
            col_box = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(c_left), Inches(current_top), Inches(col_w), Inches(avail_h))
            col_box.fill.solid()
            col_box.fill.fore_color.rgb = theme["card_bg"]
            col_box.line.color.rgb = theme["card_border"]
            col_box.line.width = Pt(1)

            ctf = col_box.text_frame
            ctf.word_wrap = True
            ctf.margin_left = Pt(14)
            ctf.margin_right = Pt(14)
            ctf.margin_top = Pt(12)
            ctf.margin_bottom = Pt(12)

            # Judul Kolom
            col_title = col.get("title", f"Kolom {col_idx+1}")
            cp = ctf.paragraphs[0]
            cp.text = col_title
            cp.font.name = "Segoe UI"
            cp.font.size = Pt(14)
            cp.font.bold = True
            cp.font.color.rgb = theme["primary"]

            # Inner Callout / Alert jika ada
            if "callout" in col and col["callout"]:
                c_alert = ctf.add_paragraph()
                c_alert.text = f"💡 {col['callout']}"
                c_alert.font.name = "Segoe UI"
                c_alert.font.size = Pt(10)
                c_alert.font.italic = True
                c_alert.font.color.rgb = theme["text_dark"]
                c_alert.space_before = Pt(6)
                c_alert.space_after = Pt(6)

            # Daftar Item / Bullet Point dalam Kolom
            items = col.get("items", [])
            for item in items:
                p_item = ctf.add_paragraph()
                p_item.space_before = Pt(6)
                # Tambahkan bullet run berformat
                add_formatted_runs(p_item, f"•  {item}", font_name="Segoe UI", font_size=10.5, default_color=theme["text_dark"])

        current_top += avail_h + 0.2

    # 6. Render Stand-Alone Callout Box (jika ada)
    callout_data = slide_data.get("callout")
    if callout_data:
        callout_text = callout_data if isinstance(callout_data, str) else callout_data.get("text", "")
        callout_title = "" if isinstance(callout_data, str) else callout_data.get("title", "Insight & Rekomendasi")
        callout_type = "info" if isinstance(callout_data, str) else callout_data.get("type", "info")

        callout_h = 1.05
        callout_shape = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(margin_left), Inches(current_top), Inches(usable_width), Inches(callout_h))
        callout_shape.fill.solid()
        callout_shape.fill.fore_color.rgb = theme["card_bg"]
        accent_color = parse_color(callout_type, theme["accent"], theme)
        callout_shape.line.color.rgb = accent_color
        callout_shape.line.width = Pt(1.5)

        # Left thick vertical decorative line
        l_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(margin_left), Inches(current_top), Inches(0.12), Inches(callout_h))
        l_bar.fill.solid()
        l_bar.fill.fore_color.rgb = accent_color
        l_bar.line.fill.background()

        ctf = callout_shape.text_frame
        ctf.word_wrap = True
        ctf.margin_left = Pt(18)
        ctf.margin_top = Pt(8)
        ctf.margin_bottom = Pt(8)

        if callout_title:
            cp_t = ctf.paragraphs[0]
            cp_t.text = f"💡 {callout_title}"
            cp_t.font.name = "Segoe UI"
            cp_t.font.size = Pt(12)
            cp_t.font.bold = True
            cp_t.font.color.rgb = accent_color

            cp_body = ctf.add_paragraph()
            add_formatted_runs(cp_body, callout_text, font_name="Segoe UI", font_size=10.5, default_color=theme["text_dark"])
            cp_body.space_before = Pt(3)
        else:
            cp_body = ctf.paragraphs[0]
            add_formatted_runs(cp_body, f"💡 {callout_text}", font_name="Segoe UI", font_size=10.5, default_color=theme["text_dark"])

        current_top += callout_h + 0.2

    # 7. Render Modern Zebra Table (jika ada)
    table_data = slide_data.get("table")
    if table_data and isinstance(table_data, dict) and "rows" in table_data:
        headers = table_data.get("headers", [])
        rows = table_data.get("rows", [])
        all_rows = []
        if headers:
            all_rows.append(headers)
        all_rows.extend(rows)

        if all_rows:
            num_rows = len(all_rows)
            num_cols = len(all_rows[0])
            table_w = usable_width
            avail_h = min(max(2.0, (total_h - 0.4) - current_top), num_rows * 0.38)

            t_shape = slide.shapes.add_table(num_rows, num_cols, Inches(margin_left), Inches(current_top), Inches(table_w), Inches(avail_h))
            tbl = t_shape.table

            for r_idx, row in enumerate(all_rows):
                is_header = (r_idx == 0 and bool(headers))
                for c_idx, cell_value in enumerate(row):
                    if c_idx < num_cols:
                        cell = tbl.cell(r_idx, c_idx)
                        cell.text = ""
                        cell.vertical_anchor = MSO_ANCHOR.MIDDLE
                        p = cell.text_frame.paragraphs[0]
                        p.alignment = PP_ALIGN.LEFT
                        if is_header:
                            add_formatted_runs(p, str(cell_value), font_name="Segoe UI", font_size=11.5, default_color=theme["white"], default_bold=True)
                            cell.fill.solid()
                            cell.fill.fore_color.rgb = theme["primary"]
                        else:
                            add_formatted_runs(p, str(cell_value), font_name="Segoe UI", font_size=10, default_color=theme["text_dark"])
                            cell.fill.solid()
                            cell.fill.fore_color.rgb = theme["card_bg"] if r_idx % 2 == 1 else theme["bg"]

            current_top += avail_h + 0.2

    # 8. Render Freeform Canvas Elements (jika ada)
    elements = slide_data.get("elements", [])
    if elements and isinstance(elements, list):
        for el in elements:
            el_type = el.get("type", "card").lower()
            x = float(el.get("x", margin_left))
            y = float(el.get("y", current_top))
            w = float(el.get("w", 3.0))
            h = float(el.get("h", 1.5))

            if el_type in ("card", "shape", "rounded_rectangle", "rectangle"):
                shape_enum = MSO_SHAPE.ROUNDED_RECTANGLE if "round" in el_type else MSO_SHAPE.RECTANGLE
                sh = slide.shapes.add_shape(shape_enum, Inches(x), Inches(y), Inches(w), Inches(h))
                sh.fill.solid()
                sh.fill.fore_color.rgb = parse_color(el.get("fill"), theme["card_bg"], theme)
                b_color = parse_color(el.get("border"), theme["card_border"], theme)
                sh.line.color.rgb = b_color
                sh.line.width = Pt(float(el.get("border_width", 1.0)))

                if "text" in el or "paragraphs" in el:
                    tf = sh.text_frame
                    tf.word_wrap = True
                    tf.margin_left = Pt(8)
                    tf.margin_right = Pt(8)
                    tf.margin_top = Pt(8)
                    tf.margin_bottom = Pt(8)
                    if "text" in el:
                        p = tf.paragraphs[0]
                        add_formatted_runs(p, str(el["text"]), font_name="Segoe UI", font_size=float(el.get("font_size", 11)), default_color=parse_color(el.get("font_color"), theme["text_dark"], theme), default_bold=bool(el.get("bold", False)))
                    if "paragraphs" in el:
                        for idx, p_spec in enumerate(el["paragraphs"]):
                            p = tf.paragraphs[0] if (idx == 0 and "text" not in el) else tf.add_paragraph()
                            add_formatted_runs(p, str(p_spec.get("text", "")), font_name="Segoe UI", font_size=float(p_spec.get("font_size", 11)), default_color=parse_color(p_spec.get("font_color"), theme["text_dark"], theme), default_bold=bool(p_spec.get("bold", False)))

            elif el_type == "textbox":
                tb = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
                tf = tb.text_frame
                tf.word_wrap = True
                p = tf.paragraphs[0]
                add_formatted_runs(p, str(el.get("text", "")), font_name="Segoe UI", font_size=float(el.get("font_size", 12)), default_color=parse_color(el.get("font_color"), theme["text_dark"], theme), default_bold=bool(el.get("bold", False)))

            elif el_type == "line":
                line_sh = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
                line_sh.fill.solid()
                line_sh.fill.fore_color.rgb = parse_color(el.get("color"), theme["primary"], theme)
                line_sh.line.fill.background()

    # 9. Speaker Notes
    notes_text = slide_data.get("notes")
    if notes_text:
        slide.notes_slide.notes_text_frame.text = str(notes_text)

    return slide


def add_title_slide(prs: Presentation, title: str, subtitle: str, theme: Dict[str, RGBColor]):
    """Membuat slide cover judul dengan visual modern 16:9."""
    blank_layout = prs.slide_layouts[6]
    slide = prs.slides.add_slide(blank_layout)

    # Accent top border bar
    top_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0), Inches(0), prs.slide_width, Inches(0.15))
    top_bar.fill.solid()
    top_bar.fill.fore_color.rgb = theme["primary"]
    top_bar.line.fill.background()

    # Background subtle card
    bg_shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0.8), Inches(1.2), Inches(11.733), Inches(5.2))
    bg_shape.fill.solid()
    bg_shape.fill.fore_color.rgb = theme["card_bg"]
    bg_shape.line.color.rgb = theme["card_border"]

    # Left decorative vertical accent line
    accent_bar = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(1.4), Inches(2.0), Inches(0.12), Inches(3.4))
    accent_bar.fill.solid()
    accent_bar.fill.fore_color.rgb = theme["primary"]
    accent_bar.line.fill.background()

    # Title & Subtitle text box
    tb = slide.shapes.add_textbox(Inches(1.8), Inches(2.0), Inches(10.0), Inches(3.4))
    tf = tb.text_frame
    tf.word_wrap = True

    p_title = tf.paragraphs[0]
    p_title.text = title
    p_title.font.name = "Segoe UI"
    p_title.font.size = Pt(38)
    p_title.font.bold = True
    p_title.font.color.rgb = theme["primary"]

    if subtitle:
        p_sub = tf.add_paragraph()
        p_sub.text = subtitle
        p_sub.font.name = "Segoe UI"
        p_sub.font.size = Pt(18)
        p_sub.font.color.rgb = theme["text_muted"]
        p_sub.space_before = Pt(16)

    p_date = tf.add_paragraph()
    p_date.text = f"Dibuat oleh GoAssistant • {datetime.now().strftime('%d %B %Y')}"
    p_date.font.name = "Segoe UI"
    p_date.font.size = Pt(11)
    p_date.font.italic = True
    p_date.font.color.rgb = theme["text_muted"]
    p_date.space_before = Pt(24)

    return slide


def cmd_edit_slide(file_path: str, output_path: str, slide_number: int, slide_data: Dict[str, Any], theme_name: Optional[str] = None) -> Dict[str, Any]:
    file_path = expand_path(file_path)
    output_path = expand_path(output_path) if output_path else file_path

    if not os.path.exists(file_path):
        raise FileNotFoundError(f"File sumber tidak ditemukan: {file_path}")

    prs = Presentation(file_path)
    if not (1 <= slide_number <= len(prs.slides)):
        raise IndexError(f"Nomor slide {slide_number} di luar jangkauan (total slide: {len(prs.slides)})")

    slide = prs.slides[slide_number - 1]
    theme = get_theme(theme_name or slide_data.get("theme"))

    # Simpan catatan pembicara lama jika di request baru tidak ada pengganti
    old_notes = ""
    if slide.has_notes_slide:
        old_notes = slide.notes_slide.notes_text_frame.text
    if not slide_data.get("notes") and old_notes:
        slide_data["notes"] = old_notes

    # Hapus shape konten visual lama secara bersih
    shapes_to_delete = [s for s in slide.shapes]
    for s in shapes_to_delete:
        sp = s._element
        parent = sp.getparent()
        if parent is not None:
            parent.remove(sp)

    # Render ulang slide dengan layout visual kaya
    render_rich_slide(slide, slide_data, theme, prs)

    os.makedirs(os.path.dirname(output_path), exist_ok=True)
    prs.save(output_path)

    return {
        "success": True,
        "input_path": file_path,
        "output_path": output_path,
        "slide_number": slide_number,
        "message": f"Slide {slide_number} berhasil di-styling ulang secara in-place dengan kualitas eksekutif."
    }


def cmd_create(spec: Dict[str, Any]) -> Dict[str, Any]:
    output_path = spec.get("output_path", "")
    if not output_path:
        os.makedirs("data/output", exist_ok=True)
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        output_path = f"data/output/presentation_{timestamp}.pptx"
    output_path = expand_path(output_path)

    prs = Presentation()
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
        blank_layout = prs.slide_layouts[6]
        slide = prs.slides.add_slide(blank_layout)
        render_rich_slide(slide, s_data, theme, prs)

    os.makedirs(os.path.dirname(output_path), exist_ok=True)
    prs.save(output_path)

    return {
        "success": True,
        "output_path": output_path,
        "slide_count": len(prs.slides),
        "theme": spec.get("theme", "modern_blue"),
        "message": f"Presentasi berhasil dibuat dengan {len(prs.slides)} slide."
    }


def cmd_add_slide(file_path: str, output_path: str, slide_data: Dict[str, Any], theme_name: Optional[str] = None) -> Dict[str, Any]:
    file_path = expand_path(file_path)
    output_path = expand_path(output_path) if output_path else file_path

    if not os.path.exists(file_path):
        raise FileNotFoundError(f"File sumber tidak ditemukan: {file_path}")

    prs = Presentation(file_path)
    theme = get_theme(theme_name or slide_data.get("theme"))

    blank_layout = prs.slide_layouts[6]
    slide = prs.slides.add_slide(blank_layout)
    render_rich_slide(slide, slide_data, theme, prs)

    os.makedirs(os.path.dirname(output_path), exist_ok=True)
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

    # Edit Slide (In-Place)
    edit_parser = subparsers.add_parser("edit_slide", help="Mendesain ulang/memformat slide eksisting secara in-place")
    edit_parser.add_argument("--file", required=True, help="Path berkas PPTX sumber")
    edit_parser.add_argument("--output", default=None, help="Path berkas PPTX output (opsional, default: overwrite file sumber)")
    edit_parser.add_argument("--slide", type=int, required=True, help="Nomor slide target (1-based)")
    edit_parser.add_argument("--slide-json", required=True, help="JSON string atau path file JSON spesifikasi slide")
    edit_parser.add_argument("--theme", default=None, help="Nama tema (opsional)")

    # Create
    create_parser = subparsers.add_parser("create", help="Membuat presentasi baru")
    create_parser.add_argument("--json", required=True, help="JSON string atau path file JSON spesifikasi presentasi")

    # Add Slide
    add_parser = subparsers.add_parser("add_slide", help="Menambahkan slide ke presentasi")
    add_parser.add_argument("--file", required=True, help="Path berkas PPTX sumber")
    add_parser.add_argument("--output", default=None, help="Path berkas PPTX output")
    add_parser.add_argument("--slide-json", required=True, help="JSON string atau path file JSON spesifikasi slide baru")
    add_parser.add_argument("--theme", default=None, help="Nama tema")

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
        elif args.action == "edit_slide":
            slide_raw = args.slide_json
            if os.path.exists(slide_raw):
                with open(slide_raw, "r", encoding="utf-8") as f:
                    slide_data = json.load(f)
            else:
                slide_data = json.loads(slide_raw)
            out_p = args.output if args.output else args.file
            result = cmd_edit_slide(args.file, out_p, args.slide, slide_data, args.theme)
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
