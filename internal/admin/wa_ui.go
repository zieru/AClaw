package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/channel/whatsapp"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type WhatsAppUI struct {
	db           *storage.DB
	channelUI    *ChannelUI
	mu           sync.RWMutex
	cachedGroups map[string][]*whatsapp.JoinedGroupInfo
}

func NewWhatsAppUI(db *storage.DB, cui *ChannelUI) *WhatsAppUI {
	return &WhatsAppUI{
		db:           db,
		channelUI:    cui,
		cachedGroups: make(map[string][]*whatsapp.JoinedGroupInfo),
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
	btnAllow := menu.Data("🟢 Allow All (Terbuka)", fmt.Sprintf("chan_wa_set_dm_%s__allow", channelID))
	btnTrusted := menu.Data("🟡 Trusted List Only", fmt.Sprintf("chan_wa_set_dm_%s__trusted", channelID))
	btnBlock := menu.Data("🔴 Block All DM", fmt.Sprintf("chan_wa_set_dm_%s__block", channelID))
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
	btnAllow := menu.Data("🟢 Allow All Groups", fmt.Sprintf("chan_wa_set_grp_%s__allow_all", channelID))
	btnWhite := menu.Data("🟡 Whitelist Only", fmt.Sprintf("chan_wa_set_grp_%s__whitelist", channelID))
	btnBlock := menu.Data("🔴 Block All Groups", fmt.Sprintf("chan_wa_set_grp_%s__block", channelID))
	btnWiz := menu.Data("🧙‍♂️ Wizard Pilih Grup (Auto-Detect)", fmt.Sprintf("chan_wa_gwiz_%s_0", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnAllow),
		menu.Row(btnWhite),
		menu.Row(btnBlock),
		menu.Row(btnWiz),
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
	btnReq := menu.Data("🎯 Wajib Mention / Reply (Disarankan)", fmt.Sprintf("chan_wa_set_men_%s__require_mention", channelID))
	btnAll := menu.Data("📢 Balas Semua Pesan Grup", fmt.Sprintf("chan_wa_set_men_%s__all", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnReq),
		menu.Row(btnAll),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderGroupWhitelistWizard renders visual group selector with auto-discovery and pagination
func (ui *WhatsAppUI) RenderGroupWhitelistWizard(c tele.Context, channelID string, page int) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	mgr := whatsapp.GetManager()
	var adapter *whatsapp.NativeAdapter
	if mgr != nil {
		adapter = mgr.GetAdapter(channelID)
	}

	if adapter == nil || !adapter.IsConnected() {
		menu := &tele.ReplyMarkup{}
		btnManual := menu.Data("➕ Input Manual ID Grup", fmt.Sprintf("chan_wa_input_grp_%s", channelID))
		btnBack := menu.Data("⬅️ Kembali ke Whitelist", fmt.Sprintf("chan_wa_list_menu_%s", channelID))
		menu.Inline(menu.Row(btnManual), menu.Row(btnBack))

		return c.EditOrSend("⚠️ <b>WhatsApp Tidak Terhubung</b>\n\nUntuk mendeteksi grup secara otomatis, akun WhatsApp bot harus berstatus online/terhubung.\n\nAnda tetap dapat menambahkan ID grup secara manual:", menu, tele.ModeHTML)
	}

	// Fetch joined groups from WhatsApp
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	groups, err := adapter.GetJoinedGroups(ctx)
	if err != nil {
		menu := &tele.ReplyMarkup{}
		btnRetry := menu.Data("🔄 Coba Lagi", fmt.Sprintf("chan_wa_gwiz_%s_%d", channelID, page))
		btnBack := menu.Data("⬅️ Kembali ke Whitelist", fmt.Sprintf("chan_wa_list_menu_%s", channelID))
		menu.Inline(menu.Row(btnRetry), menu.Row(btnBack))

		return c.EditOrSend(fmt.Sprintf("❌ <b>Gagal Mengambil Daftar Grup:</b>\n<code>%s</code>", html.EscapeString(err.Error())), menu, tele.ModeHTML)
	}

	ui.mu.Lock()
	ui.cachedGroups[channelID] = groups
	ui.mu.Unlock()

	totalGroups := len(groups)
	if totalGroups == 0 {
		menu := &tele.ReplyMarkup{}
		btnManual := menu.Data("➕ Input Manual ID Grup", fmt.Sprintf("chan_wa_input_grp_%s", channelID))
		btnBack := menu.Data("⬅️ Kembali ke Whitelist", fmt.Sprintf("chan_wa_list_menu_%s", channelID))
		menu.Inline(menu.Row(btnManual), menu.Row(btnBack))

		return c.EditOrSend(fmt.Sprintf("👥 <b>WIZARD GRUP WHATSAPP (%s)</b>\n\nAkun WhatsApp ini belum bergabung di grup manapun.\nSilakan masukkan bot ke dalam grup WhatsApp terlebih dahulu.", html.EscapeString(ch.Name)), menu, tele.ModeHTML)
	}

	const groupsPerPage = 6
	totalPages := (totalGroups + groupsPerPage - 1) / groupsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	startIdx := page * groupsPerPage
	endIdx := startIdx + groupsPerPage
	if endIdx > totalGroups {
		endIdx = totalGroups
	}

	pageGroups := groups[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧙‍♂️ <b>WIZARD WHITELIST GRUP (%s)</b>\n\n", html.EscapeString(ch.Name)))
	sb.WriteString(fmt.Sprintf("Halaman <code>%d/%d</code> (Total: <code>%d grup diikuti</code>)\n\n", page+1, totalPages, totalGroups))
	sb.WriteString("Klik tombol di bawah untuk <b>mengaktifkan / menonaktifkan (Toggle)</b> whitelist grup:\n\n")

	menu := &tele.ReplyMarkup{}
	var itemRows []tele.Row

	for i, g := range pageGroups {
		globalIdx := startIdx + i
		statusIcon := "⬜"
		statusText := "Nonaktif"
		if g.IsWhitelisted {
			statusIcon = "✅"
			statusText = "Whitelisted"
		}

		sb.WriteString(fmt.Sprintf("%d. %s <b>%s</b>\n   • JID: <code>%s</code>\n   • Status: <i>%s</i>\n\n", globalIdx+1, statusIcon, html.EscapeString(g.Name), html.EscapeString(g.JID), statusText))

		btnLabel := fmt.Sprintf("%s %s", statusIcon, g.Name)
		if len([]rune(btnLabel)) > 26 {
			btnLabel = string([]rune(btnLabel)[:23]) + "..."
		}
		btn := menu.Data(btnLabel, fmt.Sprintf("chan_wa_gtgl_%s_%d_%d", channelID, page, globalIdx))
		itemRows = append(itemRows, menu.Row(btn))
	}

	for _, r := range itemRows {
		menu.Inline(r)
	}

	// Pagination Navigation
	var navRow []tele.Btn
	if page > 0 {
		navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("chan_wa_gwiz_%s_%d", channelID, page-1)))
	}
	navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "mod_noop"))
	if page < totalPages-1 {
		navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("chan_wa_gwiz_%s_%d", channelID, page+1)))
	}
	if len(navRow) > 0 {
		menu.Inline(menu.Row(navRow...))
	}

	btnManual := menu.Data("➕ Input Manual", fmt.Sprintf("chan_wa_input_grp_%s", channelID))
	btnRefresh := menu.Data("🔄 Refresh List", fmt.Sprintf("chan_wa_gwiz_%s_%d", channelID, page))
	btnClear := menu.Data("🗑️ Kosongkan Whitelist", fmt.Sprintf("chan_wa_clr_grp_%s", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu Whitelist", fmt.Sprintf("chan_wa_list_menu_%s", channelID))

	menu.Inline(
		menu.Row(btnManual, btnRefresh),
		menu.Row(btnClear, btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleToggleGroupWhitelist toggles a group's whitelist status from the wizard
func (ui *WhatsAppUI) HandleToggleGroupWhitelist(c tele.Context, channelID string, page, globalIdx int) error {
	ch, err := ui.db.GetChannel(channelID)
	if err != nil || ch == nil {
		return c.Reply("❌ Channel tidak ditemukan.")
	}

	ui.mu.RLock()
	groups := ui.cachedGroups[channelID]
	ui.mu.RUnlock()

	if globalIdx < 0 || globalIdx >= len(groups) {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Grup tidak ditemukan dalam cache"})
		return ui.RenderGroupWhitelistWizard(c, channelID, page)
	}

	targetGroup := groups[globalIdx]
	targetJID := targetGroup.JID

	var st whatsapp.WhatsAppSettings
	if ch.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &st)
	} else {
		st = whatsapp.DefaultWhatsAppSettings()
	}

	// Toggle in allowed groups
	found := false
	var newAllowed []string
	for _, g := range st.AllowedGroups {
		if strings.EqualFold(strings.TrimSpace(g), strings.TrimSpace(targetJID)) {
			found = true
		} else {
			newAllowed = append(newAllowed, g)
		}
	}

	statusMsg := ""
	if found {
		st.AllowedGroups = newAllowed
		targetGroup.IsWhitelisted = false
		statusMsg = fmt.Sprintf("⬜ Whitelist dinonaktifkan: %s", targetGroup.Name)
	} else {
		st.AllowedGroups = append(st.AllowedGroups, targetJID)
		targetGroup.IsWhitelisted = true
		statusMsg = fmt.Sprintf("✅ Whitelist diaktifkan: %s", targetGroup.Name)
	}

	stBytes, _ := json.Marshal(st)
	ch.SettingsJSON = string(stBytes)
	_ = ui.db.SaveChannel(ch)

	if mgr := whatsapp.GetManager(); mgr != nil {
		if ad := mgr.GetAdapter(channelID); ad != nil {
			_ = ad.UpdateSettings(st)
		}
	}

	_ = c.Respond(&tele.CallbackResponse{Text: statusMsg})
	return ui.RenderGroupWhitelistWizard(c, channelID, page)
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

	sb.WriteString("💡 <i>Gunakan tombol Wizard untuk memilih grup langsung dari WhatsApp.</i>")

	menu := &tele.ReplyMarkup{}
	btnWizGrp := menu.Data("🧙‍♂️ Wizard Pilih Grup (Auto-Detect)", fmt.Sprintf("chan_wa_gwiz_%s_0", channelID))
	btnAddTrust := menu.Data("➕ Tambah Nomor Trusted", fmt.Sprintf("chan_wa_input_trust_%s", channelID))
	btnAddGrp := menu.Data("➕ Tambah Manual ID Grup", fmt.Sprintf("chan_wa_input_grp_%s", channelID))
	btnClearTrust := menu.Data("🗑️ Kosongkan Trusted", fmt.Sprintf("chan_wa_clr_trust_%s", channelID))
	btnClearGrp := menu.Data("🗑️ Kosongkan Grup", fmt.Sprintf("chan_wa_clr_grp_%s", channelID))
	btnBack := menu.Data("⬅️ Kembali ke Menu WA", fmt.Sprintf("chan_ed_pick_%s", channelID))

	menu.Inline(
		menu.Row(btnWizGrp),
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
