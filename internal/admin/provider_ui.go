package admin

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
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
		sb.WriteString("(Belum ada provider yang dikonfigurasi. Klik tombol di bawah untuk menambahkan!)\n\n")
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

			modelsStr := formatModelsSummary(p.Models, 3)

			provEntry := fmt.Sprintf("%d. %s <b>%s</b> (<code>%s</code> | Tipe: <code>%s</code>)\n"+
				"   • Default Model: <code>%s</code>\n"+
				"   • Models (%d): <code>%s</code>\n"+
				"   • Key Pool: <b>%d key</b> (Strategi: <code>%s</code>)\n",
				i+1, statusIcon, html.EscapeString(p.Name), html.EscapeString(p.ID), html.EscapeString(p.Type),
				html.EscapeString(p.DefaultModel),
				len(p.Models), html.EscapeString(modelsStr),
				keyCount, html.EscapeString(p.KeyStrategy))

			if p.ProxyEnabled {
				grp := p.ProxyGroup
				if grp == "" {
					grp = "default"
				}
				provEntry += fmt.Sprintf("   • Proxy Pool: 🟢 <b>Aktif</b> (Group: <code>%s</code>)\n", html.EscapeString(grp))
			} else {
				provEntry += "   • Proxy Pool: ⚪ <i>Direct / Off</i>\n"
			}
			if p.BaseURL != "" {
				provEntry += fmt.Sprintf("   • Base URL: <code>%s</code>\n", html.EscapeString(p.BaseURL))
			}
			provEntry += "\n"

			if sb.Len()+len(provEntry) > 3700 {
				sb.WriteString(fmt.Sprintf("<i>...dan %d provider lainnya (klik tombol provider di bawah).</i>\n\n", len(providers)-i))
				break
			}
			sb.WriteString(provEntry)
		}
	}

	sb.WriteString("💡 <i>Klik tombol provider di bawah untuk inspeksi & pengaturan cepat:</i>")
	return sb.String()
}

// ProviderMenuKeyboard builds dynamic interactive buttons for each registered provider
func (ui *ProviderUI) ProviderMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	providers, _ := ui.db.ListProviders()

	var rows []tele.Row

	// Per-provider quick buttons (max 2 per row)
	var provButtons []tele.Btn
	for _, p := range providers {
		status := "🟢"
		if !p.IsActive {
			status = "🔴"
		}
		label := fmt.Sprintf("%s %s", status, p.Name)
		btn := menu.Data(label, fmt.Sprintf("prov_view_%s", p.ID))
		provButtons = append(provButtons, btn)
	}

	for i := 0; i < len(provButtons); i += 2 {
		if i+1 < len(provButtons) {
			rows = append(rows, menu.Row(provButtons[i], provButtons[i+1]))
		} else {
			rows = append(rows, menu.Row(provButtons[i]))
		}
	}

	// Action buttons
	btnWizard := menu.Data("➕ Tambah Provider (Wizard)", "wiz_start")
	btnTestAll := menu.Data("🧪 Test Latensi Semua", "prov_test_all")
	btnEdit := menu.Data("✏️ Edit Wizard", "wiz_edit_start")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	rows = append(rows, menu.Row(btnWizard, btnEdit))
	rows = append(rows, menu.Row(btnTestAll))
	rows = append(rows, menu.Row(btnBack))

	menu.Inline(rows...)
	return menu
}

// RenderProviderDashboard renders a single provider's details and interactive control buttons
func (ui *ProviderUI) RenderProviderDashboard(p *storage.ProviderRecord) (string, *tele.ReplyMarkup) {
	statusStr := "🟢 <b>Aktif</b>"
	if !p.IsActive {
		statusStr = "🔴 <b>Nonaktif</b>"
	}

	keyCount := len(p.APIKeys)
	if keyCount == 0 && p.APIKey != "" {
		keyCount = 1
	}

	modelsStr := formatModelsSummary(p.Models, 6)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🤖 <b>DASHBOARD PROVIDER: %s</b>\n\n", html.EscapeString(p.Name)))
	sb.WriteString(fmt.Sprintf("• ID: <code>%s</code>\n", html.EscapeString(p.ID)))
	sb.WriteString(fmt.Sprintf("• Tipe Driver: <code>%s</code>\n", html.EscapeString(p.Type)))
	sb.WriteString(fmt.Sprintf("• Status: %s\n", statusStr))
	sb.WriteString(fmt.Sprintf("• Default Model: <code>%s</code>\n", html.EscapeString(p.DefaultModel)))
	sb.WriteString(fmt.Sprintf("• Model Terdaftar (%d): <code>%s</code>\n", len(p.Models), html.EscapeString(modelsStr)))
	if len(p.Models) > 6 {
		sb.WriteString("   💡 <i>Gunakan menu <code>/model</code> untuk melihat seluruh model secara terpaginasi.</i>\n")
	}
	sb.WriteString(fmt.Sprintf("• Key Pool: <b>%d API Key</b> (Strategi: <code>%s</code>)\n", keyCount, html.EscapeString(p.KeyStrategy)))

	if p.ProxyEnabled {
		grp := p.ProxyGroup
		if grp == "" {
			grp = "default"
		}
		sb.WriteString(fmt.Sprintf("• Upstream Proxy: 🟢 <b>Aktif</b> (Group: <code>%s</code>)\n", html.EscapeString(grp)))
	} else {
		sb.WriteString("• Upstream Proxy: ⚪ <i>Direct / Mati</i>\n")
	}

	if p.BaseURL != "" {
		sb.WriteString(fmt.Sprintf("• Base URL: <code>%s</code>\n", html.EscapeString(p.BaseURL)))
	}

	menu := &tele.ReplyMarkup{}
	btnTest := menu.Data("⚡ Test Ping & Latensi", fmt.Sprintf("prov_test_%s", p.ID))
	tglLabel := "🔴 Nonaktifkan"
	if !p.IsActive {
		tglLabel = "🟢 Aktifkan"
	}
	btnToggle := menu.Data(tglLabel, fmt.Sprintf("prov_tgl_%s", p.ID))
	btnEdit := menu.Data("✏️ Edit Provider", fmt.Sprintf("wiz_ed_pick_%s", p.ID))
	btnDel := menu.Data("🗑️ Hapus", fmt.Sprintf("wiz_ed_del_yes_%s", p.ID))
	btnBack := menu.Data("⬅️ Kembali ke Daftar", "menu_providers")

	menu.Inline(
		menu.Row(btnTest, btnToggle),
		menu.Row(btnEdit, btnDel),
		menu.Row(btnBack),
	)

	return sb.String(), menu
}

// HandleToggleActive toggles provider active state
func (ui *ProviderUI) HandleToggleActive(c tele.Context, provID string) error {
	p, err := ui.db.GetProvider(provID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	p.IsActive = !p.IsActive
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan status: %v", err))
	}
	ui.syncProviderToManager(p)

	txt, kb := ui.RenderProviderDashboard(p)
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

// HandleTestLatency runs a test request to the specified provider
func (ui *ProviderUI) HandleTestLatency(c tele.Context, provID string) error {
	p, err := ui.db.GetProvider(provID)
	if err != nil || p == nil {
		return c.Reply("❌ Provider tidak ditemukan.")
	}

	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Menguji koneksi %s...", p.Name)})

	inst, ok := ui.providerManager.Get(p.Name)
	if !ok {
		// Try sync first
		ui.syncProviderToManager(p)
		inst, ok = ui.providerManager.Get(p.Name)
		if !ok {
			return c.Reply(fmt.Sprintf("❌ Provider <b>%s</b> belum aktif atau tidak terdaftar di runtime manager.", html.EscapeString(p.Name)), tele.ModeHTML)
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	testModel := p.DefaultModel
	if testModel == "" && len(p.Models) > 0 {
		testModel = p.Models[0]
	}

	req := provider.ChatRequest{
		Model: testModel,
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "ping"},
		},
		MaxTokens:   5,
		Temperature: 0.1,
	}

	resp, err := inst.GenerateChat(ctx, req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return c.Reply(fmt.Sprintf("❌ <b>Uji Koneksi Gagal (%s)</b>\n• Latensi: <code>%d ms</code>\n• Error: <code>%s</code>", html.EscapeString(p.Name), latency, html.EscapeString(err.Error())), tele.ModeHTML)
	}

	replyPreview := strings.TrimSpace(resp.Content)
	if len(replyPreview) > 50 {
		replyPreview = replyPreview[:50] + "..."
	}

	return c.Reply(fmt.Sprintf("✅ <b>Uji Koneksi Berhasil! (%s)</b>\n• Model: <code>%s</code>\n• Latensi: <b>%d ms</b>\n• Respon Sample: <i>\"%s\"</i>", html.EscapeString(p.Name), html.EscapeString(testModel), latency, html.EscapeString(replyPreview)), tele.ModeHTML)
}

// HandleTestAllProviders runs concurrent ping latency tests across all active providers
func (ui *ProviderUI) HandleTestAllProviders(c tele.Context) error {
	providers, err := ui.db.ListProviders()
	if err != nil || len(providers) == 0 {
		return c.Reply("ℹ️ Belum ada provider yang terdaftar.")
	}

	_ = c.Respond(&tele.CallbackResponse{Text: "🧪 Memulai pengujian paralel seluruh provider..."})
	msg, _ := c.Bot().Send(c.Chat(), "⏳ <b>Sedang menguji latensi seluruh provider AI...</b>", tele.ModeHTML)

	type testResult struct {
		Name     string
		Model    string
		Latency  int64
		Success  bool
		ErrorMsg string
	}

	var results []testResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range providers {
		if !p.IsActive {
			continue
		}
		pCopy := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, ok := ui.providerManager.Get(pCopy.Name)
			if !ok {
				ui.syncProviderToManager(&pCopy)
				inst, ok = ui.providerManager.Get(pCopy.Name)
			}

			if !ok {
				mu.Lock()
				results = append(results, testResult{
					Name:     pCopy.Name,
					Success:  false,
					ErrorMsg: "Tidak terdaftar di manager",
				})
				mu.Unlock()
				return
			}

			testModel := pCopy.DefaultModel
			if testModel == "" && len(pCopy.Models) > 0 {
				testModel = pCopy.Models[0]
			}

			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()

			_, errGen := inst.GenerateChat(ctx, provider.ChatRequest{
				Model: testModel,
				Messages: []provider.ChatMessage{
					{Role: provider.RoleUser, Content: "hi"},
				},
				MaxTokens:   5,
				Temperature: 0.1,
			})
			latency := time.Since(start).Milliseconds()

			mu.Lock()
			if errGen != nil {
				results = append(results, testResult{
					Name:     pCopy.Name,
					Model:    testModel,
					Latency:  latency,
					Success:  false,
					ErrorMsg: errGen.Error(),
				})
			} else {
				results = append(results, testResult{
					Name:    pCopy.Name,
					Model:   testModel,
					Latency: latency,
					Success: true,
				})
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	var sb strings.Builder
	sb.WriteString("🧪 <b>HASIL UJI LATENSI PROVIDER AI</b>\n\n")

	for i, r := range results {
		if r.Success {
			sb.WriteString(fmt.Sprintf("%d. 🟢 <b>%s</b> (<code>%s</code>): <b>%d ms</b>\n", i+1, html.EscapeString(r.Name), html.EscapeString(r.Model), r.Latency))
		} else {
			sb.WriteString(fmt.Sprintf("%d. 🔴 <b>%s</b> (<code>%s</code>): <i>Gagal (%d ms)</i> - <code>%s</code>\n", i+1, html.EscapeString(r.Name), html.EscapeString(r.Model), r.Latency, html.EscapeString(r.ErrorMsg)))
		}
	}

	menu := &tele.ReplyMarkup{}
	btnBack := menu.Data("⬅️ Kembali ke Menu Provider", "menu_providers")
	menu.Inline(menu.Row(btnBack))

	if msg != nil {
		_, _ = c.Bot().Edit(msg, sb.String(), tele.ModeHTML, menu)
		return nil
	}
	return c.Send(sb.String(), menu, tele.ModeHTML)
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

	for _, k := range p.APIKeys {
		if k == key {
			return c.Reply("⚠️ API Key ini sudah ada di dalam pool provider tersebut.")
		}
	}

	p.APIKeys = append(p.APIKeys, key)
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan API key: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("🔑 API Key berhasil ditambahkan ke pool provider <b>%s</b>!\nTotal Key saat ini: <b>%d</b> (Strategi: <code>%s</code>)", html.EscapeString(p.Name), len(p.APIKeys), html.EscapeString(p.KeyStrategy)), tele.ModeHTML)
}

// HandleSetKeys processes `/setkeys <provider_id> <key1,key2,...>`
func (ui *ProviderUI) HandleSetKeys(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setkeys 9router sk-key1,sk-key2,sk-key3</code>", tele.ModeHTML)
	}

	id := args[0]
	keysRaw := strings.Join(args[1:], " ")

	var newKeys []string
	for _, k := range strings.Split(keysRaw, ",") {
		clean := strings.TrimSpace(k)
		if clean != "" {
			newKeys = append(newKeys, clean)
		}
	}

	if len(newKeys) == 0 {
		return c.Reply("⚠️ Daftar key kosong.")
	}

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.APIKeys = newKeys
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memperbarui pool API key: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("🔑 Pool API key untuk provider <b>%s</b> berhasil disetel!\nTotal Key: <b>%d key</b> (Strategi: <code>%s</code>)", html.EscapeString(p.Name), len(p.APIKeys), html.EscapeString(p.KeyStrategy)), tele.ModeHTML)
}

// HandleDelKey processes `/delkey <provider_id> <index|key>`
func (ui *ProviderUI) HandleDelKey(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh:\n• <code>/delkey 9router 1</code> (hapus key index ke-1)\n• <code>/delkey 9router sk-xxxx</code> (hapus by key value)", tele.ModeHTML)
	}

	id := args[0]
	target := strings.TrimSpace(args[1])

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	idx, errIdx := strconv.Atoi(target)
	deleted := false

	if errIdx == nil && idx > 0 && idx <= len(p.APIKeys) {
		p.APIKeys = append(p.APIKeys[:idx-1], p.APIKeys[idx:]...)
		deleted = true
	} else {
		var filtered []string
		for _, k := range p.APIKeys {
			if k == target {
				deleted = true
			} else {
				filtered = append(filtered, k)
			}
		}
		p.APIKeys = filtered
	}

	if !deleted {
		return c.Reply("⚠️ API Key tidak ditemukan dalam pool provider tersebut.")
	}

	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan perubahan: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("🗑️ API Key berhasil dihapus dari pool provider <b>%s</b>!\nSisa Key: <b>%d key</b>", html.EscapeString(p.Name), len(p.APIKeys)), tele.ModeHTML)
}

// HandleSetKeyStrategy processes `/keystrategy <provider_id> <round-robin|failover|random>`
func (ui *ProviderUI) HandleSetKeyStrategy(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/keystrategy 9router round-robin</code>\nPilihan: <code>round-robin</code>, <code>failover</code>, <code>random</code>", tele.ModeHTML)
	}

	id := args[0]
	strat := strings.ToLower(args[1])

	if strat != "round-robin" && strat != "failover" && strat != "random" {
		return c.Reply("⚠️ Strategi rotasi tidak valid! Pilihan: <code>round-robin</code>, <code>failover</code>, <code>random</code>", tele.ModeHTML)
	}

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.KeyStrategy = strat
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan strategi: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("🔄 Strategi rotasi multi-key untuk provider <b>%s</b> berhasil diubah menjadi: <code>%s</code>", html.EscapeString(p.Name), strat), tele.ModeHTML)
}

// HandleSetModels processes `/setmodels <provider_id> <m1,m2,...>`
func (ui *ProviderUI) HandleSetModels(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setmodels 9router gpt-4o,gpt-4o-mini,claude-3-5-sonnet</code>", tele.ModeHTML)
	}

	id := args[0]
	modelsRaw := strings.Join(args[1:], " ")

	var models []string
	for _, m := range strings.Split(modelsRaw, ",") {
		clean := strings.TrimSpace(m)
		if clean != "" {
			models = append(models, clean)
		}
	}

	p, err := ui.db.GetProvider(id)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider dengan ID '%s' tidak ditemukan", html.EscapeString(id)))
	}

	p.Models = models
	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan daftar model: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)
	return c.Reply(fmt.Sprintf("🤖 Daftar model untuk provider <b>%s</b> berhasil diperbarui!\nTotal model: <b>%d</b>", html.EscapeString(p.Name), len(models)), tele.ModeHTML)
}

// HandleFetchModels fetches models dynamically from `/v1/models` endpoint
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

	key := p.APIKey
	if len(p.APIKeys) > 0 {
		key = p.APIKeys[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	models, errDetect := provider.FetchRemoteModels(ctx, p.Type, p.BaseURL, key, nil)
	if errDetect != nil || len(models) == 0 {
		return c.Reply(fmt.Sprintf("⚠️ Gagal mendeteksi model secara otomatis: %v\nSilakan masukkan model secara manual via <code>/setmodels %s &lt;m1,m2&gt;</code>", errDetect, p.ID), tele.ModeHTML)
	}

	p.Models = models
	if p.DefaultModel == "" && len(models) > 0 {
		p.DefaultModel = models[0]
	}

	if err := ui.db.SaveProvider(p); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan hasil deteksi model: %v", html.EscapeString(err.Error())))
	}

	ui.syncProviderToManager(p)

	preview := strings.Join(models, ", ")
	if len(preview) > 300 {
		preview = preview[:300] + "... (dan model lainnya)"
	}

	return c.Reply(fmt.Sprintf("✅ <b>%d model berhasil dideteksi dari provider %s!</b>\n\n• Model default: <code>%s</code>\n• Daftar model: <code>%s</code>", len(models), html.EscapeString(p.Name), html.EscapeString(p.DefaultModel), html.EscapeString(preview)), tele.ModeHTML)
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

	ui.providerManager.RegisterWithID(p.ID, inst, p.Priority)
}

func formatModelsSummary(models []string, maxDisplay int) string {
	if len(models) == 0 {
		return "(default only)"
	}
	if len(models) <= maxDisplay {
		return strings.Join(models, ", ")
	}
	sample := strings.Join(models[:maxDisplay], ", ")
	return fmt.Sprintf("%s, ... (+%d model lainnya)", sample, len(models)-maxDisplay)
}
