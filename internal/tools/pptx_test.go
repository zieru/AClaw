package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPPTXTool(t *testing.T) {
	// 1. Verifikasi deteksi python & script
	pyBin, err := resolvePython()
	if err != nil {
		t.Skipf("Python tidak tersedia di environment test: %v", err)
	}
	if pyBin == "" {
		t.Fatal("Python binary terdeteksi kosong")
	}

	scriptPath, err := findScriptPath()
	if err != nil {
		t.Fatalf("Script pptx_cli.py tidak ditemukan: %v", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("File script pptx_cli.py tidak ada: %v", err)
	}

	tool := &PPTXTool{}
	if tool.Name() != "pptx_tool" {
		t.Fatalf("Expected tool name 'pptx_tool', got '%s'", tool.Name())
	}

	ctx := context.Background()
	tempDir := t.TempDir()
	outPPTX := filepath.Join(tempDir, "test_generated.pptx")

	// 2. Test Create Presentation
	createArgs := map[string]interface{}{
		"action":      "create",
		"output_path": outPPTX,
		"theme":       "modern_blue",
		"presentation_data": map[string]interface{}{
			"title":    "Laporan Uji Coba GoAssistant",
			"subtitle": "Evaluasi Tool PPTX Otomatis",
			"slides": []interface{}{
				map[string]interface{}{
					"title": "Metrik Performa",
					"cards": []interface{}{
						map[string]interface{}{"title": "CPU Usage", "value": "12%", "desc": "Optimal"},
						map[string]interface{}{"title": "RAM Usage", "value": "28MB", "desc": "Ultra Ringan"},
					},
					"bullets": []interface{}{
						"Sistem berjalan stabil tanpa peningkatan memory leak.",
						"Semua fungsi terverifikasi dengan baik.",
					},
					"notes": "Speaker notes untuk slide metrik",
				},
				map[string]interface{}{
					"title": "Tabel Ringkasan Regional",
					"table": map[string]interface{}{
						"headers": []interface{}{"Region", "Cluster", "Status"},
						"rows": []interface{}{
							[]interface{}{"Sumbagut", "Medan", "Aktif"},
							[]interface{}{"Sumbagteng", "Pekanbaru", "Aktif"},
						},
					},
				},
			},
		},
	}

	resCreate, err := tool.Execute(ctx, createArgs)
	if err != nil {
		t.Fatalf("Execute create gagal: %v", err)
	}
	if !strings.Contains(resCreate, "[ATTACH_FILE:") {
		t.Fatalf("Respon create harus mengandung tag [ATTACH_FILE:], got: %s", resCreate)
	}
	if _, err := os.Stat(outPPTX); err != nil {
		t.Fatalf("File PPTX hasil create tidak ditemukan di disk: %v", err)
	}

	// 3. Test Inspect Presentation
	inspectArgs := map[string]interface{}{
		"action":    "inspect",
		"file_path": outPPTX,
	}
	resInspect, err := tool.Execute(ctx, inspectArgs)
	if err != nil {
		t.Fatalf("Execute inspect gagal: %v", err)
	}
	if !strings.Contains(resInspect, "Laporan Uji Coba GoAssistant") {
		t.Errorf("Respon inspect harus memuat judul presentasi, got: %s", resInspect)
	}
	if !strings.Contains(resInspect, "Metrik Performa") {
		t.Errorf("Respon inspect harus memuat judul slide 2, got: %s", resInspect)
	}

	// 4. Test Replace Text
	outReplaced := filepath.Join(tempDir, "test_replaced.pptx")
	replaceArgs := map[string]interface{}{
		"action":      "replace_text",
		"file_path":   outPPTX,
		"output_path": outReplaced,
		"replacements": map[string]interface{}{
			"Laporan Uji Coba GoAssistant": "Laporan Resmi GoAssistant 2026",
			"12%":                          "15%",
		},
	}
	resReplace, err := tool.Execute(ctx, replaceArgs)
	if err != nil {
		t.Fatalf("Execute replace_text gagal: %v", err)
	}
	if !strings.Contains(resReplace, "[ATTACH_FILE:") {
		t.Fatalf("Respon replace harus mengandung tag [ATTACH_FILE:], got: %s", resReplace)
	}

	// Verifikasi hasil replace melalui inspect file baru
	inspectReplacedArgs := map[string]interface{}{
		"action":    "inspect",
		"file_path": outReplaced,
	}
	resInspectReplaced, err := tool.Execute(ctx, inspectReplacedArgs)
	if err != nil {
		t.Fatalf("Execute inspect on replaced file gagal: %v", err)
	}
	if !strings.Contains(resInspectReplaced, "Laporan Resmi GoAssistant 2026") {
		t.Errorf("File hasil replace harus memuat teks baru, got: %s", resInspectReplaced)
	}

	// 5. Test Add Slide
	outAdded := filepath.Join(tempDir, "test_added.pptx")
	addSlideArgs := map[string]interface{}{
		"action":      "add_slide",
		"file_path":   outReplaced,
		"output_path": outAdded,
		"slide_data": map[string]interface{}{
			"title": "Kesimpulan Akhir",
			"bullets": []interface{}{
				"Implementasi berjalan 100% sukses.",
			},
		},
	}
	resAdd, err := tool.Execute(ctx, addSlideArgs)
	if err != nil {
		t.Fatalf("Execute add_slide gagal: %v", err)
	}
	if !strings.Contains(resAdd, "[ATTACH_FILE:") {
		t.Fatalf("Respon add_slide harus mengandung tag [ATTACH_FILE:], got: %s", resAdd)
	}

	// Verifikasi slide tambahan ada di inspect
	inspectAddedArgs := map[string]interface{}{
		"action":    "inspect",
		"file_path": outAdded,
	}
	resInspectAdded, err := tool.Execute(ctx, inspectAddedArgs)
	if err != nil {
		t.Fatalf("Execute inspect on added file gagal: %v", err)
	}
	if !strings.Contains(resInspectAdded, "Kesimpulan Akhir") {
		t.Errorf("File hasil add_slide harus memuat slide 'Kesimpulan Akhir', got: %s", resInspectAdded)
	}

	// 6. Test Edit Slide In-Place with Rich Styling (KPIs, Badges, Columns, Callout)
	outEdited := filepath.Join(tempDir, "test_edited.pptx")
	editSlideArgs := map[string]interface{}{
		"action":       "edit_slide",
		"file_path":    outAdded,
		"output_path":  outEdited,
		"slide_number": 2, // Re-style slide 2
		"slide_data": map[string]interface{}{
			"title":          "Executive Worklist Summary",
			"subtitle":       "Area Sumatera • Periode November 2026",
			"category_badge": "CONFIDENTIAL",
			"theme":          "telkomsel_red",
			"kpis": []interface{}{
				map[string]interface{}{"title": "Total Target", "value": "14.776", "badge": "100%", "badge_type": "neutral"},
				map[string]interface{}{"title": "Obsolete", "value": "13.854", "badge": "93.8%", "badge_type": "danger"},
				map[string]interface{}{"title": "Ready Success", "value": "11.205", "badge": "75.8%", "badge_type": "success"},
			},
			"columns": []interface{}{
				map[string]interface{}{
					"title": "Validasi Parameter",
					"items": []interface{}{
						"<b>NTE ACS:</b> Obsolete Version 13.854",
						"<b>Tiket Open:</b> 2.455 tiket berjalan",
					},
				},
				map[string]interface{}{
					"title":   "Gap Analysis",
					"callout": "Potensi sukses 75.8% (11.205 SSL)",
					"items": []interface{}{
						"Prioritaskan eksekusi batch 1",
						"Selesaikan tiket berjalan paralel",
					},
				},
			},
			"callout": map[string]interface{}{
				"title": "Rekomendasi Utama",
				"text":  "Segera distribusikan worklist tervalidasi ke tim lapangan.",
				"type":  "warning",
			},
			"notes": "Speaker notes slide 2 dipertahankan dan diperbarui.",
		},
	}
	resEdit, err := tool.Execute(ctx, editSlideArgs)
	if err != nil {
		t.Fatalf("Execute edit_slide gagal: %v", err)
	}
	if !strings.Contains(resEdit, "[ATTACH_FILE:") {
		t.Fatalf("Respon edit_slide harus mengandung tag [ATTACH_FILE:], got: %s", resEdit)
	}

	// Verifikasi slide yang diedit via inspect
	inspectEditedArgs := map[string]interface{}{
		"action":    "inspect",
		"file_path": outEdited,
	}
	resInspectEdited, err := tool.Execute(ctx, inspectEditedArgs)
	if err != nil {
		t.Fatalf("Execute inspect on edited file gagal: %v", err)
	}
	if !strings.Contains(resInspectEdited, "Executive Worklist Summary") {
		t.Errorf("File hasil edit_slide harus memuat 'Executive Worklist Summary', got: %s", resInspectEdited)
	}
	if !strings.Contains(resInspectEdited, "Speaker notes slide 2") {
		t.Errorf("File hasil edit_slide harus memuat speaker notes baru, got: %s", resInspectEdited)
	}
}
