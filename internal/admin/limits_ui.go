package admin

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type LimitsUI struct {
	db *storage.DB
}

func NewLimitsUI(db *storage.DB) *LimitsUI {
	return &LimitsUI{db: db}
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

	sb.WriteString("\n📋 <b>Cara Mengubah Pembatasan & Footer:</b>\n")
	sb.WriteString("Format: <code>/setlimit &lt;global|channel|chat&gt; &lt;id&gt; &lt;param&gt; &lt;value&gt;</code>\n")
	sb.WriteString("Atau shortcut: <code>/setfooter &lt;global|channel|chat&gt; &lt;id&gt; &lt;off|tokens|full&gt;</code>\n\n")
	sb.WriteString("Pilihan <code>&lt;param&gt;</code>:\n")
	sb.WriteString("• <code>footer</code> (<b>off</b> / <b>tokens</b> / <b>full</b>)\n")
	sb.WriteString("• <code>max_upload</code> (angka MB, cth: 5)\n")
	sb.WriteString("• <code>max_tokens</code> (angka token, cth: 4096)\n")
	sb.WriteString("• <code>max_history</code> (angka turn, cth: 15)\n")
	sb.WriteString("• <code>auto_compaction</code> (on / off)\n")
	sb.WriteString("• <code>compaction_threshold</code> (angka turn, cth: 10)\n")
	sb.WriteString("• <code>model</code> (nama model, cth: gpt-4o-mini)\n\n")
	sb.WriteString("<b>Contoh Perintah:</b>\n")
	sb.WriteString("• <code>/setlimit global system footer full</code>\n")
	sb.WriteString("• <code>/setfooter channel tg_main tokens</code>\n")
	sb.WriteString("• <code>/setfooter chat -100123456789 off</code>\n")
	sb.WriteString("• <code>/setlimit global system max_upload 15</code>\n")

	return sb.String()
}

// LimitsKeyboard returns inline keyboard with quick toggle for global footer mode
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
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnOff, btnTokens, btnFull),
		menu.Row(btnBack),
	)
	return menu
}

// HandleSetLimit processes `/setlimit` command
func (ui *LimitsUI) HandleSetLimit(c tele.Context) error {
	args := c.Args()
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
		// e.g. `/setfooter full` -> sets global system footer
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
