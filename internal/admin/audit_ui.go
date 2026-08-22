package admin

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"strings"
	"time"

	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type AuditUI struct {
	db *storage.DB
}

func NewAuditUI(db *storage.DB) *AuditUI {
	return &AuditUI{db: db}
}

// RenderStatsSummary returns metrics and token consumption in HTML format
func (ui *AuditUI) RenderStatsSummary() string {
	todayStart := time.Now().Truncate(24 * time.Hour)
	todayStats, err := ui.db.GetStatsSummary(todayStart)
	if err != nil {
		return fmt.Sprintf("❌ Error mengambil statistik: %v", html.EscapeString(err.Error()))
	}

	allTimeStats, _ := ui.db.GetStatsSummary(time.Time{})

	var sb strings.Builder
	sb.WriteString("📊 <b>LAPORAN AUDIT LOG & PENGGUNAAN TOKEN</b>\n\n")

	sb.WriteString("📅 <b>Statistik Hari Ini:</b>\n")
	sb.WriteString(fmt.Sprintf("• Total Permintaan: <code>%d req</code>\n", todayStats.TotalRequests))
	sb.WriteString(fmt.Sprintf("• Total Token: <code>%d tokens</code> (In: %d, Out: %d)\n", todayStats.TotalTokens, todayStats.PromptTokens, todayStats.CompTokens))
	sb.WriteString(fmt.Sprintf("• Estimasi Biaya: <code>$%.4f USD</code>\n", todayStats.TotalCost))
	sb.WriteString(fmt.Sprintf("• Rata-rata Latensi: <code>%d ms</code>\n", todayStats.AvgLatencyMs))
	sb.WriteString(fmt.Sprintf("• Error: <code>%d</code>\n\n", todayStats.ErrorCount))

	if allTimeStats != nil {
		sb.WriteString("📈 <b>Statistik Sepanjang Waktu (All-Time):</b>\n")
		sb.WriteString(fmt.Sprintf("• Total Permintaan: <code>%d req</code>\n", allTimeStats.TotalRequests))
		sb.WriteString(fmt.Sprintf("• Total Token: <code>%d tokens</code>\n", allTimeStats.TotalTokens))
		sb.WriteString(fmt.Sprintf("• Total Estimasi Biaya: <code>$%.4f USD</code>\n\n", allTimeStats.TotalCost))
	}

	sb.WriteString("💡 <i>Pilih aksi di bawah untuk inspeksi log interaktif:</i>")
	return sb.String()
}

// StatsKeyboard builds keyboard for stats screen
func (ui *AuditUI) StatsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnLogs := menu.Data("📜 Buka Daftar Log Interaktif", "menu_logs")
	btnExport := menu.Data("📥 Export CSV", "btn_export_logs")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	menu.Inline(
		menu.Row(btnLogs),
		menu.Row(btnExport, btnBack),
	)
	return menu
}

// HandleLogs processes `/logs`
func (ui *AuditUI) HandleLogs(c tele.Context) error {
	return ui.HandleLogsWithPage(c, 0, false)
}

// HandleLogsWithPage provides interactive paginated log browser with inspect buttons
func (ui *AuditUI) HandleLogsWithPage(c tele.Context, page int, onlyErrors bool) error {
	if page < 0 {
		page = 0
	}

	// Fetch recent 50 logs
	allLogs, err := ui.db.GetRecentAuditLogs(50)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error mengambil log: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	var filteredLogs []storage.AuditLogRecord
	for _, l := range allLogs {
		if onlyErrors {
			if l.Status != "success" || l.ErrorMessage != "" {
				filteredLogs = append(filteredLogs, l)
			}
		} else {
			filteredLogs = append(filteredLogs, l)
		}
	}

	if len(filteredLogs) == 0 {
		text := "ℹ️ Belum ada catatan aktivitas request di sistem."
		if onlyErrors {
			text = "✅ <b>Tidak ada log error yang tercatat!</b>"
		}
		menu := &tele.ReplyMarkup{}
		btnToggle := menu.Data("📜 Tampilkan Semua Log", "log_toggle_err_0")
		btnBack := menu.Data("⬅️ Menu Utama", "menu_main")
		menu.Inline(menu.Row(btnToggle), menu.Row(btnBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	}

	pageSize := 5
	totalItems := len(filteredLogs)
	totalPages := (totalItems + pageSize - 1) / pageSize
	if page >= totalPages {
		page = totalPages - 1
	}

	startIdx := page * pageSize
	endIdx := startIdx + pageSize
	if endIdx > totalItems {
		endIdx = totalItems
	}

	pageLogs := filteredLogs[startIdx:endIdx]

	var sb strings.Builder
	filterTitle := "📜 <b>LOG AKTIVITAS TERAKHIR</b>"
	if onlyErrors {
		filterTitle = "⚠️ <b>LOG ERROR SAJA</b>"
	}
	sb.WriteString(fmt.Sprintf("%s (Hal %d/%d)\n\n", filterTitle, page+1, totalPages))

	for i, l := range pageLogs {
		itemNum := startIdx + i + 1
		statusIcon := "✅"
		if l.Status != "success" || l.ErrorMessage != "" {
			statusIcon = "❌"
		}
		sb.WriteString(fmt.Sprintf("<b>#%d</b> %s [<code>%s</code>] <b>%s</b> (%s)\n", itemNum, statusIcon, l.Timestamp.Format("15:04:05"), html.EscapeString(l.Model), html.EscapeString(l.ChannelType)))
		sb.WriteString(fmt.Sprintf("   • User: <code>%s</code> | Token: <code>%d</code> | Latensi: <code>%dms</code>\n", html.EscapeString(l.UserName), l.TotalTokens, l.LatencyMs))

		if l.ClientRequest != "" {
			reqPrev := l.ClientRequest
			if len(reqPrev) > 50 {
				reqPrev = reqPrev[:50] + "..."
			}
			sb.WriteString(fmt.Sprintf("   • Prompt: <i>\"%s\"</i>\n", html.EscapeString(reqPrev)))
		}
		if l.ErrorMessage != "" {
			errPrev := l.ErrorMessage
			if len(errPrev) > 60 {
				errPrev = errPrev[:60] + "..."
			}
			sb.WriteString(fmt.Sprintf("   • ⚠️ Error: <code>%s</code>\n", html.EscapeString(errPrev)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("💡 <i>Klik tombol [🔍 #Nomor] di bawah untuk melihat payload lengkap:</i>")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	// Inspect buttons row
	var inspectButtons []tele.Btn
	for i, l := range pageLogs {
		itemNum := startIdx + i + 1
		btn := menu.Data(fmt.Sprintf("🔍 #%d", itemNum), fmt.Sprintf("log_view_%s", l.ID))
		inspectButtons = append(inspectButtons, btn)
	}
	if len(inspectButtons) > 0 {
		rows = append(rows, menu.Row(inspectButtons...))
	}

	// Pagination row
	var navButtons []tele.Btn
	errFlagStr := "0"
	if onlyErrors {
		errFlagStr = "1"
	}

	if page > 0 {
		navButtons = append(navButtons, menu.Data("◀️ Prev", fmt.Sprintf("log_p_%d_%s", page-1, errFlagStr)))
	}
	navButtons = append(navButtons, menu.Data("🔄 Refresh", fmt.Sprintf("log_p_%d_%s", page, errFlagStr)))
	if page < totalPages-1 {
		navButtons = append(navButtons, menu.Data("▶️ Next", fmt.Sprintf("log_p_%d_%s", page+1, errFlagStr)))
	}
	if len(navButtons) > 0 {
		rows = append(rows, menu.Row(navButtons...))
	}

	// Filter toggle row
	var filterBtn tele.Btn
	if onlyErrors {
		filterBtn = menu.Data("📜 Tampilkan Semua", fmt.Sprintf("log_toggle_err_%d", page))
	} else {
		filterBtn = menu.Data("⚠️ Hanya Error", fmt.Sprintf("log_toggle_err_%d", page))
	}
	btnExport := menu.Data("📥 Export CSV", "btn_export_logs")
	rows = append(rows, menu.Row(filterBtn, btnExport))

	// Back row
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	rows = append(rows, menu.Row(btnBack))

	menu.Inline(rows...)
	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleViewLogByID displays detailed information about a single audit log entry
func (ui *AuditUI) HandleViewLogByID(c tele.Context, logID string) error {
	logRecord, err := ui.db.GetAuditLogByID(logID)
	if err != nil || logRecord == nil {
		return c.Reply("❌ Log dengan ID tersebut tidak ditemukan.")
	}

	var sb strings.Builder
	statusStr := "✅ SUCCESS"
	if logRecord.Status != "success" {
		statusStr = "❌ ERROR"
	}

	sb.WriteString(fmt.Sprintf("🔍 <b>DETAIL AUDIT LOG REQUEST (%s)</b>\n\n", statusStr))
	sb.WriteString(fmt.Sprintf("• ID: <code>%s</code>\n", logRecord.ID))
	sb.WriteString(fmt.Sprintf("• Waktu: <code>%s</code>\n", logRecord.Timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("• Provider: <b>%s</b> (%s)\n", html.EscapeString(logRecord.Provider), html.EscapeString(logRecord.Model)))
	sb.WriteString(fmt.Sprintf("• User: <code>%s</code> | Channel: <code>%s</code> (%s)\n", html.EscapeString(logRecord.UserName), html.EscapeString(logRecord.ChannelID), html.EscapeString(logRecord.ChannelType)))
	sb.WriteString(fmt.Sprintf("• Latensi: <code>%d ms</code> | Token: <code>%d</code> (Cost: <code>$%.6f</code>)\n", logRecord.LatencyMs, logRecord.TotalTokens, logRecord.CostUSD))

	if logRecord.ErrorMessage != "" {
		sb.WriteString(fmt.Sprintf("• Error: <code>%s</code>\n", html.EscapeString(logRecord.ErrorMessage)))
	}
	sb.WriteString("\n")

	sb.WriteString("👤 <b>[1] CLIENT REQUEST (User Prompt):</b>\n")
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n\n", html.EscapeString(logRecord.ClientRequest)))

	sb.WriteString("🤖 <b>[2] ASSEMBLED SYSTEM PROMPT:</b>\n")
	sysPreview := logRecord.SystemPrompt
	if len(sysPreview) > 1500 {
		sysPreview = sysPreview[:1500] + "\n...[dipotong untuk preview Telegram]"
	}
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n\n", html.EscapeString(sysPreview)))

	sb.WriteString("💬 <b>[3] PROVIDER RESPONSE (AI Output):</b>\n")
	respPreview := logRecord.ProviderResponse
	if len(respPreview) > 1500 {
		respPreview = respPreview[:1500] + "\n...[dipotong]"
	}
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>", html.EscapeString(respPreview)))

	menu := &tele.ReplyMarkup{}
	btnBackLogs := menu.Data("⬅️ Kembali ke Daftar Log", "menu_logs")
	btnExport := menu.Data("📥 Export CSV", "btn_export_logs")
	btnMainMenu := menu.Data("🏠 Menu Utama", "menu_main")
	menu.Inline(
		menu.Row(btnBackLogs),
		menu.Row(btnExport, btnMainMenu),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleViewLog processes `/viewlog [id]` (or latest if no id given)
func (ui *AuditUI) HandleViewLog(c tele.Context) error {
	args := c.Args()
	if len(args) > 0 {
		return ui.HandleViewLogByID(c, args[0])
	}

	recent, errRecent := ui.db.GetRecentAuditLogs(1)
	if errRecent != nil || len(recent) == 0 {
		return c.Reply("ℹ️ Belum ada log aktivitas untuk ditampilkan.")
	}

	return ui.HandleViewLogByID(c, recent[0].ID)
}

// HandleExportLogs exports logs to a CSV file and sends to Telegram
func (ui *AuditUI) HandleExportLogs(c tele.Context) error {
	logs, err := ui.db.GetRecentAuditLogs(500)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error mengambil data log: %v", html.EscapeString(err.Error())))
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Write CSV Header
	_ = w.Write([]string{
		"ID", "Timestamp", "ChannelType", "ChannelID", "UserID", "UserName",
		"Provider", "Model", "PromptTokens", "CompletionTokens", "TotalTokens",
		"CostUSD", "LatencyMs", "Status", "ErrorMessage", "ClientRequest", "ProviderResponse",
	})

	for _, l := range logs {
		_ = w.Write([]string{
			l.ID,
			l.Timestamp.Format("2006-01-02 15:04:05"),
			l.ChannelType,
			l.ChannelID,
			l.UserID,
			l.UserName,
			l.Provider,
			l.Model,
			fmt.Sprintf("%d", l.PromptTokens),
			fmt.Sprintf("%d", l.CompletionTokens),
			fmt.Sprintf("%d", l.TotalTokens),
			fmt.Sprintf("%.6f", l.CostUSD),
			fmt.Sprintf("%d", l.LatencyMs),
			l.Status,
			l.ErrorMessage,
			l.ClientRequest,
			l.ProviderResponse,
		})
	}
	w.Flush()

	doc := &tele.Document{
		File:     tele.FromReader(&buf),
		FileName: fmt.Sprintf("goassistant_audit_logs_%s.csv", time.Now().Format("20060102_150405")),
		Caption:  fmt.Sprintf("📊 Export Audit Logs (%d baris aktivitas)", len(logs)),
	}

	return c.Send(doc)
}
