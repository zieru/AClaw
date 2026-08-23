package admin

import (
	"context"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type WizardStep int

const (
	StepNone WizardStep = iota
	StepChooseType
	StepCustomDetails
	StepEnterAPIKey
	StepSelectDefaultModel
	StepEnterGeminiWebAuth
	StepEditBaseURL
	StepEditAddKeys
	StepEditReplaceKeys
	StepEditCustomModel
	StepEditProxyGroup
)

type WizardSession struct {
	Step              WizardStep
	IsEditing         bool
	EditingProviderID string
	ID                string
	Name              string
	Type              string
	BaseURL           string
	APIKeys           []string
	DetectedModels    []string
	DefaultModel      string
	UpdatedAt         time.Time
}

type ProviderWizard struct {
	mu              sync.RWMutex
	sessions        map[int64]*WizardSession
	db              *storage.DB
	providerManager *provider.Manager
	proxyPool       *proxy.Pool
	bot             *tele.Bot
}

func NewProviderWizard(db *storage.DB, pm *provider.Manager, pool *proxy.Pool, bot *tele.Bot) *ProviderWizard {
	return &ProviderWizard{
		sessions:        make(map[int64]*WizardSession),
		db:              db,
		providerManager: pm,
		proxyPool:       pool,
		bot:             bot,
	}
}

// StartWizard launches the interactive setup wizard for creating a new provider
func (w *ProviderWizard) StartWizard(c tele.Context) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	w.mu.Lock()
	w.sessions[userID] = &WizardSession{
		Step:      StepChooseType,
		UpdatedAt: time.Now(),
	}
	w.mu.Unlock()

	text := "🧙‍♂️ <b>SETUP WIZARD PROVIDER AI</b> (Langkah 1/3)\n\n" +
		"Pilih jenis provider yang ingin Anda hubungkan ke GoAssistant:"

	menu := &tele.ReplyMarkup{}
	btn9Router := menu.Data("⚡ 9Router Gateway", "wiz_type_9router")
	btnOpenAI := menu.Data("🟢 OpenAI Official", "wiz_type_openai")
	btnDeepSeek := menu.Data("🤖 DeepSeek Official", "wiz_type_deepseek")
	btnGroq := menu.Data("🚀 Groq (Llama 3.3)", "wiz_type_groq")
	btnGemini := menu.Data("✨ Google Gemini", "wiz_type_gemini")
	btnGeminiWeb := menu.Data("🌐 Gemini Web (Scrape)", "wiz_type_gemini_web")
	btnClaude := menu.Data("🧠 Anthropic Claude", "wiz_type_anthropic")
	btnOllama := menu.Data("🦙 Ollama Local", "wiz_type_ollama")
	btnCustom := menu.Data("⚙️ Custom Endpoint", "wiz_type_custom")
	btnCancel := menu.Data("❌ Batal", "wiz_cancel")

	menu.Inline(
		menu.Row(btn9Router, btnOpenAI),
		menu.Row(btnDeepSeek, btnGroq),
		menu.Row(btnGemini, btnGeminiWeb),
		menu.Row(btnClaude, btnOllama),
		menu.Row(btnCustom),
		menu.Row(btnCancel),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// StartEditWizard launches the interactive editor for existing providers
func (w *ProviderWizard) StartEditWizard(c tele.Context, targetProvID string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	if targetProvID != "" {
		p, err := w.db.GetProvider(targetProvID)
		if err != nil || p == nil {
			return c.Reply(fmt.Sprintf("❌ Provider dengan ID '<code>%s</code>' tidak ditemukan.", html.EscapeString(targetProvID)), tele.ModeHTML)
		}
		w.mu.Lock()
		w.sessions[userID] = &WizardSession{
			IsEditing:         true,
			EditingProviderID: p.ID,
			UpdatedAt:         time.Now(),
		}
		w.mu.Unlock()
		return w.RenderProviderEditDashboard(c, p)
	}

	// Show list of existing providers to choose from
	providers, err := w.db.ListProviders()
	if err != nil || len(providers) == 0 {
		return c.Reply("⚠️ Belum ada provider AI yang terdaftar. Gunakan tombol <b>🧙‍♂️ Setup Wizard</b> untuk menambahkan provider baru.", tele.ModeHTML)
	}

	w.mu.Lock()
	w.sessions[userID] = &WizardSession{
		IsEditing: true,
		UpdatedAt: time.Now(),
	}
	w.mu.Unlock()

	text := "✏️ <b>WIZARD EDIT PROVIDER AI</b>\n\nPilih provider yang ingin Anda ubah konfigurasinya:"

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, p := range providers {
		provCopy := p
		statusIcon := "🟢"
		if !provCopy.IsActive {
			statusIcon = "🔴"
		}
		btn := menu.Data(fmt.Sprintf("%s %s (%s)", statusIcon, provCopy.Name, provCopy.ID), fmt.Sprintf("wiz_ed_pick_%s", provCopy.ID))
		rows = append(rows, menu.Row(btn))
	}

	btnCancel := menu.Data("⬅️ Kembali ke Menu Provider", "menu_providers")
	rows = append(rows, menu.Row(btnCancel))
	menu.Inline(rows...)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderProviderEditDashboard displays the full interactive edit options for a provider
func (w *ProviderWizard) RenderProviderEditDashboard(c tele.Context, p *storage.ProviderRecord) error {
	statusText := "🟢 <b>Aktif</b>"
	if !p.IsActive {
		statusText = "🔴 <b>Nonaktif</b>"
	}

	keyCount := len(p.APIKeys)
	if keyCount == 0 && p.APIKey != "" {
		keyCount = 1
	}

	proxyStatus := "⚪ <i>Direct / Off</i>"
	if p.ProxyEnabled {
		grp := p.ProxyGroup
		if grp == "" {
			grp = "default"
		}
		proxyStatus = fmt.Sprintf("🟢 <b>Aktif</b> (Group: <code>%s</code>)", html.EscapeString(grp))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🛠️ <b>EDIT PROVIDER: %s</b> (<code>%s</code>)\n\n", html.EscapeString(p.Name), html.EscapeString(p.ID)))
	sb.WriteString(fmt.Sprintf("• <b>Status:</b> %s\n", statusText))
	sb.WriteString(fmt.Sprintf("• <b>Tipe:</b> <code>%s</code>\n", html.EscapeString(p.Type)))
	sb.WriteString(fmt.Sprintf("• <b>Default Model:</b> <code>%s</code>\n", html.EscapeString(p.DefaultModel)))
	sb.WriteString(fmt.Sprintf("• <b>Model Terdaftar:</b> %d model\n", len(p.Models)))
	sb.WriteString(fmt.Sprintf("• <b>Key Pool:</b> %d key (Strategi: <code>%s</code>)\n", keyCount, html.EscapeString(p.KeyStrategy)))
	sb.WriteString(fmt.Sprintf("• <b>Proxy Upstream:</b> %s\n", proxyStatus))
	if p.BaseURL != "" {
		sb.WriteString(fmt.Sprintf("• <b>Base URL:</b> <code>%s</code>\n", html.EscapeString(p.BaseURL)))
	}
	sb.WriteString("\nPilih pengaturan yang ingin diubah:")

	menu := &tele.ReplyMarkup{}
	btnDetect := menu.Data("🔄 Auto-Detect Models", "wiz_ed_detect")
	btnDefMod := menu.Data("⭐ Ganti Default Model", "wiz_ed_defmod")
	btnKeysRep := menu.Data("🔑 Ganti Semua Key", "wiz_ed_keys_rep")
	btnKeysAdd := menu.Data("➕ Tambah Key", "wiz_ed_keys_add")
	btnKeyStrat := menu.Data("🔀 Strategi Key", "wiz_ed_keystrat")
	btnBaseURL := menu.Data("🌐 Ubah Base URL", "wiz_ed_baseurl")
	btnProxy := menu.Data("🛡️ Set Proxy Pool", "wiz_ed_proxy")
	btnToggle := menu.Data("🔘 Toggle Aktif/Nonaktif", "wiz_ed_toggle")
	btnDel := menu.Data("🗑️ Hapus Provider", "wiz_ed_del")
	btnBack := menu.Data("⬅️ Selesai / Kembali", "menu_providers")

	menu.Inline(
		menu.Row(btnDetect, btnDefMod),
		menu.Row(btnKeysRep, btnKeysAdd),
		menu.Row(btnKeyStrat, btnBaseURL),
		menu.Row(btnProxy, btnToggle),
		menu.Row(btnDel),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleTypeSelect processes provider type choice for setup wizard
func (w *ProviderWizard) HandleTypeSelect(c tele.Context, pType string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	w.mu.Lock()
	sess, exists := w.sessions[userID]
	if !exists {
		sess = &WizardSession{UpdatedAt: time.Now()}
		w.sessions[userID] = sess
	}
	sess.Type = strings.ToLower(pType)
	sess.UpdatedAt = time.Now()
	w.mu.Unlock()

	switch sess.Type {
	case "9router":
		sess.ID = "9router"
		sess.Name = "9Router Gateway"
		sess.BaseURL = "https://api.9router.com/v1"
		sess.Step = StepEnterAPIKey
		return w.promptAPIKey(c, sess)

	case "openai":
		sess.ID = "openai"
		sess.Name = "OpenAI Official"
		sess.BaseURL = "https://api.openai.com/v1"
		sess.Step = StepEnterAPIKey
		return w.promptAPIKey(c, sess)

	case "deepseek":
		sess.ID = "deepseek"
		sess.Name = "DeepSeek Official"
		sess.BaseURL = "https://api.deepseek.com/v1"
		sess.Step = StepEnterAPIKey
		return w.promptAPIKey(c, sess)

	case "groq":
		sess.ID = "groq"
		sess.Name = "Groq Cloud"
		sess.BaseURL = "https://api.groq.com/openai/v1"
		sess.Step = StepEnterAPIKey
		return w.promptAPIKey(c, sess)

	case "gemini":
		sess.ID = "gemini"
		sess.Name = "Google Gemini"
		sess.BaseURL = "https://generativelanguage.googleapis.com"
		sess.Step = StepEnterAPIKey
		return w.promptAPIKey(c, sess)

	case "gemini_web", "gemini_scrape":
		sess.ID = "gemini_web"
		sess.Name = "Gemini Web (Google Auth)"
		sess.Type = "gemini_web"
		sess.BaseURL = "https://gemini.google.com"
		sess.Step = StepEnterGeminiWebAuth
		sess.DetectedModels = []string{"gemini-web-pro", "gemini-web-flash", "gemini-web-ultra"}
		sess.DefaultModel = "gemini-web-pro"
		return w.promptGeminiWebAuth(c, sess)

	case "anthropic":
		sess.ID = "anthropic"
		sess.Name = "Anthropic Claude"
		sess.BaseURL = "https://api.anthropic.com"
		sess.Step = StepEnterAPIKey
		return w.promptAPIKey(c, sess)

	case "ollama":
		sess.ID = "ollama"
		sess.Name = "Ollama Local"
		sess.BaseURL = "http://localhost:11434/v1"
		sess.APIKeys = []string{""}
		return w.autoDiscoverAndPromptModels(c, sess)

	case "custom":
		sess.Step = StepCustomDetails
		text := "⚙️ <b>CUSTOM PROVIDER SETUP</b>\n\n" +
			"Kirimkan format identitas provider dengan format:\n" +
			"<code>id|Nama Provider|Base_URL</code>\n\n" +
			"<b>Contoh:</b>\n" +
			"<code>together|Together AI|https://api.together.xyz/v1</code>\n" +
			"<code>vllm|Local vLLM Server|http://192.168.1.100:8000/v1</code>"
		return c.EditOrSend(text, tele.ModeHTML)
	}

	return nil
}

func (w *ProviderWizard) promptGeminiWebAuth(c tele.Context, sess *WizardSession) error {
	text := "🌐 <b>SETUP GEMINI WEB (GOOGLE AUTH SCRAPER)</b>\n\n" +
		"Provider ini memungkinkan Anda menggunakan Google Gemini Web secara gratis melalui scraping sesi Google.\n\n" +
		"<b>Langkah Login Google Auth:</b>\n" +
		"1️⃣ Buka link login berikut di browser Anda:\n" +
		"👉 <a href=\"https://gemini.google.com/app\">https://gemini.google.com/app</a>\n\n" +
		"2️⃣ Login dengan Akun Google Anda hingga masuk ke halaman obrolan Gemini.\n\n" +
		"3️⃣ <b>Cara Mengambil Kredensial:</b>\n" +
		"• <b>Opsi A (Link / URL):</b> Salin URL address bar / link redirect setelah login, ATAU\n" +
		"• <b>Opsi B (Cookies DevTools):</b> Tekan <code>F12</code> -> Tab <b>Application/Storage</b> -> <b>Cookies</b> -> Salin nilai cookie <code>__Secure-1PSID</code> (dan <code>__Secure-1PSIDTS</code> bila ada), ATAU\n" +
		"• <b>Opsi C (Cookie Header):</b> Salin seluruh cookie header (<code>key=value; ...</code>)\n\n" +
		"4️⃣ <b>Paste / Kirimkan link atau cookie tersebut ke chat ini:</b>"

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal Setup", "wiz_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (w *ProviderWizard) promptAPIKey(c tele.Context, sess *WizardSession) error {
	text := fmt.Sprintf("🔑 <b>MASUKKAN API KEY</b> (%s)\n\n"+
		"Silakan kirimkan API Key untuk <b>%s</b>.\n\n"+
		"💡 <i>Tips 9Router: Anda dapat mengirim lebih dari 1 key (pisahkan dengan koma atau baris baru) untuk mengaktifkan fitur rotasi & failover otomatis.</i>",
		html.EscapeString(sess.Name), html.EscapeString(sess.Name))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal Setup", "wiz_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleTextMessage handles text sent during wizard state (Setup & Edit)
func (w *ProviderWizard) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.Step == StepNone {
		return false, nil // Not in wizard mode
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") {
		provID := sess.EditingProviderID
		w.CancelWizard(userID)
		if sess.IsEditing && provID != "" {
			p, _ := w.db.GetProvider(provID)
			if p != nil {
				return true, w.RenderProviderEditDashboard(c, p)
			}
		}
		return true, c.Reply("❌ Operasi wizard dibatalkan.")
	}

	switch sess.Step {
	case StepEnterGeminiWebAuth:
		_ = c.Notify(tele.Typing)
		parsedCookies, err := provider.ParseGoogleAuthInput(msgText)
		if err != nil {
			return true, c.Reply(fmt.Sprintf("⚠️ %v\n\nSilakan pastikan Anda menyalin URL hasil login atau cookie <code>__Secure-1PSID</code> dengan benar, lalu kirimkan kembali.", err), tele.ModeHTML)
		}

		var cookieParts []string
		for k, v := range parsedCookies {
			cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", k, v))
		}
		rawCookieStr := strings.Join(cookieParts, "; ")

		testProv := provider.NewGeminiWebProvider(sess.Name, rawCookieStr, sess.DefaultModel, sess.DetectedModels)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		_, testErr := testProv.FetchSNlM0e(ctx)
		if testErr != nil {
			return true, c.Reply(fmt.Sprintf("⚠️ <b>Autentikasi Gemini Web Gagal:</b>\n%v\n\nSilakan periksa kembali apakah akun Google Anda sudah login di browser dan cookie <code>__Secure-1PSID</code> masih aktif, lalu kirim ulang.", html.EscapeString(testErr.Error())), tele.ModeHTML)
		}

		sess.APIKeys = []string{rawCookieStr}
		return true, w.finishWizard(c, sess)

	case StepCustomDetails:
		parts := strings.Split(msgText, "|")
		if len(parts) < 3 {
			return true, c.Reply("⚠️ Format salah! Gunakan: <code>id|Nama|BaseURL</code>\nContoh: <code>myai|My AI|https://api.myai.com/v1</code>", tele.ModeHTML)
		}
		sess.ID = strings.TrimSpace(parts[0])
		sess.Name = strings.TrimSpace(parts[1])
		rawBaseURL := strings.TrimSpace(parts[2])
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/")
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/chat/completions")
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/chat")
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/completions")
		sess.BaseURL = rawBaseURL
		sess.Step = StepEnterAPIKey
		return true, w.promptAPIKey(c, sess)

	case StepEnterAPIKey:
		rawKeys := strings.FieldsFunc(msgText, func(r rune) bool {
			return r == ',' || r == '\n' || r == ';'
		})
		var cleanKeys []string
		for _, k := range rawKeys {
			k = strings.TrimSpace(k)
			if k != "" {
				cleanKeys = append(cleanKeys, k)
			}
		}

		if len(cleanKeys) == 0 {
			cleanKeys = []string{msgText}
		}
		sess.APIKeys = cleanKeys
		return true, w.autoDiscoverAndPromptModels(c, sess)

	case StepSelectDefaultModel:
		sess.DefaultModel = msgText
		return true, w.finishWizard(c, sess)

	case StepEditBaseURL:
		p, err := w.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			w.CancelWizard(userID)
			return true, c.Reply("❌ Provider tidak ditemukan.")
		}
		rawBaseURL := strings.TrimSpace(msgText)
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/")
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/chat/completions")
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/chat")
		rawBaseURL = strings.TrimSuffix(rawBaseURL, "/completions")
		p.BaseURL = rawBaseURL
		_ = w.db.SaveProvider(p)
		w.syncProviderToManager(p)
		sess.Step = StepNone
		_ = c.Reply(fmt.Sprintf("✅ Base URL untuk <b>%s</b> berhasil diubah ke: <code>%s</code>", html.EscapeString(p.Name), html.EscapeString(p.BaseURL)), tele.ModeHTML)
		return true, w.RenderProviderEditDashboard(c, p)

	case StepEditReplaceKeys:
		p, err := w.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			w.CancelWizard(userID)
			return true, c.Reply("❌ Provider tidak ditemukan.")
		}
		rawKeys := strings.FieldsFunc(msgText, func(r rune) bool {
			return r == ',' || r == '\n' || r == ';'
		})
		var cleanKeys []string
		for _, k := range rawKeys {
			k = strings.TrimSpace(k)
			if k != "" {
				cleanKeys = append(cleanKeys, k)
			}
		}
		if len(cleanKeys) == 0 {
			cleanKeys = []string{msgText}
		}
		p.APIKeys = cleanKeys
		p.APIKey = cleanKeys[0]
		_ = w.db.SaveProvider(p)
		w.syncProviderToManager(p)
		sess.Step = StepNone
		_ = c.Reply(fmt.Sprintf("✅ Key pool untuk <b>%s</b> berhasil diganti (%d key aktif)!", html.EscapeString(p.Name), len(p.APIKeys)), tele.ModeHTML)
		return true, w.RenderProviderEditDashboard(c, p)

	case StepEditAddKeys:
		p, err := w.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			w.CancelWizard(userID)
			return true, c.Reply("❌ Provider tidak ditemukan.")
		}
		rawKeys := strings.FieldsFunc(msgText, func(r rune) bool {
			return r == ',' || r == '\n' || r == ';'
		})
		addedCount := 0
		for _, k := range rawKeys {
			k = strings.TrimSpace(k)
			if k != "" && !contains(p.APIKeys, k) {
				p.APIKeys = append(p.APIKeys, k)
				addedCount++
			}
		}
		if len(p.APIKeys) > 0 && p.APIKey == "" {
			p.APIKey = p.APIKeys[0]
		}
		_ = w.db.SaveProvider(p)
		w.syncProviderToManager(p)
		sess.Step = StepNone
		_ = c.Reply(fmt.Sprintf("✅ Berhasil menambahkan %d key baru ke <b>%s</b>! Total pool: %d key.", addedCount, html.EscapeString(p.Name), len(p.APIKeys)), tele.ModeHTML)
		return true, w.RenderProviderEditDashboard(c, p)

	case StepEditCustomModel:
		p, err := w.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			w.CancelWizard(userID)
			return true, c.Reply("❌ Provider tidak ditemukan.")
		}
		p.DefaultModel = msgText
		if !contains(p.Models, msgText) {
			p.Models = append(p.Models, msgText)
		}
		_ = w.db.SaveProvider(p)
		w.syncProviderToManager(p)
		sess.Step = StepNone
		_ = c.Reply(fmt.Sprintf("✅ Default model untuk <b>%s</b> diset ke: <code>%s</code>", html.EscapeString(p.Name), html.EscapeString(p.DefaultModel)), tele.ModeHTML)
		return true, w.RenderProviderEditDashboard(c, p)

	case StepEditProxyGroup:
		p, err := w.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			w.CancelWizard(userID)
			return true, c.Reply("❌ Provider tidak ditemukan.")
		}
		p.ProxyEnabled = true
		p.ProxyGroup = strings.ToLower(msgText)
		_ = w.db.SaveProvider(p)
		w.syncProviderToManager(p)
		sess.Step = StepNone
		_ = c.Reply(fmt.Sprintf("✅ Proxy pool group untuk <b>%s</b> diset ke <code>%s</code>!", html.EscapeString(p.Name), html.EscapeString(p.ProxyGroup)), tele.ModeHTML)
		return true, w.RenderProviderEditDashboard(c, p)
	}

	return false, nil
}

func (w *ProviderWizard) autoDiscoverAndPromptModels(c tele.Context, sess *WizardSession) error {
	_ = c.Notify(tele.Typing)
	_ = c.Send("🔍 <i>Menghubungi endpoint /models untuk mendeteksi model yang tersedia...</i>", tele.ModeHTML)

	firstKey := ""
	if len(sess.APIKeys) > 0 {
		firstKey = sess.APIKeys[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	detected, err := provider.FetchRemoteModels(ctx, sess.Type, sess.BaseURL, firstKey, nil)
	if err != nil || len(detected) == 0 {
		// Fallback default list
		sess.DetectedModels = []string{"gpt-4o-mini", "gpt-4o", "deepseek-chat"}
		if sess.Type == "gemini" {
			sess.DetectedModels = []string{"gemini-2.0-flash", "gemini-1.5-pro", "gemini-1.5-flash"}
		} else if sess.Type == "anthropic" {
			sess.DetectedModels = []string{"claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022"}
		}
	} else {
		sess.DetectedModels = detected
	}

	sess.Step = StepSelectDefaultModel

	var sb strings.Builder
	sb.WriteString("🎉 <b>DETEKSI MODEL BERHASIL!</b>\n\n")
	sb.WriteString(fmt.Sprintf("Ditemukan <b>%d model</b> aktif di endpoint <code>%s</code>:\n", len(sess.DetectedModels), html.EscapeString(sess.BaseURL)))

	displayCount := len(sess.DetectedModels)
	if displayCount > 8 {
		displayCount = 8
	}
	for i := 0; i < displayCount; i++ {
		sb.WriteString(fmt.Sprintf("• <code>%s</code>\n", html.EscapeString(sess.DetectedModels[i])))
	}
	if len(sess.DetectedModels) > 8 {
		sb.WriteString(fmt.Sprintf("<i>...dan %d model lainnya.</i>\n", len(sess.DetectedModels)-8))
	}

	sb.WriteString("\nPilih salah satu model di bawah sebagai <b>Default Model</b> (atau ketik nama model khusus):")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	var curRow []tele.Btn

	for i := 0; i < displayCount; i++ {
		mName := sess.DetectedModels[i]
		uniqueID := fmt.Sprintf("wiz_mod_%d", i)
		btn := menu.Data(mName, uniqueID)
		curRow = append(curRow, btn)
		if len(curRow) == 2 {
			rows = append(rows, menu.Row(curRow...))
			curRow = nil
		}
	}
	if len(curRow) > 0 {
		rows = append(rows, menu.Row(curRow...))
	}

	btnCancel := menu.Data("❌ Batal", "wiz_cancel")
	rows = append(rows, menu.Row(btnCancel))
	menu.Inline(rows...)

	return c.Send(sb.String(), menu, tele.ModeHTML)
}

// HandleModelSelect processes model chosen from button during setup wizard
func (w *ProviderWizard) HandleModelSelect(c tele.Context, modelIndex int) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.Step != StepSelectDefaultModel {
		return c.Reply("⚠️ Sesi wizard sudah berakhir. Ketik <code>/wizard</code> untuk mulai kembali.", tele.ModeHTML)
	}

	if modelIndex >= 0 && modelIndex < len(sess.DetectedModels) {
		sess.DefaultModel = sess.DetectedModels[modelIndex]
	} else if len(sess.DetectedModels) > 0 {
		sess.DefaultModel = sess.DetectedModels[0]
	} else {
		sess.DefaultModel = "gpt-4o-mini"
	}

	return w.finishWizard(c, sess)
}

func (w *ProviderWizard) finishWizard(c tele.Context, sess *WizardSession) error {
	firstKey := ""
	if len(sess.APIKeys) > 0 {
		firstKey = sess.APIKeys[0]
	}

	record := &storage.ProviderRecord{
		ID:           sess.ID,
		Name:         sess.Name,
		Type:         sess.Type,
		BaseURL:      sess.BaseURL,
		APIKey:       firstKey,
		APIKeys:      sess.APIKeys,
		DefaultModel: sess.DefaultModel,
		Models:       sess.DetectedModels,
		Strategy:     "failsafe",
		KeyStrategy:  "round-robin",
		IsActive:     true,
		Priority:     10,
	}

	if err := w.db.SaveProvider(record); err != nil {
		return c.Send(fmt.Sprintf("❌ Gagal menyimpan ke database: %v", html.EscapeString(err.Error())))
	}

	w.syncProviderToManager(record)
	w.CancelWizard(c.Sender().ID)

	text := fmt.Sprintf("✅ <b>PROVIDER BERHASIL DIAKTIFKAN!</b>\n\n"+
		"🤖 <b>Nama:</b> %s (<code>%s</code>)\n"+
		"🌐 <b>Tipe:</b> <code>%s</code>\n"+
		"⭐ <b>Default Model:</b> <code>%s</code>\n"+
		"📦 <b>Model Terdaftar:</b> %d model\n"+
		"🔑 <b>Key Pool:</b> %d key aktif (Rotasi: <code>round-robin</code>)\n\n"+
		"<i>Provider siap digunakan oleh seluruh bot & channel!</i>",
		html.EscapeString(record.Name), html.EscapeString(record.ID),
		html.EscapeString(record.Type), html.EscapeString(record.DefaultModel),
		len(record.Models), len(record.APIKeys))

	return c.EditOrSend(text, BackToMenuKeyboard(), tele.ModeHTML)
}

// --- Edit Provider Actions ---

// HandleEditAutoDetect runs auto-detect for an existing provider
func (w *ProviderWizard) HandleEditAutoDetect(c tele.Context, providerID string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	_ = c.Notify(tele.Typing)
	_ = c.Send(fmt.Sprintf("🔍 <i>Mendeteksi model dari %s di <code>%s/models</code>...</i>", html.EscapeString(p.Name), html.EscapeString(p.BaseURL)), tele.ModeHTML)

	firstKey := p.APIKey
	if len(p.APIKeys) > 0 {
		firstKey = p.APIKeys[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	detected, err := provider.FetchRemoteModels(ctx, p.Type, p.BaseURL, firstKey, nil)
	if err != nil || len(detected) == 0 {
		return c.Reply(fmt.Sprintf("❌ Gagal mendeteksi model: %v", err))
	}

	p.Models = detected
	if p.DefaultModel == "" || !contains(detected, p.DefaultModel) {
		p.DefaultModel = detected[0]
	}

	_ = w.db.SaveProvider(p)
	w.syncProviderToManager(p)

	_ = c.Reply(fmt.Sprintf("🎉 <b>BERHASIL!</b> Ditemukan <b>%d model</b> untuk <b>%s</b>. Default model saat ini: <code>%s</code>", len(detected), html.EscapeString(p.Name), html.EscapeString(p.DefaultModel)), tele.ModeHTML)
	return w.RenderProviderEditDashboard(c, p)
}

// HandleEditPickDefaultModel shows buttons of registered models to choose a default model
func (w *ProviderWizard) HandleEditPickDefaultModel(c tele.Context, providerID string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	models := p.Models
	if len(models) == 0 && p.DefaultModel != "" {
		models = []string{p.DefaultModel}
	}
	if len(models) == 0 {
		models = []string{"gpt-4o-mini", "gpt-4o", "deepseek-chat"}
	}

	text := fmt.Sprintf("⭐ <b>PILIH DEFAULT MODEL UNTUK %s</b>\n\n"+
		"Default Model saat ini: <code>%s</code>\n\n"+
		"Pilih model baru dari tombol di bawah, atau ketik nama model khusus di chat:",
		html.EscapeString(p.Name), html.EscapeString(p.DefaultModel))

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	var curRow []tele.Btn

	displayCount := len(models)
	if displayCount > 12 {
		displayCount = 12
	}

	for i := 0; i < displayCount; i++ {
		mName := models[i]
		uniqueID := fmt.Sprintf("wiz_edm_%d", i)
		btn := menu.Data(mName, uniqueID)
		curRow = append(curRow, btn)
		if len(curRow) == 2 {
			rows = append(rows, menu.Row(curRow...))
			curRow = nil
		}
	}
	if len(curRow) > 0 {
		rows = append(rows, menu.Row(curRow...))
	}

	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("wiz_ed_pick_%s", p.ID))
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	if c.Sender() != nil {
		w.mu.Lock()
		w.sessions[c.Sender().ID] = &WizardSession{
			IsEditing:         true,
			EditingProviderID: p.ID,
			Step:              StepEditCustomModel,
			DetectedModels:    models,
			UpdatedAt:         time.Now(),
		}
		w.mu.Unlock()
	}

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditSetDefaultModel updates the default model from button index
func (w *ProviderWizard) HandleEditSetDefaultModel(c tele.Context, modelIndex int) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.EditingProviderID == "" {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editprovider</code>.", tele.ModeHTML)
	}

	p, err := w.db.GetProvider(sess.EditingProviderID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	models := p.Models
	if len(sess.DetectedModels) > 0 {
		models = sess.DetectedModels
	}

	if modelIndex >= 0 && modelIndex < len(models) {
		p.DefaultModel = models[modelIndex]
	}

	_ = w.db.SaveProvider(p)
	w.syncProviderToManager(p)

	w.mu.Lock()
	sess.Step = StepNone
	w.mu.Unlock()

	_ = c.Reply(fmt.Sprintf("✅ Default model untuk <b>%s</b> berhasil diset ke <code>%s</code>!", html.EscapeString(p.Name), html.EscapeString(p.DefaultModel)), tele.ModeHTML)
	return w.RenderProviderEditDashboard(c, p)
}

// HandleEditKeyStrategyMenu shows options for key rotation strategy
func (w *ProviderWizard) HandleEditKeyStrategyMenu(c tele.Context, providerID string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	text := fmt.Sprintf("🔀 <b>STRATEGI ROTASI KEY: %s</b>\n\n"+
		"Strategi saat ini: <code>%s</code>\n\n"+
		"• <b>Round-Robin:</b> Membagi beban request merata ke setiap key secara berurutan.\n"+
		"• <b>Failover:</b> Selalu menggunakan key utama, baru beralih ke key berikutnya jika limit/error.\n"+
		"• <b>Random:</b> Memilih key secara acak untuk setiap request.\n\n"+
		"Pilih strategi baru:", html.EscapeString(p.Name), html.EscapeString(p.KeyStrategy))

	menu := &tele.ReplyMarkup{}
	btnRR := menu.Data("🔄 Round-Robin", "wiz_ed_strat_rr")
	btnFO := menu.Data("🛡️ Failover", "wiz_ed_strat_fo")
	btnRD := menu.Data("🎲 Random", "wiz_ed_strat_rd")
	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("wiz_ed_pick_%s", p.ID))

	menu.Inline(
		menu.Row(btnRR, btnFO),
		menu.Row(btnRD),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditSetKeyStrategy sets strategy and updates provider
func (w *ProviderWizard) HandleEditSetKeyStrategy(c tele.Context, providerID string, strat string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	p.KeyStrategy = strat
	_ = w.db.SaveProvider(p)
	w.syncProviderToManager(p)

	_ = c.Reply(fmt.Sprintf("✅ Strategi key untuk <b>%s</b> berhasil diubah ke: <code>%s</code>", html.EscapeString(p.Name), strat), tele.ModeHTML)
	return w.RenderProviderEditDashboard(c, p)
}

// HandleEditProxyMenu shows proxy configuration menu
func (w *ProviderWizard) HandleEditProxyMenu(c tele.Context, providerID string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	text := fmt.Sprintf("🛡️ <b>PROXY POOL: %s</b>\n\n"+
		"Status proxy saat ini: %s\n\n"+
		"Pilih opsi proxy untuk provider ini:",
		html.EscapeString(p.Name),
		map[bool]string{true: fmt.Sprintf("🟢 Aktif (Group: <code>%s</code>)", p.ProxyGroup), false: "⚪ Direct (Nonaktif)"}[p.ProxyEnabled])

	menu := &tele.ReplyMarkup{}
	btnOff := menu.Data("⚪ Direct / Nonaktifkan Proxy", "wiz_ed_px_off")
	btnDef := menu.Data("🌐 Gunakan Group 'default'", "wiz_ed_px_def")
	btnCust := menu.Data("✏️ Ketik Nama Group Khusus", "wiz_ed_px_cust")
	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("wiz_ed_pick_%s", p.ID))

	menu.Inline(
		menu.Row(btnOff),
		menu.Row(btnDef, btnCust),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditToggleActive toggles active status
func (w *ProviderWizard) HandleEditToggleActive(c tele.Context, providerID string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	p.IsActive = !p.IsActive
	_ = w.db.SaveProvider(p)
	w.syncProviderToManager(p)

	statusStr := "Diaktifkan 🟢"
	if !p.IsActive {
		statusStr = "Dinonaktifkan 🔴"
	}
	_ = c.Reply(fmt.Sprintf("✅ Provider <b>%s</b> berhasil <b>%s</b>!", html.EscapeString(p.Name), statusStr), tele.ModeHTML)
	return w.RenderProviderEditDashboard(c, p)
}

// HandleEditDeletePrompt asks for delete confirmation
func (w *ProviderWizard) HandleEditDeletePrompt(c tele.Context, providerID string) error {
	p, err := w.db.GetProvider(providerID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	text := fmt.Sprintf("⚠️ <b>KONFIRMASI HAPUS PROVIDER</b>\n\n"+
		"Apakah Anda yakin ingin menghapus provider <b>%s</b> (<code>%s</code>)?\n\n"+
		"<i>Tindakan ini permanen dan akan menghapus key pool serta pendaftaran model terkait.</i>",
		html.EscapeString(p.Name), html.EscapeString(p.ID))

	menu := &tele.ReplyMarkup{}
	btnYes := menu.Data("🗑️ Ya, Hapus Sekarang", fmt.Sprintf("wiz_ed_del_yes_%s", p.ID))
	btnNo := menu.Data("❌ Batal", fmt.Sprintf("wiz_ed_pick_%s", p.ID))

	menu.Inline(
		menu.Row(btnYes),
		menu.Row(btnNo),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditDeleteConfirm executes deletion
func (w *ProviderWizard) HandleEditDeleteConfirm(c tele.Context, providerID string) error {
	p, _ := w.db.GetProvider(providerID)
	if p != nil {
		_ = w.db.DeleteProvider(providerID)
		w.providerManager.Unregister(p.Name)
	}

	if c.Sender() != nil {
		w.CancelWizard(c.Sender().ID)
	}

	_ = c.Reply(fmt.Sprintf("🗑️ Provider <b>%s</b> berhasil dihapus dari sistem.", html.EscapeString(providerID)), tele.ModeHTML)
	return w.StartEditWizard(c, "")
}

// PromptEditStep sets session step and prompts text input
func (w *ProviderWizard) PromptEditStep(c tele.Context, providerID string, step WizardStep, promptMsg string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	w.mu.Lock()
	w.sessions[userID] = &WizardSession{
		IsEditing:         true,
		EditingProviderID: providerID,
		Step:              step,
		UpdatedAt:         time.Now(),
	}
	w.mu.Unlock()

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("wiz_ed_pick_%s", providerID))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(promptMsg, menu, tele.ModeHTML)
}

func (w *ProviderWizard) syncProviderToManager(p *storage.ProviderRecord) {
	if !p.IsActive {
		w.providerManager.Unregister(p.Name)
		return
	}

	keys := p.APIKeys
	if len(keys) == 0 && p.APIKey != "" {
		keys = []string{p.APIKey}
	}

	models := p.Models
	if len(models) == 0 && p.DefaultModel != "" {
		models = []string{p.DefaultModel}
	}

	var inst provider.Provider
	switch p.Type {
	case "gemini_web", "gemini_scrape":
		authData := p.APIKey
		if len(keys) > 0 {
			authData = strings.Join(keys, "; ")
		}
		webInst := provider.NewGeminiWebProvider(p.Name, authData, p.DefaultModel, models)
		if w.db != nil {
			webInst.SetOnCookieUpdate(func(provName, newCookies string, cookieMap map[string]string) {
				pRec, err := w.db.GetProvider(provName)
				if err == nil && pRec != nil {
					pRec.APIKey = newCookies
					pRec.APIKeys = []string{newCookies}
					_ = w.db.SaveProvider(pRec)
				}
			})
		}
		inst = webInst
	case "gemini":
		inst = provider.NewGeminiProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
	case "anthropic":
		inst = provider.NewAnthropicProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
	default:
		inst = provider.NewOpenAIProviderWithKeys(p.Name, p.Type, p.BaseURL, keys, p.KeyStrategy, p.DefaultModel, models)
	}

	if p.ProxyEnabled && w.proxyPool != nil {
		proxyClient := w.proxyPool.NewHTTPClientForGroup(p.ProxyGroup, 90*time.Second)
		inst.SetHTTPClient(proxyClient)
	}

	w.providerManager.Register(inst, p.Priority)
}

// CancelWizard clears state
func (w *ProviderWizard) CancelWizard(userID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.sessions, userID)
}

// GetSession returns active session
func (w *ProviderWizard) GetSession(userID int64) (*WizardSession, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	s, ok := w.sessions[userID]
	return s, ok
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

