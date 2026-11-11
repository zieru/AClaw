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
		"Mendukung: 1) 'inspect' untuk membaca struktur & isi teks slide, 2) 'replace_text' untuk mengganti teks/placeholder " +
		"template seperti {{variabel}}, 3) 'create' untuk membuat presentasi baru secara dinamis, dan 4) 'add_slide' untuk " +
		"menambahkan slide baru. Berkas presentasi hasil olahan otomatis dilampirkan langsung ke chat pengguna."
}

func (t *PPTXTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"action": {
				Type:        "string",
				Description: "Aksi yang ingin dilakukan: 'inspect' (baca konten & tabel slide), 'replace_text' (ganti teks/placeholder), 'create' (buat presentasi baru), 'add_slide' (tambah slide ke file yang ada).",
				Enum:        []string{"inspect", "replace_text", "create", "add_slide"},
			},
			"file_path": {
				Type:        "string",
				Description: "Path ke berkas PPTX sumber di server (wajib untuk action 'inspect', 'replace_text', dan 'add_slide').",
			},
			"output_path": {
				Type:        "string",
				Description: "Path berkas PPTX output hasil olahan (opsional, jika tidak diisi otomatis disimpan di folder data/output/).",
			},
			"replacements": {
				Type:        "object",
				Description: "Pasangan key-value teks/placeholder yang akan diganti (untuk action 'replace_text'). Contoh: {'{{title}}': 'Laporan Q3', '{{cluster}}': 'Batam'}.",
			},
			"slide_number": {
				Type:        "integer",
				Description: "Nomor slide target (1-based) jika ingin membatasi operasi pada slide tertentu (opsional).",
			},
			"presentation_data": {
				Type:        "object",
				Description: "Struktur data presentasi lengkap (untuk action 'create'). Contoh: {'title': 'Judul', 'subtitle': 'Subjudul', 'theme': 'telkomsel_red', 'slides': [{'title': 'Slide 1', 'bullets': ['Poin 1', 'Poin 2']}]}.",
			},
			"slide_data": {
				Type:        "object",
				Description: "Struktur data slide baru (untuk action 'add_slide'). Contoh: {'title': 'Rencana Q4', 'bullets': ['Poin A', 'Poin B'], 'notes': 'Catatan'}.",
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

// findScriptPath mencari lokasi script pptx_cli.py di disk atau mengekstraknya otomatis dari binary ter-embed
func findScriptPath() (string, error) {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "scripts", "pptx_engine", "pptx_cli.py"),
		filepath.Join(cwd, "data", "pptx_engine", "pptx_cli.py"),
		filepath.Join(cwd, "..", "scripts", "pptx_engine", "pptx_cli.py"),
		filepath.Join(cwd, "..", "..", "scripts", "pptx_engine", "pptx_cli.py"),
		"scripts/pptx_engine/pptx_cli.py",
		"data/pptx_engine/pptx_cli.py",
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "scripts", "pptx_engine", "pptx_cli.py"),
			filepath.Join(exeDir, "data", "pptx_engine", "pptx_cli.py"),
			filepath.Join(exeDir, "..", "scripts", "pptx_engine", "pptx_cli.py"),
		)
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return filepath.Abs(c)
		}
	}

	// Jika file belum ada di disk (misal binary standalone di server), otomatis ekstrak dari binary ter-embed!
	extractDirs := []string{
		filepath.Join(cwd, "scripts", "pptx_engine"),
		filepath.Join(cwd, "data", "pptx_engine"),
		filepath.Join(os.TempDir(), "goassistant_pptx"),
	}

	for _, targetDir := range extractDirs {
		if err := pypptx.EnsureScriptsExtracted(targetDir); err == nil {
			cand := filepath.Join(targetDir, "pptx_cli.py")
			if info, err := os.Stat(cand); err == nil && !info.IsDir() {
				return filepath.Abs(cand)
			}
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
		cmdArgs = []string{scriptPath, "inspect", "--file", filePathVal}

	case "replace_text":
		filePathVal, ok := args["file_path"].(string)
		if !ok || strings.TrimSpace(filePathVal) == "" {
			return "", fmt.Errorf("parameter 'file_path' wajib diisi untuk action 'replace_text'")
		}
		replMap, ok := args["replacements"].(map[string]interface{})
		if !ok || len(replMap) == 0 {
			return "", fmt.Errorf("parameter 'replacements' (object key-value) wajib diisi untuk action 'replace_text'")
		}

		outPath, _ = args["output_path"].(string)
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
		if strings.TrimSpace(outPath) == "" {
			if pOut, ok := presData["output_path"].(string); ok && strings.TrimSpace(pOut) != "" {
				outPath = pOut
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
		slideData, ok := args["slide_data"].(map[string]interface{})
		if !ok || len(slideData) == 0 {
			return "", fmt.Errorf("parameter 'slide_data' wajib diisi untuk action 'add_slide'")
		}

		outPath, _ = args["output_path"].(string)
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

	default:
		return "", fmt.Errorf("action '%s' tidak didukung. Pilih: inspect, replace_text, create, atau add_slide", action)
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
	}

	return outputStr, nil
}
