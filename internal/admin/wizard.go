package admin

import (
	"context"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/provider"
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
)

type WizardSession struct {
	Step           WizardStep
	ID             string
	Name           string
	Type           string
	BaseURL        string
	APIKeys        []string
	DetectedModels []string
	DefaultModel   string
	UpdatedAt      time.Time
}

type ProviderWizard struct {
	mu              sync.RWMutex
	sessions        map[int64]*WizardSession
	db              *storage.DB
	providerManager *provider.Manager
	bot             *tele.Bot
}

func NewProviderWizard(db *storage.DB, pm *provider.Manager, bot *tele.Bot) *ProviderWizard {
	return &ProviderWizard{
		sessions:        make(map[int64]*WizardSession),
		db:              db,
		providerManager: pm,
		bot:             bot,
	}
}

// StartWizard launches the interactive setup wizard
func (w *ProviderWizard) StartWizard(c tele.Context) error {
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
	btnClaude := menu.Data("🧠 Anthropic Claude", "wiz_type_anthropic")
	btnOllama := menu.Data("🦙 Ollama Local", "wiz_type_ollama")
	btnCustom := menu.Data("⚙️ Custom Endpoint", "wiz_type_custom")
	btnCancel := menu.Data("❌ Batal", "wiz_cancel")

	menu.Inline(
		menu.Row(btn9Router, btnOpenAI),
		menu.Row(btnDeepSeek, btnGroq),
		menu.Row(btnGemini, btnClaude),
		menu.Row(btnOllama, btnCustom),
		menu.Row(btnCancel),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleTypeSelect processes provider type choice
func (w *ProviderWizard) HandleTypeSelect(c tele.Context, pType string) error {
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

// HandleTextMessage handles text sent during wizard state
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
		w.CancelWizard(userID)
		return true, c.Reply("❌ Setup wizard dibatalkan.")
	}

	switch sess.Step {
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
		// Manual model text entry
		sess.DefaultModel = msgText
		return true, w.finishWizard(c, sess)
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
	sb.WriteString(fmt.Sprintf("🎉 <b>DETEKSI MODEL BERHASIL!</b>\n\n"))
	sb.WriteString(fmt.Sprintf("Ditemukan <b>%d model</b> aktif di endpoint <code>%s</code>:\n", len(sess.DetectedModels), html.EscapeString(sess.BaseURL)))

	// Display sample models
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

	// Create buttons for top models
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

// HandleModelSelect processes model chosen from button
func (w *ProviderWizard) HandleModelSelect(c tele.Context, modelIndex int) error {
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

	// Register to in-memory manager
	var inst provider.Provider
	switch sess.Type {
	case "gemini":
		inst = provider.NewGeminiProviderWithKeys(record.Name, record.APIKeys, record.KeyStrategy, record.DefaultModel, record.Models)
	case "anthropic":
		inst = provider.NewAnthropicProviderWithKeys(record.Name, record.APIKeys, record.KeyStrategy, record.DefaultModel, record.Models)
	default:
		inst = provider.NewOpenAIProviderWithKeys(record.Name, record.Type, record.BaseURL, record.APIKeys, record.KeyStrategy, record.DefaultModel, record.Models)
	}
	w.providerManager.Register(inst, record.Priority)

	// Clean wizard session
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
