package admin

import (
	"context"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/search"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

// SearchUIHandler manages Telegram interactive controls for the multi-provider search engine
type SearchUIHandler struct {
	cfg                 *config.AppConfig
	db                  *storage.DB
	waitingTavilyKey    sync.Map
	waitingFirecrawlKey sync.Map
	waitingFirecrawlURL sync.Map
	waitingTestQuery    sync.Map
}

// NewSearchUIHandler creates a new SearchUIHandler instance
func NewSearchUIHandler(cfg *config.AppConfig, db *storage.DB) *SearchUIHandler {
	return &SearchUIHandler{
		cfg: cfg,
		db:  db,
	}
}

func (h *SearchUIHandler) persistConfig() {
	if h.db == nil {
		return
	}
	eng := search.GetGlobalEngine()
	_ = search.SaveDynamicConfig(h.db, eng.Config())
}

// HandleSearchDashboard renders the Search Engine Control Plane dashboard
func (h *SearchUIHandler) HandleSearchDashboard(c tele.Context) error {
	eng := search.GetGlobalEngine()
	sCfg := eng.Config()

	var sb strings.Builder
	sb.WriteString("🔍 <b>Web Search Multi-Provider Control Plane</b>\n\n")

	activeProv := strings.ToUpper(sCfg.Provider)
	if activeProv == "" {
		activeProv = "AUTO"
	}
	sb.WriteString(fmt.Sprintf("• Mode Aktif: <code>%s</code>\n", html.EscapeString(activeProv)))

	stratLabel := "🛡️ Fallback (Priority Failover)"
	if strings.ToLower(sCfg.Strategy) == "roundrobin" {
		stratLabel = "⚖️ Round-Robin (Rotasi Beban Berimbang)"
	}
	sb.WriteString(fmt.Sprintf("• Strategi: <b>%s</b>\n", stratLabel))

	failoverLabel := "❌ Nonaktif"
	if sCfg.FallbackEnabled {
		failoverLabel = "✅ Aktif (Otomatis beralih jika limit/error)"
	}
	sb.WriteString(fmt.Sprintf("• Failover Cadangan: <b>%s</b>\n", failoverLabel))
	sb.WriteString(fmt.Sprintf("• Batas Dokumen Hasil: <b>%d dokumen</b>\n\n", sCfg.MaxResults))

	sb.WriteString("<b>Status Ketersediaan Provider:</b>\n")
	tavilyStatus := "⚪ Belum diset"
	if sCfg.Tavily.APIKey != "" {
		depthBadge := "⚡ Basic"
		if strings.ToLower(sCfg.Tavily.SearchDepth) == "advanced" {
			depthBadge = "🔬 Advanced"
		}
		tavilyStatus = fmt.Sprintf("🟢 Terkonfigurasi (<code>%s</code> | %s)", maskAPIKey(sCfg.Tavily.APIKey), depthBadge)
	}
	sb.WriteString(fmt.Sprintf("• <b>Tavily AI Search</b>: %s\n", tavilyStatus))

	firecrawlStatus := "⚪ Belum diset"
	if sCfg.Firecrawl.APIKey != "" {
		firecrawlStatus = fmt.Sprintf("🟢 Terkonfigurasi (<code>%s</code>)", maskAPIKey(sCfg.Firecrawl.APIKey))
	}
	sb.WriteString(fmt.Sprintf("• <b>Firecrawl Web</b>: %s\n", firecrawlStatus))
	if sCfg.Firecrawl.BaseURL != "" && sCfg.Firecrawl.BaseURL != "https://api.firecrawl.dev" {
		sb.WriteString(fmt.Sprintf("  └ <i>Custom URL: <code>%s</code></i>\n", html.EscapeString(sCfg.Firecrawl.BaseURL)))
	}
	sb.WriteString("• <b>DuckDuckGo</b>: 🟢 Siap (Fallback Zero-Auth)\n\n")

	sb.WriteString("💡 <i>Pilih provider, ubah strategi, atau atur API key & pengaturan lanjutan menggunakan tombol di bawah:</i>")

	menu := h.BuildKeyboard(sCfg)
	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// BuildKeyboard creates inline buttons for provider selection, strategy toggles, and keys
func (h *SearchUIHandler) BuildKeyboard(sCfg config.SearchConfig) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}

	currProv := strings.ToLower(strings.TrimSpace(sCfg.Provider))
	if currProv == "" {
		currProv = "auto"
	}

	formatBtn := func(label, val string) string {
		if currProv == val {
			return fmt.Sprintf("🔘 %s", label)
		}
		return label
	}

	btnAuto := menu.Data(formatBtn("Auto", "auto"), "search_prov_auto")
	btnTavily := menu.Data(formatBtn("Tavily", "tavily"), "search_prov_tavily")
	btnFirecrawl := menu.Data(formatBtn("Firecrawl", "firecrawl"), "search_prov_firecrawl")
	btnDDG := menu.Data(formatBtn("DuckDuckGo", "duckduckgo"), "search_prov_duckduckgo")

	// Strategy toggle button (shows what it will switch to)
	stratBtnLabel := "⚖️ Ganti ke Round-Robin"
	if strings.ToLower(sCfg.Strategy) == "roundrobin" {
		stratBtnLabel = "🛡️ Ganti ke Fallback"
	}
	btnToggleStrat := menu.Data(stratBtnLabel, "search_toggle_strategy")

	// Failover toggle button
	fbBtnLabel := "🔄 Failover: ✅ ON"
	if !sCfg.FallbackEnabled {
		fbBtnLabel = "🔄 Failover: ❌ OFF"
	}
	btnToggleFB := menu.Data(fbBtnLabel, "search_toggle_fallback")

	btnSetTavily := menu.Data("🔑 Set Tavily Key", "search_set_tavily")
	btnSetFirecrawl := menu.Data("🔑 Set Firecrawl Key", "search_set_firecrawl")
	btnAdv := menu.Data("⚙️ Pengaturan Lanjutan", "search_adv_menu")
	btnTest := menu.Data("🧪 Test Search Query", "search_test_prompt")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnAuto, btnTavily),
		menu.Row(btnFirecrawl, btnDDG),
		menu.Row(btnToggleStrat, btnToggleFB),
		menu.Row(btnSetTavily, btnSetFirecrawl),
		menu.Row(btnAdv, btnTest),
		menu.Row(btnBack),
	)

	return menu
}

// HandleAdvancedSearchMenu displays advanced settings for Tavily and Firecrawl
func (h *SearchUIHandler) HandleAdvancedSearchMenu(c tele.Context) error {
	eng := search.GetGlobalEngine()
	sCfg := eng.Config()

	var sb strings.Builder
	sb.WriteString("⚙️ <b>Pengaturan Lanjutan Web Search</b>\n\n")

	depthStr := "⚡ Basic (Cepat)"
	if strings.ToLower(sCfg.Tavily.SearchDepth) == "advanced" {
		depthStr = "🔬 Advanced (Riset Mendalam)"
	}
	sb.WriteString(fmt.Sprintf("• <b>Tavily Search Depth</b>: %s\n", depthStr))

	ansStr := "❌ OFF"
	if sCfg.Tavily.IncludeAnswer {
		ansStr = "✅ ON (Ringkasan AI instan)"
	}
	sb.WriteString(fmt.Sprintf("• <b>Tavily Instant AI Answer</b>: %s\n", ansStr))

	fcURL := sCfg.Firecrawl.BaseURL
	if fcURL == "" {
		fcURL = "https://api.firecrawl.dev (Official)"
	}
	sb.WriteString(fmt.Sprintf("• <b>Firecrawl Base URL</b>: <code>%s</code>\n", html.EscapeString(fcURL)))
	sb.WriteString(fmt.Sprintf("• <b>Batas Dokumen Hasil</b>: <b>%d</b> dokumen\n\n", sCfg.MaxResults))

	sb.WriteString("<i>Gunakan tombol di bawah untuk mengubah parameter spesifik:</i>")

	menu := &tele.ReplyMarkup{}

	depthBtnLabel := "🔬 Ubah Depth: Advanced"
	if strings.ToLower(sCfg.Tavily.SearchDepth) == "advanced" {
		depthBtnLabel = "⚡ Ubah Depth: Basic"
	}
	btnToggleDepth := menu.Data(depthBtnLabel, "search_adv_toggle_depth")

	ansBtnLabel := "💡 AI Answer: ❌ OFF"
	if !sCfg.Tavily.IncludeAnswer {
		ansBtnLabel = "💡 AI Answer: ✅ ON"
	}
	btnToggleAns := menu.Data(ansBtnLabel, "search_adv_toggle_answer")

	btnSetFCURL := menu.Data("🌐 Ganti Firecrawl URL", "search_adv_set_fc_url")

	formatLimit := func(n int) string {
		if sCfg.MaxResults == n {
			return fmt.Sprintf("🔘 Limit %d", n)
		}
		return fmt.Sprintf("Limit %d", n)
	}
	btnLim3 := menu.Data(formatLimit(3), "search_adv_lim_3")
	btnLim5 := menu.Data(formatLimit(5), "search_adv_lim_5")
	btnLim10 := menu.Data(formatLimit(10), "search_adv_lim_10")

	btnBack := menu.Data("⬅️ Kembali ke Menu Search", "menu_search")

	menu.Inline(
		menu.Row(btnToggleDepth, btnToggleAns),
		menu.Row(btnSetFCURL),
		menu.Row(btnLim3, btnLim5, btnLim10),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleToggleTavilyDepthCallback switches between basic and advanced search depth
func (h *SearchUIHandler) HandleToggleTavilyDepthCallback(c tele.Context) error {
	eng := search.GetGlobalEngine()
	curr := strings.ToLower(eng.Config().Tavily.SearchDepth)
	newDepth := "advanced"
	if curr == "advanced" {
		newDepth = "basic"
	}
	eng.SetTavilyDepth(newDepth)
	if h.cfg != nil {
		h.cfg.Search.Tavily.SearchDepth = newDepth
	}
	h.persistConfig()
	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Tavily Depth diubah ke: %s", strings.ToUpper(newDepth))})
	return h.HandleAdvancedSearchMenu(c)
}

// HandleToggleTavilyAnswerCallback toggles Tavily instant AI answer inclusion
func (h *SearchUIHandler) HandleToggleTavilyAnswerCallback(c tele.Context) error {
	eng := search.GetGlobalEngine()
	newVal := !eng.Config().Tavily.IncludeAnswer
	eng.SetTavilyIncludeAnswer(newVal)
	if h.cfg != nil {
		h.cfg.Search.Tavily.IncludeAnswer = newVal
	}
	h.persistConfig()
	status := "DINONAKTIFKAN"
	if newVal {
		status = "DIAKTIFKAN"
	}
	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Tavily AI Answer %s", status)})
	return h.HandleAdvancedSearchMenu(c)
}

// HandlePromptFirecrawlURL prompts user for custom Firecrawl Base URL
func (h *SearchUIHandler) HandlePromptFirecrawlURL(c tele.Context) error {
	_ = c.Respond()
	h.waitingFirecrawlURL.Store(c.Sender().ID, true)
	return c.Send("🌐 <b>PENGATURAN FIRECRAWL BASE URL</b>\n\n"+
		"Silakan reply pesan ini dengan Base URL Firecrawl (contoh: <code>https://api.firecrawl.dev</code> atau <code>http://localhost:3002</code> jika self-hosted).\n"+
		"Ketik <code>reset</code> atau <code>default</code> untuk kembali ke official endpoint.\n\n"+
		"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

// HandleSetMaxResultsCallback updates max results limit
func (h *SearchUIHandler) HandleSetMaxResultsCallback(c tele.Context, limit int) error {
	eng := search.GetGlobalEngine()
	eng.SetMaxResults(limit)
	if h.cfg != nil {
		h.cfg.Search.MaxResults = limit
	}
	h.persistConfig()
	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Batas dokumen diubah ke: %d", limit)})
	return h.HandleAdvancedSearchMenu(c)
}

// HandleSwitchProviderCallback switches active search provider
func (h *SearchUIHandler) HandleSwitchProviderCallback(c tele.Context, prov string) error {
	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Mode provider diubah ke: %s", strings.ToUpper(prov))})
	eng := search.GetGlobalEngine()
	eng.SetProvider(prov)
	if h.cfg != nil {
		h.cfg.Search.Provider = prov
	}
	h.persistConfig()
	return h.HandleSearchDashboard(c)
}

// HandleToggleStrategyCallback switches strategy between fallback and roundrobin
func (h *SearchUIHandler) HandleToggleStrategyCallback(c tele.Context) error {
	eng := search.GetGlobalEngine()
	curr := strings.ToLower(eng.Strategy())
	newStrat := "roundrobin"
	if curr == "roundrobin" {
		newStrat = "fallback"
	}

	eng.SetStrategy(newStrat)
	if h.cfg != nil {
		h.cfg.Search.Strategy = newStrat
	}
	h.persistConfig()

	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Strategi diubah ke: %s", strings.ToUpper(newStrat))})
	return h.HandleSearchDashboard(c)
}

// HandleToggleFallbackCallback toggles automatic fallback status
func (h *SearchUIHandler) HandleToggleFallbackCallback(c tele.Context) error {
	eng := search.GetGlobalEngine()
	newVal := !eng.Config().FallbackEnabled
	eng.SetFallback(newVal)
	if h.cfg != nil {
		h.cfg.Search.FallbackEnabled = newVal
	}
	h.persistConfig()

	statusText := "Failover DIAKTIFKAN"
	if !newVal {
		statusText = "Failover DINONAKTIFKAN"
	}
	_ = c.Respond(&tele.CallbackResponse{Text: statusText})
	return h.HandleSearchDashboard(c)
}

// HandlePromptTavilyKey prompts user for Tavily API key
func (h *SearchUIHandler) HandlePromptTavilyKey(c tele.Context) error {
	_ = c.Respond()
	h.waitingTavilyKey.Store(c.Sender().ID, true)
	return c.Send("🔑 <b>PENGATURAN TAVILY API KEY</b>\n\n"+
		"Silakan reply pesan ini dengan API Key Tavily Anda (contoh: <code>tvly-dev-***</code>).\n"+
		"Ketik <code>hapus</code> untuk mengosongkan key saat ini.\n\n"+
		"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

// HandlePromptFirecrawlKey prompts user for Firecrawl API key
func (h *SearchUIHandler) HandlePromptFirecrawlKey(c tele.Context) error {
	_ = c.Respond()
	h.waitingFirecrawlKey.Store(c.Sender().ID, true)
	return c.Send("🔑 <b>PENGATURAN FIRECRAWL API KEY</b>\n\n"+
		"Silakan reply pesan ini dengan API Key Firecrawl Anda (contoh: <code>fc-***</code>).\n"+
		"Ketik <code>hapus</code> untuk mengosongkan key saat ini.\n\n"+
		"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

// HandlePromptTestQuery prompts user for a test search query
func (h *SearchUIHandler) HandlePromptTestQuery(c tele.Context) error {
	_ = c.Respond()
	h.waitingTestQuery.Store(c.Sender().ID, true)
	return c.Send("🧪 <b>TEST WEB SEARCH LIVE</b>\n\n"+
		"Silakan kirimkan kata kunci pencarian yang ingin diuji:\n"+
		"<i>Contoh: rilis golang terbaru</i>\n\n"+
		"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

// HandleTextMessage intercepts input for Tavily key, Firecrawl key, Firecrawl URL, or test queries
func (h *SearchUIHandler) HandleTextMessage(c tele.Context) (bool, error) {
	senderID := c.Sender().ID
	txt := strings.TrimSpace(c.Text())

	if txt == "/cancel" || txt == "/stop" {
		if _, ok := h.waitingTavilyKey.Load(senderID); ok {
			h.waitingTavilyKey.Delete(senderID)
			return true, c.Send("🛑 Pengaturan Tavily API Key dibatalkan.", tele.ModeHTML)
		}
		if _, ok := h.waitingFirecrawlKey.Load(senderID); ok {
			h.waitingFirecrawlKey.Delete(senderID)
			return true, c.Send("🛑 Pengaturan Firecrawl API Key dibatalkan.", tele.ModeHTML)
		}
		if _, ok := h.waitingFirecrawlURL.Load(senderID); ok {
			h.waitingFirecrawlURL.Delete(senderID)
			return true, c.Send("🛑 Pengaturan Firecrawl Base URL dibatalkan.", tele.ModeHTML)
		}
		if _, ok := h.waitingTestQuery.Load(senderID); ok {
			h.waitingTestQuery.Delete(senderID)
			return true, c.Send("🛑 Uji coba pencarian web dibatalkan.", tele.ModeHTML)
		}
	}

	// 1. Tavily API Key Input
	if _, ok := h.waitingTavilyKey.Load(senderID); ok {
		h.waitingTavilyKey.Delete(senderID)
		eng := search.GetGlobalEngine()
		if strings.EqualFold(txt, "hapus") || strings.EqualFold(txt, "clear") || strings.EqualFold(txt, "delete") {
			eng.SetTavilyKey("")
			if h.cfg != nil {
				h.cfg.Search.Tavily.APIKey = ""
			}
			h.persistConfig()
			return true, c.Send("🗑️ Tavily API Key berhasil dihapus/dikosongkan.", tele.ModeHTML)
		}
		eng.SetTavilyKey(txt)
		if h.cfg != nil {
			h.cfg.Search.Tavily.APIKey = txt
		}
		h.persistConfig()
		return true, c.Send(fmt.Sprintf("✅ <b>Tavily API Key berhasil disimpan!</b>\nKey: <code>%s</code>", maskAPIKey(txt)), tele.ModeHTML)
	}

	// 2. Firecrawl API Key Input
	if _, ok := h.waitingFirecrawlKey.Load(senderID); ok {
		h.waitingFirecrawlKey.Delete(senderID)
		eng := search.GetGlobalEngine()
		if strings.EqualFold(txt, "hapus") || strings.EqualFold(txt, "clear") || strings.EqualFold(txt, "delete") {
			eng.SetFirecrawlKey("")
			if h.cfg != nil {
				h.cfg.Search.Firecrawl.APIKey = ""
			}
			h.persistConfig()
			return true, c.Send("🗑️ Firecrawl API Key berhasil dihapus/dikosongkan.", tele.ModeHTML)
		}
		eng.SetFirecrawlKey(txt)
		if h.cfg != nil {
			h.cfg.Search.Firecrawl.APIKey = txt
		}
		h.persistConfig()
		return true, c.Send(fmt.Sprintf("✅ <b>Firecrawl API Key berhasil disimpan!</b>\nKey: <code>%s</code>", maskAPIKey(txt)), tele.ModeHTML)
	}

	// 2b. Firecrawl Base URL Input
	if _, ok := h.waitingFirecrawlURL.Load(senderID); ok {
		h.waitingFirecrawlURL.Delete(senderID)
		eng := search.GetGlobalEngine()
		targetURL := txt
		if strings.EqualFold(txt, "reset") || strings.EqualFold(txt, "default") {
			targetURL = "https://api.firecrawl.dev"
		}
		eng.SetFirecrawlBaseURL(targetURL)
		if h.cfg != nil {
			h.cfg.Search.Firecrawl.BaseURL = targetURL
		}
		h.persistConfig()
		return true, c.Send(fmt.Sprintf("✅ <b>Firecrawl Base URL berhasil disimpan!</b>\nEndpoint: <code>%s</code>", html.EscapeString(targetURL)), tele.ModeHTML)
	}

	// 3. Test Search Query Input
	if _, ok := h.waitingTestQuery.Load(senderID); ok {
		h.waitingTestQuery.Delete(senderID)
		return true, h.ExecuteLiveTest(c, txt)
	}

	return false, nil
}

// ExecuteLiveTest runs a test query and sends results back to user
func (h *SearchUIHandler) ExecuteLiveTest(c tele.Context, query string) error {
	waitMsg, _ := c.Bot().Send(c.Recipient(), fmt.Sprintf("⏳ Menjalankan pencarian web untuk: <i>%s</i>...", html.EscapeString(query)), tele.ModeHTML)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	t0 := time.Now()
	res, err := search.GetGlobalEngine().Search(ctx, query)
	elapsed := time.Since(t0)

	if waitMsg != nil {
		_ = c.Bot().Delete(waitMsg)
	}

	if err != nil {
		return c.Send(fmt.Sprintf("❌ <b>Uji Coba Pencarian Gagal!</b>\n\nQuery: <code>%s</code>\nDurasi: <code>%v</code>\nError: <code>%s</code>",
			html.EscapeString(query), elapsed.Round(time.Millisecond), html.EscapeString(err.Error())), tele.ModeHTML)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>HASIL UJI COBA PENCARIAN WEB</b>\n\n"))
	sb.WriteString(fmt.Sprintf("• Query: <code>%s</code>\n", html.EscapeString(res.Query)))
	sb.WriteString(fmt.Sprintf("• Dilayani oleh: <b>%s</b>\n", strings.ToUpper(res.Provider)))
	sb.WriteString(fmt.Sprintf("• Durasi: <code>%v</code>\n", elapsed.Round(time.Millisecond)))
	sb.WriteString(fmt.Sprintf("• Jumlah Dokumen: <b>%d</b>\n\n", len(res.Results)))

	if res.Answer != "" {
		sb.WriteString(fmt.Sprintf("💡 <b>Jawaban AI Instan:</b>\n%s\n\n", html.EscapeString(res.Answer)))
	}

	for i, item := range res.Results {
		if i >= 3 {
			break
		}
		snippet := item.Snippet
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		sb.WriteString(fmt.Sprintf("<b>%d. %s</b>\nURL: <code>%s</code>\n<i>%s</i>\n\n",
			i+1, html.EscapeString(item.Title), html.EscapeString(item.URL), html.EscapeString(snippet)))
	}

	return c.Send(sb.String(), tele.ModeHTML)
}

// Commands: /setsearchprovider <prov>
func (h *SearchUIHandler) HandleSetProviderCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("ℹ️ Penggunaan: <code>/setsearchprovider &lt;auto|tavily|firecrawl|duckduckgo&gt;</code>", tele.ModeHTML)
	}
	prov := strings.ToLower(args[0])
	search.GetGlobalEngine().SetProvider(prov)
	if h.cfg != nil {
		h.cfg.Search.Provider = prov
	}
	h.persistConfig()
	return c.Reply(fmt.Sprintf("✅ Provider web search diubah ke: <b>%s</b>", strings.ToUpper(prov)), tele.ModeHTML)
}

// Commands: /setsearchstrategy <fallback|roundrobin>
func (h *SearchUIHandler) HandleSetStrategyCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("ℹ️ Penggunaan: <code>/setsearchstrategy &lt;fallback|roundrobin&gt;</code>", tele.ModeHTML)
	}
	strat := strings.ToLower(args[0])
	search.GetGlobalEngine().SetStrategy(strat)
	if h.cfg != nil {
		h.cfg.Search.Strategy = strat
	}
	h.persistConfig()
	return c.Reply(fmt.Sprintf("✅ Strategi pencarian diubah ke: <b>%s</b>", strings.ToUpper(strat)), tele.ModeHTML)
}

func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}
