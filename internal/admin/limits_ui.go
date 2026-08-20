package admin

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

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
)

type LimitsSession struct {
	Step      LimitsStep
	Scope     string
	ScopeID   string
	UpdatedAt time.Time
}

type LimitsUI struct {
	db       *storage.DB
	mu       sync.RWMutex
	sessions map[int64]*LimitsSession
}

func NewLimitsUI(db *storage.DB) *LimitsUI {
	return &LimitsUI{
		db:       db,
		sessions: make(map[int64]*LimitsSession),
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
		}
	}

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
	sb.WriteString("• Atau gunakan perintah manual: <code>/setlimit &lt;global|channel|chat&gt; &lt;id&gt; &lt;param&gt; &lt;value&gt;</code>\n")

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
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnOff, btnTokens, btnFull),
		menu.Row(btnWizard),
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

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🛡️ <b>LIMITS DASHBOARD: <code>%s:%s</code></b>\n\n", html.EscapeString(scope), html.EscapeString(scopeID)))
	sb.WriteString(fmt.Sprintf("• 📁 <b>Max Upload:</b> <code>%d MB</code>\n", pol.MaxUploadFileMB))
	sb.WriteString(fmt.Sprintf("• 🪙 <b>Max Output Tokens:</b> <code>%d tokens</code>\n", pol.MaxTokens))
	sb.WriteString(fmt.Sprintf("• 💬 <b>Max History Turns:</b> <code>%d turns</code>\n", pol.MaxHistoryTurns))
	sb.WriteString(fmt.Sprintf("• 🗜️ <b>Auto-Compaction:</b> %s (Ambang: <code>%d turns</code>)\n", autoCompStr, pol.CompactionThreshold))
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
	btnChangeScope := menu.Data("🔄 Ganti Scope", "lim_wiz_start")
	btnBack := menu.Data("⬅️ Kembali ke Menu", "menu_limits")

	menu.Inline(
		menu.Row(btnFooter, btnUpload),
		menu.Row(btnTokens, btnHistory),
		menu.Row(btnCompact, btnModel),
		menu.Row(btnChangeScope, btnBack),
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
		return c.Reply("⚠️ Format salah!\nContoh: <code>/setlimit global system footer full</code>\nAtau: <code>/setlimit global system max_upload 15</code>", tele.ModeHTML)
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

