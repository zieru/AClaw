package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// BrowserAutomationTool provides complete browser automation (OpenClaw style) capable of rendering dynamic JS, clicking, typing, executing JS, and taking screenshots.
type BrowserAutomationTool struct{}

func (b *BrowserAutomationTool) Name() string {
	return "browser"
}

func (b *BrowserAutomationTool) Description() string {
	return "Browser otomatis (Chrome/Edge) untuk mengakses dan berinteraksi dengan website modern berbasis JavaScript (React, Vue, SPA). AI dapat membuka URL, membaca konten yang dirender JS, mengklik tombol, mengisi form input, mengeksekusi script JavaScript, atau mengambil screenshot gambar web."
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

func (b *BrowserAutomationTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
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

	browserPath := findBrowserBinary()

	switch action {
	case "screenshot":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi untuk aksi screenshot")
		}
		if browserPath == "" {
			return "", fmt.Errorf("browser Chrome/Edge tidak ditemukan di host untuk mengambil screenshot")
		}
		return takeScreenshot(ctx, browserPath, targetURL, waitSeconds)

	case "click":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		if selector == "" {
			return "", fmt.Errorf("parameter 'selector' wajib diisi untuk aksi click (contoh: '#btn-submit' atau 'button')")
		}
		jsClick := fmt.Sprintf(`
			const el = document.querySelector('%s');
			if (el) {
				el.click();
				'Berhasil mengklik elemen: %s';
			} else {
				'Elemen tidak ditemukan dengan selector: %s';
			}
		`, escapeJS(selector), escapeJS(selector), escapeJS(selector))
		return executeBrowserJS(ctx, browserPath, targetURL, jsClick, waitSeconds)

	case "type":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		if selector == "" {
			return "", fmt.Errorf("parameter 'selector' wajib diisi untuk aksi type")
		}
		jsType := fmt.Sprintf(`
			const el = document.querySelector('%s');
			if (el) {
				el.focus();
				el.value = '%s';
				el.dispatchEvent(new Event('input', { bubbles: true }));
				el.dispatchEvent(new Event('change', { bubbles: true }));
				'Berhasil mengisi teks ke input %s';
			} else {
				'Elemen input tidak ditemukan: %s';
			}
		`, escapeJS(selector), escapeJS(inputText), escapeJS(selector), escapeJS(selector))
		return executeBrowserJS(ctx, browserPath, targetURL, jsType, waitSeconds)

	case "eval_js":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		if strings.TrimSpace(customJS) == "" {
			return "", fmt.Errorf("parameter 'script' wajib diisi untuk aksi eval_js")
		}
		return executeBrowserJS(ctx, browserPath, targetURL, customJS, waitSeconds)

	case "scroll":
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		jsScroll := `
			window.scrollTo(0, document.body.scrollHeight / 2);
			setTimeout(() => window.scrollTo(0, document.body.scrollHeight), 500);
			'Scroll selesai';
		`
		return executeBrowserJS(ctx, browserPath, targetURL, jsScroll, waitSeconds)

	default: // "open"
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi")
		}
		return openAndInspectPage(ctx, browserPath, targetURL, waitSeconds)
	}
}

func openAndInspectPage(ctx context.Context, browserPath, targetURL string, waitSeconds int) (string, error) {
	if browserPath != "" {
		extractScript := `
			(() => {
				const title = document.title || '';
				const metaDesc = document.querySelector('meta[name="description"]')?.content || '';
				
				const interactive = [];
				document.querySelectorAll('button, a[href], input, textarea, select').forEach((el, idx) => {
					if (idx > 25) return;
					const text = (el.innerText || el.value || el.placeholder || el.getAttribute('aria-label') || '').trim();
					const tag = el.tagName.toLowerCase();
					let sel = el.id ? '#' + el.id : (el.name ? tag + '[name="' + el.name + '"]' : tag);
					if (text) {
						interactive.push('[' + tag.toUpperCase() + '] "' + text.slice(0, 30) + '" (selector: ' + sel + ')');
					}
				});

				return JSON.stringify({
					title: title,
					description: metaDesc,
					interactive_elements: interactive.slice(0, 15)
				});
			})()
		`

		renderedHTML, err := dumpRenderedHTML(ctx, browserPath, targetURL, waitSeconds)
		if err == nil && len(renderedHTML) > 0 {
			cleanText := extractCleanText(renderedHTML)
			if len(cleanText) > 4500 {
				cleanText = cleanText[:4500] + "\n...[konten dipotong untuk ringkasan]"
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("🌐 <b>Browser Page: %s</b>\n\n", targetURL))
			sb.WriteString(fmt.Sprintf("📄 <b>Konten Rendered (JS Aktif):</b>\n%s\n\n", cleanText))

			infoJSON, errJS := executeBrowserJS(ctx, browserPath, targetURL, extractScript, 1)
			if errJS == nil && strings.Contains(infoJSON, "interactive_elements") {
				var info struct {
					Title       string   `json:"title"`
					Interactive []string `json:"interactive_elements"`
				}
				if json.Unmarshal([]byte(infoJSON), &info) == nil {
					if len(info.Interactive) > 0 {
						sb.WriteString("🔘 <b>Elemen Interaktif Ditemukan (Bisa Diklik/Diisi):</b>\n")
						for _, el := range info.Interactive {
							sb.WriteString(fmt.Sprintf("• <code>%s</code>\n", el))
						}
					}
				}
			}

			return sb.String(), nil
		}
	}

	rawBody, err := fetchHTTPContent(ctx, targetURL)
	if err != nil {
		return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
	}
	cleanText := extractCleanText(rawBody)
	if len(cleanText) > 5000 {
		cleanText = cleanText[:5000] + "\n...[konten dipotong]"
	}
	return fmt.Sprintf("🌐 <b>Browser Content: %s</b>\n\n%s", targetURL, cleanText), nil
}

func executeBrowserJS(ctx context.Context, browserPath, targetURL, jsCode string, waitSeconds int) (string, error) {
	if browserPath == "" {
		return "", fmt.Errorf("browser Chrome/Edge tidak ditemukan untuk mengeksekusi JavaScript")
	}

	tempHTMLDir := filepath.Join(os.TempDir(), "goassistant_browser")
	_ = os.MkdirAll(tempHTMLDir, 0755)

	scriptRunner := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body>
<iframe id="targetFrame" src="%s" style="width:1280px;height:800px;"></iframe>
<script>
window.addEventListener('load', () => {
	setTimeout(() => {
		try {
			const res = (() => { %s })();
			document.body.setAttribute('data-result', typeof res === 'object' ? JSON.stringify(res) : String(res));
		} catch(e) {
			document.body.setAttribute('data-result', 'Error: ' + e.message);
		}
	}, %d);
});
</script>
</body>
</html>`, targetURL, jsCode, waitSeconds*1000)

	runnerFile := filepath.Join(tempHTMLDir, fmt.Sprintf("run_%d.html", time.Now().UnixNano()))
	if err := os.WriteFile(runnerFile, []byte(scriptRunner), 0644); err != nil {
		return "", err
	}
	defer os.Remove(runnerFile)

	fileURL := "file:///" + filepath.ToSlash(runnerFile)
	rendered, err := dumpRenderedHTML(ctx, browserPath, fileURL, waitSeconds+2)
	if err != nil {
		return "", err
	}

	reResult := regexp.MustCompile(`data-result="([^"]*)"`)
	matches := reResult.FindStringSubmatch(rendered)
	if len(matches) > 1 {
		return matches[1], nil
	}

	return "Eksekusi JavaScript selesai.", nil
}

func escapeJS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func findBrowserBinary() string {
	var candidates []string

	if runtime.GOOS == "windows" {
		candidates = []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
		}
		if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
			candidates = append(candidates,
				filepath.Join(localApp, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(localApp, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(localApp, `BraveSoftware\Brave-Browser\Application\brave.exe`),
			)
		}
	} else {
		candidates = []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium-browser",
			"chromium",
			"microsoft-edge",
			"brave-browser",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
		}
	}

	for _, p := range candidates {
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		} else {
			if path, err := exec.LookPath(p); err == nil {
				return path
			}
		}
	}

	return ""
}

func dumpRenderedHTML(ctx context.Context, browserPath, targetURL string, waitSeconds int) (string, error) {
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds+10)*time.Second)
	defer cancel()

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		fmt.Sprintf("--virtual-time-budget=%d", waitSeconds*1000),
		"--dump-dom",
		targetURL,
	}

	cmd := exec.CommandContext(execCtx, browserPath, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func takeScreenshot(ctx context.Context, browserPath, targetURL string, waitSeconds int) (string, error) {
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds+15)*time.Second)
	defer cancel()

	screenshotDir := filepath.Join("data", "screenshots")
	_ = os.MkdirAll(screenshotDir, 0755)

	outPath := filepath.Join(screenshotDir, fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano()))
	absOutPath, _ := filepath.Abs(outPath)

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--window-size=1280,800",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		fmt.Sprintf("--virtual-time-budget=%d", waitSeconds*1000),
		fmt.Sprintf("--screenshot=%s", absOutPath),
		targetURL,
	}

	cmd := exec.CommandContext(execCtx, browserPath, args...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gagal mengambil screenshot: %w", err)
	}

	if _, err := os.Stat(absOutPath); err != nil {
		return "", fmt.Errorf("file screenshot tidak berhasil dibuat")
	}

	return fmt.Sprintf("📸 <b>Screenshot Berhasil Diambil!</b>\n• URL: <code>%s</code>\n• File: <code>%s</code>\n\n[ATTACH_FILE:%s|CAPTION:Screenshot %s]", targetURL, absOutPath, absOutPath, targetURL), nil
}

func fetchHTTPContent(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
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
