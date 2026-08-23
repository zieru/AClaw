package admin

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type LimitsStep int

const (
	LimitsStepNone LimitsStep = iota
	LimitsStepScopeChatID
	LimitsStepCustomUpload
	LimitsStepCustomTokens
	LimitsStepCustomHistory
	LimitsStepCustomThreshold
	LimitsStepCustomModel
	LimitsStepCustomTimeoutAPI
	LimitsStepCustomTimeoutHandler
	LimitsStepCustomMaxAudit
	LimitsStepCustomBudget
)

type LimitsSession struct {
	Step      LimitsStep
	Scope     string
	ScopeID   string
	UpdatedAt time.Time
}

type LimitsUI struct {
	db          *storage.DB
	provManager *provider.Manager
	mu          sync.RWMutex
	sessions    map[int64]*LimitsSession
}

func NewLimitsUI(db *storage.DB, pm *provider.Manager) *LimitsUI {
	return &LimitsUI{
		db:          db,
		provManager: pm,
		sessions:    make(map[int64]*LimitsSession),
	}
}

// RenderLimitsSummary returns text summarizing policies in HTML format
func (ui *LimitsUI) RenderLimitsSummary() string {
	globalPol, _ := ui.db.GetPolicy("global", "system")
	if globalPol == nil {
		globalPol = &storage.PolicyRecord{
			MaxUploadFileMB:     10,
			MaxTokens:           2048,
			MaxHistoryTurns:     20,
			AutoCompaction:      true,
			CompactionThreshold: 15,
			FooterMode:          "off",
			MaxAuditLogs:        5000,
		}
	}

	cfg := config.Get()
	apiTimeout := 90
	handlerTimeout := 120
	if cfg != nil {
		apiTimeout = cfg.Timeouts.APICallSeconds
		handlerTimeout = cfg.Timeouts.HandlerSeconds
	}
	if globalPol.TimeoutAPISeconds > 0 {
		apiTimeout = globalPol.TimeoutAPISeconds
	}
	if globalPol.TimeoutHandlerSec > 0 {
		handlerTimeout = globalPol.TimeoutHandlerSec
	}

	maxAudit := 5000
	if globalPol.MaxAuditLogs > 0 {
		maxAudit = globalPol.MaxAuditLogs
	}

	auditCount, _ := ui.db.CountAuditLogs()

	var sb strings.Builder
	sb.WriteString("🛡️ <b>PENGATURAN PEMBATASAN & GOVERNANCE</b>\n\n")
	sb.WriteString("🌐 <b>Global Default Policy:</b>\n")
	sb.WriteString(fmt.Sprintf("• Max Upload File: <code>%d MB</code>\n", globalPol.MaxUploadFileMB))
	sb.WriteString(fmt.Sprintf("• Max Output Tokens: <code>%d tokens</code>\n", globalPol.MaxTokens))
	sb.WriteString(fmt.Sprintf("• Max History Turns: <code>%d turns</code>\n", globalPol.MaxHistoryTurns))
	autoCompStr := "Aktif ✅"
	if !globalPol.AutoCompaction {
		autoCompStr = "Nonaktif ❌"
	}
	sb.WriteString(fmt.Sprintf("• Auto-Compaction: <code>%s</code> (Threshold: <code>%d turns</code>)\n", autoCompStr, globalPol.CompactionThreshold))
	sb.WriteString(fmt.Sprintf("• ⏱️ Timeout API Call: <code>%d detik</code>\n", apiTimeout))
	sb.WriteString(fmt.Sprintf("• ⏳ Timeout Handler: <code>%d detik</code>\n", handlerTimeout))
	sb.WriteString(fmt.Sprintf("• 📜 Rotasi Audit Log: Maks <code>%d logs</code> (Saat ini: <code>%d logs</code>)\n", maxAudit, auditCount))
	if globalPol.TokenBudget > 0 {
		sb.WriteString(fmt.Sprintf("• 💰 Token Budget: <code>%d tokens</code>\n", globalPol.TokenBudget))
	}
	if globalPol.ModelOverride != "" {
		sb.WriteString(fmt.Sprintf("• Model Override: <code>%s</code>\n", html.EscapeString(globalPol.ModelOverride)))
	}

	footerLabel := "Nonaktif ❌ (off)"
	switch strings.ToLower(globalPol.FooterMode) {
	case "tokens":
		footerLabel = "Tokens Only 🪙 (tokens)"
	case "full":
		footerLabel = "Lengkap / Full 📊 (latency, tokens, ctx, model)"
	}
	sb.WriteString(fmt.Sprintf("• Tampilan Footer: <code>%s</code>\n", footerLabel))

	sb.WriteString("\n📋 <b>Cara Mengubah Pembatasan:</b>\n")
	sb.WriteString("• Klik tombol <b>🧙‍♂️ Atur Limits (Wizard)</b> untuk mengubah via tombol interaktif.\n")
	sb.WriteString("• Atau gunakan perintah manual:\n")
	sb.WriteString("  <code>/setlimit global system timeout_api 90</code>\n")
	sb.WriteString("  <code>/setlimit global system timeout_handler 120</code>\n")
	sb.WriteString("  <code>/setlimit global system max_audit 5000</code>\n")
	sb.WriteString("  <code>/setlimit global system max_upload 25</code>\n")

	return sb.String()
}

// LimitsKeyboard returns inline keyboard with quick toggle for global footer mode and wizard launcher
func (ui *LimitsUI) LimitsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	globalPol, _ := ui.db.GetPolicy("global", "system")
	currentMode := "off"
	if globalPol != nil && globalPol.FooterMode != "" {
		currentMode = strings.ToLower(globalPol.FooterMode)
	}

	btnOffText := "❌ Footer: Off"
	if currentMode == "off" {
		btnOffText = "✅ [❌ Footer: Off]"
	}
	btnTokensText := "🪙 Footer: Tokens"
	if currentMode == "tokens" {
		btnTokensText = "✅ [🪙 Footer: Tokens]"
	}
	btnFullText := "📊 Footer: Full"
	if currentMode == "full" {
		btnFullText = "✅ [📊 Footer: Full]"
	}

	btnOff := menu.Data(btnOffText, "set_footer_global_off")
	btnTokens := menu.Data(btnTokensText, "set_footer_global_tokens")
	btnFull := menu.Data(btnFullText, "set_footer_global_full")
	btnWizard := menu.Data("🧙‍♂️ Atur Limits (Wizard)", "lim_wiz_start")
	btnRotateNow := menu.Data("🧹 Pangkas / Rotasi Log Sekarang", "lim_do_rotate_audit")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnOff, btnTokens, btnFull),
		menu.Row(btnWizard),
		menu.Row(btnRotateNow),
		menu.Row(btnBack),
	)
	return menu
}

// StartLimitsWizard starts the interactive limits configurator
func (ui *LimitsUI) StartLimitsWizard(c tele.Context) error {
	text := "🛡️ <b>WIZARD PENGATURAN LIMITS & GOVERNANCE</b>\n\n" +
		"Pilih <b>Scope / Tingkat Pembatasan</b> yang ingin Anda konfigurasikan:\n\n" +
		"• <b>Global (System):</b> Berlaku untuk semua bot, channel, dan percakapan.\n" +
		"• <b>Channel:</b> Berlaku khusus untuk 1 instance bot (TG/WA).\n" +
		"• <b>Chat / Group ID:</b> Override khusus untuk 1 grup atau user ID tertentu."

	menu := &tele.ReplyMarkup{}
	btnGlobal := menu.Data("🌐 Global System Default", "lim_sc_global")
	btnChan := menu.Data("📱 Channel Spesifik", "lim_sc_chan_menu")
	btnChat := menu.Data("💬 Chat / Group ID", "lim_sc_chat_prompt")
	btnBack := menu.Data("⬅️ Kembali ke Menu Limits", "menu_limits")

	menu.Inline(
		menu.Row(btnGlobal),
		menu.Row(btnChan, btnChat),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderChannelPickMenu displays channel list to configure
func (ui *LimitsUI) RenderChannelPickMenu(c tele.Context) error {
	channels, err := ui.db.ListChannels()
	if err != nil || len(channels) == 0 {
		return c.Reply("⚠️ Belum ada channel yang terdaftar. Mengarahkan ke Global Limits...", tele.ModeHTML)
	}

	text := "📱 <b>PILIH CHANNEL UNTUK PENGATURAN LIMITS:</b>"
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, ch := range channels {
		chCopy := ch
		btn := menu.Data(fmt.Sprintf("📱 %s (%s)", chCopy.Name, chCopy.ID), fmt.Sprintf("lim_sc_ch_%s", chCopy.ID))
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ Kembali", "lim_wiz_start")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderScopeLimitsDashboard renders full limits dashboard for given scope
func (ui *LimitsUI) RenderScopeLimitsDashboard(c tele.Context, scope, scopeID string) error {
	pol, err := ui.db.GetPolicy(scope, scopeID)
	if err != nil || pol == nil {
		pol = &storage.PolicyRecord{
			Scope:               scope,
			ScopeID:             scopeID,
			MaxUploadFileMB:     10,
			MaxTokens:           2048,
			MaxHistoryTurns:     20,
			AutoCompaction:      true,
			CompactionThreshold: 15,
			FooterMode:          "off",
			MaxAuditLogs:        5000,
		}
	}

	if c.Sender() != nil {
		ui.mu.Lock()
		ui.sessions[c.Sender().ID] = &LimitsSession{
			Scope:     scope,
			ScopeID:   scopeID,
			UpdatedAt: time.Now(),
		}
		ui.mu.Unlock()
	}

	autoCompStr := "🟢 Aktif"
	if !pol.AutoCompaction {
		autoCompStr = "🔴 Nonaktif"
	}

	cfg := config.Get()
	apiTimeout := 90
	handlerTimeout := 120
	if cfg != nil {
		apiTimeout = cfg.Timeouts.APICallSeconds
		handlerTimeout = cfg.Timeouts.HandlerSeconds
	}
	if pol.TimeoutAPISeconds > 0 {
		apiTimeout = pol.TimeoutAPISeconds
	}
	if pol.TimeoutHandlerSec > 0 {
		handlerTimeout = pol.TimeoutHandlerSec
	}

	maxAudit := 5000
	if pol.MaxAuditLogs > 0 {
		maxAudit = pol.MaxAuditLogs
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🛡️ <b>LIMITS DASHBOARD: <code>%s:%s</code></b>\n\n", html.EscapeString(scope), html.EscapeString(scopeID)))
	sb.WriteString(fmt.Sprintf("• 📁 <b>Max Upload:</b> <code>%d MB</code>\n", pol.MaxUploadFileMB))
	sb.WriteString(fmt.Sprintf("• 🪙 <b>Max Output Tokens:</b> <code>%d tokens</code>\n", pol.MaxTokens))
	sb.WriteString(fmt.Sprintf("• 💬 <b>Max History Turns:</b> <code>%d turns</code>\n", pol.MaxHistoryTurns))
	sb.WriteString(fmt.Sprintf("• 🗜️ <b>Auto-Compaction:</b> %s (Ambang: <code>%d turns</code>)\n", autoCompStr, pol.CompactionThreshold))
	sb.WriteString(fmt.Sprintf("• ⏱️ <b>Timeout API Call:</b> <code>%d detik</code>\n", apiTimeout))
	sb.WriteString(fmt.Sprintf("• ⏳ <b>Timeout Handler:</b> <code>%d detik</code>\n", handlerTimeout))
	sb.WriteString(fmt.Sprintf("• 📜 <b>Maksimal Audit Log:</b> <code>%d logs</code>\n", maxAudit))
	if pol.TokenBudget > 0 {
		sb.WriteString(fmt.Sprintf("• 💰 <b>Token Budget:</b> <code>%d tokens</code>\n", pol.TokenBudget))
	}
	if pol.ModelOverride != "" {
		sb.WriteString(fmt.Sprintf("• 🤖 <b>Model Override:</b> <code>%s</code>\n", html.EscapeString(pol.ModelOverride)))
	} else {
		sb.WriteString("• 🤖 <b>Model Override:</b> <i>(Default Provider)</i>\n")
	}
	sb.WriteString(fmt.Sprintf("• 📊 <b>Tampilan Footer:</b> <code>%s</code>\n\n", pol.FooterMode))
	sb.WriteString("Pilih parameter yang ingin diubah:")

	menu := &tele.ReplyMarkup{}
	btnFooter := menu.Data("📊 Footer Mode", "lim_set_footer_menu")
	btnUpload := menu.Data("📁 Max Upload", "lim_set_upload_menu")
	btnTokens := menu.Data("🪙 Max Tokens", "lim_set_tokens_menu")
	btnHistory := menu.Data("💬 Max History", "lim_set_history_menu")
	btnCompact := menu.Data("🗜️ Auto-Compaction", "lim_set_compact_menu")
	btnModel := menu.Data("🤖 Model Override", "lim_set_model_menu")
	btnTimeAPI := menu.Data("⏱️ Timeout API", "lim_set_timeout_api_menu")
	btnTimeHandler := menu.Data("⏳ Timeout Handler", "lim_set_timeout_handler_menu")
	btnAuditMax := menu.Data("📜 Rotasi Audit", "lim_set_audit_max_menu")
	btnBudget := menu.Data("💰 Token Budget", "lim_set_budget_menu")
	btnPruneAudit := menu.Data("🧹 Pangkas Log", "lim_do_rotate_audit")
	btnChangeScope := menu.Data("🔄 Ganti Scope", "lim_wiz_start")
	btnBack := menu.Data("⬅️ Kembali", "menu_limits")

	menu.Inline(
		menu.Row(btnFooter, btnUpload),
		menu.Row(btnTokens, btnHistory),
		menu.Row(btnCompact, btnModel),
		menu.Row(btnTimeAPI, btnTimeHandler),
		menu.Row(btnAuditMax, btnBudget),
		menu.Row(btnPruneAudit, btnChangeScope),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleTextMessage handles custom numerical / text input for limits wizard
func (ui *LimitsUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID

	ui.mu.RLock()
	sess, exists := ui.sessions[userID]
	ui.mu.RUnlock()

	if !exists || sess.Step == LimitsStepNone {
		return false, nil
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") {
		scope, scopeID := sess.Scope, sess.ScopeID
		ui.CancelWizard(userID)
		if scope != "" {
			return true, ui.RenderScopeLimitsDashboard(c, scope, scopeID)
		}
		return true, c.Reply("❌ Wizard limits dibatalkan.")
	}

	switch sess.Step {
	case LimitsStepScopeChatID:
		chatID := msgText
		sess.Scope = "chat"
		sess.ScopeID = chatID
		sess.Step = LimitsStepNone
		return true, ui.RenderScopeLimitsDashboard(c, "chat", chatID)

	case LimitsStepCustomUpload:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan angka positif (dalam MB). Cth: <code>20</code>", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.MaxUploadFileMB = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Max Upload untuk <code>%s:%s</code> diset ke <b>%d MB</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomTokens:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan angka token positif. Cth: <code>4096</code>", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.MaxTokens = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Max Output Tokens untuk <code>%s:%s</code> diset ke <b>%d tokens</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomHistory:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan jumlah turn positif. Cth: <code>25</code>", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.MaxHistoryTurns = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Max History Turns untuk <code>%s:%s</code> diset ke <b>%d turns</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomThreshold:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan angka turn threshold positif. Cth: <code>15</code>", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.CompactionThreshold = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Compaction Threshold untuk <code>%s:%s</code> diset ke <b>%d turns</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomModel:
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.ModelOverride = msgText
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Model Override untuk <code>%s:%s</code> diset ke <code>%s</code>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), html.EscapeString(msgText)), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomTimeoutAPI:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan angka timeout positif dalam detik (contoh: <code>90</code>):", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.TimeoutAPISeconds = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Timeout API Call untuk <code>%s:%s</code> diset ke <b>%d detik</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomTimeoutHandler:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan angka timeout handler positif dalam detik (contoh: <code>120</code>):", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.TimeoutHandlerSec = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Timeout Handler untuk <code>%s:%s</code> diset ke <b>%d detik</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomMaxAudit:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan batas baris log positif (contoh: <code>5000</code>):", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.MaxAuditLogs = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Batas Rotasi Audit Log untuk <code>%s:%s</code> diset ke <b>%d baris</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)

	case LimitsStepCustomBudget:
		n, err := strconv.Atoi(msgText)
		if err != nil || n <= 0 {
			return true, c.Reply("⚠️ Harap masukkan token budget positif (contoh: <code>8000</code>):", tele.ModeHTML)
		}
		pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
		}
		pol.TokenBudget = n
		_ = ui.db.SavePolicy(pol)
		sess.Step = LimitsStepNone
		_ = c.Reply(fmt.Sprintf("✅ Token Budget untuk <code>%s:%s</code> diset ke <b>%d tokens</b>!", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), n), tele.ModeHTML)
		return true, ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
	}

	return false, nil
}

// CancelWizard clears user session
func (ui *LimitsUI) CancelWizard(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.sessions, userID)
}

// GetSession returns session
func (ui *LimitsUI) GetSession(userID int64) (*LimitsSession, bool) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	s, ok := ui.sessions[userID]
	return s, ok
}

// SetSessionStep sets active wizard step
func (ui *LimitsUI) SetSessionStep(userID int64, step LimitsStep) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if sess, ok := ui.sessions[userID]; ok {
		sess.Step = step
		sess.UpdatedAt = time.Now()
	}
}

// HandleSetLimit processes `/setlimit` command
func (ui *LimitsUI) HandleSetLimit(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return ui.StartLimitsWizard(c)
	}
	if len(args) < 4 {
		return c.Reply("⚠️ Format salah!\nContoh:\n• <code>/setlimit global system timeout_api 90</code>\n• <code>/setlimit global system timeout_handler 120</code>\n• <code>/setlimit global system max_audit 5000</code>\n• <code>/setlimit global system footer full</code>\n• <code>/setlimit global system max_upload 15</code>", tele.ModeHTML)
	}

	scope := strings.ToLower(args[0])
	scopeID := args[1]
	param := strings.ToLower(args[2])
	val := args[3]

	if scope != "global" && scope != "channel" && scope != "chat" {
		return c.Reply("❌ Scope harus berupa: <code>global</code>, <code>channel</code>, atau <code>chat</code>", tele.ModeHTML)
	}

	pol, err := ui.db.GetPolicy(scope, scopeID)
	if err != nil || pol == nil {
		pol = &storage.PolicyRecord{
			Scope:               scope,
			ScopeID:             scopeID,
			MaxUploadFileMB:     10,
			MaxTokens:           2048,
			MaxHistoryTurns:     20,
			AutoCompaction:      true,
			CompactionThreshold: 15,
			FooterMode:          "off",
			MaxAuditLogs:        5000,
		}
	}

	switch param {
	case "footer", "footermode", "footer_mode":
		v := strings.ToLower(val)
		if v != "off" && v != "tokens" && v != "full" && v != "none" {
			return c.Reply("❌ Nilai footer harus salah satu dari: <code>off</code>, <code>tokens</code>, atau <code>full</code>", tele.ModeHTML)
		}
		if v == "none" {
			v = "off"
		}
		pol.FooterMode = v
	case "max_upload", "upload":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai max_upload harus angka positif (MB)")
		}
		pol.MaxUploadFileMB = n
	case "max_tokens", "tokens":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai max_tokens harus angka positif")
		}
		pol.MaxTokens = n
	case "max_history", "history":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai max_history harus angka positif")
		}
		pol.MaxHistoryTurns = n
	case "auto_compaction", "compaction":
		pol.AutoCompaction = strings.ToLower(val) == "on" || strings.ToLower(val) == "true" || val == "1"
	case "compaction_threshold", "threshold":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai compaction_threshold harus angka positif")
		}
		pol.CompactionThreshold = n
	case "model":
		pol.ModelOverride = val
	case "timeout_api", "api_timeout", "timeout_call":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai timeout_api harus angka positif dalam detik (contoh: 90)")
		}
		pol.TimeoutAPISeconds = n
	case "timeout_handler", "handler_timeout":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai timeout_handler harus angka positif dalam detik (contoh: 120)")
		}
		pol.TimeoutHandlerSec = n
	case "max_audit", "max_audit_logs", "audit_max", "audit_limit":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai max_audit harus angka positif baris log (contoh: 5000)")
		}
		pol.MaxAuditLogs = n
	case "token_budget", "budget":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return c.Reply("❌ Nilai token_budget harus angka positif (contoh: 8000)")
		}
		pol.TokenBudget = n
	default:
		return c.Reply(fmt.Sprintf("❌ Parameter '%s' tidak dikenali", html.EscapeString(param)))
	}

	if err := ui.db.SavePolicy(pol); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan policy: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("✅ Berhasil memperbarui policy untuk <code>%s:%s</code>!\nParameter <code>%s</code> diset ke <code>%s</code>.", html.EscapeString(scope), html.EscapeString(scopeID), html.EscapeString(param), html.EscapeString(val)), tele.ModeHTML)
}

// HandleSetFooter processes `/setfooter` shortcut command
func (ui *LimitsUI) HandleSetFooter(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("ℹ️ <b>Format Shortcut /setfooter:</b>\n• <code>/setfooter global system &lt;off|tokens|full&gt;</code>\n• <code>/setfooter channel &lt;channel_id&gt; &lt;off|tokens|full&gt;</code>\n• <code>/setfooter chat &lt;chat_id&gt; &lt;off|tokens|full&gt;</code>", tele.ModeHTML)
	}

	var scope, scopeID, mode string
	if len(args) == 1 {
		scope = "global"
		scopeID = "system"
		mode = strings.ToLower(args[0])
	} else if len(args) >= 3 {
		scope = strings.ToLower(args[0])
		scopeID = args[1]
		mode = strings.ToLower(args[2])
	} else {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setfooter global system full</code> atau <code>/setfooter full</code>", tele.ModeHTML)
	}

	if scope != "global" && scope != "channel" && scope != "chat" {
		return c.Reply("❌ Scope harus berupa: <code>global</code>, <code>channel</code>, atau <code>chat</code>", tele.ModeHTML)
	}

	if mode != "off" && mode != "tokens" && mode != "full" && mode != "none" {
		return c.Reply("❌ Mode footer harus berupa: <code>off</code>, <code>tokens</code>, atau <code>full</code>", tele.ModeHTML)
	}
	if mode == "none" {
		mode = "off"
	}

	pol, err := ui.db.GetPolicy(scope, scopeID)
	if err != nil || pol == nil {
		pol = &storage.PolicyRecord{
			Scope:               scope,
			ScopeID:             scopeID,
			MaxUploadFileMB:     10,
			MaxTokens:           2048,
			MaxHistoryTurns:     20,
			AutoCompaction:      true,
			CompactionThreshold: 15,
		}
	}

	pol.FooterMode = mode
	if err := ui.db.SavePolicy(pol); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan footer policy: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("✅ Tampilan footer untuk <code>%s:%s</code> berhasil diatur ke: <b>%s</b>", html.EscapeString(scope), html.EscapeString(scopeID), html.EscapeString(mode)), tele.ModeHTML)
}

// RenderFooterMenu renders options for footer mode
func (ui *LimitsUI) RenderFooterMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("📊 <b>PILIH TAMPILAN FOOTER (<code>%s:%s</code>)</b>\n\n"+
		"• <b>Off:</b> Tanpa footer\n"+
		"• <b>Tokens:</b> Hanya jumlah token\n"+
		"• <b>Full:</b> Info lengkap (latency, tokens, context, model)", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	bOff := menu.Data("❌ Off", "lim_set_val_footer_off")
	bTok := menu.Data("🪙 Tokens Only", "lim_set_val_footer_tokens")
	bFull := menu.Data("📊 Full (Lengkap)", "lim_set_val_footer_full")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(bOff, bTok),
		menu.Row(bFull),
		menu.Row(bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderUploadMenu renders upload size options
func (ui *LimitsUI) RenderUploadMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("📁 <b>ATUR MAX UPLOAD FILE (<code>%s:%s</code>)</b>\n\nPilih batas ukuran file maksimal:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b5 := menu.Data("5 MB", "lim_set_val_upload_5")
	b10 := menu.Data("10 MB", "lim_set_val_upload_10")
	b20 := menu.Data("20 MB", "lim_set_val_upload_20")
	b50 := menu.Data("50 MB", "lim_set_val_upload_50")
	bCust := menu.Data("✏️ Custom MB", "lim_input_upload")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b5, b10),
		menu.Row(b20, b50),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderTokensMenu renders max token output options
func (ui *LimitsUI) RenderTokensMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("🪙 <b>ATUR MAX OUTPUT TOKENS (<code>%s:%s</code>)</b>\n\nPilih batas token output per respon:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b1k := menu.Data("1,024", "lim_set_val_tokens_1024")
	b2k := menu.Data("2,048", "lim_set_val_tokens_2048")
	b4k := menu.Data("4,096", "lim_set_val_tokens_4096")
	b8k := menu.Data("8,192", "lim_set_val_tokens_8192")
	bCust := menu.Data("✏️ Custom Tokens", "lim_input_tokens")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b1k, b2k),
		menu.Row(b4k, b8k),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderHistoryMenu renders context history turns options
func (ui *LimitsUI) RenderHistoryMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("💬 <b>ATUR MAX CONTEXT HISTORY TURNS (<code>%s:%s</code>)</b>\n\nPilih jumlah putaran chat yang diingat dalam sesi:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b10 := menu.Data("10 Turns", "lim_set_val_history_10")
	b20 := menu.Data("20 Turns", "lim_set_val_history_20")
	b30 := menu.Data("30 Turns", "lim_set_val_history_30")
	b50 := menu.Data("50 Turns", "lim_set_val_history_50")
	bCust := menu.Data("✏️ Custom Turns", "lim_input_history")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b10, b20),
		menu.Row(b30, b50),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderCompactionMenu renders auto compaction toggle and threshold options
func (ui *LimitsUI) RenderCompactionMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
	autoComp := true
	if pol != nil {
		autoComp = pol.AutoCompaction
	}
	compStatus := "🟢 Aktif (ON)"
	if !autoComp {
		compStatus = "🔴 Nonaktif (OFF)"
	}

	text := fmt.Sprintf("🗜️ <b>ATUR AUTO-COMPACTION (<code>%s:%s</code>)</b>\n\n"+
		"Status Auto-Compaction: <b>%s</b>\n\nPilih ambang batas (threshold turns) atau toggle status:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), compStatus)

	menu := &tele.ReplyMarkup{}
	bTgl := menu.Data("🔘 Toggle ON/OFF", "lim_set_val_compact_toggle")
	b10 := menu.Data("Threshold 10", "lim_set_val_thresh_10")
	b15 := menu.Data("Threshold 15", "lim_set_val_thresh_15")
	b25 := menu.Data("Threshold 25", "lim_set_val_thresh_25")
	bCust := menu.Data("✏️ Custom Threshold", "lim_input_threshold")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(bTgl),
		menu.Row(b10, b15),
		menu.Row(b25, bCust),
		menu.Row(bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderModelMenu renders interactive visual model override selector
func (ui *LimitsUI) RenderModelMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}

	pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
	curModel := "🔄 (Mengikuti Default Provider)"
	if pol != nil && pol.ModelOverride != "" {
		curModel = fmt.Sprintf("🎯 <code>%s</code>", html.EscapeString(pol.ModelOverride))
	}

	text := fmt.Sprintf("🤖 <b>PENGATURAN MODEL OVERRIDE (<code>%s:%s</code>)</b>\n\n"+
		"Model Aktif: %s\n\n"+
		"Pilih salah satu metode untuk menentukan model AI yang digunakan:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), curModel)

	menu := &tele.ReplyMarkup{}
	bCombos := menu.Data("🔀 Pilih Fallback Combo", "lim_mod_combos")
	bProvs := menu.Data("🤖 Pilih Provider & Model", "lim_mod_provs")
	bCust := menu.Data("✏️ Input Manual Nama Model", "lim_input_model")
	bReset := menu.Data("🔄 Reset ke Default Router", "lim_set_val_model_none")
	bBack := menu.Data("⬅️ Kembali ke Menu Limits", "lim_back_dash")

	menu.Inline(
		menu.Row(bCombos),
		menu.Row(bProvs),
		menu.Row(bCust, bReset),
		menu.Row(bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderLimitCombosPicker renders active fallback combos for selection
func (ui *LimitsUI) RenderLimitCombosPicker(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}

	var combos []*storage.ModelComboRecord
	if ui.provManager != nil {
		combos = ui.provManager.ListCombos()
	} else {
		rawCombos, _ := ui.db.ListCombos()
		for i := range rawCombos {
			combos = append(combos, &rawCombos[i])
		}
	}

	menu := &tele.ReplyMarkup{}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔀 <b>PILIH FALLBACK COMBO (<code>%s:%s</code>)</b>\n\n", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID)))

	if len(combos) == 0 {
		sb.WriteString("<i>(Belum ada fallback combo yang terdaftar. Buat dengan /combowizard)</i>\n\n")
	} else {
		sb.WriteString("Klik salah satu combo di bawah untuk diterapkan:\n\n")
		var rows []tele.Row
		var curRow []tele.Btn
		for i, combo := range combos {
			var targets []string
			for _, t := range combo.Targets {
				targets = append(targets, fmt.Sprintf("%s/%s", t.ProviderID, t.Model))
			}
			sb.WriteString(fmt.Sprintf("%d. <b>%s</b>: <code>%s</code>\n", i+1, html.EscapeString(combo.Name), html.EscapeString(strings.Join(targets, " ➔ "))))

			btn := menu.Data(fmt.Sprintf("🔀 %s", combo.Name), fmt.Sprintf("lim_mod_set_%s", combo.Name))
			curRow = append(curRow, btn)
			if len(curRow) == 2 {
				rows = append(rows, menu.Row(curRow...))
				curRow = []tele.Btn{}
			}
		}
		if len(curRow) > 0 {
			rows = append(rows, menu.Row(curRow...))
		}
		for _, r := range rows {
			menu.Inline(r)
		}
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "lim_mod_menu")
	menu.Inline(menu.Row(btnBack))

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// RenderLimitProvidersPicker renders list of active AI providers
func (ui *LimitsUI) RenderLimitProvidersPicker(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}

	if ui.provManager == nil {
		return c.Reply("❌ Provider Manager tidak tersedia.")
	}

	providers := ui.provManager.ListAll()
	menu := &tele.ReplyMarkup{}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🤖 <b>PILIH PROVIDER AI (<code>%s:%s</code>)</b>\n\n", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID)))

	if len(providers) == 0 {
		sb.WriteString("<i>(Tidak ada provider AI aktif yang terdaftar)</i>\n\n")
	} else {
		sb.WriteString("Pilih provider untuk melihat daftar model yang dapat digunakan:\n\n")
		var rows []tele.Row
		var curRow []tele.Btn
		for _, p := range providers {
			allModels := ui.getAllModelsForProvider(p)
			btnText := fmt.Sprintf("🤖 %s (%d)", p.Name(), len(allModels))
			btn := menu.Data(btnText, fmt.Sprintf("lim_mod_prov_%s_0", p.Name()))
			curRow = append(curRow, btn)
			if len(curRow) == 2 {
				rows = append(rows, menu.Row(curRow...))
				curRow = []tele.Btn{}
			}
		}
		if len(curRow) > 0 {
			rows = append(rows, menu.Row(curRow...))
		}
		for _, r := range rows {
			menu.Inline(r)
		}
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "lim_mod_menu")
	menu.Inline(menu.Row(btnBack))

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// RenderLimitProviderModelsPicker renders models for a provider with pagination
func (ui *LimitsUI) RenderLimitProviderModelsPicker(c tele.Context, provName string, page int) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}

	if ui.provManager == nil {
		return c.Reply("❌ Provider Manager tidak tersedia.")
	}

	p, ok := ui.provManager.Get(provName)
	if !ok || p == nil {
		return c.Reply("❌ Provider tidak ditemukan atau sedang dinonaktifkan.")
	}

	allModels := ui.getAllModelsForProvider(p)
	totalModels := len(allModels)
	if totalModels == 0 {
		menu := &tele.ReplyMarkup{}
		btnBack := menu.Data("⬅️ Pilih Provider Lain", "lim_mod_provs")
		menu.Inline(menu.Row(btnBack))
		return c.EditOrSend(fmt.Sprintf("🤖 <b>PROVIDER: %s</b>\n\n<i>(Tidak ada model terdaftar untuk provider ini)</i>", html.EscapeString(provName)), menu, tele.ModeHTML)
	}

	const modelsPerPage = 6
	totalPages := (totalModels + modelsPerPage - 1) / modelsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	startIdx := page * modelsPerPage
	endIdx := startIdx + modelsPerPage
	if endIdx > totalModels {
		endIdx = totalModels
	}

	pageModels := allModels[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🤖 <b>DAFTAR MODEL: %s</b>\n", html.EscapeString(strings.ToUpper(provName))))
	sb.WriteString(fmt.Sprintf("Scope: <code>%s:%s</code> | Hal <code>%d/%d</code> (Total: <code>%d</code>)\n\n", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID), page+1, totalPages, totalModels))
	sb.WriteString("Klik model di bawah untuk mengaktifkannya:\n\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for i, m := range pageModels {
		globalIdx := startIdx + i + 1
		isDef := strings.EqualFold(m, p.DefaultModel())
		defTag := ""
		if isDef {
			defTag = " ⭐ (Default)"
		}
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>%s\n", globalIdx, html.EscapeString(m), defTag))

		btnLabel := m
		if len([]rune(btnLabel)) > 26 {
			btnLabel = string([]rune(btnLabel)[:23]) + "..."
		}
		if isDef {
			btnLabel = "⭐ " + btnLabel
		}
		btn := menu.Data(btnLabel, fmt.Sprintf("lim_mod_pick_%s_%d", provName, startIdx+i))
		rows = append(rows, menu.Row(btn))
	}

	for _, r := range rows {
		menu.Inline(r)
	}

	// Pagination Navigation
	var navRow []tele.Btn
	if page > 0 {
		navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("lim_mod_prov_%s_%d", provName, page-1)))
	}
	navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "mod_noop"))
	if page < totalPages-1 {
		navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("lim_mod_prov_%s_%d", provName, page+1)))
	}
	if len(navRow) > 0 {
		menu.Inline(menu.Row(navRow...))
	}

	btnBackProv := menu.Data("⬅️ Daftar Provider", "lim_mod_provs")
	btnBackMain := menu.Data("🏠 Menu Model", "lim_mod_menu")
	menu.Inline(menu.Row(btnBackProv, btnBackMain))

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandlePickProviderModel handles model selection from provider models list
func (ui *LimitsUI) HandlePickProviderModel(c tele.Context, provName string, modelIdx int) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}

	if ui.provManager == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Provider Manager tidak tersedia"})
		return ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
	}

	p, ok := ui.provManager.Get(provName)
	if !ok || p == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Provider tidak ditemukan"})
		return ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
	}

	allModels := ui.getAllModelsForProvider(p)
	if modelIdx < 0 || modelIdx >= len(allModels) {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Model tidak valid"})
		return ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
	}

	chosenModel := allModels[modelIdx]
	pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
	if pol == nil {
		pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
	}
	pol.ModelOverride = chosenModel
	_ = ui.db.SavePolicy(pol)

	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🎯 Model '%s' aktif!", chosenModel)})
	return ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
}

func (ui *LimitsUI) getAllModelsForProvider(p provider.Provider) []string {
	seen := make(map[string]bool)
	var list []string

	if def := strings.TrimSpace(p.DefaultModel()); def != "" {
		list = append(list, def)
		seen[strings.ToLower(def)] = true
	}

	for _, m := range p.Models() {
		m = strings.TrimSpace(m)
		if m != "" && !seen[strings.ToLower(m)] {
			seen[strings.ToLower(m)] = true
			list = append(list, m)
		}
	}

	return list
}

// RenderTimeoutAPIMenu renders timeout API call options
func (ui *LimitsUI) RenderTimeoutAPIMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("⏱️ <b>ATUR TIMEOUT API CALL (<code>%s:%s</code>)</b>\n\nPilih batas waktu maksimal pemanggilan API AI:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b30 := menu.Data("30 Detik", "lim_set_val_timeapi_30")
	b60 := menu.Data("60 Detik", "lim_set_val_timeapi_60")
	b90 := menu.Data("90 Detik", "lim_set_val_timeapi_90")
	b120 := menu.Data("120 Detik", "lim_set_val_timeapi_120")
	bCust := menu.Data("✏️ Custom Detik", "lim_input_timeapi")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b30, b60),
		menu.Row(b90, b120),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderTimeoutHandlerMenu renders timeout handler options
func (ui *LimitsUI) RenderTimeoutHandlerMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("⏳ <b>ATUR TIMEOUT HANDLER (<code>%s:%s</code>)</b>\n\nPilih batas waktu total eksekusi handler pesan:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b60 := menu.Data("60 Detik", "lim_set_val_timehand_60")
	b120 := menu.Data("120 Detik", "lim_set_val_timehand_120")
	b180 := menu.Data("180 Detik", "lim_set_val_timehand_180")
	b300 := menu.Data("300 Detik", "lim_set_val_timehand_300")
	bCust := menu.Data("✏️ Custom Detik", "lim_input_timehand")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b60, b120),
		menu.Row(b180, b300),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderAuditMaxMenu renders audit log rotation limit options
func (ui *LimitsUI) RenderAuditMaxMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("📜 <b>ATUR ROTASI AUDIT LOG (<code>%s:%s</code>)</b>\n\nPilih batas maksimal riwayat log sebelum dirotasi:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b1k := menu.Data("1,000 Baris", "lim_set_val_auditmax_1000")
	b3k := menu.Data("3,000 Baris", "lim_set_val_auditmax_3000")
	b5k := menu.Data("5,000 Baris", "lim_set_val_auditmax_5000")
	b10k := menu.Data("10,000 Baris", "lim_set_val_auditmax_10000")
	bCust := menu.Data("✏️ Custom Baris", "lim_input_auditmax")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b1k, b3k),
		menu.Row(b5k, b10k),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderBudgetMenu renders token budget options
func (ui *LimitsUI) RenderBudgetMenu(c tele.Context) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	text := fmt.Sprintf("💰 <b>ATUR TOKEN BUDGET (<code>%s:%s</code>)</b>\n\nPilih batas akumulasi token budget:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))

	menu := &tele.ReplyMarkup{}
	b0 := menu.Data("♾️ Unlimited (0)", "lim_set_val_budget_0")
	b10k := menu.Data("10,000", "lim_set_val_budget_10000")
	b50k := menu.Data("50,000", "lim_set_val_budget_50000")
	b100k := menu.Data("100,000", "lim_set_val_budget_100000")
	bCust := menu.Data("✏️ Custom Tokens", "lim_input_budget")
	bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
	menu.Inline(
		menu.Row(b0, b10k),
		menu.Row(b50k, b100k),
		menu.Row(bCust, bBack),
	)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleSetVal applies setting change and refreshes dashboard
func (ui *LimitsUI) HandleSetVal(c tele.Context, param, val string) error {
	sess, ok := ui.GetSession(c.Sender().ID)
	if !ok || sess.Scope == "" {
		return ui.StartLimitsWizard(c)
	}
	pol, _ := ui.db.GetPolicy(sess.Scope, sess.ScopeID)
	if pol == nil {
		pol = &storage.PolicyRecord{
			Scope:               sess.Scope,
			ScopeID:             sess.ScopeID,
			MaxUploadFileMB:     10,
			MaxTokens:           2048,
			MaxHistoryTurns:     20,
			AutoCompaction:      true,
			CompactionThreshold: 15,
			FooterMode:          "off",
			MaxAuditLogs:        5000,
		}
	}

	switch param {
	case "footer":
		pol.FooterMode = val
	case "upload":
		if n, err := strconv.Atoi(val); err == nil {
			pol.MaxUploadFileMB = n
		}
	case "tokens":
		if n, err := strconv.Atoi(val); err == nil {
			pol.MaxTokens = n
		}
	case "history":
		if n, err := strconv.Atoi(val); err == nil {
			pol.MaxHistoryTurns = n
		}
	case "compact":
		if val == "toggle" {
			pol.AutoCompaction = !pol.AutoCompaction
		}
	case "thresh":
		if n, err := strconv.Atoi(val); err == nil {
			pol.CompactionThreshold = n
		}
	case "model":
		if val == "none" {
			pol.ModelOverride = ""
		} else {
			pol.ModelOverride = val
		}
	case "timeapi":
		if n, err := strconv.Atoi(val); err == nil {
			pol.TimeoutAPISeconds = n
		}
	case "timehand":
		if n, err := strconv.Atoi(val); err == nil {
			pol.TimeoutHandlerSec = n
		}
	case "auditmax":
		if n, err := strconv.Atoi(val); err == nil {
			pol.MaxAuditLogs = n
		}
	case "budget":
		if n, err := strconv.Atoi(val); err == nil {
			pol.TokenBudget = n
		}
	}

	_ = ui.db.SavePolicy(pol)
	return ui.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
}

// HandleRotateAudit executes manual pruning of audit logs
func (ui *LimitsUI) HandleRotateAudit(c tele.Context) error {
	sess, _ := ui.GetSession(c.Sender().ID)
	scope, scopeID := "global", "system"
	if sess != nil && sess.Scope != "" {
		scope, scopeID = sess.Scope, sess.ScopeID
	}
	pol, _ := ui.db.GetPolicy(scope, scopeID)
	maxLogs := 5000
	if pol != nil && pol.MaxAuditLogs > 0 {
		maxLogs = pol.MaxAuditLogs
	}
	_, _ = ui.db.RotateAuditLogs(maxLogs)
	_ = c.Reply("🧹 Pemangkasan / rotasi log audit berhasil dijalankan!", tele.ModeHTML)
	return ui.RenderScopeLimitsDashboard(c, scope, scopeID)
}
