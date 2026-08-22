package tools

import (
	"context"
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

// BrowserAutomationTool provides lightweight headless browser automation without heavy Selenium dependencies
type BrowserAutomationTool struct{}

func (b *BrowserAutomationTool) Name() string {
	return "browser_automate"
}

func (b *BrowserAutomationTool) Description() string {
	return "Membuka halaman web di headless browser (Chrome/Edge), mengekstrak teks hasil render JS, mengambil screenshot halaman (.png), atau mengekstrak HTML DOM."
}

func (b *BrowserAutomationTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"url": {
				Type:        "string",
				Description: "URL web yang ingin dikunjungi (contoh: https://news.ycombinator.com).",
			},
			"action": {
				Type:        "string",
				Description: "Aksi yang ingin dilakukan: 'read' (baca teks halaman render JS), 'screenshot' (ambil gambar tangkapan layar web), atau 'html' (ambil DOM HTML rendered). Default: read.",
				Enum:        []string{"read", "screenshot", "html"},
			},
			"wait_seconds": {
				Type:        "integer",
				Description: "Waktu tunggu render JavaScript dalam detik (1 - 10 detik). Default: 2.",
			},
		},
		Required: []string{"url"},
	}
}

func (b *BrowserAutomationTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	rawURL, ok := args["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("parameter 'url' wajib diisi")
	}

	targetURL := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	action := "read"
	if act, ok := args["action"].(string); ok && act != "" {
		action = strings.ToLower(act)
	}

	waitSeconds := 2
	if w, ok := args["wait_seconds"].(float64); ok && w > 0 {
		waitSeconds = int(w)
		if waitSeconds > 10 {
			waitSeconds = 10
		}
	}

	browserPath := findBrowserBinary()

	switch action {
	case "screenshot":
		if browserPath == "" {
			return "", fmt.Errorf("headless browser (Chrome/Edge/Chromium) tidak ditemukan di sistem host untuk mengambil screenshot")
		}
		return takeScreenshot(ctx, browserPath, targetURL, waitSeconds)

	case "html":
		if browserPath != "" {
			htmlContent, err := dumpRenderedHTML(ctx, browserPath, targetURL, waitSeconds)
			if err == nil && len(htmlContent) > 0 {
				if len(htmlContent) > 10000 {
					htmlContent = htmlContent[:10000] + "\n...[dipotong karena melebihi 10000 karakter]"
				}
				return htmlContent, nil
			}
		}
		// Fallback to HTTP GET
		return fetchHTTPContent(ctx, targetURL)

	default: // "read"
		if browserPath != "" {
			renderedHTML, err := dumpRenderedHTML(ctx, browserPath, targetURL, waitSeconds)
			if err == nil && len(renderedHTML) > 0 {
				cleanText := extractCleanText(renderedHTML)
				if len(cleanText) > 6000 {
					cleanText = cleanText[:6000] + "\n...[konten dipotong untuk ringkasan]"
				}
				return fmt.Sprintf("📄 <b>Konten Web (Rendered): %s</b>\n\n%s", targetURL, cleanText), nil
			}
		}

		// Fallback to HTTP GET + Text extraction
		rawBody, err := fetchHTTPContent(ctx, targetURL)
		if err != nil {
			return "", fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
		}
		cleanText := extractCleanText(rawBody)
		if len(cleanText) > 6000 {
			cleanText = cleanText[:6000] + "\n...[konten dipotong]"
		}
		return fmt.Sprintf("📄 <b>Konten Web: %s</b>\n\n%s", targetURL, cleanText), nil
	}
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
		// Also check user local AppData
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

	return fmt.Sprintf("📸 <b>Screenshot Berhasil Diambil!</b>\n• URL: <code>%s</code>\n• File: <code>%s</code>\n\n(File gambar tersimpan dan dapat dikirimkan ke chat via send_file)", targetURL, absOutPath), nil
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
