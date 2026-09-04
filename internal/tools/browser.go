package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// BrowserAutomationTool provides complete browser automation inspired by browser-use
// via Chrome DevTools Protocol (go-rod) with numeric element indexing (Set-of-Marks),
// robust React/Vue event handling, and deterministic action execution.
type BrowserAutomationTool struct{}

func (b *BrowserAutomationTool) Name() string {
	return "browser"
}

func (b *BrowserAutomationTool) Description() string {
	return "Browser otomatis (Chrome/Edge/Docker Zenika) berbasis arsitektur browser-use & Chrome DevTools Protocol (CDP). Mampu merender website modern berbasis JavaScript (React, Vue, SPA). AI dapat membuka URL, membaca teks bersih, melihat elemen interaktif berindeks numerik [0..N], mengklik elemen via index, mengisi form input via index dengan dukungan event React, menekan tombol keyboard (Enter/Escape/Tab), scroll, atau mengambil screenshot berlabel Set-of-Marks (SoM)."
}

func (b *BrowserAutomationTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"action": {
				Type:        "string",
				Description: "Aksi browser yang ingin dilakukan: 'open' (buka URL dan bangun pohon DOM interaktif berindeks [0..N]), 'click' (klik elemen berdasarkan nomor index atau selector), 'type' (isi teks ke form input berdasarkan nomor index atau selector), 'press_key' (tekan tombol keyboard seperti Enter/Escape/Tab), 'scroll' (scroll halaman ke bawah/atas atau ke elemen index tertentu), 'screenshot' (ambil tangkapan layar .png dengan opsi Set-of-Marks berlabel angka), 'eval_js' (eksekusi JavaScript di halaman). Default: open.",
				Enum:        []string{"open", "click", "type", "press_key", "scroll", "screenshot", "eval_js"},
			},
			"url": {
				Type:        "string",
				Description: "URL halaman web yang ingin dikunjungi (wajib untuk aksi 'open', opsional untuk aksi interaksi lanjutan jika tab aktif sudah berada di halaman web).",
			},
			"index": {
				Type:        "integer",
				Description: "Nomor index elemen interaktif target dari pohon DOM hasil 'open' atau interaksi sebelumnya (contoh: 0, 1, 2). Sangat direkomendasikan dibanding selector biasa karena memiliki akurasi 100% dan bebas halusinasi.",
			},
			"selector": {
				Type:        "string",
				Description: "Fallback jika tidak menggunakan index: Target elemen untuk aksi 'click' atau 'type'. Bisa berupa teks tombol/label tampilan, CSS selector, atau XPath.",
			},
			"text": {
				Type:        "string",
				Description: "Teks yang ingin dimasukkan ke dalam form input untuk aksi 'type'.",
			},
			"clear": {
				Type:        "boolean",
				Description: "Hapus teks yang sudah ada di dalam input sebelum mengetikkan teks baru pada aksi 'type' (default: true).",
			},
			"press_enter": {
				Type:        "boolean",
				Description: "Otomatis tekan tombol Enter setelah mengetikkan teks pada aksi 'type' (default: false, sangat berguna untuk search bar).",
			},
			"key": {
				Type:        "string",
				Description: "Nama tombol keyboard untuk aksi 'press_key' (contoh: 'Enter', 'Escape', 'Tab', 'Backspace', 'ArrowDown', 'ArrowUp').",
			},
			"direction": {
				Type:        "string",
				Description: "Arah scroll untuk aksi 'scroll': 'down' (default) atau 'up'.",
				Enum:        []string{"down", "up"},
			},
			"som": {
				Type:        "boolean",
				Description: "Tampilkan badge Set-of-Marks (SoM: kotak highlight & nomor index berwarna di atas elemen) pada aksi 'screenshot' (default: true).",
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
		Required: []string{"action"},
	}
}

type browserSession struct {
	mu         sync.Mutex
	browser    *rod.Browser
	launcher   *launcher.Launcher
	page       *rod.Page
	currentURL string
	lastUsed   time.Time
}

var globalBrowserSession = &browserSession{}

func (s *browserSession) GetPage(ctx context.Context, targetURL string, waitSeconds int, forceReload bool) (*rod.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastUsed = time.Now()

	// 1. Inisialisasi browser jika belum ada
	if s.browser == nil {
		if dockerURL, dockerErr := ensureDockerChrome(ctx); dockerErr == nil && dockerURL != "" {
			bInst := rod.New().ControlURL(dockerURL).Context(context.Background())
			if errConn := bInst.Connect(); errConn == nil {
				s.browser = bInst
			}
		}

		if s.browser == nil {
			l := launcher.New().
				Headless(true).
				NoSandbox(true).
				Leakless(true).
				Set("disable-gpu").
				Set("disable-dev-shm-usage").
				Set("disable-software-rasterizer").
				Set("renderer-process-limit", "2").
				Set("js-flags", "--max-old-space-size=256").
				Set("disable-extensions").
				Set("disable-background-networking").
				Set("disable-sync").
				Set("mute-audio").
				Set("no-first-run").
				Set("no-default-browser-check")

			if bin := findLocalBrowserBinary(); bin != "" {
				l.Bin(bin)
			}

			controlURL, errLaunch := l.Launch()
			if errLaunch != nil {
				return nil, fmt.Errorf("gagal menjalankan browser: %w", errLaunch)
			}
			s.launcher = l

			bInst := rod.New().ControlURL(controlURL).Context(context.Background())
			if errConn := bInst.Connect(); errConn != nil {
				l.Kill()
				l.Cleanup()
				s.launcher = nil
				return nil, fmt.Errorf("gagal terhubung ke browser: %w", errConn)
			}
			s.browser = bInst
		}
	}

	// 2. Cek apakah page masih responsif
	if s.page != nil {
		_, errCheck := s.page.Eval("() => 1")
		if errCheck != nil {
			_ = s.page.Close()
			s.page = nil
			s.currentURL = ""
		}
	}

	// Buat page baru jika belum ada
	if s.page == nil {
		p, err := s.browser.Page(proto.TargetCreateTarget{URL: ""})
		if err != nil {
			return nil, fmt.Errorf("gagal membuat tab browser: %w", err)
		}
		// Viewport desktop standar 1280x800 agar web modern/SPA tidak collapse ke mobile burger
		_ = p.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
			Width:             1280,
			Height:            800,
			DeviceScaleFactor: 1,
			Mobile:            false,
		})
		s.page = p
		s.currentURL = ""
	}

	timeoutDur := time.Duration(waitSeconds+15) * time.Second
	pTimeout := s.page.Timeout(timeoutDur)

	// 3. Smart Navigation
	needNavigate := false
	if targetURL != "" {
		if s.currentURL == "" || forceReload {
			needNavigate = true
		} else if !strings.EqualFold(s.currentURL, targetURL) {
			needNavigate = true
		}
	}

	if needNavigate {
		if err := pTimeout.Navigate(targetURL); err != nil {
			return nil, fmt.Errorf("gagal membuka URL %s: %w", targetURL, err)
		}
		_ = pTimeout.WaitLoad()
		time.Sleep(time.Duration(waitSeconds) * time.Second)
		s.currentURL = targetURL
	}

	return pTimeout, nil
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

	if action == "open" {
		if cached, hit := GetGlobalToolCache().Get(b.Name(), args); hit {
			return cached, nil
		}
	}

	waitSeconds := 3
	if w, ok := args["wait_seconds"].(float64); ok && w > 0 {
		waitSeconds = int(w)
		if waitSeconds > 10 {
			waitSeconds = 10
		}
	}

	// Parsing parameter index (browser-use pattern)
	var indexPtr *int
	if idxVal, ok := args["index"]; ok && idxVal != nil {
		switch v := idxVal.(type) {
		case float64:
			i := int(v)
			indexPtr = &i
		case int:
			indexPtr = &v
		}
	}

	selector, _ := args["selector"].(string)
	inputText, _ := args["text"].(string)
	customJS, _ := args["script"].(string)
	keyName, _ := args["key"].(string)
	direction, _ := args["direction"].(string)

	clear := true
	if clr, ok := args["clear"].(bool); ok {
		clear = clr
	}

	pressEnter := false
	if pe, ok := args["press_enter"].(bool); ok {
		pressEnter = pe
	}

	withSoM := true
	if somVal, ok := args["som"].(bool); ok {
		withSoM = somVal
	}

	// Untuk aksi 'open', lakukan navigasi atau reload jika diminta URL baru.
	// Untuk aksi interaksi, gunakan tab aktif jika URL sama atau kosong
	forceReload := (action == "open")

	page, err := globalBrowserSession.GetPage(ctx, targetURL, waitSeconds, forceReload)
	if err != nil {
		return "", err
	}

	activeURL := targetURL
	if activeURL == "" {
		if info, errInfo := page.Info(); errInfo == nil && info != nil {
			activeURL = info.URL
		}
	}

	switch action {
	case "screenshot":
		return rodTakeScreenshot(page, activeURL, withSoM, waitSeconds)

	case "click":
		if indexPtr == nil && strings.TrimSpace(selector) == "" {
			return "", fmt.Errorf("parameter 'index' (nomor elemen, contoh: 0, 1, 2) atau 'selector' wajib diisi untuk aksi click")
		}
		return rodClickElement(page, indexPtr, selector, waitSeconds)

	case "type":
		if indexPtr == nil && strings.TrimSpace(selector) == "" {
			return "", fmt.Errorf("parameter 'index' (nomor elemen input, contoh: 0, 1, 2) atau 'selector' wajib diisi untuk aksi type")
		}
		return rodTypeInput(page, indexPtr, selector, inputText, clear, pressEnter, waitSeconds)

	case "press_key":
		return rodPressKey(page, keyName, waitSeconds)

	case "scroll":
		return rodScroll(page, indexPtr, direction, waitSeconds)

	case "eval_js":
		if strings.TrimSpace(customJS) == "" {
			return "", fmt.Errorf("parameter 'script' wajib diisi untuk aksi eval_js")
		}
		return rodEvalJS(page, activeURL, customJS, waitSeconds)

	default: // "open"
		if targetURL == "" {
			return "", fmt.Errorf("parameter 'url' wajib diisi untuk aksi open")
		}
		res, err := rodOpenAndInspect(page, targetURL, waitSeconds)
		if err == nil && strings.TrimSpace(res) != "" {
			GetGlobalToolCache().Set(b.Name(), args, res, 15*time.Minute)
		}
		return res, err
	}
}

// ensureDockerChrome memeriksa apakah container Docker zenika/alpine-chrome aktif, atau otomatis menyalakannya
func ensureDockerChrome(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 1 * time.Second}

	// 1. Cek jika port CDP 9222 sudah siap & aktif
	if resp, err := client.Get("http://127.0.0.1:9222/json/version"); err == nil && resp.StatusCode == 200 {
		_ = resp.Body.Close()
		if u, err := launcher.ResolveURL("http://127.0.0.1:9222"); err == nil && u != "" {
			return u, nil
		}
	}

	// 2. Cek apakah CLI docker terinstall di sistem host
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker CLI tidak ditemukan di host: %w", err)
	}

	// 3. Inspeksi status container goassistant-chrome
	checkCtx, checkCancel := context.WithTimeout(ctx, 4*time.Second)
	defer checkCancel()

	out, _ := exec.CommandContext(checkCtx, "docker", "ps", "-a", "--filter", "name=goassistant-chrome", "--format", "{{.Names}}#{{.Status}}").Output()
	rawStatus := strings.TrimSpace(string(out))

	if !strings.Contains(rawStatus, "goassistant-chrome") {
		// Container belum ada -> buat dan jalankan container secara otomatis
		runCtx, runCancel := context.WithTimeout(ctx, 45*time.Second)
		defer runCancel()

		runCmd := exec.CommandContext(runCtx, "docker", "run", "-d",
			"--name", "goassistant-chrome",
			"-p", "127.0.0.1:9222:9222",
			"--restart=unless-stopped",
			"--shm-size=256m",
			"--memory=512m",
			"zenika/alpine-chrome",
			"--no-sandbox",
			"--remote-debugging-address=0.0.0.0",
			"--remote-debugging-port=9222",
		)
		if err := runCmd.Run(); err != nil {
			return "", fmt.Errorf("gagal meluncurkan docker container zenika/alpine-chrome: %w", err)
		}
	} else if !strings.Contains(rawStatus, "Up") {
		// Container sudah ada namun mati -> start container
		startCtx, startCancel := context.WithTimeout(ctx, 6*time.Second)
		defer startCancel()
		_ = exec.CommandContext(startCtx, "docker", "start", "goassistant-chrome").Run()
	}

	// 4. Polling hingga endpoint CDP merespon (max 6 detik)
	for i := 0; i < 12; i++ {
		time.Sleep(500 * time.Millisecond)
		if resp, err := client.Get("http://127.0.0.1:9222/json/version"); err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			if u, err := launcher.ResolveURL("http://127.0.0.1:9222"); err == nil && u != "" {
				return u, nil
			}
		}
	}

	return "", fmt.Errorf("timeout menunggu Docker Chrome CDP siap di 127.0.0.1:9222")
}

// buildDomTreeScript menginjeksi engine DOM parsing ala browser-use untuk mendeteksi
// seluruh elemen interaktif visible, memberi data-ga-index, dan menyimpannya di window.__ga_elements
const buildDomTreeScript = `() => {
	window.__ga_elements = [];
	document.querySelectorAll('[data-ga-index]').forEach(el => el.removeAttribute('data-ga-index'));

	function isVisible(el) {
		if (!el || el.nodeType !== Node.ELEMENT_NODE) return false;
		const rect = el.getBoundingClientRect();
		if (rect.width < 3 || rect.height < 3) return false;
		const style = window.getComputedStyle(el);
		if (style.display === 'none' || style.visibility === 'hidden' || parseFloat(style.opacity) < 0.05) return false;
		const vw = window.innerWidth || document.documentElement.clientWidth;
		const vh = window.innerHeight || document.documentElement.clientHeight;
		if (rect.bottom < -200 || rect.top > vh + 800 || rect.right < -200 || rect.left > vw + 200) return false;
		return true;
	}

	function isInteractive(el) {
		if (!isVisible(el)) return false;
		const tag = el.tagName.toLowerCase();
		if (['button', 'select', 'textarea', 'option'].includes(tag)) return true;
		if (tag === 'input') {
			const type = (el.type || 'text').toLowerCase();
			return type !== 'hidden';
		}
		if (tag === 'a' && (el.hasAttribute('href') || el.hasAttribute('onclick') || el.getAttribute('role') === 'button')) return true;

		const role = (el.getAttribute('role') || '').toLowerCase();
		const interactiveRoles = ['button', 'link', 'checkbox', 'radio', 'combobox', 'menuitem', 'tab', 'switch', 'searchbox', 'option'];
		if (interactiveRoles.includes(role)) return true;

		if (el.hasAttribute('onclick') || el.hasAttribute('jsaction')) return true;
		if (el.hasAttribute('contenteditable') && el.getAttribute('contenteditable') !== 'false') return true;
		if (el.getAttribute('tabindex') === '0') return true;
		if (el.hasAttribute('aria-haspopup') || el.hasAttribute('aria-expanded')) return true;

		const style = window.getComputedStyle(el);
		if (style.cursor === 'pointer') {
			if (el.children.length === 1 && isInteractive(el.children[0])) return false;
			return true;
		}
		return false;
	}

	const all = Array.from(document.querySelectorAll('*'));
	const interactiveList = [];
	let idx = 0;

	for (const el of all) {
		if (interactiveList.length >= 60) break;
		if (!isInteractive(el)) continue;

		if (el.children.length === 1 && isInteractive(el.children[0])) continue;

		el.setAttribute('data-ga-index', String(idx));
		window.__ga_elements[idx] = el;

		const tag = el.tagName.toLowerCase();
		const role = el.getAttribute('role') || '';
		const type = el.getAttribute('type') || '';
		const placeholder = el.getAttribute('placeholder') || '';
		const ariaLabel = el.getAttribute('aria-label') || el.getAttribute('title') || el.getAttribute('alt') || '';
		const value = el.value !== undefined && tag === 'input' ? el.value : '';
		const href = el.getAttribute('href') || '';
		const checked = el.checked ? ' checked' : '';
		const expanded = el.getAttribute('aria-expanded');

		let text = (el.innerText || el.textContent || '').trim();
		text = text.replace(/\s+/g, ' ').slice(0, 45);

		let desc = '<' + tag;
		if (type) desc += ' type="' + type + '"';
		if (role) desc += ' role="' + role + '"';
		if (placeholder) desc += ' placeholder="' + placeholder + '"';
		if (ariaLabel) desc += ' aria-label="' + ariaLabel + '"';
		if (value) desc += ' value="' + value.slice(0, 30) + '"';
		if (href && href !== '#' && !href.startsWith('javascript:')) desc += ' href="' + href.slice(0, 35) + '"';
		if (checked) desc += checked;
		if (expanded !== null) desc += ' aria-expanded="' + expanded + '"';
		desc += '>';
		if (text && tag !== 'input') desc += ' ' + text + ' </' + tag + '>';

		interactiveList.push({
			index: idx,
			desc: desc
		});

		idx++;
	}

	return interactiveList;
}`

type domElementInfo struct {
	Index int    `json:"index"`
	Desc  string `json:"desc"`
}

// buildIndexedDOM membangun snapshot elemen interaktif dan mengembalikan teks format ringkas untuk LLM
func buildIndexedDOM(page *rod.Page) (string, error) {
	res, err := page.Eval(buildDomTreeScript)
	if err != nil || res == nil {
		return "", err
	}

	var elements []domElementInfo
	if errJSON := json.Unmarshal([]byte(res.Value.String()), &elements); errJSON != nil {
		return "", errJSON
	}

	if len(elements) == 0 {
		return "🔘 <i>(Tidak ditemukan elemen interaktif tambahan pada viewport aktif)</i>\n", nil
	}

	var sb strings.Builder
	sb.WriteString("🔘 <b>Elemen Interaktif Ditemukan (Gunakan parameter <code>index</code> untuk klik/type):</b>\n")
	for _, el := range elements {
		sb.WriteString(fmt.Sprintf("[%d] <code>%s</code>\n", el.Index, el.Desc))
	}
	return sb.String(), nil
}

func rodOpenAndInspect(page *rod.Page, targetURL string, waitSeconds int) (string, error) {
	htmlContent, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("gagal membaca HTML dari halaman: %w", err)
	}

	cleanText := extractCleanText(htmlContent)
	if len(cleanText) > 4000 {
		cleanText = cleanText[:4000] + "\n...[konten dipotong untuk efisiensi token]"
	}

	indexedDOM, _ := buildIndexedDOM(page)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌐 <b>Browser Page: %s</b>\n\n", targetURL))
	sb.WriteString(fmt.Sprintf("📄 <b>Konten Teks Terbaca:</b>\n%s\n\n", cleanText))
	if indexedDOM != "" {
		sb.WriteString(indexedDOM)
	}

	return sb.String(), nil
}

// findElement mencari elemen menggunakan Index (browser-use pattern), XPath, CSS, Teks tampilan, atau Regex
func findElement(page *rod.Page, indexPtr *int, selector string) (*rod.Element, error) {
	// 1. Prioritas Utama: Index Numerik (browser-use pattern)
	if indexPtr != nil && *indexPtr >= 0 {
		idx := *indexPtr
		// Cek apakah data-ga-index ada di DOM
		if el, err := page.Element(fmt.Sprintf(`[data-ga-index="%d"]`, idx)); err == nil && el != nil {
			_ = el.ScrollIntoView()
			return el, nil
		}

		// Fallback: Re-index DOM sekali lagi jika DOM sempat mengalami transisi/mutasi
		_, _ = page.Eval(buildDomTreeScript)
		if el, err := page.Element(fmt.Sprintf(`[data-ga-index="%d"]`, idx)); err == nil && el != nil {
			_ = el.ScrollIntoView()
			return el, nil
		}

		return nil, fmt.Errorf("elemen dengan index [%d] tidak ditemukan pada DOM aktif. Silakan panggil action 'open' untuk memperbarui pohon elemen", idx)
	}

	// 2. Fallback: Selector atau Teks manual
	target := strings.TrimSpace(selector)
	if target == "" {
		return nil, fmt.Errorf("parameter 'index' (angka) atau 'selector' wajib diisi")
	}

	// XPath
	if strings.HasPrefix(target, "//") || strings.HasPrefix(target, "xpath:") {
		xpath := strings.TrimPrefix(target, "xpath:")
		if el, err := page.ElementX(xpath); err == nil && el != nil {
			_ = el.ScrollIntoView()
			return el, nil
		}
	}

	// CSS selector
	if el, err := page.Element(target); err == nil && el != nil {
		_ = el.ScrollIntoView()
		return el, nil
	}

	// Teks tampilan / Regex
	textTags := "button, a, div, span, li, p, label, [role='button'], [role='combobox'], [role='option'], [role='menuitem'], [aria-haspopup]"
	if el, err := page.ElementR(textTags, target); err == nil && el != nil {
		_ = el.ScrollIntoView()
		return el, nil
	}

	// Case-insensitive XPath
	cleanTarget := strings.ToLower(target)
	xpathText := fmt.Sprintf("//*[contains(translate(text(), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), '%s') or contains(translate(@aria-label, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), '%s') or contains(translate(@placeholder, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), '%s')]", cleanTarget, cleanTarget, cleanTarget)
	if el, err := page.ElementX(xpathText); err == nil && el != nil {
		_ = el.ScrollIntoView()
		return el, nil
	}

	return nil, fmt.Errorf("elemen tidak ditemukan dengan selector atau teks '%s'", target)
}

func rodClickElement(page *rod.Page, indexPtr *int, selector string, waitSeconds int) (string, error) {
	el, err := findElement(page, indexPtr, selector)
	if err != nil {
		return "", err
	}

	targetLabel := selector
	if indexPtr != nil {
		targetLabel = fmt.Sprintf("index [%d]", *indexPtr)
	}

	// Scroll elemen ke tengah layar
	_ = el.ScrollIntoView()

	// 1. Coba klik fisik CDP native
	clickErr := el.Click(proto.InputMouseButtonLeft, 1)
	if clickErr != nil {
		// 2. Fallback: React/Vue synthetic event & JS click dispatch
		_, jsErr := el.Eval(`() => {
			this.scrollIntoView({ behavior: 'instant', block: 'center' });
			this.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
			this.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true }));
			this.click();
			this.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
		}`)
		if jsErr != nil {
			return "", fmt.Errorf("gagal mengklik %s: %w (js fallback error: %v)", targetLabel, clickErr, jsErr)
		}
	}

	time.Sleep(time.Duration(waitSeconds) * time.Second)

	pageTitle, _ := page.Eval("() => document.title")
	titleStr := ""
	if pageTitle != nil {
		titleStr = pageTitle.Value.Str()
	}

	currentURL := ""
	if info, errInfo := page.Info(); errInfo == nil && info != nil {
		currentURL = info.URL
	}

	// Otomatis refresh pohon DOM setelah interaksi klik
	updatedDOM, _ := buildIndexedDOM(page)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>Berhasil mengklik %s</b>\n", targetLabel))
	if currentURL != "" {
		sb.WriteString(fmt.Sprintf("• URL: <code>%s</code>\n", currentURL))
	}
	sb.WriteString(fmt.Sprintf("• Judul Halaman: <i>%s</i>\n\n", titleStr))
	if updatedDOM != "" {
		sb.WriteString(updatedDOM)
	}

	return sb.String(), nil
}

func rodTypeInput(page *rod.Page, indexPtr *int, selector, inputText string, clear bool, pressEnter bool, waitSeconds int) (string, error) {
	el, err := findElement(page, indexPtr, selector)
	if err != nil {
		return "", err
	}

	targetLabel := selector
	if indexPtr != nil {
		targetLabel = fmt.Sprintf("index [%d]", *indexPtr)
	}

	_ = el.ScrollIntoView()
	_ = el.Focus()

	if clear {
		_ = el.SelectAllText()
		// Reset nilai dengan React prototype setter hack
		_, _ = el.Eval(`() => {
			const proto = window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value');
			if (desc && desc.set) {
				desc.set.call(this, '');
			} else {
				this.value = '';
			}
			this.dispatchEvent(new Event('input', { bubbles: true }));
			this.dispatchEvent(new Event('change', { bubbles: true }));
		}`)
	}

	// Input teks via CDP
	inputErr := el.Input(inputText)
	if inputErr != nil {
		// Fallback: React setter hack
		setValScript := fmt.Sprintf(`() => {
			const proto = window.HTMLInputElement.prototype;
			const desc = Object.getOwnPropertyDescriptor(proto, 'value');
			if (desc && desc.set) {
				desc.set.call(this, %q);
			} else {
				this.value = %q;
			}
			this.dispatchEvent(new Event('input', { bubbles: true }));
			this.dispatchEvent(new Event('change', { bubbles: true }));
		}`, inputText, inputText)
		if _, jsErr := el.Eval(setValScript); jsErr != nil {
			return "", fmt.Errorf("gagal memasukkan teks ke %s: %w", targetLabel, jsErr)
		}
	}

	// Tekan tombol Enter jika diminta
	if pressEnter {
		time.Sleep(150 * time.Millisecond)
		_ = page.Keyboard.Press(input.Enter)
		// Fallback JS Enter event
		_, _ = el.Eval(`() => {
			this.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true }));
			this.dispatchEvent(new KeyboardEvent('keypress', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true }));
			this.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true }));
			if (this.form) {
				if (this.form.requestSubmit) this.form.requestSubmit();
				else this.form.submit();
			}
		}`)
	}

	time.Sleep(time.Duration(waitSeconds) * time.Second)

	pageTitle, _ := page.Eval("() => document.title")
	titleStr := ""
	if pageTitle != nil {
		titleStr = pageTitle.Value.Str()
	}

	currentURL := ""
	if info, errInfo := page.Info(); errInfo == nil && info != nil {
		currentURL = info.URL
	}

	updatedDOM, _ := buildIndexedDOM(page)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>Berhasil mengisi teks:</b> <i>\"%s\"</i> ke %s\n", inputText, targetLabel))
	if pressEnter {
		sb.WriteString("• Tombol Enter otomatis ditekan.\n")
	}
	if currentURL != "" {
		sb.WriteString(fmt.Sprintf("• URL: <code>%s</code>\n", currentURL))
	}
	sb.WriteString(fmt.Sprintf("• Judul Halaman: <i>%s</i>\n\n", titleStr))
	if updatedDOM != "" {
		sb.WriteString(updatedDOM)
	}

	return sb.String(), nil
}

func mapKeyNameToRodKey(k string) (input.Key, bool) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "enter", "return":
		return input.Enter, true
	case "escape", "esc":
		return input.Escape, true
	case "tab":
		return input.Tab, true
	case "backspace":
		return input.Backspace, true
	case "delete", "del":
		return input.Delete, true
	case "arrowdown", "down":
		return input.ArrowDown, true
	case "arrowup", "up":
		return input.ArrowUp, true
	case "arrowleft", "left":
		return input.ArrowLeft, true
	case "arrowright", "right":
		return input.ArrowRight, true
	case "space":
		return input.Space, true
	case "pageup":
		return input.PageUp, true
	case "pagedown":
		return input.PageDown, true
	case "home":
		return input.Home, true
	case "end":
		return input.End, true
	default:
		return 0, false
	}
}

func rodPressKey(page *rod.Page, keyName string, waitSeconds int) (string, error) {
	keyName = strings.TrimSpace(keyName)
	if keyName == "" {
		return "", fmt.Errorf("parameter 'key' wajib diisi (contoh: 'Enter', 'Escape', 'Tab', 'Backspace', 'ArrowDown')")
	}

	if rodKey, ok := mapKeyNameToRodKey(keyName); ok {
		_ = page.Keyboard.Press(rodKey)
	}

	script := fmt.Sprintf(`(k) => {
		const target = document.activeElement || document.body;
		const opts = { key: k, code: k, bubbles: true, cancelable: true };
		target.dispatchEvent(new KeyboardEvent('keydown', opts));
		target.dispatchEvent(new KeyboardEvent('keypress', opts));
		target.dispatchEvent(new KeyboardEvent('keyup', opts));
		return document.title;
	}`)
	_, _ = page.Eval(script, keyName)

	time.Sleep(time.Duration(waitSeconds) * time.Second)
	return fmt.Sprintf("⌨️ <b>Berhasil menekan tombol keyboard:</b> <code>%s</code>", keyName), nil
}

func rodScroll(page *rod.Page, indexPtr *int, direction string, waitSeconds int) (string, error) {
	if indexPtr != nil && *indexPtr >= 0 {
		el, err := findElement(page, indexPtr, "")
		if err != nil {
			return "", err
		}
		_ = el.ScrollIntoView()
		time.Sleep(time.Duration(waitSeconds) * time.Second)

		updatedDOM, _ := buildIndexedDOM(page)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📜 <b>Berhasil scroll ke elemen index [%d]</b>\n\n", *indexPtr))
		if updatedDOM != "" {
			sb.WriteString(updatedDOM)
		}
		return sb.String(), nil
	}

	deltaY := 600
	dirLabel := "bawah"
	if strings.EqualFold(direction, "up") {
		deltaY = -600
		dirLabel = "atas"
	}

	script := fmt.Sprintf(`() => { window.scrollBy(0, %d); }`, deltaY)
	if _, err := page.Eval(script); err != nil {
		return "", fmt.Errorf("gagal melakukan scroll: %w", err)
	}

	time.Sleep(time.Duration(waitSeconds) * time.Second)
	updatedDOM, _ := buildIndexedDOM(page)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📜 <b>Berhasil scroll ke %s</b>\n\n", dirLabel))
	if updatedDOM != "" {
		sb.WriteString(updatedDOM)
	}

	return sb.String(), nil
}

func rodEvalJS(page *rod.Page, targetURL, script string, waitSeconds int) (string, error) {
	safeWrapper := fmt.Sprintf(`() => {
		try {
			const fn = new Function(%q);
			const res = fn();
			if (res === undefined) return "(Eksekusi JS berhasil tanpa nilai kembali/undefined)";
			if (typeof res === "object") return JSON.stringify(res, null, 2);
			return String(res);
		} catch (e) {
			return "Error JS: " + e.message;
		}
	}`, script)

	res, err := page.Eval(safeWrapper)
	if err != nil {
		res, err = page.Eval(script)
		if err != nil {
			return "", fmt.Errorf("eksekusi JavaScript gagal: %w", err)
		}
	}

	time.Sleep(time.Duration(waitSeconds) * time.Second)
	return fmt.Sprintf("⚡ <b>Hasil Eksekusi JavaScript:</b>\n<pre>%s</pre>", res.Value.String()), nil
}

func rodTakeScreenshot(page *rod.Page, targetURL string, withSoM bool, waitSeconds int) (string, error) {
	screenshotDir := filepath.Join("data", "screenshots")
	_ = os.MkdirAll(screenshotDir, 0755)

	cleanupOldScreenshots(screenshotDir, 24*time.Hour)

	// Set-of-Marks visual overlay renderer ala browser-use
	if withSoM {
		drawScript := `() => {
			const old = document.getElementById('__ga_som_container');
			if (old) old.remove();

			const container = document.createElement('div');
			container.id = '__ga_som_container';
			container.style.cssText = 'position:fixed;top:0;left:0;width:100vw;height:100vh;pointer-events:none;z-index:2147483647;font-family:sans-serif;';

			const colors = ['#E53E3E', '#DD6B20', '#D69E2E', '#38A169', '#319795', '#3182CE', '#805AD5', '#D53F8C'];

			(window.__ga_elements || []).forEach((el, idx) => {
				if (!el || !el.isConnected) return;
				const rect = el.getBoundingClientRect();
				if (rect.width < 4 || rect.height < 4) return;
				if (rect.bottom < 0 || rect.top > window.innerHeight || rect.right < 0 || rect.left > window.innerWidth) return;

				const color = colors[idx % colors.length];

				const box = document.createElement('div');
				box.style.cssText = 'position:fixed;left:' + rect.left + 'px;top:' + rect.top + 'px;width:' + rect.width + 'px;height:' + rect.height + 'px;border:2px solid ' + color + ';box-sizing:border-box;pointer-events:none;';

				const badge = document.createElement('div');
				badge.innerText = String(idx);
				badge.style.cssText = 'position:absolute;left:-2px;top:-18px;background:' + color + ';color:#fff;font-size:11px;font-weight:bold;padding:1px 4px;border-radius:2px;line-height:14px;box-shadow:0 1px 3px rgba(0,0,0,0.4);white-space:nowrap;pointer-events:none;';

				box.appendChild(badge);
				container.appendChild(box);
			});

			document.body.appendChild(container);
			return true;
		}`
		_, _ = page.Eval(drawScript)
		time.Sleep(300 * time.Millisecond)
	}

	outPath := filepath.Join(screenshotDir, fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano()))
	absOutPath, _ := filepath.Abs(outPath)

	imgBytes, err := page.Screenshot(false, &proto.PageCaptureScreenshot{
		Format: proto.PageCaptureScreenshotFormatPng,
	})

	// Bersihkan overlay SoM segera setelah capture
	if withSoM {
		_, _ = page.Eval("() => { const c = document.getElementById('__ga_som_container'); if (c) c.remove(); }")
	}

	if err != nil {
		return "", fmt.Errorf("gagal mengambil screenshot: %w", err)
	}

	if err := os.WriteFile(absOutPath, imgBytes, 0644); err != nil {
		return "", fmt.Errorf("gagal menyimpan file screenshot: %w", err)
	}

	caption := fmt.Sprintf("Screenshot %s", targetURL)
	if withSoM {
		caption += " (Set-of-Marks [0..N])"
	}

	return fmt.Sprintf("📸 <b>Screenshot Berhasil Diambil (CDP)!</b>\n• URL: <code>%s</code>\n• Mode: %s\n• File: <code>%s</code>\n\n[ATTACH_FILE:%s|CAPTION:%s]", targetURL, map[bool]string{true: "Set-of-Marks (Numbered [0..N])", false: "Clean (No Labels)"}[withSoM], absOutPath, absOutPath, caption), nil
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

func extractCleanText(htmlStr string) string {
	s := reScript.ReplaceAllString(htmlStr, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reTags.ReplaceAllString(s, " ")
	s = reSpaces.ReplaceAllString(s, " ")
	s = reLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func findLocalBrowserBinary() string {
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

var (
	reScript = regexp.MustCompile(`(?is)<script.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style.*?</style>`)
	reTags   = regexp.MustCompile(`<[^>]+>`)
	reSpaces = regexp.MustCompile(`[ \t]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)
