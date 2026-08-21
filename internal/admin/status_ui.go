package admin

import (
	"fmt"
	"html"
	"os"
	"runtime"
	"strings"
	"time"

	"goassistant/internal/tokensaver"
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

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	allocMB := float64(mem.Alloc) / (1024 * 1024)
	sysMB := float64(mem.Sys) / (1024 * 1024)
	numGoroutine := runtime.NumGoroutine()

	dbSizeStr := "0 KB"
	if fi, err := os.Stat(a.cfg.Server.DBPath); err == nil {
		dbSizeKB := float64(fi.Size()) / 1024
		if dbSizeKB > 1024 {
			dbSizeStr = fmt.Sprintf("%.2f MB", dbSizeKB/1024)
		} else {
			dbSizeStr = fmt.Sprintf("%.1f KB", dbSizeKB)
		}
	}

	sb.WriteString("🖥️ <b>Sistem & Runtime:</b>\n")
	sb.WriteString(fmt.Sprintf("• Versi: <code>v%s</code> (%s)\n", version.Version, version.BuildDate))
	sb.WriteString(fmt.Sprintf("• Uptime: <code>%s</code>\n", uptimeStr))
	sb.WriteString(fmt.Sprintf("• Runtime: <code>%s (%s/%s)</code>\n", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("• RAM (Alloc / Sys): <code>%.2f MB / %.2f MB</code>\n", allocMB, sysMB))
	sb.WriteString(fmt.Sprintf("• Goroutines: <code>%d aktif</code> | GC: <code>%d cycles</code>\n", numGoroutine, mem.NumGC))
	sb.WriteString(fmt.Sprintf("• SQLite DB: <code>%s (%s)</code>\n\n", html.EscapeString(a.cfg.Server.DBPath), dbSizeStr))

	// 2. AI Engine & Router
	allProviders := a.provManager.ListAll()
	var activeProvName, activeModel string
	if len(allProviders) > 0 {
		activeProvName = allProviders[0].Name()
		activeModel = allProviders[0].DefaultModel()
	} else {
		activeProvName = "Tidak ada provider aktif"
		activeModel = "-"
	}
	combos := a.provManager.ListCombos()

	sb.WriteString("🤖 <b>AI Engine & Router:</b>\n")
	sb.WriteString(fmt.Sprintf("• Active Provider: <b>%s</b>\n", html.EscapeString(activeProvName)))
	sb.WriteString(fmt.Sprintf("• Default Model: <code>%s</code>\n", html.EscapeString(activeModel)))
	sb.WriteString(fmt.Sprintf("• Total Provider: <code>%d terdaftar</code>\n", len(allProviders)))
	sb.WriteString(fmt.Sprintf("• Model Combos: <code>%d combo fallback</code>\n\n", len(combos)))

	// 3. Proxy Pool (9Router Engine)
	proxyStatus := "🔴 <b>Nonaktif</b>"
	if a.proxyPool != nil && a.proxyPool.IsEnabled() {
		proxyStatus = fmt.Sprintf("🟢 <b>Aktif</b> (Strategi: <code>%s</code>)", html.EscapeString(a.proxyPool.GetStrategy()))
	}
	sb.WriteString("🌐 <b>Proxy Pool:</b>\n")
	sb.WriteString(fmt.Sprintf("• Status: %s\n\n", proxyStatus))

	// 4. Token Saver (RTK & Compression)
	policy := a.db.GetResolvedPolicy("admin", fmt.Sprintf("%d", c.Chat().ID))
	tsStats := tokensaver.GetStats()
	savedTokens := tsStats.TotalTokensSaved.Load()
	origTokens := tsStats.TotalOriginalTokens.Load()
	var pctSaved float64
	if origTokens > 0 {
		pctSaved = (float64(savedTokens) / float64(origTokens)) * 100
	}

	sb.WriteString("🌿 <b>Token Saver:</b>\n")
	sb.WriteString(fmt.Sprintf("• Mode Aktif: <code>%s</code>\n", html.EscapeString(policy.TokenSaverMode)))
	sb.WriteString(fmt.Sprintf("• Total Hemat: <code>%d tokens</code> (<code>%.1f%%</code>)\n\n", savedTokens, pctSaved))

	// 5. Current Chat Context Session
	session, _ := a.sessManager.GetOrCreate("admin", fmt.Sprintf("%d", c.Chat().ID), fmt.Sprintf("%d", c.Sender().ID))
	msgCount := 0
	if session != nil {
		msgCount, _ = a.db.CountSessionMessages(session.ID)
	}

	sb.WriteString("🧠 <b>Konteks Sesi Saat Ini:</b>\n")
	if session != nil {
		sb.WriteString(fmt.Sprintf("• Session ID: <code>%s</code>\n", html.EscapeString(session.ID)))
		sb.WriteString(fmt.Sprintf("• Riwayat Aktif: <code>%d pesan</code> (Max Turns: <code>%d</code>)\n", msgCount, policy.MaxHistoryTurns))
		if session.Summary != "" {
			sb.WriteString("• Ringkasan Sesi: <i>Tersedia</i>\n")
		}
	} else {
		sb.WriteString("• Status: <i>Sesi Kosong</i>\n")
	}
	sb.WriteString("\n")

	// 6. Navigation Commands
	sb.WriteString("💡 <b>Perintah Konteks Cepat:</b>\n")
	sb.WriteString("• <code>/new</code> - Mulai sesi baru & bersihkan riwayat\n")
	sb.WriteString("• <code>/stop</code> - Hentikan respon AI yang sedang diproses\n")
	sb.WriteString("• <code>/menu</code> - Buka dashboard control plane")

	return sb.String()
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
