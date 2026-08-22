package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"goassistant/internal/channel/whatsapp"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type WhatsAppUI struct {
	db        *storage.DB
	channelUI *ChannelUI
}

func NewWhatsAppUI(db *storage.DB, cui *ChannelUI) *WhatsAppUI {
	return &WhatsAppUI{
		db:        db,
		channelUI: cui,
	}
}

// RenderWADashboard displays rich WhatsApp channel details and management options
func (ui *WhatsAppUI) RenderWADashboard(c tele.Context, ch *storage.ChannelRecord) error {
	mgr := whatsapp.GetManager()
	var adapter *whatsapp.NativeAdapter
	if mgr != nil {
		adapter = mgr.GetAdapter(ch.ID)
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" && ch.SettingsJSON != "{}" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	connStatus := "🔴 <b>Terputus (Offline)</b>"
	if adapter != nil {
		if adapter.IsConnected() && adapter.IsLoggedIn() {
			connStatus = "🟢 <b>Terhubung (Online & Linked)</b>"
		} else if adapter.IsConnected() {
			connStatus = "🟡 <b>Menunggu Scan QR</b>"
		}
	}

	phoneStr := "<i>Belum tertaut</i>"
	if st.JID != "" {
		phoneStr = fmt.Sprintf("<code>%s</code>", html.EscapeString(st.JID))
	}

	dmBadge := "🟢 Terbuka (Allow All)"
	switch st.DMPolicy {
	case whatsapp.DMPolicyTrusted:
		dmBadge = fmt.Sprintf("🟡 Trusted List Sahaja (%d nomor)", len(st.TrustedNumbers))
	case whatsapp.DMPolicyBlock:
		dmBadge = "🔴 Blokir Semua DM"
	}

	grpBadge := "🟢 Semua Grup (Allow All)"
	switch st.GroupPolicy {
	case whatsapp.GroupPolicyWhitelist:
		grpBadge = fmt.Sprintf("🟡 Whitelist Grup (%d grup)", len(st.AllowedGroups))
	case whatsapp.GroupPolicyBlock:
		grpBadge = "🔴 Blokir Semua Grup"
	}

	mentionBadge := "🎯 Wajib Mention / Reply"
	if st.MentionPolicy == whatsapp.MentionPolicyAll {
		mentionBadge = "📢 Semua Pesan Grup"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📱 <b>MANAJEMEN WHATSAPP: %s</b>\n\n", html.EscapeString(ch.Name)))
	sb.WriteString(fmt.Sprintf("• <b>Status Sinyal:</b> %s\n", connStatus))
	sb.WriteString(fmt.Sprintf("• <b>Channel ID:</b> <code>%s</code>\n", html.EscapeString(ch.ID)))
	sb.WriteString(fmt.Sprintf("• <b>Nomor / JID:</b> %s\n\n", phoneStr))
	sb.WriteString("🛡️ <b>Kebijakan Privasi & Chat:</b>\n")
	sb.WriteString(fmt.Sprintf("• <b>Direct Message (DM):</b> %s\n", dmBadge))
	sb.WriteString(fmt.Sprintf("• <b>Akses Grup:</b> %s\n", grpBadge))
	sb.WriteString(fmt.Sprintf("• <b>Respon Grup:</b> %s\n\n", mentionBadge))
	sb.WriteString("Pilih menu konfigurasi di bawah:")

	menu := &tele.ReplyMarkup{}
	btnQR := menu.Data("📲 Scan / Pairing QR", fmt.Sprintf("chan_wa_qr_%s", ch.ID))
	btnPairCode := menu.Data("🔢 Tautkan Kode 8-Digit", fmt.Sprintf("chan_wa_paircode_prompt_%s", ch.ID))
	btnDM := menu.Data("💬 DM Policy", fmt.Sprintf("chan_wa_dm_menu_%s", ch.ID))
	btnGrp := menu.Data("👥 Group Policy", fmt.Sprintf("chan_wa_grp_menu_%s", ch.ID))
	btnMen := menu.Data("🎯 Mention Mode", fmt.Sprintf("chan_wa_men_menu_%s", ch.ID))
	btnLists := menu.Data("📋 Kelola Whitelist", fmt.Sprintf("chan_wa_list_menu_%s", ch.ID))
	btnTools := menu.Data("🧰 Permissions Tool", fmt.Sprintf("tool_wiz_ch_%s", ch.ID))
	btnPol := menu.Data("⚙️ Limit & Model Policy", fmt.Sprintf("pol_wiz_ch_%s", ch.ID))
	btnDel := menu.Data("🗑️ Hapus Channel", fmt.Sprintf("chan_del_%s", ch.ID))
	btnBack := menu.Data("⬅️ Kembali", "chan_wiz_start")

	menu.Inline(
		menu.Row(btnQR, btnPairCode),
		menu.Row(btnDM, btnGrp),
		menu.Row(btnMen, btnLists),
		menu.Row(btnTools, btnPol),
		menu.Row(btnDel, btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// SendQRCodePhoto sends or refreshes the QR code image in Telegram
func (ui *WhatsAppUI) SendQRCodePhoto(c tele.Context, channelID string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	mgr := whatsapp.GetManager()
	if mgr == nil {
		return c.Reply("❌ WhatsApp Manager belum aktif.")
	}

	adapter, err := mgr.CreateOrGetAdapter(ch)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menginisialisasi adapter WA: %v", err))
	}

	if adapter.IsLoggedIn() && adapter.IsConnected() {
		return c.Reply(fmt.Sprintf("✅ WhatsApp <b>%s</b> sudah terhubung ke nomor <code>%s</code>!\nTidak perlu scan QR lagi.", html.EscapeString(ch.Name), html.EscapeString(adapter.GetSettings().JID)), tele.ModeHTML)
	}

	// Trigger pairing if not already running
	_ = adapter.StartPairing()

	qrBytes, _ := adapter.GetLastQR()
	if len(qrBytes) == 0 {
		return c.Reply("⏳ Sedang meminta QR Code ke server WhatsApp...\nSilakan klik tombol <b>🔄 Refresh QR</b> dalam beberapa detik atau gunakan opsi <b>🔢 Tautkan via Nomor HP</b>.", &tele.ReplyMarkup{
			InlineKeyboard: [][]tele.InlineButton{
				{
					{Text: "🔄 Refresh QR", Data: fmt.Sprintf("chan_wa_qr_%s", channelID)},
					{Text: "🔢 Tautkan via No HP", Data: fmt.Sprintf("chan_wa_paircode_prompt_%s", channelID)},
				},
				{
					{Text: "⬅️ Dashboard", Data: fmt.Sprintf("chan_ed_pick_%s", channelID)},
				},
			},
		}, tele.ModeHTML)
	}

	photo := &tele.Photo{
		File:    tele.FromReader(bytes.NewReader(qrBytes)),
		Caption: fmt.Sprintf("📲 <b>SCAN QR CODE WHATSAPP</b>\n\nChannel: <b>%s</b> (<code>%s</code>)\n\n1. Buka aplikasi WhatsApp di HP Anda.\n2. Buka <b>Pengaturan / Setelan</b> ➡️ <b>Perangkat Tertaut (Linked Devices)</b>.\n3. Ketuk <b>Tautkan Perangkat</b> lalu scan QR Code di atas.\n\n<i>Klik 'Refresh QR' jika QR code kadaluarsa atau gunakan tombol 'Tautkan via No HP'.</i>", html.EscapeString(ch.Name), html.EscapeString(ch.ID)),
	}

	menu := &tele.ReplyMarkup{}
	btnRefresh := menu.Data("🔄 Refresh QR", fmt.Sprintf("chan_wa_qr_%s", channelID))
	btnPairCode := menu.Data("🔢 Tautkan via No HP", fmt.Sprintf("chan_wa_paircode_prompt_%s", channelID))
	btnDash := menu.Data("⬅️ Kembali ke Dashboard", fmt.Sprintf("chan_ed_pick_%s", channelID))
	menu.Inline(
		menu.Row(btnRefresh, btnPairCode),
		menu.Row(btnDash),
	)

	return c.Send(photo, menu, tele.ModeHTML)
}

// PromptPairCode prompts user for their phone number to generate an 8-digit pairing code
func (ui *WhatsAppUI) PromptPairCode(c tele.Context, channelID string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	ui.channelUI.SetSessionStep(c.Sender().ID, channelID, ChannelStepPairPhoneCode)

	text := fmt.Sprintf("🔢 <b>TAUTKAN WHATSAPP DENGAN NOMOR TELEPON (%s)</b>\n\n"+
		"Kirimkan nomor WhatsApp yang ingin ditautkan (dengan kode negara):\n\n"+
		"<b>Contoh:</b>\n"+
		"<code>6281234567890</code>\n"+
		"<code>628987654321</code>\n\n"+
		"<i>Sistem akan meminta kode 8 digit resmi dari WhatsApp yang dapat langsung Anda masukkan di aplikasi WhatsApp di HP.</i>", html.EscapeString(ch.Name))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("chan_ed_pick_%s", channelID))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderDMPolicyMenu shows options to configure DM access
func (ui *WhatsAppUI) RenderDMPolicyMenu(c tele.Context, channelID string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	text := fmt.Sprintf("💬 <b>ATUR DIRECT MESSAGE (DM) POLICY: %s</b>\n\n"+
		"Tentukan siapa saja yang boleh mengirim pesan pribadi langsung ke nomor ini:\n\n"+
		"• <b>Allow All (🟢):</b> Respon siapapun yang mengirim DM.\n"+
		"• <b>Trusted Only (🟡):</b> Hanya balas nomor yang ada di Trusted List.\n"+
		"• <b>Block DM (🔴):</b> Abaikan/tolak semua chat personal (khusus grup).\n\n"+
		"Status Saat Ini: <b>%s</b>", html.EscapeString(ch.Name), strings.ToUpper(st.DMPolicy))

	menu := &tele.ReplyMarkup{}
	btnAllow := menu.Data("🟢 Allow All (Terbuka)", fmt.Sprintf("chan_wa_set_dm_%s_allow", channelID))
	btnTrusted := menu.Data("🟡 Trusted List Only", fmt.Sprintf("chan_wa_set_dm_%s_trusted", channelID))
	btnBlock := menu.Data("🔴 Block All DM", fmt.Sprintf("chan_wa_set_dm_%s_block", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnAllow),
		menu.Row(btnTrusted),
		menu.Row(btnBlock),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderGroupPolicyMenu shows options to configure group access
func (ui *WhatsAppUI) RenderGroupPolicyMenu(c tele.Context, channelID string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	text := fmt.Sprintf("👥 <b>ATUR GROUP ACCESS POLICY: %s</b>\n\n"+
		"Tentukan di grup WhatsApp mana bot boleh aktif:\n\n"+
		"• <b>Allow All Groups (🟢):</b> Aktif di semua grup tempat bot bergabung.\n"+
		"• <b>Whitelist Groups (🟡):</b> Hanya aktif di grup yang terdaftar.\n"+
		"• <b>Block All Groups (🔴):</b> Nonaktifkan fitur grup total.\n\n"+
		"Status Saat Ini: <b>%s</b>", html.EscapeString(ch.Name), strings.ToUpper(st.GroupPolicy))

	menu := &tele.ReplyMarkup{}
	btnAllow := menu.Data("🟢 Allow All Groups", fmt.Sprintf("chan_wa_set_grp_%s_allow_all", channelID))
	btnWhite := menu.Data("🟡 Whitelist Only", fmt.Sprintf("chan_wa_set_grp_%s_whitelist", channelID))
	btnBlock := menu.Data("🔴 Block All Groups", fmt.Sprintf("chan_wa_set_grp_%s_block", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnAllow),
		menu.Row(btnWhite),
		menu.Row(btnBlock),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderMentionPolicyMenu shows options to configure group trigger behavior
func (ui *WhatsAppUI) RenderMentionPolicyMenu(c tele.Context, channelID string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	text := fmt.Sprintf("🎯 <b>ATUR GROUP MENTION MODE: %s</b>\n\n"+
		"Tentukan bagaimana bot merespons obrolan di dalam grup:\n\n"+
		"• <b>Wajib Mention / Reply (🎯):</b> Bot hanya merespons jika di-@mention atau quote reply. Sangat disarankan agar grup tidak bising.\n"+
		"• <b>Semua Pesan Grup (📢):</b> Bot membalas setiap pesan teks di dalam grup.\n\n"+
		"Status Saat Ini: <b>%s</b>", html.EscapeString(ch.Name), strings.ToUpper(st.MentionPolicy))

	menu := &tele.ReplyMarkup{}
	btnReq := menu.Data("🎯 Wajib Mention / Reply (Disarankan)", fmt.Sprintf("chan_wa_set_men_%s_require_mention", channelID))
	btnAll := menu.Data("📢 Balas Semua Pesan Grup", fmt.Sprintf("chan_wa_set_men_%s_all", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnReq),
		menu.Row(btnAll),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderWhitelistManagerMenu shows trusted numbers and allowed groups
func (ui *WhatsAppUI) RenderWhitelistManagerMenu(c tele.Context, channelID string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 <b>KELOLA WHITELIST & TRUSTED LIST (%s)</b>\n\n", html.EscapeString(ch.Name)))

	sb.WriteString("👤 <b>Trusted Phone Numbers (DM):</b>\n")
	if len(st.TrustedNumbers) == 0 {
		sb.WriteString("<i>(Kosong - semua nomor akan diabaikan jika DM Policy = Trusted)</i>\n\n")
	} else {
		for i, n := range st.TrustedNumbers {
			sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, html.EscapeString(n)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("👥 <b>Allowed Group JIDs (Grup):</b>\n")
	if len(st.AllowedGroups) == 0 {
		sb.WriteString("<i>(Kosong - semua grup akan diabaikan jika Group Policy = Whitelist)</i>\n\n")
	} else {
		for i, g := range st.AllowedGroups {
			sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, html.EscapeString(g)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("💡 <i>Kirim perintah atau tombol untuk menambah/menghapus list.</i>")

	menu := &tele.ReplyMarkup{}
	btnAddTrust := menu.Data("➕ Tambah Nomor Trusted", fmt.Sprintf("chan_wa_input_trust_%s", channelID))
	btnAddGrp := menu.Data("➕ Tambah ID Grup", fmt.Sprintf("chan_wa_input_grp_%s", channelID))
	btnClearTrust := menu.Data("🗑️ Kosongkan Trusted", fmt.Sprintf("chan_wa_clr_trust_%s", channelID))
	btnClearGrp := menu.Data("🗑️ Kosongkan Grup", fmt.Sprintf("chan_wa_clr_grp_%s", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnAddTrust, btnAddGrp),
		menu.Row(btnClearTrust, btnClearGrp),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleSetDMPolicy updates DM policy
func (ui *WhatsAppUI) HandleSetDMPolicy(c tele.Context, channelID, policy string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	st.DMPolicy = policy
	stBytes, _ := json.Marshal(st)
	ch.SettingsJSON = string(stBytes)
	_ = ui.db.SaveChannel(ch)

	if mgr := whatsapp.GetManager(); mgr != nil {
		if ad := mgr.GetAdapter(channelID); ad != nil {
			_ = ad.UpdateSettings(st)
		}
	}

	return ui.RenderDMPolicyMenu(c, channelID)
}

// HandleSetGroupPolicy updates Group policy
func (ui *WhatsAppUI) HandleSetGroupPolicy(c tele.Context, channelID, policy string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	st.GroupPolicy = policy
	stBytes, _ := json.Marshal(st)
	ch.SettingsJSON = string(stBytes)
	_ = ui.db.SaveChannel(ch)

	if mgr := whatsapp.GetManager(); mgr != nil {
		if ad := mgr.GetAdapter(channelID); ad != nil {
			_ = ad.UpdateSettings(st)
		}
	}

	return ui.RenderGroupPolicyMenu(c, channelID)
}

// HandleSetMentionPolicy updates Mention policy
func (ui *WhatsAppUI) HandleSetMentionPolicy(c tele.Context, channelID, policy string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	st.MentionPolicy = policy
	stBytes, _ := json.Marshal(st)
	ch.SettingsJSON = string(stBytes)
	_ = ui.db.SaveChannel(ch)

	if mgr := whatsapp.GetManager(); mgr != nil {
		if ad := mgr.GetAdapter(channelID); ad != nil {
			_ = ad.UpdateSettings(st)
		}
	}

	return ui.RenderMentionPolicyMenu(c, channelID)
}

// HandleClearLists empties trusted or group list
func (ui *WhatsAppUI) HandleClearLists(c tele.Context, channelID, listType string) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	if listType == "trust" {
		st.TrustedNumbers = []string{}
	} else if listType == "grp" {
		st.AllowedGroups = []string{}
	}

	stBytes, _ := json.Marshal(st)
	ch.SettingsJSON = string(stBytes)
	_ = ui.db.SaveChannel(ch)

	if mgr := whatsapp.GetManager(); mgr != nil {
		if ad := mgr.GetAdapter(channelID); ad != nil {
			_ = ad.UpdateSettings(st)
		}
	}

	return ui.RenderWhitelistManagerMenu(c, channelID)
}
