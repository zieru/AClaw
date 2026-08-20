package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/storage"
	"goassistant/internal/tools"
	tele "gopkg.in/telebot.v3"
)

type ChannelUI struct {
	db           *storage.DB
	toolRegistry *tools.Registry
}

func NewChannelUI(db *storage.DB, tr *tools.Registry) *ChannelUI {
	return &ChannelUI{db: db, toolRegistry: tr}
}

// RenderChannelsList returns summary of all channels in HTML format
func (ui *ChannelUI) RenderChannelsList() string {
	channels, err := ui.db.ListChannels()
	if err != nil {
		return fmt.Sprintf("❌ Error mengambil data channel: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("📱 <b>DAFTAR CHANNEL KOMUNIKASI (WA / TELEGRAM)</b>\n\n")

	if len(channels) == 0 {
		sb.WriteString("(Belum ada channel yang ditambahkan)\n\n")
	} else {
		for i, ch := range channels {
			statusIcon := "🟢"
			if !ch.IsActive {
				statusIcon = "🔴"
			}
			typeIcon := "✈️ TG"
			if ch.Type == "whatsapp" {
				typeIcon = "🟢 WA"
			}

			sb.WriteString(fmt.Sprintf("%d. %s [%s] <b>%s</b>\n", i+1, statusIcon, typeIcon, html.EscapeString(ch.Name)))
			sb.WriteString(fmt.Sprintf("   • Channel ID: <code>%s</code>\n", html.EscapeString(ch.ID)))
			sb.WriteString(fmt.Sprintf("   • Default Agent: <code>%s</code>\n", html.EscapeString(ch.DefaultAgent)))

			// Check tool permissions for this channel
			perms, _ := ui.db.GetChannelToolPerms(ch.ID)
			allowedCount := 0
			for _, t := range ui.toolRegistry.ListAll() {
				if allowed, ok := perms[t.Name()]; !ok || allowed {
					allowedCount++
				}
			}
			sb.WriteString(fmt.Sprintf("   • Tools Diizinkan: <code>%d/%d</code>\n\n", allowedCount, len(ui.toolRegistry.ListAll())))
		}
	}

	sb.WriteString("📋 <b>Perintah Manajemen Channel:</b>\n")
	sb.WriteString("• <code>/addchannel &lt;id&gt; &lt;telegram|whatsapp&gt; &lt;name&gt; &lt;token_atau_webhook_url&gt;</code>\n")
	sb.WriteString("• <code>/delchannel &lt;channel_id&gt;</code>\n")
	sb.WriteString("• <code>/toolperms &lt;channel_id&gt; &lt;tool_name&gt; &lt;allow|deny&gt;</code>\n")
	sb.WriteString("• <code>/tools</code> (Melihat daftar seluruh tools)\n\n")
	sb.WriteString("<b>Contoh Menambahkan Channel Bot:</b>\n")
	sb.WriteString("<code>/addchannel public_tg telegram \"Bot Publik Group\" 7654321:AAFxxx...</code>\n")

	return sb.String()
}

// RenderToolsList returns all tools in registry
func (ui *ChannelUI) RenderToolsList() string {
	allTools := ui.toolRegistry.ListAll()

	var sb strings.Builder
	sb.WriteString("🧰 <b>DAFTAR TOOL SISTEM (AI FUNCTION CALLING)</b>\n\n")

	for i, t := range allTools {
		sb.WriteString(fmt.Sprintf("%d. 🔧 <b>%s</b>\n", i+1, html.EscapeString(t.Name())))
		sb.WriteString(fmt.Sprintf("   • Deskripsi: %s\n\n", html.EscapeString(t.Description())))
	}

	sb.WriteString("💡 <b>Atur Izin Tool per Channel:</b>\n")
	sb.WriteString("<code>/toolperms &lt;channel_id&gt; &lt;tool_name&gt; &lt;allow|deny&gt;</code>\n")
	sb.WriteString("Contoh pembatasan: <code>/toolperms public_tg bash_exec deny</code>\n")

	return sb.String()
}

// HandleAddChannel processes `/addchannel`
func (ui *ChannelUI) HandleAddChannel(c tele.Context) error {
	args := c.Args()
	if len(args) < 4 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/addchannel tg_sales telegram \"Sales Bot\" 123456:ABC-DEF</code>", tele.ModeHTML)
	}

	id := args[0]
	chType := strings.ToLower(args[1])
	name := args[2]
	identifier := args[3]

	if chType != "telegram" && chType != "whatsapp" {
		return c.Reply("❌ Tipe channel harus <code>telegram</code> atau <code>whatsapp</code>", tele.ModeHTML)
	}

	rec := &storage.ChannelRecord{
		ID:         id,
		Type:       chType,
		Name:       name,
		Identifier: identifier,
		IsActive:   true,
	}

	if err := ui.db.SaveChannel(rec); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan channel: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("✅ Channel <b>%s</b> (<code>%s</code>) berhasil ditambahkan!\nSistem akan memuat channel saat startup.", html.EscapeString(name), html.EscapeString(id)), tele.ModeHTML)
}

// HandleToolPerms processes `/toolperms`
func (ui *ChannelUI) HandleToolPerms(c tele.Context) error {
	args := c.Args()
	if len(args) < 3 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/toolperms public_tg bash_exec deny</code>", tele.ModeHTML)
	}

	channelID := args[0]
	toolName := args[1]
	action := strings.ToLower(args[2])

	isAllowed := action == "allow" || action == "on" || action == "true" || action == "1"

	if err := ui.db.SetChannelToolPerm(channelID, toolName, isAllowed); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mengatur izin tool: %v", html.EscapeString(err.Error())))
	}

	statusStr := "DIIZINKAN (Allowed) ✅"
	if !isAllowed {
		statusStr = "DIBLOKIR (Blocked) ⛔"
	}

	return c.Reply(fmt.Sprintf("✅ Tool <code>%s</code> untuk channel <code>%s</code> kini berstatus: <b>%s</b>", html.EscapeString(toolName), html.EscapeString(channelID), statusStr), tele.ModeHTML)
}
