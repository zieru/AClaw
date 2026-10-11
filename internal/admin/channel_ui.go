package admin

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/channel/whatsapp"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
	tele "gopkg.in/telebot.v3"
)

type ChannelStep int

const (
	ChannelStepNone ChannelStep = iota
	ChannelStepIDAndName
	ChannelStepIdentifier
	ChannelStepGrapariChoice
	ChannelStepEditIdentifier
	ChannelStepAddTrustedNumbers
	ChannelStepAddAllowedGroups
	ChannelStepPairPhoneCode
)

type ChannelSession struct {
	Step             ChannelStep
	IsEditing        bool
	EditingChannelID string
	Type             string
	ID               string
	Name             string
	Identifier       string
	GrapariOnly      bool
	UpdatedAt        time.Time
}

type ChannelUI struct {
	db           *storage.DB
	toolRegistry *tools.Registry
	waUI         *WhatsAppUI
	mu           sync.RWMutex
	sessions     map[int64]*ChannelSession
}

func NewChannelUI(db *storage.DB, tr *tools.Registry) *ChannelUI {
	cui := &ChannelUI{
		db:           db,
		toolRegistry: tr,
		sessions:     make(map[int64]*ChannelSession),
	}
	cui.waUI = NewWhatsAppUI(db, cui)
	return cui
}

func (ui *ChannelUI) GetWhatsAppUI() *WhatsAppUI {
	return ui.waUI
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

			grpBadge := "🔴 Umum"
			if ch.IsGrapariOnly() {
				grpBadge = "🟢 Khusus GraPARI"
			}

			sb.WriteString(fmt.Sprintf("%d. %s [%s] <b>%s</b>\n", i+1, statusIcon, typeIcon, html.EscapeString(ch.Name)))
			sb.WriteString(fmt.Sprintf("   • Channel ID: <code>%s</code>\n", html.EscapeString(ch.ID)))
			sb.WriteString(fmt.Sprintf("   • Mode Khusus GraPARI: <code>%s</code>\n", grpBadge))
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
	sb.WriteString("• <code>/channelwizard</code> - Wizard interaktif kelola & tambah channel\n")
	sb.WriteString("• <code>/toolwizard</code> - Wizard matriks izin tool AI per channel\n")
	sb.WriteString("• <code>/addchannel &lt;id&gt; &lt;telegram|whatsapp&gt; &lt;name&gt; &lt;token_atau_webhook&gt; [grapari:y|n]</code>\n")
	sb.WriteString("• <code>/delchannel &lt;channel_id&gt;</code>\n")
	sb.WriteString("• <code>/toolperms &lt;channel_id&gt; &lt;tool_name&gt; &lt;allow|deny&gt;</code>\n")

	return sb.String()
}

// ChannelsKeyboard returns channel list keyboard with wizard buttons
func (ui *ChannelUI) ChannelsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnWizard := menu.Data("🧙‍♂️ Channel Wizard", "chan_wiz_start")
	btnTools := menu.Data("🧰 Matrix Tools", "tool_wiz_start")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnWizard, btnTools),
		menu.Row(btnBack),
	)
	return menu
}

// StartChannelWizard starts the interactive channel creator / editor
func (ui *ChannelUI) StartChannelWizard(c tele.Context) error {
	channels, _ := ui.db.ListChannels()

	text := "📱 <b>WIZARD MANAJEMEN CHANNEL (TELEGRAM & WHATSAPP)</b>\n\n" +
		"Pilih aksi untuk channel komunikasi GoAssistant:\n" +
		"<i>Klik nama channel di bawah untuk membuka dashboard & toggle Mode Khusus GraPARI.</i>"

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	btnAdd := menu.Data("➕ Tambah Channel Baru", "chan_wiz_add_type")
	rows = append(rows, menu.Row(btnAdd))

	if len(channels) > 0 {
		for _, ch := range channels {
			chCopy := ch
			statusIcon := "🟢"
			if !chCopy.IsActive {
				statusIcon = "🔴"
			}
			grpTag := ""
			if chCopy.IsGrapariOnly() {
				grpTag = " [GraPARI]"
			}
			btn := menu.Data(fmt.Sprintf("%s %s%s (%s)", statusIcon, chCopy.Name, grpTag, chCopy.ID), fmt.Sprintf("chan_ed_pick_%s", chCopy.ID))
			rows = append(rows, menu.Row(btn))
		}
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Channels", "menu_channels")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderAddChannelTypeMenu prompts for Telegram or WhatsApp
func (ui *ChannelUI) RenderAddChannelTypeMenu(c tele.Context) error {
	text := "📱 <b>PILIH TIPE CHANNEL BARU:</b>\n\n" +
		"• <b>Telegram Bot:</b> Bot publik tambahan yang dikontrol oleh GoAssistant.\n" +
		"• <b>WhatsApp Native:</b> Multi-Device Linked Device (Scan QR langsung via Telegram)."

	menu := &tele.ReplyMarkup{}
	btnTG := menu.Data("✈️ Telegram Bot", "chan_type_telegram")
	btnWA := menu.Data("🟢 WhatsApp (Linked Device)", "chan_type_whatsapp")
	btnBack := menu.Data("⬅️ Batal / Kembali", "chan_wiz_start")

	menu.Inline(
		menu.Row(btnTG, btnWA),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// PromptChannelIDAndName prompts user to send `id|name`
func (ui *ChannelUI) PromptChannelIDAndName(c tele.Context, chType string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &ChannelSession{
		Step:      ChannelStepIDAndName,
		Type:      chType,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := fmt.Sprintf("📱 <b>SETUP IDENTITAS CHANNEL (%s)</b>\n\n"+
		"Kirimkan format identitas channel:\n"+
		"<code>id|Nama Tampilan</code>\n\n"+
		"<b>Contoh:</b>\n"+
		"<code>tg_support|Customer Support Bot</code>\n"+
		"<code>wa_sales|WhatsApp Official Sales</code>", strings.ToUpper(chType))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "chan_wiz_start")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// GetSession returns the active wizard session for a user
func (ui *ChannelUI) GetSession(userID int64) (*ChannelSession, bool) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	sess, ok := ui.sessions[userID]
	return sess, ok
}

// PromptGrapariOnly displays the interactive Y/N prompt for GraPARI-only mode
func (ui *ChannelUI) PromptGrapariOnly(c tele.Context, sess *ChannelSession) error {
	sess.Step = ChannelStepGrapariChoice
	sess.UpdatedAt = time.Now()

	text := fmt.Sprintf("🏢 <b>KHUSUS GRAPARI? (Y/N)</b>\n\n"+
		"Apakah channel <b>%s</b> (<code>%s</code>) ini khusus operasional SOP GraPARI?\n\n"+
		"• <b>Jika YA (Y):</b> Bot HANYA dapat mengeksekusi tool SOP <code>g3a_search_grapari_knowledge</code> dan pencarian situs resmi <code>search_telkomsel_web</code>. Pertanyaan di luar ranah Telkomsel otomatis ditolak langsung.\n"+
		"• <b>Jika TIDAK (N):</b> Bot berjalan normal sebagai asisten umum dengan seluruh tools yang diizinkan.\n\n"+
		"<i>Silakan klik tombol di bawah atau ketik Y / N:</i>",
		html.EscapeString(sess.Name), html.EscapeString(sess.ID))

	menu := &tele.ReplyMarkup{}
	btnYes := menu.Data("✅ Ya (Khusus GraPARI)", "chan_wiz_grp_y")
	btnNo := menu.Data("❌ Tidak (General Bot)", "chan_wiz_grp_n")
	btnCancel := menu.Data("⬅️ Batal", "chan_wiz_start")

	menu.Inline(
		menu.Row(btnYes, btnNo),
		menu.Row(btnCancel),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// FinalizeCreateChannel completes channel creation with chosen GraPARI mode
func (ui *ChannelUI) FinalizeCreateChannel(c tele.Context, sess *ChannelSession, grapariOnly bool) error {
	rec := &storage.ChannelRecord{
		ID:           sess.ID,
		Type:         sess.Type,
		Name:         sess.Name,
		Identifier:   sess.Identifier,
		IsActive:     true,
		SettingsJSON: "{}",
	}
	rec.SetGrapariOnly(grapariOnly)

	if err := ui.db.SaveChannel(rec); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan channel: %v", html.EscapeString(err.Error())))
	}

	// Jika GraPARI-only, otomatis konfigurasi permissions agar tool GraPARI diizinkan
	if grapariOnly {
		for _, t := range ui.toolRegistry.ListAll() {
			allowed := (t.Name() == "g3a_search_grapari_knowledge" || t.Name() == "search_telkomsel_web")
			_ = ui.db.SetChannelToolPerm(rec.ID, t.Name(), allowed)
		}
	}

	userID := int64(0)
	if c.Sender() != nil {
		userID = c.Sender().ID
	}
	ui.CancelWizard(userID)

	modeDesc := "Umum (General Assistant)"
	if grapariOnly {
		modeDesc = "Khusus SOP & Operasional GraPARI (g3a_search_grapari_knowledge)"
	}

	if sess.Type == "whatsapp" {
		_ = c.Reply(fmt.Sprintf("🎉 <b>CHANNEL WHATSAPP DIBUAT!</b>\n\n• Nama: <b>%s</b>\n• ID: <code>%s</code>\n• Mode: <b>%s</b>\n\nMenyiapkan QR Code...",
			html.EscapeString(rec.Name), html.EscapeString(rec.ID), modeDesc), tele.ModeHTML)
		_ = ui.waUI.SendQRCodePhoto(c, rec.ID)
		return nil
	}

	_ = c.Reply(fmt.Sprintf("🎉 <b>CHANNEL BERHASIL DITAMBAHKAN!</b>\n\n• Nama: <b>%s</b>\n• ID: <code>%s</code>\n• Tipe: <code>%s</code>\n• Mode: <b>%s</b>\n\n<i>Channel siap menerima pesan saat sistem dijalankan!</i>",
		html.EscapeString(rec.Name), html.EscapeString(rec.ID), html.EscapeString(rec.Type), modeDesc), tele.ModeHTML)
	return ui.RenderChannelDashboard(c, rec)
}

// RenderChannelDashboard displays channel details and management options
func (ui *ChannelUI) RenderChannelDashboard(c tele.Context, ch *storage.ChannelRecord) error {
	if ch.Type == "whatsapp" {
		return ui.waUI.RenderWADashboard(c, ch)
	}

	statusText := "🟢 <b>Aktif</b>"
	if !ch.IsActive {
		statusText = "🔴 <b>Nonaktif</b>"
	}

	grapariStatus := "🔴 <b>Tidak (Umum)</b>"
	grapariBtnText := "🏢 Khusus GraPARI: OFF"
	if ch.IsGrapariOnly() {
		grapariStatus = "🟢 <b>Ya (SOP GraPARI Sahaja)</b>"
		grapariBtnText = "🏢 Khusus GraPARI: ON"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📱 <b>MANAJEMEN CHANNEL TELEGRAM: %s</b>\n\n", html.EscapeString(ch.Name)))
	sb.WriteString(fmt.Sprintf("• <b>Status:</b> %s\n", statusText))
	sb.WriteString(fmt.Sprintf("• <b>Channel ID:</b> <code>%s</code>\n", html.EscapeString(ch.ID)))
	sb.WriteString(fmt.Sprintf("• <b>Tipe:</b> <code>%s</code>\n", html.EscapeString(ch.Type)))
	sb.WriteString(fmt.Sprintf("• <b>Mode Khusus GraPARI:</b> %s\n", grapariStatus))
	sb.WriteString(fmt.Sprintf("• <b>Bot Token:</b> <code>%s</code>\n\n", html.EscapeString(ch.Identifier)))
	sb.WriteString("Pilih aksi untuk channel ini:")

	menu := &tele.ReplyMarkup{}
	btnToggle := menu.Data("🔘 Toggle Status", fmt.Sprintf("chan_tgl_%s", ch.ID))
	btnEditToken := menu.Data("🔑 Ganti Token", fmt.Sprintf("chan_ed_tok_%s", ch.ID))
	btnGrapari := menu.Data(grapariBtnText, fmt.Sprintf("chan_tgl_grp_%s", ch.ID))
	btnToolPerms := menu.Data("🧰 Atur Permissions Tool", fmt.Sprintf("tool_wiz_ch_%s", ch.ID))
	btnPol := menu.Data("⚙️ Limit & Model Policy", fmt.Sprintf("pol_wiz_ch_%s", ch.ID))
	btnDel := menu.Data("🗑️ Hapus Channel", fmt.Sprintf("chan_del_%s", ch.ID))
	btnBack := menu.Data("⬅️ Kembali", "chan_wiz_start")

	menu.Inline(
		menu.Row(btnToggle, btnEditToken),
		menu.Row(btnGrapari, btnToolPerms),
		menu.Row(btnPol, btnDel),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// StartToolWizard renders channel picker or direct tool matrix
func (ui *ChannelUI) StartToolWizard(c tele.Context, channelID string) error {
	channels, err := ui.db.ListChannels()
	if err != nil || len(channels) == 0 {
		return c.Reply("⚠️ Belum ada channel yang terdaftar. Tambahkan channel terlebih dahulu.", tele.ModeHTML)
	}

	if channelID == "" {
		text := "🧰 <b>WIZARD HAK AKSES TOOLS AI (TOOL PERMISSIONS MATRIX)</b>\n\n" +
			"Pilih channel yang ingin diatur izin penggunaan tool AI-nya:"

		menu := &tele.ReplyMarkup{}
		var rows []tele.Row

		for _, ch := range channels {
			chCopy := ch
			btn := menu.Data(fmt.Sprintf("📱 %s (%s)", chCopy.Name, chCopy.ID), fmt.Sprintf("tool_wiz_ch_%s", chCopy.ID))
			rows = append(rows, menu.Row(btn))
		}

		btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
		rows = append(rows, menu.Row(btnBack))
		menu.Inline(rows...)

		return c.EditOrSend(text, menu, tele.ModeHTML)
	}

	// Render Matrix for channelID
	perms, _ := ui.db.GetChannelToolPerms(channelID)
	allTools := ui.toolRegistry.ListAll()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧰 <b>TOOL PERMISSIONS MATRIX: <code>%s</code></b>\n\n", html.EscapeString(channelID)))
	sb.WriteString("Klik tombol tool di bawah untuk <b>mengizinkan (🟢)</b> atau <b>memblokir (🔴)</b> tool pada channel ini:\n\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, t := range allTools {
		tName := t.Name()
		allowed := true
		if val, exists := perms[tName]; exists {
			allowed = val
		}

		statusEmoji := "🟢"
		statusText := "ON"
		if !allowed {
			statusEmoji = "🔴"
			statusText = "OFF"
		}

		btn := menu.Data(fmt.Sprintf("%s %s: %s", statusEmoji, tName, statusText), fmt.Sprintf("tperm_%s_%s", channelID, tName))
		rows = append(rows, menu.Row(btn))
	}

	btnChangeChan := menu.Data("🔄 Ganti Channel", "tool_wiz_start")
	btnBack := menu.Data("⬅️ Kembali ke Menu Tools", "menu_tools")
	rows = append(rows, menu.Row(btnChangeChan, btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleToggleToolPerm toggles permission of a tool for a channel
func (ui *ChannelUI) HandleToggleToolPerm(c tele.Context, channelID, toolName string) error {
	perms, _ := ui.db.GetChannelToolPerms(channelID)
	allowed := true
	if val, exists := perms[toolName]; exists {
		allowed = val
	}
	newAllowed := !allowed

	_ = ui.db.SetChannelToolPerm(channelID, toolName, newAllowed)
	return ui.StartToolWizard(c, channelID)
}

// HandleTextMessage handles input during channel setup
func (ui *ChannelUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID

	ui.mu.RLock()
	sess, exists := ui.sessions[userID]
	ui.mu.RUnlock()

	if !exists || sess.Step == ChannelStepNone {
		return false, nil
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") {
		ui.CancelWizard(userID)
		return true, c.Reply("❌ Wizard channel dibatalkan.")
	}

	switch sess.Step {
	case ChannelStepIDAndName:
		parts := strings.Split(msgText, "|")
		if len(parts) < 2 {
			return true, c.Reply("⚠️ Format salah! Gunakan: <code>id|Nama Channel</code>\nContoh: <code>tg_sales|Bot Sales Support</code>", tele.ModeHTML)
		}
		sess.ID = strings.ToLower(strings.TrimSpace(parts[0]))
		sess.Name = strings.TrimSpace(parts[1])

		if sess.Type == "whatsapp" {
			sess.Identifier = ""
			return true, ui.PromptGrapariOnly(c, sess)
		}

		sess.Step = ChannelStepIdentifier
		promptText := fmt.Sprintf("🔑 <b>MASUKKAN BOT TOKEN (%s)</b>\n\n"+
			"Silakan kirimkan Bot Token dari @BotFather untuk bot Telegram ini:",
			html.EscapeString(sess.Name))
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", "chan_wiz_start")
		menu.Inline(menu.Row(btnCancel))
		return true, c.Reply(promptText, menu, tele.ModeHTML)

	case ChannelStepIdentifier:
		sess.Identifier = msgText
		return true, ui.PromptGrapariOnly(c, sess)

	case ChannelStepGrapariChoice:
		lower := strings.ToLower(msgText)
		var grapariOnly bool
		if lower == "y" || lower == "yes" || lower == "ya" || lower == "1" || strings.HasPrefix(lower, "y") {
			grapariOnly = true
		} else if lower == "n" || lower == "no" || lower == "tidak" || lower == "0" || strings.HasPrefix(lower, "n") {
			grapariOnly = false
		} else {
			return true, c.Reply("⚠️ Mohon ketik <b>Y</b> (Ya) atau <b>N</b> (Tidak), atau klik tombol di atas.", tele.ModeHTML)
		}
		return true, ui.FinalizeCreateChannel(c, sess, grapariOnly)

	case ChannelStepEditIdentifier:
		ch, err := ui.db.GetChannel(sess.EditingChannelID)
		if err != nil || ch == nil {
			ui.CancelWizard(userID)
			return true, c.Reply("❌ Channel tidak ditemukan.")
		}
		ch.Identifier = msgText
		_ = ui.db.SaveChannel(ch)
		ui.CancelWizard(userID)
		_ = c.Reply(fmt.Sprintf("✅ Token / Endpoint untuk channel <b>%s</b> berhasil diperbarui!", html.EscapeString(ch.Name)), tele.ModeHTML)
		return true, ui.RenderChannelDashboard(c, ch)

	case ChannelStepAddTrustedNumbers:
		ch, err := ui.db.GetChannel(sess.EditingChannelID)
		if err != nil || ch == nil {
			ui.CancelWizard(userID)
			return true, c.Reply("❌ Channel tidak ditemukan.")
		}
		var st whatsapp.WhatsAppSettings
		if ch.SettingsJSON != "" {
			_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
		} else {
			st = whatsapp.DefaultWhatsAppSettings()
		}

		rawItems := strings.FieldsFunc(msgText, func(r rune) bool {
			return r == ',' || r == '\n' || r == ';'
		})
		for _, item := range rawItems {
			cleaned := strings.TrimPrefix(strings.TrimSpace(item), "+")
			if cleaned != "" {
				// avoid duplicates
				exists := false
				for _, n := range st.TrustedNumbers {
					if n == cleaned {
						exists = true
						break
					}
				}
				if !exists {
					st.TrustedNumbers = append(st.TrustedNumbers, cleaned)
				}
			}
		}

		stBytes, _ := json.Marshal(st)
		ch.SettingsJSON = string(stBytes)
		_ = ui.db.SaveChannel(ch)
		if mgr := whatsapp.GetManager(); mgr != nil {
			if ad := mgr.GetAdapter(ch.ID); ad != nil {
				_ = ad.UpdateSettings(st)
			}
		}

		ui.CancelWizard(userID)
		_ = c.Reply("✅ Nomor berhasil ditambahkan ke Trusted List WhatsApp!", tele.ModeHTML)
		return true, ui.waUI.RenderWhitelistManagerMenu(c, ch.ID)

	case ChannelStepAddAllowedGroups:
		ch, err := ui.db.GetChannel(sess.EditingChannelID)
		if err != nil || ch == nil {
			ui.CancelWizard(userID)
			return true, c.Reply("❌ Channel tidak ditemukan.")
		}
		var st whatsapp.WhatsAppSettings
		if ch.SettingsJSON != "" {
			_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
		} else {
			st = whatsapp.DefaultWhatsAppSettings()
		}

		rawItems := strings.FieldsFunc(msgText, func(r rune) bool {
			return r == ',' || r == '\n' || r == ';'
		})
		for _, item := range rawItems {
			cleaned := strings.TrimSpace(item)
			if cleaned != "" {
				exists := false
				for _, g := range st.AllowedGroups {
					if g == cleaned {
						exists = true
						break
					}
				}
				if !exists {
					st.AllowedGroups = append(st.AllowedGroups, cleaned)
				}
			}
		}

		stBytes, _ := json.Marshal(st)
		ch.SettingsJSON = string(stBytes)
		_ = ui.db.SaveChannel(ch)
		if mgr := whatsapp.GetManager(); mgr != nil {
			if ad := mgr.GetAdapter(ch.ID); ad != nil {
				_ = ad.UpdateSettings(st)
			}
		}

		ui.CancelWizard(userID)
		_ = c.Reply("✅ ID Grup berhasil ditambahkan ke Whitelist WhatsApp!", tele.ModeHTML)
		return true, ui.waUI.RenderWhitelistManagerMenu(c, ch.ID)

	case ChannelStepPairPhoneCode:
		ch, err := ui.db.GetChannel(sess.EditingChannelID)
		if err != nil || ch == nil {
			ui.CancelWizard(userID)
			return true, c.Reply("❌ Channel tidak ditemukan.")
		}
		mgr := whatsapp.GetManager()
		if mgr == nil {
			ui.CancelWizard(userID)
			return true, c.Reply("❌ WhatsApp Manager belum aktif.")
		}
		adapter, err := mgr.CreateOrGetAdapter(ch)
		if err != nil {
			ui.CancelWizard(userID)
			return true, c.Reply(fmt.Sprintf("❌ Gagal menginisialisasi adapter WA: %v", err))
		}

		code, err := adapter.PairPhone(msgText)
		if err != nil {
			ui.CancelWizard(userID)
			return true, c.Reply(fmt.Sprintf("❌ Gagal meminta pairing code: %v\n\nPastikan nomor WA sudah benar dan memiliki awalan kode negara (contoh: <code>628123456789</code>).", html.EscapeString(err.Error())), tele.ModeHTML)
		}

		ui.CancelWizard(userID)

		text := fmt.Sprintf("🎉 <b>KODE PAIRING WHATSAPP (8-DIGIT)</b>\n\n"+
			"Nomor: <code>%s</code>\n\n"+
			"🔑 Kode Anda:\n<b><code>%s</code></b>\n\n"+
			"<b>Langkah Aktivasi di HP:</b>\n"+
			"1. Buka aplikasi <b>WhatsApp</b> di HP.\n"+
			"2. Buka <b>Setelan / Pengaturan</b> ➡️ <b>Perangkat Tertaut (Linked Devices)</b>.\n"+
			"3. Ketuk <b>Tautkan Perangkat</b>.\n"+
			"4. Di bawah layar scanner, ketuk <b>'Tautkan dengan nomor telepon saja'</b> (<i>Link with phone number instead</i>).\n"+
			"5. Masukkan 8 karakter kode di atas: <b><code>%s</code></b>",
			html.EscapeString(msgText), code, code)

		menu := &tele.ReplyMarkup{}
		btnDash := menu.Data("⬅️ Ke Dashboard WA", fmt.Sprintf("chan_ed_pick_%s", ch.ID))
		menu.Inline(menu.Row(btnDash))

		return true, c.Reply(text, menu, tele.ModeHTML)
	}

	return false, nil
}

// CancelWizard clears user session
func (ui *ChannelUI) CancelWizard(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.sessions, userID)
}

// SetSessionStep sets active wizard step
func (ui *ChannelUI) SetSessionStep(userID int64, chID string, step ChannelStep) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.sessions[userID] = &ChannelSession{
		IsEditing:        true,
		EditingChannelID: chID,
		Step:             step,
		UpdatedAt:        time.Now(),
	}
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
	sb.WriteString("• Gunakan tombol <b>🧰 Matriks Tools (Wizard)</b> untuk mengatur tombol toggle izin secara visual.\n")
	sb.WriteString("• Atau command: <code>/toolperms &lt;channel_id&gt; &lt;tool_name&gt; &lt;allow|deny&gt;</code>\n")

	return sb.String()
}

// HandleAddChannel processes `/addchannel`
func (ui *ChannelUI) HandleAddChannel(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return ui.StartChannelWizard(c)
	}
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
		ID:           id,
		Type:         chType,
		Name:         name,
		Identifier:   identifier,
		IsActive:     true,
		SettingsJSON: "{}",
	}

	grapariOnly := false
	if len(args) >= 5 {
		opt := strings.ToLower(args[4])
		if opt == "y" || opt == "yes" || opt == "ya" || opt == "1" || opt == "grapari" {
			grapariOnly = true
		}
	}
	rec.SetGrapariOnly(grapariOnly)

	if err := ui.db.SaveChannel(rec); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan channel: %v", html.EscapeString(err.Error())))
	}

	if grapariOnly {
		for _, t := range ui.toolRegistry.ListAll() {
			allowed := (t.Name() == "g3a_search_grapari_knowledge" || t.Name() == "search_telkomsel_web")
			_ = ui.db.SetChannelToolPerm(rec.ID, t.Name(), allowed)
		}
	}

	modeStr := ""
	if grapariOnly {
		modeStr = " (Mode: Khusus GraPARI)"
	}

	return c.Reply(fmt.Sprintf("✅ Channel <b>%s</b> (<code>%s</code>)%s berhasil ditambahkan!\nSistem akan memuat channel saat startup.", html.EscapeString(name), html.EscapeString(id), modeStr), tele.ModeHTML)
}

// HandleToolPerms processes `/toolperms`
func (ui *ChannelUI) HandleToolPerms(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return ui.StartToolWizard(c, "")
	}
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

