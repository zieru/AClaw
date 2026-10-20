package admin

import (
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	bf "goassistant/internal/bifrost"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type BifrostUIHandler struct {
	db       *storage.DB
	bot      *tele.Bot
	mu       sync.Mutex
	inputFor map[int64]string // user ID -> pending input ("proxy_url")
}

func NewBifrostUIHandler(db *storage.DB, bot *tele.Bot) *BifrostUIHandler {
	return &BifrostUIHandler{
		db:       db,
		bot:      bot,
		inputFor: make(map[int64]string),
	}
}

// RenderBifrostDashboard displays the current Bifrost settings overview.
func (ui *BifrostUIHandler) RenderBifrostDashboard() (string, *tele.ReplyMarkup) {
	cfg := bf.LoadDynamicConfig(ui.db)

	var sb strings.Builder
	sb.WriteString("🌉 <b>BIFROST AI GATEWAY SETTINGS</b>\n\n")

	sb.WriteString("⚡ <b>Concurrency & Workers:</b>\n")
	sb.WriteString(fmt.Sprintf("  • Workers per Provider: <b>%d</b>\n", cfg.Concurrency))
	sb.WriteString(fmt.Sprintf("  • Buffer Queue Size: <b>%d</b>\n\n", cfg.BufferSize))

	sb.WriteString("🌐 <b>Network & Reliability:</b>\n")
	sb.WriteString(fmt.Sprintf("  • Upstream Timeout: <b>%ds</b>\n", cfg.TimeoutSeconds))
	sb.WriteString(fmt.Sprintf("  • Max Retries: <b>%d kali</b>\n", cfg.MaxRetries))
	sb.WriteString(fmt.Sprintf("  • Retry Max Interval: <b>%ds</b>\n", cfg.RetryBackoffMaxSec))
	allowPriv := "🟢 Aktif"
	if !cfg.AllowPrivateNetwork {
		allowPriv = "🔴 Mati"
	}
	skipTLS := "🔴 Validasi TLS"
	if cfg.InsecureSkipVerify {
		skipTLS = "⚠️ Insecure / Skip Verify"
	}
	sb.WriteString(fmt.Sprintf("  • Allow LAN/Private: %s\n", allowPriv))
	sb.WriteString(fmt.Sprintf("  • TLS Verify: %s\n\n", skipTLS))

	sb.WriteString("🌊 <b>Stream Resilience:</b>\n")
	sb.WriteString(fmt.Sprintf("  • Stream Idle Timeout: <b>%ds</b>\n", cfg.StreamIdleTimeout))
	sb.WriteString(fmt.Sprintf("  • Keep-Alive Timeout: <b>%ds</b>\n", cfg.KeepAliveTimeout))
	usageTxt := "🟢 Aktif"
	if !cfg.WaitForUsage {
		usageTxt = "🔴 Mati"
	}
	doneTxt := "🔴 Standar [DONE]"
	if cfg.DoesNotSendDone {
		doneTxt = "🟢 Aktif (Toleran no [DONE])"
	}
	sb.WriteString(fmt.Sprintf("  • Wait for Usage Chunk: %s\n", usageTxt))
	sb.WriteString(fmt.Sprintf("  • No Done Marker Workaround: %s\n\n", doneTxt))

	sb.WriteString("🧠 <b>Prompt Caching:</b>\n")
	pcTxt := "🟢 Aktif"
	if !cfg.PromptCache {
		pcTxt = "🔴 Mati"
	}
	sb.WriteString(fmt.Sprintf("  • Auto-Inject Cache: %s\n\n", pcTxt))

	sb.WriteString("🛡️ <b>Upstream Proxy & Debug:</b>\n")
	proxyVal := "<i>Direct (Tanpa Proxy)</i>"
	if cfg.ProxyURL != "" {
		proxyVal = fmt.Sprintf("<code>%s</code>", html.EscapeString(cfg.ProxyURL))
	}
	sb.WriteString(fmt.Sprintf("  • Proxy: %s\n", proxyVal))

	menu := &tele.ReplyMarkup{}
	btnWorkers := menu.Data("⚡ Concurrency", "bf_menu_workers")
	btnNet := menu.Data("🌐 Network & Timeout", "bf_menu_net")
	btnStream := menu.Data("🌊 Stream Resilience", "bf_menu_stream")
	btnCache := menu.Data("🧠 Prompt Cache", "bf_menu_cache")
	btnProxy := menu.Data("🛡️ Proxy Gateway", "bf_menu_proxy")
	btnReload := menu.Data("🔄 Apply & Reload All", "bf_reload_all")
	btnBack := menu.Data("⬅️ Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnWorkers, btnNet),
		menu.Row(btnStream, btnCache),
		menu.Row(btnProxy, btnReload),
		menu.Row(btnBack),
	)

	return sb.String(), menu
}

// Submenu: Workers & Concurrency
func (ui *BifrostUIHandler) RenderWorkersMenu() (string, *tele.ReplyMarkup) {
	cfg := bf.LoadDynamicConfig(ui.db)

	text := fmt.Sprintf("⚡ <b>PENGATURAN CONCURRENCY & WORKERS</b>\n\n"+
		"• Current Workers: <b>%d</b>\n"+
		"• Current Buffer Size: <b>%d</b>\n\n"+
		"<i>Catatan: VPS 1 Core direkomendasikan 4 - 16 worker agar tidak membebani memori.</i>",
		cfg.Concurrency, cfg.BufferSize)

	menu := &tele.ReplyMarkup{}
	b4 := menu.Data(formatActiveLabel("4", cfg.Concurrency == 4), "bf_set_conc_4")
	b8 := menu.Data(formatActiveLabel("8", cfg.Concurrency == 8), "bf_set_conc_8")
	b16 := menu.Data(formatActiveLabel("16", cfg.Concurrency == 16), "bf_set_conc_16")
	b32 := menu.Data(formatActiveLabel("32", cfg.Concurrency == 32), "bf_set_conc_32")

	buf32 := menu.Data(formatActiveLabel("Buf 32", cfg.BufferSize == 32), "bf_set_buf_32")
	buf64 := menu.Data(formatActiveLabel("Buf 64", cfg.BufferSize == 64), "bf_set_buf_64")
	buf128 := menu.Data(formatActiveLabel("Buf 128", cfg.BufferSize == 128), "bf_set_buf_128")

	btnBack := menu.Data("⬅️ Kembali ke Bifrost", "menu_bifrost")

	menu.Inline(
		menu.Row(b4, b8, b16, b32),
		menu.Row(buf32, buf64, buf128),
		menu.Row(btnBack),
	)
	return text, menu
}

// Submenu: Network & Timeout
func (ui *BifrostUIHandler) RenderNetworkMenu() (string, *tele.ReplyMarkup) {
	cfg := bf.LoadDynamicConfig(ui.db)

	text := fmt.Sprintf("🌐 <b>PENGATURAN NETWORK & TIMEOUT</b>\n\n"+
		"• Request Timeout: <b>%ds</b>\n"+
		"• Max Retries: <b>%d</b>\n"+
		"• Allow Private Network: <b>%v</b>\n"+
		"• Skip TLS Verify: <b>%v</b>\n",
		cfg.TimeoutSeconds, cfg.MaxRetries, cfg.AllowPrivateNetwork, cfg.InsecureSkipVerify)

	menu := &tele.ReplyMarkup{}

	t30 := menu.Data(formatActiveLabel("30s", cfg.TimeoutSeconds == 30), "bf_set_to_30")
	t60 := menu.Data(formatActiveLabel("60s", cfg.TimeoutSeconds == 60), "bf_set_to_60")
	t90 := menu.Data(formatActiveLabel("90s", cfg.TimeoutSeconds == 90), "bf_set_to_90")
	t120 := menu.Data(formatActiveLabel("120s", cfg.TimeoutSeconds == 120), "bf_set_to_120")

	r0 := menu.Data(formatActiveLabel("Retry 0", cfg.MaxRetries == 0), "bf_set_retry_0")
	r1 := menu.Data(formatActiveLabel("Retry 1", cfg.MaxRetries == 1), "bf_set_retry_1")
	r2 := menu.Data(formatActiveLabel("Retry 2", cfg.MaxRetries == 2), "bf_set_retry_2")
	r3 := menu.Data(formatActiveLabel("Retry 3", cfg.MaxRetries == 3), "bf_set_retry_3")

	privLabel := "🟢 Allow LAN: ON"
	if !cfg.AllowPrivateNetwork {
		privLabel = "🔴 Allow LAN: OFF"
	}
	btnPriv := menu.Data(privLabel, "bf_tgl_private")

	tlsLabel := "🔒 TLS Check: STRICT"
	if cfg.InsecureSkipVerify {
		tlsLabel = "⚠️ TLS Check: INSECURE"
	}
	btnTLS := menu.Data(tlsLabel, "bf_tgl_tls")

	btnBack := menu.Data("⬅️ Kembali ke Bifrost", "menu_bifrost")

	menu.Inline(
		menu.Row(t30, t60, t90, t120),
		menu.Row(r0, r1, r2, r3),
		menu.Row(btnPriv, btnTLS),
		menu.Row(btnBack),
	)
	return text, menu
}

// Submenu: Stream Resilience
func (ui *BifrostUIHandler) RenderStreamMenu() (string, *tele.ReplyMarkup) {
	cfg := bf.LoadDynamicConfig(ui.db)

	text := fmt.Sprintf("🌊 <b>PENGATURAN STREAM RESILIENCE</b>\n\n"+
		"• Stream Idle Timeout: <b>%ds</b> (Koneksi diputus jika upstream membisu)\n"+
		"• Wait for Usage: <b>%v</b> (Tunggu chunk usage token)\n"+
		"• No [DONE] Marker Workaround: <b>%v</b> (Toleransi upstream abnormal)\n",
		cfg.StreamIdleTimeout, cfg.WaitForUsage, cfg.DoesNotSendDone)

	menu := &tele.ReplyMarkup{}

	s15 := menu.Data(formatActiveLabel("15s", cfg.StreamIdleTimeout == 15), "bf_set_st_15")
	s30 := menu.Data(formatActiveLabel("30s", cfg.StreamIdleTimeout == 30), "bf_set_st_30")
	s60 := menu.Data(formatActiveLabel("60s", cfg.StreamIdleTimeout == 60), "bf_set_st_60")
	s120 := menu.Data(formatActiveLabel("120s", cfg.StreamIdleTimeout == 120), "bf_set_st_120")

	uLabel := "🟢 Wait Usage: ON"
	if !cfg.WaitForUsage {
		uLabel = "🔴 Wait Usage: OFF"
	}
	btnUsage := menu.Data(uLabel, "bf_tgl_usage")

	doneLabel := "🔴 No-Done Tol.: OFF"
	if cfg.DoesNotSendDone {
		doneLabel = "🟢 No-Done Tol.: ON"
	}
	btnDone := menu.Data(doneLabel, "bf_tgl_done")

	btnBack := menu.Data("⬅️ Kembali ke Bifrost", "menu_bifrost")

	menu.Inline(
		menu.Row(s15, s30, s60, s120),
		menu.Row(btnUsage, btnDone),
		menu.Row(btnBack),
	)
	return text, menu
}

// Submenu: Prompt Cache
func (ui *BifrostUIHandler) RenderCacheMenu() (string, *tele.ReplyMarkup) {
	cfg := bf.LoadDynamicConfig(ui.db)

	text := fmt.Sprintf("🧠 <b>PENGATURAN PROMPT CACHING</b>\n\n"+
		"Bifrost secara otomatis dapat menyisipkan breakpoint cache ke upstream (Anthropic Claude, Gemini, OpenAI) untuk menghemat biaya input token.\n\n"+
		"• Status Auto-Inject Cache: <b>%v</b>\n",
		cfg.PromptCache)

	menu := &tele.ReplyMarkup{}
	cLabel := "🟢 Prompt Cache: AKTIF"
	if !cfg.PromptCache {
		cLabel = "🔴 Prompt Cache: NONAKTIF"
	}
	btnTgl := menu.Data(cLabel, "bf_tgl_cache")
	btnBack := menu.Data("⬅️ Kembali ke Bifrost", "menu_bifrost")

	menu.Inline(
		menu.Row(btnTgl),
		menu.Row(btnBack),
	)
	return text, menu
}

// Submenu: Proxy Gateway
func (ui *BifrostUIHandler) RenderProxyMenu() (string, *tele.ReplyMarkup) {
	cfg := bf.LoadDynamicConfig(ui.db)

	cur := "<i>Direct / Mati</i>"
	if cfg.ProxyURL != "" {
		cur = fmt.Sprintf("<code>%s</code>", html.EscapeString(cfg.ProxyURL))
	}

	text := fmt.Sprintf("🛡️ <b>PENGATURAN UPSTREAM PROXY GATEWAY</b>\n\n"+
		"Proxy ini digunakan Bifrost jika provider upstream memerlukan bypass ISP/region.\n\n"+
		"• Proxy Aktif: %s\n", cur)

	menu := &tele.ReplyMarkup{}
	btnSet := menu.Data("✏️ Set URL Proxy", "bf_prompt_proxy")
	btnClear := menu.Data("🗑️ Matikan / Hapus Proxy", "bf_clear_proxy")
	btnBack := menu.Data("⬅️ Kembali ke Bifrost", "menu_bifrost")

	menu.Inline(
		menu.Row(btnSet, btnClear),
		menu.Row(btnBack),
	)
	return text, menu
}

func formatActiveLabel(label string, active bool) string {
	if active {
		return "🟢 " + label
	}
	return label
}

// Handlers for callbacks

func (ui *BifrostUIHandler) HandleConcurrencySet(c tele.Context, val int) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.Concurrency = val
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderWorkersMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleBufferSizeSet(c tele.Context, val int) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.BufferSize = val
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderWorkersMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleTimeoutSet(c tele.Context, val int) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.TimeoutSeconds = val
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderNetworkMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleRetrySet(c tele.Context, val int) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.MaxRetries = val
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderNetworkMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleTogglePrivate(c tele.Context) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.AllowPrivateNetwork = !cfg.AllowPrivateNetwork
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderNetworkMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleToggleTLS(c tele.Context) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.InsecureSkipVerify = !cfg.InsecureSkipVerify
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderNetworkMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleStreamTimeoutSet(c tele.Context, val int) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.StreamIdleTimeout = val
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderStreamMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleToggleUsage(c tele.Context) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.WaitForUsage = !cfg.WaitForUsage
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderStreamMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleToggleDone(c tele.Context) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.DoesNotSendDone = !cfg.DoesNotSendDone
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderStreamMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleToggleCache(c tele.Context) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.PromptCache = !cfg.PromptCache
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderCacheMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandlePromptProxy(c tele.Context) error {
	ui.mu.Lock()
	if c.Sender() != nil {
		ui.inputFor[c.Sender().ID] = "proxy_url"
	}
	ui.mu.Unlock()

	return c.Send("🛡️ <b>Ketik URL Proxy Baru untuk Bifrost:</b>\n\n"+
		"Contoh:\n"+
		"• <code>http://127.0.0.1:8080</code>\n"+
		"• <code>socks5://user:pass@1.2.3.4:1080</code>\n\n"+
		"<i>Ketik /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleClearProxy(c tele.Context) error {
	cfg := bf.LoadDynamicConfig(ui.db)
	cfg.ProxyURL = ""
	_ = bf.SaveDynamicConfig(ui.db, cfg)
	_ = bf.ReloadAll()
	text, menu := ui.RenderProxyMenu()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *BifrostUIHandler) HandleReloadAll(c tele.Context) error {
	start := time.Now()
	err := bf.ReloadAll()
	dur := time.Since(start).Milliseconds()

	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("⚠️ Reload selesai dengan peringatan: %v", err), ShowAlert: true})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("✅ Seluruh provider Bifrost berhasil di-reload (%d ms)!", dur), ShowAlert: true})
	}

	text, menu := ui.RenderBifrostDashboard()
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// CheckInput intercepts text messages if user is awaiting input for proxy url.
func (ui *BifrostUIHandler) CheckInput(c tele.Context) bool {
	if c.Sender() == nil {
		return false
	}
	ui.mu.Lock()
	mode, ok := ui.inputFor[c.Sender().ID]
	if !ok {
		ui.mu.Unlock()
		return false
	}
	delete(ui.inputFor, c.Sender().ID)
	ui.mu.Unlock()

	txt := strings.TrimSpace(c.Text())
	if strings.HasPrefix(txt, "/cancel") {
		_ = c.Send("❌ Pengaturan proxy dibatalkan.")
		return true
	}

	if mode == "proxy_url" {
		cfg := bf.LoadDynamicConfig(ui.db)
		cfg.ProxyURL = txt
		_ = bf.SaveDynamicConfig(ui.db, cfg)
		_ = bf.ReloadAll()
		_ = c.Send(fmt.Sprintf("✅ <b>Proxy Bifrost berhasil disimpan:</b> <code>%s</code>\nKonfigurasi telah di-reload ke seluruh provider.", html.EscapeString(txt)), tele.ModeHTML)
		dashboardText, menu := ui.RenderBifrostDashboard()
		_ = c.Send(dashboardText, menu, tele.ModeHTML)
		return true
	}

	return false
}
