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

	sb.WriteString("📋 <b>Perintah Terkait Audit:</b>\n")
	sb.WriteString("• <code>/logs</code> (Melihat 10 request terakhir)\n")
	sb.WriteString("• <code>/viewlog [id]</code> (Detail lengkap System Prompt & LLM Payload)\n")
	sb.WriteString("• <code>/exportlogs</code> (Download file audit .csv)\n")

	return sb.String()
}

// HandleLogs processes `/logs`
func (ui *AuditUI) HandleLogs(c tele.Context) error {
	logs, err := ui.db.GetRecentAuditLogs(10)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error mengambil log: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	if len(logs) == 0 {
		return c.Reply("ℹ️ Belum ada catatan aktivitas request di sistem.")
	}

	var sb strings.Builder
	sb.WriteString("📜 <b>10 AKTIVITAS REQUEST TERAKHIR</b>\n\n")

	for i, l := range logs {
		statusIcon := "✅"
		if l.Status != "success" {
			statusIcon = "❌"
		}
		sb.WriteString(fmt.Sprintf("%d. %s [<code>%s</code>] <b>%s</b> (%s)\n", i+1, statusIcon, l.Timestamp.Format("15:04:05"), html.EscapeString(l.Model), html.EscapeString(l.ChannelType)))
		sb.WriteString(fmt.Sprintf("   • User: <code>%s</code> | Token: <code>%d</code> | Latensi: <code>%dms</code>\n", html.EscapeString(l.UserName), l.TotalTokens, l.LatencyMs))
		if l.ClientRequest != "" {
			reqPrev := l.ClientRequest
			if len(reqPrev) > 60 {
				reqPrev = reqPrev[:60] + "..."
			}
			sb.WriteString(fmt.Sprintf("   • Client Prompt: <i>\"%s\"</i>\n", html.EscapeString(reqPrev)))
		}
		if l.ToolsCalled != "" && l.ToolsCalled != "[]" {
			sb.WriteString(fmt.Sprintf("   • Tools: <code>%s</code>\n", html.EscapeString(l.ToolsCalled)))
		}
		if l.ErrorMessage != "" {
			errPreview := l.ErrorMessage
			if len(errPreview) > 100 {
				errPreview = errPreview[:100] + "..."
			}
			sb.WriteString(fmt.Sprintf("   • Error: <code>%s</code>\n", html.EscapeString(errPreview)))
		}
		sb.WriteString(fmt.Sprintf("   • Detail: <code>/viewlog %s</code>\n\n", l.ID))
	}

	return c.Reply(sb.String(), tele.ModeHTML)
}

// HandleViewLog processes `/viewlog [id]` (or latest if no id given)
func (ui *AuditUI) HandleViewLog(c tele.Context) error {
	args := c.Args()
	var logRecord *storage.AuditLogRecord
	var err error

	if len(args) > 0 {
		logRecord, err = ui.db.GetAuditLogByID(args[0])
	} else {
		recent, errRecent := ui.db.GetRecentAuditLogs(1)
		if errRecent != nil || len(recent) == 0 {
			return c.Reply("ℹ️ Belum ada log aktivitas untuk ditampilkan.")
		}
		logRecord = &recent[0]
	}

	if err != nil || logRecord == nil {
		return c.Reply("❌ Log dengan ID tersebut tidak ditemukan.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 <b>DETAIL AUDIT LOG REQUEST</b>\n"))
	sb.WriteString(fmt.Sprintf("• ID: <code>%s</code>\n", logRecord.ID))
	sb.WriteString(fmt.Sprintf("• Waktu: <code>%s</code>\n", logRecord.Timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("• Provider: <b>%s</b> (%s)\n", html.EscapeString(logRecord.Provider), html.EscapeString(logRecord.Model)))
	sb.WriteString(fmt.Sprintf("• User: <code>%s</code> | Channel: <code>%s</code> (%s)\n", html.EscapeString(logRecord.UserName), html.EscapeString(logRecord.ChannelID), html.EscapeString(logRecord.ChannelType)))
	sb.WriteString(fmt.Sprintf("• Latensi: <code>%d ms</code> | Token: <code>%d</code> (Cost: <code>$%.6f</code>)\n\n", logRecord.LatencyMs, logRecord.TotalTokens, logRecord.CostUSD))

	sb.WriteString("👤 <b>[1] CLIENT REQUEST (User Prompt):</b>\n")
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n\n", html.EscapeString(logRecord.ClientRequest)))

	sb.WriteString("🤖 <b>[2] ASSEMBLED SYSTEM PROMPT (Identity + MD + Memory):</b>\n")
	sysPreview := logRecord.SystemPrompt
	if len(sysPreview) > 2000 {
		sysPreview = sysPreview[:2000] + "\n...[dipotong untuk preview Telegram]"
	}
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n\n", html.EscapeString(sysPreview)))

	sb.WriteString("💬 <b>[3] PROVIDER RESPONSE (AI Output):</b>\n")
	respPreview := logRecord.ProviderResponse
	if len(respPreview) > 1500 {
		respPreview = respPreview[:1500] + "\n...[dipotong]"
	}
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>", html.EscapeString(respPreview)))

	return sendSplitMessage(c, sb.String())
}

// HandleExportLogs exports logs to a CSV file and sends to Telegram
func (ui *AuditUI) HandleExportLogs(c tele.Context) error {
	logs, err := ui.db.GetRecentAuditLogs(200)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error mengambil data: %v", html.EscapeString(err.Error())))
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// Write CSV Header with full prompt & payload
	_ = writer.Write([]string{
		"ID", "Timestamp", "ChannelType", "ChannelID", "ChatID", "UserID", "UserName",
		"Provider", "Model", "PromptTokens", "CompletionTokens", "TotalTokens", "LatencyMs",
		"CostUSD", "ToolsCalled", "ClientRequest", "SystemPrompt", "FullRequestPayload", "ProviderResponse", "Status", "ErrorMessage",
	})

	for _, l := range logs {
		_ = writer.Write([]string{
			l.ID,
			l.Timestamp.Format(time.RFC3339),
			l.ChannelType,
			l.ChannelID,
			l.ChatID,
			l.UserID,
			l.UserName,
			l.Provider,
			l.Model,
			fmt.Sprintf("%d", l.PromptTokens),
			fmt.Sprintf("%d", l.CompletionTokens),
			fmt.Sprintf("%d", l.TotalTokens),
			fmt.Sprintf("%d", l.LatencyMs),
			fmt.Sprintf("%.6f", l.CostUSD),
			l.ToolsCalled,
			l.ClientRequest,
			l.SystemPrompt,
			l.FullRequestPayload,
			l.ProviderResponse,
			l.Status,
			l.ErrorMessage,
		})
	}
	writer.Flush()

	doc := &tele.Document{
		File:     tele.FromReader(&buf),
		FileName: fmt.Sprintf("audit_logs_full_%s.csv", time.Now().Format("20060102_150405")),
		Caption:  "📊 Full Audit Log (Client Prompt, Assembled System Prompt, Payload & AI Response)",
	}

	return c.Send(doc)
}
