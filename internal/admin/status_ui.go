package admin

import (
	"fmt"
	"html"
	"os"
	"runtime"
	"strings"
	"time"

	"goassistant/internal/tokensaver"
	"goassistant/internal/version"
	tele "gopkg.in/telebot.v3"
)

var botStartTime = time.Now()

// RenderStatusSummary generates a comprehensive status dashboard in HTML format
func (a *AdminBot) RenderStatusSummary(c tele.Context) string {
	var sb strings.Builder
	sb.WriteString("⚡ <b>STATUS OPERASIONAL GOASSISTANT</b>\n\n")

	// 1. System & Runtime Metrics
	uptime := time.Since(botStartTime)
	uptimeStr := formatDuration(uptime)
	startedAtStr := botStartTime.Format("02 Jan 15:04:05")

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	allocMB := float64(mem.Alloc) / (1024 * 1024)
	totalAllocMB := float64(mem.TotalAlloc) / (1024 * 1024)
	sysMB := float64(mem.Sys) / (1024 * 1024)
	heapSysMB := float64(mem.HeapSys) / (1024 * 1024)
	numGoroutine := runtime.NumGoroutine()
	numCPU := runtime.NumCPU()
	pid := os.Getpid()
	hostname, _ := os.Hostname()

	lastGCPauseUs := float64(mem.PauseNs[(mem.NumGC+255)%256]) / 1000.0

	// Database Metrics
	dbSizeStr := "0 KB"
	if fi, err := os.Stat(a.cfg.Server.DBPath); err == nil {
		dbSizeKB := float64(fi.Size()) / 1024
		if dbSizeKB > 1024 {
			dbSizeStr = fmt.Sprintf("%.2f MB", dbSizeKB/1024)
		} else {
			dbSizeStr = fmt.Sprintf("%.1f KB", dbSizeKB)
		}
	}

	auditCount, _ := a.db.CountAuditLogs()
	totalSessions, _ := a.db.CountActiveSessions()

	sb.WriteString("🖥️ <b>Sistem & Runtime:</b>\n")
	sb.WriteString(fmt.Sprintf("• Versi: <code>%s</code>\n", version.GetFullVersion()))
	sb.WriteString(fmt.Sprintf("• Host: <code>%s</code> (PID: <code>%d</code>)\n", html.EscapeString(hostname), pid))
	sb.WriteString(fmt.Sprintf("• Uptime: <code>%s</code> (Mulai: <i>%s</i>)\n", uptimeStr, startedAtStr))
	sb.WriteString(fmt.Sprintf("• OS/Arch: <code>%s/%s</code> (<code>%d CPU Cores</code>) | Go: <code>%s</code>\n", runtime.GOOS, runtime.GOARCH, numCPU, runtime.Version()))
	sb.WriteString(fmt.Sprintf("• RAM (Heap / Sys): <code>%.2f MB / %.2f MB</code> (Total Alloc: <code>%.2f MB</code>, HeapSys: <code>%.2f MB</code>)\n", allocMB, sysMB, totalAllocMB, heapSysMB))
	sb.WriteString(fmt.Sprintf("• Goroutines: <code>%d aktif</code> | GC: <code>%d cycles</code> (Pause: <code>%.1f µs</code>)\n\n", numGoroutine, mem.NumGC, lastGCPauseUs))

	// 2. Storage & SQLite Database
	globalPol, _ := a.db.GetPolicy("global", "system")
	maxAuditLogs := 5000
	if globalPol != nil && globalPol.MaxAuditLogs > 0 {
		maxAuditLogs = globalPol.MaxAuditLogs
	}

	sb.WriteString("💾 <b>Database & Penyimpanan:</b>\n")
	sb.WriteString(fmt.Sprintf("• SQLite DB: <code>%s</code> (<code>%s</code>)\n", html.EscapeString(a.cfg.Server.DBPath), dbSizeStr))
	sb.WriteString(fmt.Sprintf("• Audit Logs: <code>%d logs</code> (Maks Rotasi: <code>%d logs</code>)\n", auditCount, maxAuditLogs))
	sb.WriteString(fmt.Sprintf("• Sesi Chat Terdaftar: <code>%d sesi</code>\n\n", totalSessions))

	// 3. AI Engine & Router
	allProviders := a.provManager.ListAll()
	activeProvCount := len(allProviders)
	if len(allProviders) > 0 {
		activeProvName = allProviders[0].Name()
		activeModel = allProviders[0].DefaultModel()
	} else {
		activeProvName = "Tidak ada provider aktif"
		activeModel = "-"
	}
	combos := a.provManager.ListCombos()

	apiTimeout := a.cfg.Timeouts.APICallSeconds
	handlerTimeout := a.cfg.Timeouts.HandlerSeconds
	if globalPol != nil {
		if globalPol.TimeoutAPISeconds > 0 {
			apiTimeout = globalPol.TimeoutAPISeconds
		}
		if globalPol.TimeoutHandlerSec > 0 {
			handlerTimeout = globalPol.TimeoutHandlerSec
		}
	}

	sb.WriteString("🤖 <b>AI Engine & Router:</b>\n")
	sb.WriteString(fmt.Sprintf("• Active Provider: <b>%s</b>\n", html.EscapeString(activeProvName)))
	sb.WriteString(fmt.Sprintf("• Default Model: <code>%s</code>\n", html.EscapeString(activeModel)))
	sb.WriteString(fmt.Sprintf("• Provider Terdaftar: <code>%d provider</code> (<code>%d aktif</code>)\n", len(allProviders), activeProvCount))
	sb.WriteString(fmt.Sprintf("• Model Combos: <code>%d fallback combo</code>\n", len(combos)))
	sb.WriteString(fmt.Sprintf("• Timeouts: API Call: <code>%ds</code> | Handler: <code>%ds</code> | SubAgent: <code>%ds</code>\n\n", apiTimeout, handlerTimeout, a.cfg.SubAgent.TimeoutSeconds))

	// 4. Proxy Pool (9Router Engine)
	proxyStatus := "🔴 <b>Nonaktif</b>"
	if a.proxyPool != nil && a.proxyPool.IsEnabled() {
		nodeCount := a.proxyPool.ActiveCount()
		proxyStatus = fmt.Sprintf("🟢 <b>Aktif</b> (Strategi: <code>%s</code> | Nodes: <code>%d</code>)", html.EscapeString(a.proxyPool.GetStrategy()), nodeCount)
	}
	sb.WriteString("🌐 <b>Proxy Pool:</b>\n")
	sb.WriteString(fmt.Sprintf("• Status: %s\n\n", proxyStatus))

	// 5. Token Saver (RTK & Compression)
	chatIDStr := ""
	if c.Chat() != nil {
		chatIDStr = fmt.Sprintf("%d", c.Chat().ID)
	}
	policy := a.db.GetResolvedPolicy("admin", chatIDStr)
	tsStats := tokensaver.GetStats()
	savedTokens := tsStats.TotalTokensSaved.Load()
	origTokens := tsStats.TotalOriginalTokens.Load()
	var pctSaved float64
	if origTokens > 0 {
		pctSaved = (float64(savedTokens) / float64(origTokens)) * 100
	}

	sb.WriteString("🌿 <b>Token Saver & Efisiensi:</b>\n")
	sb.WriteString(fmt.Sprintf("• Mode Aktif: <code>%s</code>\n", html.EscapeString(policy.TokenSaverMode)))
	sb.WriteString(fmt.Sprintf("• Total Hemat: <code>%d tokens</code> (<code>%.1f%%</code> efisiensi)\n", savedTokens, pctSaved))
	sb.WriteString(fmt.Sprintf("• Original Tokens Diproses: <code>%d tokens</code>\n\n", origTokens))

	// 6. Bot & Integration Channels
	botUsername := "@GoAssistant"
	if a.bot != nil && a.bot.Me != nil && a.bot.Me.Username != "" {
		botUsername = "@" + a.bot.Me.Username
	}
	channels, _ := a.db.ListChannels()
	cronJobs, _ := a.db.ListCronJobs()

	sb.WriteString("📱 <b>Channels & Integrasi:</b>\n")
	sb.WriteString(fmt.Sprintf("• Telegram Admin Bot: <b>%s</b> (Poll: <code>%ds</code>)\n", html.EscapeString(botUsername), a.cfg.AdminTelegram.PollTimeout))
	sb.WriteString(fmt.Sprintf("• Channel Terdaftar: <code>%d channel</code>\n", len(channels)))
	sb.WriteString(fmt.Sprintf("• Cron Scheduler: <code>%d jadwal aktif</code>\n\n", len(cronJobs)))

	// 7. Current Chat Context Session
	if c.Chat() != nil && c.Sender() != nil {
		session, _ := a.sessManager.GetOrCreate("admin", fmt.Sprintf("%d", c.Chat().ID), fmt.Sprintf("%d", c.Sender().ID))
		msgCount := 0
		if session != nil {
			msgCount, _ = a.db.CountSessionMessages(session.ID)
		}

		sb.WriteString("🧠 <b>Konteks Sesi Saat Ini:</b>\n")
		if session != nil {
			sb.WriteString(fmt.Sprintf("• Session ID: <code>%s</code>\n", html.EscapeString(session.ID)))
			sb.WriteString(fmt.Sprintf("• Riwayat Aktif: <code>%d pesan</code> (Max Turns: <code>%d</code> | Compact: <code>%d</code>)\n", msgCount, policy.MaxHistoryTurns, policy.CompactionThreshold))
			if session.Summary != "" {
				sb.WriteString("• Ringkasan Sesi: <i>Tersedia</i>\n")
			}
		} else {
			sb.WriteString("• Status: <i>Sesi Kosong</i>\n")
		}
		sb.WriteString("\n")
	}

	// 8. Navigation Commands
	sb.WriteString("💡 <b>Perintah Cepat:</b>\n")
	sb.WriteString("• <code>/new</code> - Mulai sesi baru & reset riwayat\n")
	sb.WriteString("• <code>/stop</code> - Batalkan proses respon AI\n")
	sb.WriteString("• <code>/limits</code> - Konfigurasi batasan & timeout\n")
	sb.WriteString("• <code>/menu</code> - Buka control plane dashboard")

	return sb.String()
}

// StatusKeyboard generates inline action buttons for the status page
func (a *AdminBot) StatusKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnRefresh := menu.Data("🔄 Refresh Status", "btn_refresh_status")
	btnLimits := menu.Data("🛡️ Atur Limits", "menu_limits")
	btnStats := menu.Data("📊 Audit Stats", "menu_stats")
	btnUpdate := menu.Data("🚀 Cek Update", "menu_update")
	btnMain := menu.Data("⬅️ Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnRefresh, btnLimits),
		menu.Row(btnStats, btnUpdate),
		menu.Row(btnMain),
	)
	return menu
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dh %dj %dm %ds", days, hours, mins, secs)
	}
	if hours > 0 {
		return fmt.Sprintf("%dj %dm %ds", hours, mins, secs)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	return fmt.Sprintf("%ds", secs)
}
