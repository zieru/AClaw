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
	tele "gopkg.in/telebot.v3"
)

// SearchUIHandler manages Telegram interactive controls for the multi-provider search engine
type SearchUIHandler struct {
	cfg                 *config.AppConfig
	waitingTavilyKey    sync.Map
	waitingFirecrawlKey sync.Map
	waitingTestQuery    sync.Map
}

// NewSearchUIHandler creates a new SearchUIHandler instance
func NewSearchUIHandler(cfg *config.AppConfig) *SearchUIHandler {
	return &SearchUIHandler{
		cfg: cfg,
	}
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
	sb.WriteString(fmt.Sprintf("• Failover Cadangan: <b>%s</b>\n\n", failoverLabel))

	sb.WriteString("<b>Status Ketersediaan Provider:</b>\n")
	tavilyStatus := "⚪ Belum diset"
	if sCfg.Tavily.APIKey != "" {
		tavilyStatus = fmt.Sprintf("🟢 Terkonfigurasi (<code>%s</code>)", maskAPIKey(sCfg.Tavily.APIKey))
	}
	sb.WriteString(fmt.Sprintf("• <b>Tavily AI Search</b>: %s\n", tavilyStatus))

	firecrawlStatus := "⚪ Belum diset"
	if sCfg.Firecrawl.APIKey != "" {
		firecrawlStatus = fmt.Sprintf("🟢 Terkonfigurasi (<code>%s</code>)", maskAPIKey(sCfg.Firecrawl.APIKey))
	}
	sb.WriteString(fmt.Sprintf("• <b>Firecrawl Web</b>: %s\n", firecrawlStatus))
	sb.WriteString("• <b>DuckDuckGo</b>: 🟢 Siap (Fallback Zero-Auth)\n\n")

	sb.WriteString("💡 <i>Pilih provider, ubah strategi, atau atur API key menggunakan tombol di bawah:</i>")

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
	btnTest := menu.Data("🧪 Test Search Query", "search_test_prompt")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnAuto, btnTavily),
		menu.Row(btnFirecrawl, btnDDG),
		menu.Row(btnToggleStrat, btnToggleFB),
		menu.Row(btnSetTavily, btnSetFirecrawl),
		menu.Row(btnTest),
		menu.Row(btnBack),
	)

	return menu
}

// HandleSwitchProviderCallback switches active search provider
func (h *SearchUIHandler) HandleSwitchProviderCallback(c tele.Context, prov string) error {
	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Mode provider diubah ke: %s", strings.ToUpper(prov))})
	eng := search.GetGlobalEngine()
	eng.SetProvider(prov)
	if h.cfg != nil {
		h.cfg.Search.Provider = prov
	}
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

// HandleTextMessage intercepts input for Tavily key, Firecrawl key, or test queries
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
			return true, c.Send("🗑️ Tavily API Key berhasil dihapus/dikosongkan.", tele.ModeHTML)
		}
		eng.SetTavilyKey(txt)
		if h.cfg != nil {
			h.cfg.Search.Tavily.APIKey = txt
		}
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
			return true, c.Send("🗑️ Firecrawl API Key berhasil dihapus/dikosongkan.", tele.ModeHTML)
		}
		eng.SetFirecrawlKey(txt)
		if h.cfg != nil {
			h.cfg.Search.Firecrawl.APIKey = txt
		}
		return true, c.Send(fmt.Sprintf("✅ <b>Firecrawl API Key berhasil disimpan!</b>\nKey: <code>%s</code>", maskAPIKey(txt)), tele.ModeHTML)
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
	return c.Reply(fmt.Sprintf("✅ Strategi pencarian diubah ke: <b>%s</b>", strings.ToUpper(strat)), tele.ModeHTML)
}

func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}
