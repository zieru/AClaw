package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

// BrowserAutomationTool provides complete browser automation via Chrome DevTools Protocol (go-rod)
// capable of rendering dynamic JS, clicking, typing, executing JS, and taking screenshots.
type BrowserAutomationTool struct{}

func (b *BrowserAutomationTool) Name() string {
	return "browser"
}

func (b *BrowserAutomationTool) Description() string {
	return "Browser otomatis (Chrome/Edge/Chromium) berbasis Chrome DevTools Protocol (CDP) untuk mengakses dan berinteraksi dengan website modern berbasis JavaScript (React, Vue, SPA). AI dapat membuka URL, membaca konten yang dirender JS, mengklik tombol, mengisi form input, mengeksekusi script JavaScript, atau mengambil screenshot gambar web."
}

func (b *BrowserAutomationTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"action": {
				Type:        "string",
				Description: "Aksi browser yang ingin dilakukan: 'open' (buka URL dan baca teks + elemen interaktif), 'click' (klik tombol/elemen), 'type' (ketik ke input form), 'eval_js' (eksekusi JavaScript di halaman), 'screenshot' (ambil tangkapan layar .png), 'scroll' (scroll halaman ke bawah). Default: open.",
				Enum:        []string{"open", "click", "type", "eval_js", "screenshot", "scroll"},
			},
			"url": {
				Type:        "string",
				Description: "URL halaman web yang ingin dikunjungi (wajib untuk aksi 'open', 'screenshot', dsb).",
			},
			"selector": {
				Type:        "string",
				Description: "CSS selector elemen target untuk aksi 'click' atau 'type' (contoh: '#search-box', 'button.submit', 'input[name=\"q\"]').",
			},
			"text": {
				Type:        "string",
				Description: "Teks yang ingin dimasukkan ke dalam form input untuk aksi 'type'.",
			},
			"script": {
				Type:        "string",
				Description: "Kode JavaScript yang ingin dieksekusi di console halaman untuk aksi 'eval_js'.",
			},
			"wait_seconds": {
				Type:        "integer",
				Description: "Durasi tunggu render JavaScript dalam detik (1 - 10 detik). Default: 3.",
			},
		},
		Required: []string{"url"},
	}
}

func (b *BrowserAutomationTool) Execute(ctx context.Context, args map[string]interface{}) (res string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("browser automation error: %v", r)
		}
	}()

	rawURL, _ := args["url"].(string)
	targetURL := strings.TrimSpace(rawURL)
	if targetURL != "" && !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	action := "open"
	if act, ok := args["action"].(string); ok && strings.TrimSpace(act) != "" {
		action = strings.ToLower(strings.TrimSpace(act))
	}

	waitSeconds := 3
	if w, ok := args["wait_seconds"].(float64); ok && w > 0 {
		waitSeconds = int(w)
		if waitSeconds > 10 {
			waitSeconds = 10
		}
	}

	selector, _ := args["selector"].(string)
	inputText, _ := args["text"].(string)
	customJS, _ := args["script"].(string)

	// Inisialisasi launcher dengan proteksi memory leak dan anti-zombie process
	l := launcher.New().
		Headless(true).
		NoSandbox(true).
		Leakless(true). // Mengaktifkan leakless supervisor untuk membunuh child process jika crash
		Set("disable-gpu").
		Set("disable-dev-shm-usage").
		Set("disable-software-rasterizer").
		Set("renderer-process-limit", "2").          // Batasi proses render child
		Set("js-flags", "--max-old-space-size=256"). // Batasi heap V8 JS max 256MB
		Set("disable-extensions").
		Set("disable-background-networking").
		Set("disable-sync").
		Set("mute-audio").
		Set("no-first-run").
		Set("no-default-browser-check").
		Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	defer l.Cleanup() // Membersihkan temporary user-data-dir agar disk tidak penuh

	controlURL, err := l.Launch()
	if err != nil {
		return "", fmt.Errorf("gagal menjalankan browser CDP: %w", err)
	}
	defer l.Kill() // Memastikan proses browser benar-benar mati saat eksekusi selesai

	browser := rod.New().ControlURL(controlURL).Context(ctx)
	if err := browser.Connect(); err != nil {
		return "", fmt.Errorf("gagal terhubung ke browser CDP: %w", err)
	}
	defer browser.Close()

	timeoutDur := time.Duration(waitSeconds+15) * time.Second
	page, err := stealth.Page(browser)
	if err != nil {
		page, err = browser.Page(proto.TargetCreateTarget{URL: ""})
		if err != nil {
			return "", fmt.Errorf("gagal membuat tab browser: %w", err)
		}
	}
	defer page.Close()

	page = page.Timeout(timeoutDur)

	switch action {
	case "screenshot":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi untuk aksi screenshot")
		}
		return rodTakeScreenshot(page, targetURL, waitSeconds)

	case "click":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		if selector == "" {
			return "", fmt.Errorf("parameter 'selector' wajib diisi untuk aksi click (contoh: '#btn-submit' atau 'button')")
		}
		return rodClickElement(page, targetURL, selector, waitSeconds)

	case "type":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		if selector == "" {
			return "", fmt.Errorf("parameter 'selector' wajib diisi untuk aksi type")
		}
		return rodTypeInput(page, targetURL, selector, inputText, waitSeconds)

	case "eval_js":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		if strings.TrimSpace(customJS) == "" {
			return "", fmt.Errorf("parameter 'script' wajib diisi untuk aksi eval_js")
		}
		return rodEvalJS(page, targetURL, customJS, waitSeconds)

	case "scroll":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		return rodScroll(page, targetURL, waitSeconds)

	default: // "open"
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		return rodOpenAndInspect(page, targetURL, waitSeconds)
	}
}

func rodOpenAndInspect(page *rod.Page, targetURL string, waitSeconds int) (string, error) {
	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}

	_ = page.WaitLoad()
	time.Sleep(time.Duration(waitSeconds) * time.Second)

	htmlContent, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("gagal membaca HTML dari halaman: %w", err)
	}

	cleanText := extractCleanText(htmlContent)
	if len(cleanText) > 4500 {
		cleanText = cleanText[:4500] + "\n...[konten dipotong untuk ringkasan]"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌐 <b>Browser Page: %s</b>\n\n", targetURL))
	sb.WriteString(fmt.Sprintf("📄 <b>Konten Rendered (JS Aktif - CDP):</b>\n%s\n\n", cleanText))

	// Inspeksi elemen-elemen interaktif
	extractScript := `() => {
		const interactive = [];
		document.querySelectorAll('button, a[href], input, textarea, select').forEach((el, idx) => {
			if (idx > 30) return;
			const text = (el.innerText || el.value || el.placeholder || el.getAttribute('aria-label') || '').trim();
			const tag = el.tagName.toLowerCase();
			let sel = el.id ? '#' + el.id : (el.name ? tag + '[name="' + el.name + '"]' : (el.className ? tag + '.' + el.className.trim().split(/\s+/).join('.') : tag));
			if (text) {
				interactive.push('[' + tag.toUpperCase() + '] "' + text.slice(0, 35) + '" (selector: ' + sel + ')');
			}
		});
		return interactive.slice(0, 15);
	}`

	res, errEval := page.Eval(extractScript)
	if errEval == nil && res != nil {
		var elements []string
		_ = json.Unmarshal([]byte(res.Value.String()), &elements)
		if len(elements) > 0 {
			sb.WriteString("🔘 <b>Elemen Interaktif Ditemukan (Bisa Diklik/Diisi):</b>\n")
			for _, el := range elements {
				sb.WriteString(fmt.Sprintf("• <code>%s</code>\n", el))
			}
		}
	}

	return sb.String(), nil
}

func rodClickElement(page *rod.Page, targetURL, selector string, waitSeconds int) (string, error) {
	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}

	_ = page.WaitLoad()
	time.Sleep(time.Duration(waitSeconds) * time.Second)

	el, err := page.Element(selector)
	if err != nil {
		return "", fmt.Errorf("elemen tidak ditemukan dengan selector '%s': %w", selector, err)
	}

	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return "", fmt.Errorf("gagal mengklik elemen '%s': %w", selector, err)
	}

	time.Sleep(1 * time.Second)
	pageTitle, _ := page.Eval("() => document.title")

	titleStr := ""
	if pageTitle != nil {
		titleStr = pageTitle.Value.Str()
	}

	return fmt.Sprintf("✅ <b>Berhasil mengklik elemen:</b> <code>%s</code>\n• URL: <code>%s</code>\n• Judul Halaman Sekarang: <i>%s</i>", selector, targetURL, titleStr), nil
}

func rodTypeInput(page *rod.Page, targetURL, selector, inputText string, waitSeconds int) (string, error) {
	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}

	_ = page.WaitLoad()
	time.Sleep(time.Duration(waitSeconds) * time.Second)

	el, err := page.Element(selector)
	if err != nil {
		return "", fmt.Errorf("elemen input tidak ditemukan dengan selector '%s': %w", selector, err)
	}

	if err := el.SelectAllText(); err == nil {
		_ = el.Input(inputText)
	} else {
		if err := el.Input(inputText); err != nil {
			return "", fmt.Errorf("gagal memasukkan teks ke input '%s': %w", selector, err)
		}
	}

	return fmt.Sprintf("✅ <b>Berhasil mengisi teks:</b> <i>\"%s\"</i> ke selector <code>%s</code>", inputText, selector), nil
}

func rodEvalJS(page *rod.Page, targetURL, script string, waitSeconds int) (string, error) {
	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}

	_ = page.WaitLoad()
	time.Sleep(time.Duration(waitSeconds) * time.Second)

	wrappedScript := fmt.Sprintf("() => { %s }", script)
	res, err := page.Eval(wrappedScript)
	if err != nil {
		// Coba evaluasi script secara langsung jika mode closure gagal
		res, err = page.Eval(script)
		if err != nil {
			return "", fmt.Errorf("eksekusi JavaScript gagal: %w", err)
		}
	}

	return fmt.Sprintf("⚡ <b>Hasil Eksekusi JavaScript:</b>\n<pre>%s</pre>", res.Value.String()), nil
}

func rodScroll(page *rod.Page, targetURL string, waitSeconds int) (string, error) {
	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}

	_ = page.WaitLoad()
	time.Sleep(time.Duration(waitSeconds) * time.Second)

	_, err := page.Eval("() => { window.scrollBy(0, window.innerHeight || 600); }")
	if err != nil {
		return "", fmt.Errorf("gagal melakukan scroll: %w", err)
	}

	return fmt.Sprintf("📜 <b>Berhasil scroll ke bawah pada:</b> <code>%s</code>", targetURL), nil
}

func rodTakeScreenshot(page *rod.Page, targetURL string, waitSeconds int) (string, error) {
	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}

	_ = page.WaitLoad()
	time.Sleep(time.Duration(waitSeconds) * time.Second)

	screenshotDir := filepath.Join("data", "screenshots")
	_ = os.MkdirAll(screenshotDir, 0755)

	// Otomatis bersihkan file screenshot lama (> 24 jam) agar tidak terjadi akumulasi file zombie
	cleanupOldScreenshots(screenshotDir, 24*time.Hour)

	outPath := filepath.Join(screenshotDir, fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano()))
	absOutPath, _ := filepath.Abs(outPath)

	imgBytes, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format: proto.PageCaptureScreenshotFormatPng,
	})
	if err != nil {
		return "", fmt.Errorf("gagal mengambil screenshot: %w", err)
	}

	if err := os.WriteFile(absOutPath, imgBytes, 0644); err != nil {
		return "", fmt.Errorf("gagal menyimpan file screenshot: %w", err)
	}

	return fmt.Sprintf("📸 <b>Screenshot Berhasil Diambil (CDP)!</b>\n• URL: <code>%s</code>\n• File: <code>%s</code>\n\n[ATTACH_FILE:%s|CAPTION:Screenshot %s]", targetURL, absOutPath, absOutPath, targetURL), nil
}

// cleanupOldScreenshots menghapus file screenshot lama yang melebihi maxAge
func cleanupOldScreenshots(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

var (
	reScript = regexp.MustCompile(`(?is)<script.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style.*?</style>`)
	reTags   = regexp.MustCompile(`<[^>]+>`)
	reSpaces = regexp.MustCompile(`[ \t]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)

func extractCleanText(htmlStr string) string {
	s := reScript.ReplaceAllString(htmlStr, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reTags.ReplaceAllString(s, " ")
	s = reSpaces.ReplaceAllString(s, " ")
	s = reLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
