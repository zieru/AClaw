package admin

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type ProviderUI struct {
	db              *storage.DB
	providerManager *provider.Manager
	proxyPool       *proxy.Pool
}

func NewProviderUI(db *storage.DB, pm *provider.Manager, pool *proxy.Pool) *ProviderUI {
	return &ProviderUI{db: db, providerManager: pm, proxyPool: pool}
}

// RenderProvidersList returns a summary list of all configured providers in HTML format
func (ui *ProviderUI) RenderProvidersList() string {
	providers, err := ui.db.ListProviders()
	if err != nil {
		return fmt.Sprintf("❌ Error mengambil data provider: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("🤖 <b>DAFTAR PROVIDER AI (9ROUTER ENGINE)</b>\n\n")

	if len(providers) == 0 {
		sb.WriteString("(Belum ada provider yang dikonfigurasi)\n\n")
	} else {
		for i, p := range providers {
			statusIcon := "🟢"
			if !p.IsActive {
				statusIcon = "🔴"
			}

			keyCount := len(p.APIKeys)
			if keyCount == 0 && p.APIKey != "" {
				keyCount = 1
			}

			modelsStr := "(default only)"
			if len(p.Models) > 0 {
				modelsStr = strings.Join(p.Models, ", ")
			}

			sb.WriteString(fmt.Sprintf("%d. %s <b>%s</b> (<code>%s</code> | Tipe: <code>%s</code>)\n", i+1, statusIcon, html.EscapeString(p.Name), html.EscapeString(p.ID), html.EscapeString(p.Type)))
			sb.WriteString(fmt.Sprintf("   • Default Model: <code>%s</code>\n", html.EscapeString(p.DefaultModel)))
			sb.WriteString(fmt.Sprintf("   • Models: <code>%s</code>\n", html.EscapeString(modelsStr)))
			sb.WriteString(fmt.Sprintf("   • Key Pool: <b>%d key</b> (Strategi: <code>%s</code>)\n", keyCount, html.EscapeString(p.KeyStrategy)))
			if p.ProxyEnabled {
				grp := p.ProxyGroup
				if grp == "" {
					grp = "default"
				}
				sb.WriteString(fmt.Sprintf("   • Proxy Pool: 🟢 <b>Aktif</b> (Group: <code>%s</code>)\n", html.EscapeString(grp)))
			} else {
				sb.WriteString("   • Proxy Pool: ⚪ <i>Direct / Off</i>\n")
			}
			if p.BaseURL != "" {
				sb.WriteString(fmt.Sprintf("   • Base URL: <code>%s</code>\n", html.EscapeString(p.BaseURL)))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("📋 <b>Perintah Manajemen Provider & Proxy:</b>\n")
	sb.WriteString("• <code>/wizard</code> - Interactive Setup Wizard provider baru\n")
	sb.WriteString("• <code>/editprovider [id]</code> - Interactive Edit Wizard provider\n")
	sb.WriteString("• <code>/setproviderproxy &lt;id&gt; &lt;group|off&gt;</code> - Pasang proxy pool ke provider\n")
	sb.WriteString("• <code>/addkey &lt;provider_id&gt; &lt;api_key&gt;</code> - Tambah key ke pool rotasi\n")
	sb.WriteString("• <code>/setkeys &lt;provider_id&gt; &lt;key1,key2,...&gt;</code> - Set multiple keys\n")
	sb.WriteString("• <code>/delkey &lt;provider_id&gt; &lt;index|key&gt;</code> - Hapus key dari pool\n")
	sb.WriteString("• <code>/keystrategy &lt;provider_id&gt; &lt;round-robin|failover|random&gt;</code>\n")
	sb.WriteString("• <code>/setmodels &lt;provider_id&gt; &lt;m1,m2,...&gt;</code> - Daftarkan model yang didukung\n")
	sb.WriteString("• <code>/fetchmodels &lt;provider_id&gt;</code> - Deteksi model otomatis dari /models\n")
	sb.WriteString("• <code>/combos</code> - Lihat & atur multi-provider combo chains\n")

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
		Models:       []string{defaultModel},
		KeyStrategy:  "round-robin",
		Strategy:     "failsafe",
		IsActive:     true,
		Priority:     10,
	}

	if err := ui.db.SaveProvider(record); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan provider: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(record)
	return c.Reply(fmt.Sprintf("✅ Provider <b>%s</b> (<code>%s</code>) berhasil ditambahkan!\nTambahkan API key dengan:\n<code>/addkey %s &lt;api_key&gt;</code>", html.EscapeString(name), html.EscapeString(id), html.EscapeString(id)), tele.ModeHTML)
}

// HandleAddKey processes `/addkey <provider_id> <api_key>`
func (ui *ProviderUI) HandleAddKey(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/addkey 9router sk-xxxxxxxx</code>", tele.ModeHTML)
	}

	id := args[0]
	key := strings.TrimSpace(args[1])

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	// Add key avoiding duplicates
	for _, k := range p.APIKeys {
		if k == key {
			return c.Reply("⚠️ API Key ini sudah ada di dalam pool provider tersebut.")
		}
	}
	p.APIKeys = append(p.APIKeys, key)
	p.APIKey = p.APIKeys[0]

	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan API key: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Berhasil menambahkan key baru ke provider <b>%s</b>!\nTotal pool: <b>%d key</b>", html.EscapeString(p.Name), len(p.APIKeys)), tele.ModeHTML)
}

// HandleSetKeys processes `/setkeys <provider_id> <key1,key2,...>`
func (ui *ProviderUI) HandleSetKeys(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setkeys 9router sk-key1,sk-key2,sk-key3</code>", tele.ModeHTML)
	}

	id := args[0]
	rawKeys := strings.Split(args[1], ",")

	var cleanKeys []string
	for _, k := range rawKeys {
		k = strings.TrimSpace(k)
		if k != "" {
			cleanKeys = append(cleanKeys, k)
		}
	}

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.APIKeys = cleanKeys
	if len(cleanKeys) > 0 {
		p.APIKey = cleanKeys[0]
	}

	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan key: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Key pool untuk provider <b>%s</b> berhasil diset!\nTotal: <b>%d key</b>", html.EscapeString(p.Name), len(p.APIKeys)), tele.ModeHTML)
}

// HandleDelKey processes `/delkey <provider_id> <key_or_index>`
func (ui *ProviderUI) HandleDelKey(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/delkey 9router 1</code> atau <code>/delkey 9router sk-xxxx</code>", tele.ModeHTML)
	}

	id := args[0]
	target := strings.TrimSpace(args[1])

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	var updated []string
	if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(p.APIKeys) {
		// Index based removal (1-based)
		for i, k := range p.APIKeys {
			if i+1 != idx {
				updated = append(updated, k)
			}
		}
	} else {
		// Exact string matching
		for _, k := range p.APIKeys {
			if k != target {
				updated = append(updated, k)
			}
		}
	}

	p.APIKeys = updated
	if len(updated) > 0 {
		p.APIKey = updated[0]
	} else {
		p.APIKey = ""
	}

	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menghapus key: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Key berhasil dihapus dari provider <b>%s</b>! Sisa: <b>%d key</b>", html.EscapeString(p.Name), len(p.APIKeys)), tele.ModeHTML)
}

// HandleSetKey processes legacy `/setkey`
func (ui *ProviderUI) HandleSetKey(c tele.Context) error {
	return ui.HandleAddKey(c)
}

// HandleSetKeyStrategy processes `/keystrategy <provider_id> <round-robin|failover|random>`
func (ui *ProviderUI) HandleSetKeyStrategy(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/keystrategy 9router round-robin</code>\n(Pilihan: <code>round-robin</code>, <code>failover</code>, <code>random</code>)", tele.ModeHTML)
	}

	id := args[0]
	strategy := strings.ToLower(args[1])
	if strategy != "round-robin" && strategy != "failover" && strategy != "random" {
		return c.Reply("⚠️ Strategi tidak valid. Pilih: <code>round-robin</code>, <code>failover</code>, atau <code>random</code>", tele.ModeHTML)
	}

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.KeyStrategy = strategy
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan strategi: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Strategi rotasi key untuk provider <b>%s</b> diubah menjadi <code>%s</code>!", html.EscapeString(p.Name), strategy), tele.ModeHTML)
}

// HandleSetModels processes `/setmodels <provider_id> <model1,model2,...>`
func (ui *ProviderUI) HandleSetModels(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setmodels 9router gpt-4o,gpt-4o-mini,deepseek-chat,claude-3-5-sonnet</code>", tele.ModeHTML)
	}

	id := args[0]
	rawModels := strings.Split(args[1], ",")

	var cleanModels []string
	for _, m := range rawModels {
		m = strings.TrimSpace(m)
		if m != "" {
			cleanModels = append(cleanModels, m)
		}
	}

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.Models = cleanModels
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan models: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Model list untuk provider <b>%s</b> berhasil diperbarui:\n<code>%s</code>", html.EscapeString(p.Name), html.EscapeString(strings.Join(cleanModels, ", "))), tele.ModeHTML)
}

// HandleAddModel processes `/addmodel <provider_id> <model_name>`
func (ui *ProviderUI) HandleAddModel(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/addmodel 9router claude-3-5-sonnet</code>", tele.ModeHTML)
	}

	id := args[0]
	model := strings.TrimSpace(args[1])

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	for _, m := range p.Models {
		if strings.EqualFold(m, model) {
			return c.Reply("⚠️ Model ini sudah ada di dalam daftar.")
		}
	}

	p.Models = append(p.Models, model)
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan model: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("✅ Model <code>%s</code> berhasil ditambahkan ke provider <b>%s</b>!", html.EscapeString(model), html.EscapeString(p.Name)), tele.ModeHTML)
}

// HandleSetModel processes `/setmodel`
func (ui *ProviderUI) HandleSetModel(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setmodel 9router gpt-4o-mini</code>", tele.ModeHTML)
	}

	id := args[0]
	model := strings.TrimSpace(args[1])

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

// HandleFetchModels processes `/fetchmodels <provider_id>`
func (ui *ProviderUI) HandleFetchModels(c tele.Context) error {
	args := c.Args()
	if len(args) < 1 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/fetchmodels 9router</code>", tele.ModeHTML)
	}

	id := args[0]
	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	_ = c.Notify(tele.Typing)
	_ = c.Reply(fmt.Sprintf("🔍 <i>Mendeteksi model otomatis dari %s di <code>%s/models</code>...</i>", html.EscapeString(p.Name), html.EscapeString(p.BaseURL)), tele.ModeHTML)

	firstKey := p.APIKey
	if len(p.APIKeys) > 0 {
		firstKey = p.APIKeys[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	models, err := provider.FetchRemoteModels(ctx, p.Type, p.BaseURL, firstKey, nil)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mendeteksi model: %v", html.EscapeString(err.Error())))
	}

	if len(models) == 0 {
		return c.Reply("⚠️ Tidak ada model yang ditemukan dari endpoint tersebut.")
	}

	p.Models = models
	if p.DefaultModel == "" || !contains(models, p.DefaultModel) {
		p.DefaultModel = models[0]
	}

	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan data: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>BERHASIL MENDETEKSI %d MODEL UNTUK %s!</b>\n\n", len(models), html.EscapeString(p.Name)))
	sb.WriteString(fmt.Sprintf("⭐ <b>Default Model:</b> <code>%s</code>\n\n", html.EscapeString(p.DefaultModel)))
	sb.WriteString("<b>Daftar Model Tersedia:</b>\n")
	displayCount := len(models)
	if displayCount > 15 {
		displayCount = 15
	}
	for i := 0; i < displayCount; i++ {
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, html.EscapeString(models[i])))
	}
	if len(models) > 15 {
		sb.WriteString(fmt.Sprintf("<i>...dan %d model lainnya.</i>\n", len(models)-15))
	}

	return c.Reply(sb.String(), tele.ModeHTML)
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, val) {
			return true
		}
	}
	return false
}

func (ui *ProviderUI) ProviderMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnWizard := menu.Data("🧙‍♂️ Setup Wizard", "wiz_start")
	btnEdit := menu.Data("✏️ Edit Provider", "wiz_edit_start")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	menu.Inline(
		menu.Row(btnWizard, btnEdit),
		menu.Row(btnBack),
	)
	return menu
}

// HandleDelProvider processes `/delprovider <id>`
func (ui *ProviderUI) HandleDelProvider(c tele.Context) error {
	args := c.Args()
	if len(args) < 1 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/delprovider 9router</code>", tele.ModeHTML)
	}

	id := args[0]
	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	if err := ui.db.DeleteProvider(id); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menghapus provider: %v", html.EscapeString(err.Error())))
	}

	ui.providerManager.Unregister(p.Name)
	return c.Reply(fmt.Sprintf("🗑️ Provider <b>%s</b> (<code>%s</code>) berhasil dihapus!", html.EscapeString(p.Name), html.EscapeString(id)), tele.ModeHTML)
}

// HandleSetProviderProxy processes `/setproviderproxy <id> <group|off>`
func (ui *ProviderUI) HandleSetProviderProxy(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh:\n• <code>/setproviderproxy dahl dahl_proxies</code>\n• <code>/setproviderproxy dahl default</code>\n• <code>/setproviderproxy dahl off</code>", tele.ModeHTML)
	}

	id := args[0]
	groupOrOff := strings.ToLower(args[1])

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	if groupOrOff == "off" || groupOrOff == "false" || groupOrOff == "0" || groupOrOff == "direct" {
		p.ProxyEnabled = false
		p.ProxyGroup = ""
		if err := ui.db.SaveProvider(p); err != nil {
			return c.Reply(fmt.Sprintf("❌ Gagal menyimpan konfigurasi: %v", html.EscapeString(err.Error())))
		}
		ui.syncProviderToManager(p)
		return c.Reply(fmt.Sprintf("⚪ Proxy Pool untuk provider <b>%s</b> telah <b>DINONAKTIFKAN</b> (Direct Connection).", html.EscapeString(p.Name)), tele.ModeHTML)
	}

	p.ProxyEnabled = true
	p.ProxyGroup = groupOrOff
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan konfigurasi: %v", html.EscapeString(err.Error())))
	}
	ui.syncProviderToManager(p)

	return c.Reply(fmt.Sprintf("🌐 Proxy Pool untuk provider <b>%s</b> berhasil <b>DIAKTIFKAN</b>!\n• Group Proxy: <code>%s</code>", html.EscapeString(p.Name), html.EscapeString(p.ProxyGroup)), tele.ModeHTML)
}

func (ui *ProviderUI) syncProviderToManager(p *storage.ProviderRecord) {
	if !p.IsActive {
		ui.providerManager.Unregister(p.Name)
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
	case "gemini":
		inst = provider.NewGeminiProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
	case "anthropic":
		inst = provider.NewAnthropicProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
	default: // 9router, openai, groq, deepseek, ollama, custom
		inst = provider.NewOpenAIProviderWithKeys(p.Name, p.Type, p.BaseURL, keys, p.KeyStrategy, p.DefaultModel, models)
	}

	if p.ProxyEnabled && ui.proxyPool != nil {
		proxyClient := ui.proxyPool.NewHTTPClientForGroup(p.ProxyGroup, 90*time.Second)
		inst.SetHTTPClient(proxyClient)
	}

	ui.providerManager.Register(inst, p.Priority)
}
