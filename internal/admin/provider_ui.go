package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type ProviderUI struct {
	db              *storage.DB
	providerManager *provider.Manager
}

func NewProviderUI(db *storage.DB, pm *provider.Manager) *ProviderUI {
	return &ProviderUI{db: db, providerManager: pm}
}

// RenderProvidersList returns a summary list of all configured providers in HTML format
func (ui *ProviderUI) RenderProvidersList() string {
	providers, err := ui.db.ListProviders()
	if err != nil {
		return fmt.Sprintf("❌ Error mengambil data provider: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("🤖 <b>DAFTAR PROVIDER AI TERDAFTAR</b>\n\n")

	if len(providers) == 0 {
		sb.WriteString("(Belum ada provider yang dikonfigurasi)\n\n")
	} else {
		for i, p := range providers {
			statusIcon := "🟢"
			if !p.IsActive {
				statusIcon = "🔴"
			}
			maskedKey := "(belum diset)"
			if len(p.APIKey) > 8 {
				maskedKey = p.APIKey[:4] + "..." + p.APIKey[len(p.APIKey)-4:]
			}

			sb.WriteString(fmt.Sprintf("%d. %s <b>%s</b> (Tipe: <code>%s</code>)\n", i+1, statusIcon, html.EscapeString(p.Name), html.EscapeString(p.Type)))
			sb.WriteString(fmt.Sprintf("   • ID: <code>%s</code>\n", html.EscapeString(p.ID)))
			sb.WriteString(fmt.Sprintf("   • Default Model: <code>%s</code>\n", html.EscapeString(p.DefaultModel)))
			sb.WriteString(fmt.Sprintf("   • Base URL: <code>%s</code>\n", html.EscapeString(p.BaseURL)))
			sb.WriteString(fmt.Sprintf("   • API Key: <code>%s</code>\n\n", html.EscapeString(maskedKey)))
		}
	}

	sb.WriteString("📋 <b>Perintah Manajemen Provider:</b>\n")
	sb.WriteString("• <code>/addprovider &lt;id&gt; &lt;name&gt; &lt;type&gt; [base_url] [default_model]</code>\n")
	sb.WriteString("  <i>(Pilihan type: 9router, openai, gemini, anthropic, groq, deepseek, ollama, custom)</i>\n")
	sb.WriteString("• <code>/setkey &lt;provider_id&gt; &lt;api_key&gt;</code>\n")
	sb.WriteString("• <code>/setmodel &lt;provider_id&gt; &lt;model_name&gt;</code>\n")
	sb.WriteString("• <code>/delprovider &lt;provider_id&gt;</code>\n\n")
	sb.WriteString("<b>Contoh Setup 9Router:</b>\n")
	sb.WriteString("<code>/addprovider 9router \"9Router Gateway\" 9router https://api.9router.com/v1 gpt-4o-mini</code>\n")
	sb.WriteString("<code>/setkey 9router sk-xxxxxxxxxxxx</code>\n")

	return sb.String()
}

// HandleAddProvider processes `/addprovider`
func (ui *ProviderUI) HandleAddProvider(c tele.Context) error {
	args := c.Args()
	if len(args) < 3 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/addprovider 9router \"9Router Gateway\" 9router https://api.9router.com/v1 gpt-4o-mini</code>", tele.ModeHTML)
	}

	id := args[0]
	name := args[1]
	pType := strings.ToLower(args[2])
	baseURL := ""
	defaultModel := ""

	if len(args) >= 4 {
		baseURL = args[3]
	}
	if len(args) >= 5 {
		defaultModel = args[4]
	}

	record := &storage.ProviderRecord{
		ID:           id,
		Name:         name,
		Type:         pType,
		BaseURL:      baseURL,
		DefaultModel: defaultModel,
		IsActive:     true,
		Priority:     10,
	}

	if err := ui.db.SaveProvider(record); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan provider: %v", html.EscapeString(err.Error())))
	}

	// Register in memory provider manager
	ui.syncProviderToManager(record)

	return c.Reply(fmt.Sprintf("✅ Provider <b>%s</b> (<code>%s</code>) berhasil ditambahkan!\nJangan lupa set API Key dengan:\n<code>/setkey %s &lt;api_key&gt;</code>", html.EscapeString(name), html.EscapeString(id), html.EscapeString(id)), tele.ModeHTML)
}

// HandleSetKey processes `/setkey`
func (ui *ProviderUI) HandleSetKey(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setkey 9router sk-xxxxxxxx</code>", tele.ModeHTML)
	}

	id := args[0]
	key := args[1]

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.APIKey = key
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan API key: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ API Key untuk provider <b>%s</b> berhasil diperbarui!", html.EscapeString(p.Name)), tele.ModeHTML)
}

// HandleSetModel processes `/setmodel`
func (ui *ProviderUI) HandleSetModel(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setmodel 9router gpt-4o-mini</code>", tele.ModeHTML)
	}

	id := args[0]
	model := args[1]

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.DefaultModel = model
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memperbarui model: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Default model untuk provider <b>%s</b> diset ke <code>%s</code>!", html.EscapeString(p.Name), html.EscapeString(model)), tele.ModeHTML)
}

func (ui *ProviderUI) syncProviderToManager(p *storage.ProviderRecord) {
	if !p.IsActive {
		ui.providerManager.Unregister(p.Name)
		return
	}

	var inst provider.Provider
	switch p.Type {
	case "gemini":
		inst = provider.NewGeminiProvider(p.Name, p.APIKey, p.DefaultModel)
	case "anthropic":
		inst = provider.NewAnthropicProvider(p.Name, p.APIKey, p.DefaultModel)
	default: // 9router, openai, groq, deepseek, ollama, custom
		inst = provider.NewOpenAIProvider(p.Name, p.Type, p.BaseURL, p.APIKey, p.DefaultModel)
	}

	ui.providerManager.Register(inst, p.Priority)
}
