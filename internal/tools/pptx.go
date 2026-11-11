package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"goassistant/internal/tools/pypptx"
)

type PPTXTool struct{}

func (t *PPTXTool) Name() string {
	return "pptx_tool"
}

func (t *PPTXTool) Description() string {
	return "Tool internal untuk menginspeksi, mengedit, dan membuat berkas presentasi PowerPoint (.pptx). " +
		"Mendukung: 1) 'inspect' untuk membaca struktur, teks & tabel slide, 2) 'replace_text' untuk mengganti teks/placeholder, " +
		"3) 'create' untuk membuat presentasi baru dengan desain kaya (KPIs, multi-kolom, callout, tabel, elemen bebas), " +
		"4) 'add_slide' untuk menambahkan slide baru, dan 5) 'edit_slide' untuk mendesain ulang/merombak tata letak visual " +
		"slide tertentu pada berkas yang sudah ada secara in-place (misal: merapikan styling, kartu KPI ber-badge warna, " +
		"layout 2 kolom berdampingan, alert/callout box). Berkas presentasi hasil olahan otomatis dilampirkan langsung ke chat pengguna."
}

func (t *PPTXTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"action": {
				Type:        "string",
				Description: "Aksi yang ingin dilakukan: 'inspect' (baca konten slide & tabel), 'replace_text' (ganti teks/placeholder), 'create' (buat presentasi baru), 'add_slide' (tambah slide baru), 'edit_slide' (desain ulang / styling in-place slide tertentu).",
				Enum:        []string{"inspect", "replace_text", "create", "add_slide", "edit_slide"},
			},
			"file_path": {
				Type:        "string",
				Description: "Path ke berkas PPTX sumber di server (wajib untuk 'inspect', 'replace_text', 'add_slide', dan 'edit_slide'). Mendukung path absolut maupun tilde ('~/AClaw/...').",
			},
			"output_path": {
				Type:        "string",
				Description: "Path berkas PPTX output hasil olahan (opsional, untuk 'edit_slide' jika tidak diisi otomatis overwrite file sumber).",
			},
			"replacements": {
				Type:        "object",
				Description: "Pasangan key-value teks/placeholder yang akan diganti (untuk action 'replace_text'). Contoh: {'{{title}}': 'Laporan Q3', '{{cluster}}': 'Batam'}.",
			},
			"slide_number": {
				Type:        "integer",
				Description: "Nomor slide target (1-based). Wajib untuk action 'edit_slide', opsional untuk 'replace_text'.",
			},
			"presentation_data": {
				Type:        "object",
				Description: "Struktur data presentasi lengkap (untuk action 'create') berisi 'title', 'subtitle', 'theme', dan 'slides' (array slide_data kaya).",
			},
			"slide_data": {
				Type:        "object",
				Description: "Spesifikasi tata letak dan konten visual slide kaya (untuk 'add_slide' dan 'edit_slide'). Mendukung komponen: 'title', 'subtitle', 'category_badge', 'theme', 'kpis' (array kartu metrik dengan {title, value, badge, badge_type: 'success'|'danger'|'warning'|'neutral', desc, color}), 'columns' (array kolom berdampingan dengan {title, callout, items: ['<b>Bold:</b> info', ...]}), 'callout' ({title, text, type}), 'table' ({headers: [...], rows: [[...]]}), 'elements' (canvas bebas: [{type: 'card'|'textbox'|'table'|'line', x, y, w, h, fill, border, ...}]), dan 'notes' (catatan pembicara).",
			},
			"theme": {
				Type:        "string",
				Description: "Nama tema warna (opsional, default: 'modern_blue'). Pilihan: 'modern_blue', 'telkomsel_red', 'corporate_dark', 'minimal_clean'.",
				Enum:        []string{"modern_blue", "telkomsel_red", "corporate_dark", "minimal_clean"},
			},
		},
		Required: []string{"action"},
	}
}

var (
	cachedPythonBin string
	cachedPythonMu  sync.RWMutex
)

// checkPythonHasPPTX mengecek apakah binary python memiliki package pptx
func checkPythonHasPPTX(bin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-c", "import pptx")
	return cmd.Run() == nil
}

// resolvePython mencari path executable Python yang tersedia dan memiliki package python-pptx
func resolvePython() (string, error) {
	cachedPythonMu.RLock()
	if cachedPythonBin != "" {
		if _, err := os.Stat(cachedPythonBin); err == nil {
			cachedPythonMu.RUnlock()
			return cachedPythonBin, nil
		}
	}
	cachedPythonMu.RUnlock()

	cachedPythonMu.Lock()
	defer cachedPythonMu.Unlock()

	// Double check
	if cachedPythonBin != "" {
		if _, err := os.Stat(cachedPythonBin); err == nil {
			return cachedPythonBin, nil
		}
	}

	var fallbackPython string

	// 1. Cek environment variables khusus
	if p := os.Getenv("PPTX_PYTHON"); p != "" {
		if _, err := os.Stat(p); err == nil {
			if checkPythonHasPPTX(p) {
				cachedPythonBin = p
				return p, nil
			}
			fallbackPython = p
		}
	}
	if p := os.Getenv("PYTHON_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			if checkPythonHasPPTX(p) {
				cachedPythonBin = p
				return p, nil
			}
			if fallbackPython == "" {
				fallbackPython = p
			}
		}
	}

	// 2. Cek sistem PATH terlebih dahulu (python3, python)
	for _, sysName := range []string{"python3", "python"} {
		if p, err := exec.LookPath(sysName); err == nil {
			if fallbackPython == "" {
				fallbackPython = p
			}
			if checkPythonHasPPTX(p) {
				cachedPythonBin = p
				return p, nil
			}
		}
	}

	// 3. Kumpulkan kandidat virtualenv yang ada di workspace dan server home
	cwd, _ := os.Getwd()
	checkDirs := []string{cwd, filepath.Join(cwd, ".."), filepath.Join(cwd, "..", "..")}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if exeDir != cwd {
			checkDirs = append(checkDirs, exeDir)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		checkDirs = append(checkDirs, filepath.Join(home, "AClaw"), home)
	}

	venvNames := []string{
		"venv-pptx",
		"venv",
		".venv",
		filepath.Join("scripts", "pptx_engine", "venv-pptx"),
		filepath.Join("scripts", "pptx_engine", "venv"),
		filepath.Join("scripts", "pptx_engine", ".venv"),
		filepath.Join("scripts", "browser_use_mcp", "venv"),
		filepath.Join("scripts", "browser_use_mcp", ".venv"),
	}

	var candidates []string
	for _, base := range checkDirs {
		for _, vName := range venvNames {
			if runtime.GOOS == "windows" {
				candidates = append(candidates, filepath.Join(base, vName, "Scripts", "python.exe"))
			} else {
				candidates = append(candidates, filepath.Join(base, vName, "bin", "python"))
			}
		}
	}

	// Cek kandidat virtualenv yang memiliki library python-pptx
	for _, cand := range candidates {
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			if fallbackPython == "" {
				fallbackPython = cand
			}
			if checkPythonHasPPTX(cand) {
				cachedPythonBin = cand
				return cand, nil
			}
		}
	}

	if fallbackPython != "" {
		cachedPythonBin = fallbackPython
		return fallbackPython, nil
	}

	return "", fmt.Errorf("interpreter Python tidak ditemukan. Pastikan Python terinstall dan python-pptx tersedia")
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// findScriptPath mencari lokasi script pptx_cli.py di disk dan memastikan script ter-embed diekstrak/diperbarui
func findScriptPath() (string, error) {
	cwd, _ := os.Getwd()
	extractDirs := []string{
		filepath.Join(cwd, "scripts", "pptx_engine"),
		filepath.Join(cwd, "data", "pptx_engine"),
		filepath.Join(os.TempDir(), "goassistant_pptx"),
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if exeDir != cwd {
			extractDirs = append([]string{filepath.Join(exeDir, "scripts", "pptx_engine"), filepath.Join(exeDir, "data", "pptx_engine")}, extractDirs...)
		}
	}

	for _, targetDir := range extractDirs {
		if err := pypptx.EnsureScriptsExtracted(targetDir); err == nil {
			cand := filepath.Join(targetDir, "pptx_cli.py")
			if info, err := os.Stat(cand); err == nil && !info.IsDir() {
				return filepath.Abs(cand)
			}
		}
	}

	candidates := []string{
		filepath.Join(cwd, "scripts", "pptx_engine", "pptx_cli.py"),
		filepath.Join(cwd, "data", "pptx_engine", "pptx_cli.py"),
		filepath.Join(cwd, "..", "scripts", "pptx_engine", "pptx_cli.py"),
		filepath.Join(cwd, "..", "..", "scripts", "pptx_engine", "pptx_cli.py"),
		"scripts/pptx_engine/pptx_cli.py",
		"data/pptx_engine/pptx_cli.py",
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return filepath.Abs(c)
		}
	}

	return "", fmt.Errorf("script pptx_cli.py tidak ditemukan dan gagal diekstrak dari binary")
}

func (t *PPTXTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	actionVal, ok := args["action"]
	if !ok {
		return "", fmt.Errorf("parameter 'action' wajib diisi")
	}
	action, ok := actionVal.(string)
	if !ok || strings.TrimSpace(action) == "" {
		return "", fmt.Errorf("parameter 'action' tidak valid")
	}
	action = strings.TrimSpace(strings.ToLower(action))

	pythonBin, err := resolvePython()
	if err != nil {
		return "", err
	}

	scriptPath, err := findScriptPath()
	if err != nil {
		return "", err
	}

	// Buat folder kerja sementara untuk file input JSON
	tmpDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	var cmdArgs []string
	var outPath string

	switch action {
	case "inspect":
		filePathVal, ok := args["file_path"].(string)
		if !ok || strings.TrimSpace(filePathVal) == "" {
			return "", fmt.Errorf("parameter 'file_path' wajib diisi untuk action 'inspect'")
		}
		filePathVal = normalizePath(filePathVal)
		cmdArgs = []string{scriptPath, "inspect", "--file", filePathVal}

	case "replace_text":
		filePathVal, ok := args["file_path"].(string)
		if !ok || strings.TrimSpace(filePathVal) == "" {
			return "", fmt.Errorf("parameter 'file_path' wajib diisi untuk action 'replace_text'")
		}
		filePathVal = normalizePath(filePathVal)
		replMap, ok := args["replacements"].(map[string]interface{})
		if !ok || len(replMap) == 0 {
			return "", fmt.Errorf("parameter 'replacements' (object key-value) wajib diisi untuk action 'replace_text'")
		}

		outPath, _ = args["output_path"].(string)
		outPath = normalizePath(outPath)
		if strings.TrimSpace(outPath) == "" {
			_ = os.MkdirAll("data/output", 0755)
			baseName := filepath.Base(filePathVal)
			ext := filepath.Ext(baseName)
			nameOnly := strings.TrimSuffix(baseName, ext)
			outPath = filepath.Join("data", "output", fmt.Sprintf("%s_revisi_%d%s", nameOnly, time.Now().Unix(), ext))
		}

		// Simpan replacements ke file JSON sementara untuk menghindari limitasi quoting terminal
		replBytes, err := json.Marshal(replMap)
		if err != nil {
			return "", fmt.Errorf("gagal memproses parameter replacements: %w", err)
		}
		tmpReplFile := filepath.Join(tmpDir, fmt.Sprintf("repl_%d.json", time.Now().UnixNano()))
		if err := os.WriteFile(tmpReplFile, replBytes, 0644); err != nil {
			return "", fmt.Errorf("gagal menulis file konfigurasi penggantian teks: %w", err)
		}
		defer os.Remove(tmpReplFile)

		cmdArgs = []string{scriptPath, "replace_text", "--file", filePathVal, "--output", outPath, "--replacements", tmpReplFile}

		if slideNumVal, ok := args["slide_number"].(float64); ok && slideNumVal > 0 {
			cmdArgs = append(cmdArgs, "--slide", fmt.Sprintf("%d", int(slideNumVal)))
		}

	case "create":
		presData, ok := args["presentation_data"].(map[string]interface{})
		if !ok || len(presData) == 0 {
			return "", fmt.Errorf("parameter 'presentation_data' wajib diisi untuk action 'create'")
		}

		outPath, _ = args["output_path"].(string)
		outPath = normalizePath(outPath)
		if strings.TrimSpace(outPath) == "" {
			if pOut, ok := presData["output_path"].(string); ok && strings.TrimSpace(pOut) != "" {
				outPath = normalizePath(pOut)
			} else {
				_ = os.MkdirAll("data/output", 0755)
				outPath = filepath.Join("data", "output", fmt.Sprintf("presentasi_%d.pptx", time.Now().Unix()))
			}
		}
		presData["output_path"] = outPath

		if themeVal, ok := args["theme"].(string); ok && themeVal != "" {
			presData["theme"] = themeVal
		}

		presBytes, err := json.Marshal(presData)
		if err != nil {
			return "", fmt.Errorf("gagal memproses parameter presentation_data: %w", err)
		}
		tmpSpecFile := filepath.Join(tmpDir, fmt.Sprintf("pres_%d.json", time.Now().UnixNano()))
		if err := os.WriteFile(tmpSpecFile, presBytes, 0644); err != nil {
			return "", fmt.Errorf("gagal menulis file spesifikasi presentasi: %w", err)
		}
		defer os.Remove(tmpSpecFile)

		cmdArgs = []string{scriptPath, "create", "--json", tmpSpecFile}

	case "add_slide":
		filePathVal, ok := args["file_path"].(string)
		if !ok || strings.TrimSpace(filePathVal) == "" {
			return "", fmt.Errorf("parameter 'file_path' wajib diisi untuk action 'add_slide'")
		}
		filePathVal = normalizePath(filePathVal)
		slideData, ok := args["slide_data"].(map[string]interface{})
		if !ok || len(slideData) == 0 {
			return "", fmt.Errorf("parameter 'slide_data' wajib diisi untuk action 'add_slide'")
		}

		outPath, _ = args["output_path"].(string)
		outPath = normalizePath(outPath)
		if strings.TrimSpace(outPath) == "" {
			_ = os.MkdirAll("data/output", 0755)
			baseName := filepath.Base(filePathVal)
			ext := filepath.Ext(baseName)
			nameOnly := strings.TrimSuffix(baseName, ext)
			outPath = filepath.Join("data", "output", fmt.Sprintf("%s_updated_%d%s", nameOnly, time.Now().Unix(), ext))
		}

		slideBytes, err := json.Marshal(slideData)
		if err != nil {
			return "", fmt.Errorf("gagal memproses parameter slide_data: %w", err)
		}
		tmpSlideFile := filepath.Join(tmpDir, fmt.Sprintf("slide_%d.json", time.Now().UnixNano()))
		if err := os.WriteFile(tmpSlideFile, slideBytes, 0644); err != nil {
			return "", fmt.Errorf("gagal menulis file konfigurasi slide: %w", err)
		}
		defer os.Remove(tmpSlideFile)

		theme := "modern_blue"
		if themeVal, ok := args["theme"].(string); ok && themeVal != "" {
			theme = themeVal
		}

		cmdArgs = []string{scriptPath, "add_slide", "--file", filePathVal, "--output", outPath, "--slide-json", tmpSlideFile, "--theme", theme}

	case "edit_slide":
		filePathVal, ok := args["file_path"].(string)
		if !ok || strings.TrimSpace(filePathVal) == "" {
			return "", fmt.Errorf("parameter 'file_path' wajib diisi untuk action 'edit_slide'")
		}
		filePathVal = normalizePath(filePathVal)

		slideNum := 0
		if sNumFloat, ok := args["slide_number"].(float64); ok && sNumFloat > 0 {
			slideNum = int(sNumFloat)
		} else if sNumInt, ok := args["slide_number"].(int); ok && sNumInt > 0 {
			slideNum = sNumInt
		}
		if slideNum <= 0 {
			return "", fmt.Errorf("parameter 'slide_number' (integer >= 1) wajib diisi untuk action 'edit_slide'")
		}

		slideData, ok := args["slide_data"].(map[string]interface{})
		if !ok || len(slideData) == 0 {
			return "", fmt.Errorf("parameter 'slide_data' wajib diisi untuk action 'edit_slide'")
		}

		outPath, _ = args["output_path"].(string)
		outPath = normalizePath(outPath)
		if strings.TrimSpace(outPath) == "" {
			outPath = filePathVal // in-place overwrite file sumber secara default
		}

		slideBytes, err := json.Marshal(slideData)
		if err != nil {
			return "", fmt.Errorf("gagal memproses parameter slide_data: %w", err)
		}
		tmpSlideFile := filepath.Join(tmpDir, fmt.Sprintf("slide_edit_%d.json", time.Now().UnixNano()))
		if err := os.WriteFile(tmpSlideFile, slideBytes, 0644); err != nil {
			return "", fmt.Errorf("gagal menulis file konfigurasi edit slide: %w", err)
		}
		defer os.Remove(tmpSlideFile)

		cmdArgs = []string{scriptPath, "edit_slide", "--file", filePathVal, "--slide", fmt.Sprintf("%d", slideNum), "--slide-json", tmpSlideFile, "--output", outPath}
		if themeVal, ok := args["theme"].(string); ok && themeVal != "" {
			cmdArgs = append(cmdArgs, "--theme", themeVal)
		}

	default:
		return "", fmt.Errorf("action '%s' tidak didukung. Pilih: inspect, replace_text, create, add_slide, atau edit_slide", action)
	}

	// Eksekusi script Python
	execCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, pythonBin, cmdArgs...)
	out, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(out))

	if err != nil {
		if strings.Contains(outputStr, "No module named 'pptx'") || strings.Contains(outputStr, "No module named pptx") {
			return fmt.Sprintf("❌ Modul Python 'python-pptx' belum terinstall di server (%s).\nSilakan jalankan di terminal server: pip install python-pptx", pythonBin), nil
		}
		return fmt.Sprintf("❌ Gagal mengeksekusi PPTX engine (action: %s): %v\nOutput: %s", action, err, outputStr), nil
	}

	// Parse JSON response dari Python
	var res map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(outputStr), &res); jsonErr != nil {
		return fmt.Sprintf("⚠️ Hasil eksekusi PPTX engine:\n%s", outputStr), nil
	}

	if success, _ := res["success"].(bool); !success {
		errMsg, _ := res["error"].(string)
		if errMsg == "" {
			errMsg = outputStr
		}
		return fmt.Sprintf("❌ PPTX Engine Error: %s", errMsg), nil
	}

	// Format respon ramah AI dan sertakan [ATTACH_FILE:...] bila menghasilkan file baru
	switch action {
	case "inspect":
		slides, _ := res["slides"].([]interface{})
		slideCount := len(slides)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📊 **Inspeksi Berkas PPTX: %s** (Total: %d slide)\n\n", filepath.Base(args["file_path"].(string)), slideCount))

		for _, s := range slides {
			sMap, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			num := int(sMap["slide_number"].(float64))
			title, _ := sMap["title"].(string)
			if title == "" {
				title = "(Tanpa Judul)"
			}
			sb.WriteString(fmt.Sprintf("• **Slide %d**: %s\n", num, title))

			if texts, ok := sMap["texts"].([]interface{}); ok && len(texts) > 0 {
				for _, t := range texts {
					tStr, _ := t.(string)
					tStr = strings.TrimSpace(tStr)
					if tStr != "" && tStr != title {
						// Tampilkan potongan teks singkat
						lines := strings.Split(tStr, "\n")
						for _, line := range lines {
							line = strings.TrimSpace(line)
							if line != "" {
								sb.WriteString(fmt.Sprintf("   - %s\n", line))
							}
						}
					}
				}
			}

			if tables, ok := sMap["tables"].([]interface{}); ok && len(tables) > 0 {
				sb.WriteString(fmt.Sprintf("   [Terdapat %d tabel]\n", len(tables)))
			}

			if notes, ok := sMap["notes"].(string); ok && notes != "" {
				sb.WriteString(fmt.Sprintf("   _Notes: %s_\n", notes))
			}
		}
		return sb.String(), nil

	case "replace_text":
		caption := "Presentasi Hasil Revisi Teks/Template"
		return fmt.Sprintf("[ATTACH_FILE:%s|CAPTION:%s] ✅ Berhasil mengganti teks/placeholder di berkas presentasi. File hasil tersimpan di: %s", outPath, caption, outPath), nil

	case "create":
		slideCount := 0
		if sc, ok := res["slide_count"].(float64); ok {
			slideCount = int(sc)
		}
		caption := fmt.Sprintf("Presentasi Baru (%d Slide)", slideCount)
		return fmt.Sprintf("[ATTACH_FILE:%s|CAPTION:%s] ✅ Presentasi PowerPoint baru berhasil dibuat dengan %d slide! Berkas tersimpan di: %s", outPath, caption, slideCount, outPath), nil

	case "add_slide":
		totalSlides := 0
		if ts, ok := res["total_slides"].(float64); ok {
			totalSlides = int(ts)
		}
		caption := "Presentasi dengan Slide Tambahan Baru"
		return fmt.Sprintf("[ATTACH_FILE:%s|CAPTION:%s] ✅ Slide baru berhasil ditambahkan! Total slide sekarang: %d. Berkas tersimpan di: %s", outPath, caption, totalSlides, outPath), nil

	case "edit_slide":
		slideNumStr := fmt.Sprintf("%v", args["slide_number"])
		caption := fmt.Sprintf("Presentasi Hasil Restyling Slide %s", slideNumStr)
		return fmt.Sprintf("[ATTACH_FILE:%s|CAPTION:%s] ✅ Slide %s pada berkas presentasi berhasil di-styling ulang secara in-place dengan kualitas eksekutif! Berkas tersimpan di: %s", outPath, caption, slideNumStr, outPath), nil
	}

	return outputStr, nil
}
