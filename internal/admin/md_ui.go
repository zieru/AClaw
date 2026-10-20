package admin

import (
	"fmt"
	"html"
	"io"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type MDStep int

const (
	MDStepNone MDStep = iota
	MDStepEditContent
	MDStepAppendContent
	MDStepNewFileName
	MDStepCustomChannelID
)

type MDSession struct {
	Step      MDStep
	ChannelID string
	Filename  string
	UpdatedAt time.Time
}

type MDUI struct {
	mdLoader *agent.MDLoader
	db       *storage.DB
	bot      *tele.Bot
	mu       sync.RWMutex
	sessions map[int64]*MDSession
}

func NewMDUI(loader *agent.MDLoader, db *storage.DB, bot *tele.Bot) *MDUI {
	return &MDUI{
		mdLoader: loader,
		db:       db,
		bot:      bot,
		sessions: make(map[int64]*MDSession),
	}
}

// HasActiveSession checks if user is currently interacting with MD wizard
func (ui *MDUI) HasActiveSession(userID int64) bool {
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	sess, ok := ui.sessions[userID]
	return ok && sess != nil && sess.Step != MDStepNone
}

// RenderMDList returns summary of all markdown bot files in HTML format
func (ui *MDUI) RenderMDList() string {
	files, err := ui.mdLoader.ListFiles()
	if err != nil {
		return fmt.Sprintf("❌ Error membaca daftar file MD: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("📝 <b>MANAJEMEN FILE MARKDOWN (.MD) PERSONA & PROMPT</b>\n\n")

	sb.WriteString("🌐 <b>File Global / Default (Baseline):</b>\n")
	if len(files) == 0 {
		sb.WriteString("<i>(Belum ada file .md global ditemukan di direktori data/md)</i>\n")
	} else {
		for i, f := range files {
			content, _ := ui.mdLoader.GetFile(f)
			sb.WriteString(fmt.Sprintf("%d. 📄 <b>%s</b> (%d karakter)\n", i+1, html.EscapeString(f), len(content)))
		}
	}
	sb.WriteString("\n")

	// Channels overview
	if ui.db != nil {
		channels, _ := ui.db.ListChannels()
		customChannelsCount := 0
		for _, ch := range channels {
			chFiles, _ := ui.mdLoader.ListFilesForChannel(ch.ID)
			if len(chFiles) > 0 {
				customChannelsCount++
			}
		}
		sb.WriteString(fmt.Sprintf("📢 <b>Channel Khusus:</b> %d channel terdaftar (%d memiliki MD kustom)\n\n", len(channels), customChannelsCount))
	}

	sb.WriteString("💡 <b>Fitur Multi-Channel MD:</b>\n")
	sb.WriteString("• Setiap channel chat (Telegram/WhatsApp/Grup) dapat memiliki persona dan SOP terpisah.\n")
	sb.WriteString("• Jika channel belum dikustomisasi, otomatis mewarisi file <b>Global / Default</b>.\n")
	sb.WriteString("• <b>Wizard:</b> Klik tombol di bawah untuk mengelola per-channel atau global.\n")

	return sb.String()
}

// MDMenuKeyboard returns keyboard for markdown management
func (ui *MDUI) MDMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnWizard := menu.Data("🧙‍♂️ Kelola Persona & Prompt (Wizard)", "md_wiz_start")
	btnReload := menu.Data("🔄 Reload Semua Cache", "md_reload_all")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnWizard),
		menu.Row(btnReload, btnBack),
	)
	return menu
}

// StartMDWizard starts interactive scope selector (Global vs Channels)
func (ui *MDUI) StartMDWizard(c tele.Context) error {
	text := "📝 <b>WIZARD PENGELOLAAN FILE MARKDOWN (.MD)</b>\n\n" +
		"Pilih <b>Lingkup (Scope)</b> yang ingin Anda kelola:\n\n" +
		"• 🌐 <b>Global / Default</b>: Berlaku untuk semua channel secara umum (baseline).\n" +
		"• 📢 <b>Channel Tertentu</b>: Override persona/instruksi khusus channel terpilih."

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	// Global Button
	btnGlobal := menu.Data("🌐 Global / Default MD (Baseline)", "md_scope:global")
	rows = append(rows, menu.Row(btnGlobal))

	// List channels from DB
	if ui.db != nil {
		channels, _ := ui.db.ListChannels()
		for _, ch := range channels {
			chFiles, _ := ui.mdLoader.ListFilesForChannel(ch.ID)
			statusBadge := "⚪ Default"
			if len(chFiles) > 0 {
				statusBadge = fmt.Sprintf("🟢 %d Custom", len(chFiles))
			}
			label := fmt.Sprintf("📢 [%s] %s (%s)", strings.ToUpper(ch.Type), ch.Name, statusBadge)
			btnChan := menu.Data(label, fmt.Sprintf("md_scope:%s", ch.ID))
			rows = append(rows, menu.Row(btnChan))
		}
	}

	btnCustom := menu.Data("➕ Input Channel ID Lain", "md_scope_custom")
	btnBack := menu.Data("⬅️ Kembali ke Menu MD", "menu_md")
	rows = append(rows, menu.Row(btnCustom), menu.Row(btnBack))

	menu.Inline(rows...)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// PromptCustomChannelID asks for manual channel ID
func (ui *MDUI) PromptCustomChannelID(c tele.Context) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &MDSession{
		Step:      MDStepCustomChannelID,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := "➕ <b>INPUT CHANNEL ID MANUAL</b>\n\n" +
		"Ketikkan identifier atau ID channel yang ingin Anda kelola file MD-nya (misal: <code>tg_group_123</code> atau <code>whatsapp_support</code>):"

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "md_wiz_start")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderChannelDashboard displays MD status and files for a specific channel or global
func (ui *MDUI) RenderChannelDashboard(c tele.Context, channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		channelID = "global"
	}

	var sb strings.Builder
	if channelID == "global" {
		sb.WriteString("🌐 <b>PENGELOLAAN MD: GLOBAL / DEFAULT (BASELINE)</b>\n\n")
		sb.WriteString("File di bawah ini adalah acuan standar untuk seluruh bot & channel yang belum memiliki MD khusus.\n\n")
	} else {
		chName := channelID
		if ui.db != nil {
			if ch, _ := ui.db.GetChannel(channelID); ch != nil {
				chName = fmt.Sprintf("%s (%s)", ch.Name, ch.Type)
			}
		}
		sb.WriteString(fmt.Sprintf("📢 <b>PENGELOLAAN MD: CHANNEL <code>%s</code></b>\n\n", html.EscapeString(chName)))
		sb.WriteString("File bertanda 🟢 <b>[KUSTOM]</b> akan menimpa (override) file global saat bot beroperasi di channel ini.\n\n")
	}

	statuses, err := ui.mdLoader.GetChannelMDStatus(channelID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membaca status MD: %v", err), tele.ModeHTML)
	}

	sb.WriteString("<b>Daftar File Markdown:</b>\n")
	for i, s := range statuses {
		if channelID == "global" {
			sb.WriteString(fmt.Sprintf("%d. 📄 <b>%s</b> (%d char)\n", i+1, html.EscapeString(s.Filename), s.CharCount))
		} else {
			if s.IsCustom {
				sb.WriteString(fmt.Sprintf("%d. 🟢 <b>%s</b> — <i>Kustom Khusus</i> (%d char)\n", i+1, html.EscapeString(s.Filename), s.CharCount))
			} else {
				sb.WriteString(fmt.Sprintf("%d. ⚪ <b>%s</b> — <i>Mewarisi Global</i> (%d char)\n", i+1, html.EscapeString(s.Filename), s.CharCount))
			}
		}
	}
	sb.WriteString("\nPilih file di bawah untuk melihat isi, mengedit, atau me-reset:")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	// File buttons in 2 columns
	var currentRow []tele.Btn
	for _, s := range statuses {
		badge := "📄"
		if channelID != "global" {
			if s.IsCustom {
				badge = "🟢"
			} else {
				badge = "⚪"
			}
		}
		btnLabel := fmt.Sprintf("%s %s", badge, s.Filename)
		btnCallback := fmt.Sprintf("md_f:%s:%s", channelID, s.Filename)
		btn := menu.Data(btnLabel, btnCallback)

		currentRow = append(currentRow, btn)
		if len(currentRow) == 2 {
			rows = append(rows, menu.Row(currentRow...))
			currentRow = []tele.Btn{}
		}
	}
	if len(currentRow) > 0 {
		rows = append(rows, menu.Row(currentRow...))
	}

	btnNewFile := menu.Data("➕ Buat File .MD Baru", fmt.Sprintf("md_newfile:%s", channelID))
	btnBack := menu.Data("⬅️ Ganti Scope/Channel", "md_wiz_start")
	btnMainMenu := menu.Data("🏠 Menu Utama", "menu_main")

	rows = append(rows, menu.Row(btnNewFile), menu.Row(btnBack, btnMainMenu))
	menu.Inline(rows...)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// RenderMDFileDashboard displays specific file view and management options
func (ui *MDUI) RenderMDFileDashboard(c tele.Context, channelID, filename string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		channelID = "global"
	}

	content, err := ui.mdLoader.GetFileForChannel(channelID, filename)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membaca file %s: %v", html.EscapeString(filename), err))
	}

	statuses, _ := ui.mdLoader.GetChannelMDStatus(channelID)
	isCustom := false
	for _, s := range statuses {
		if s.Filename == filename && s.IsCustom {
			isCustom = true
			break
		}
	}

	preview := content
	if len(preview) > 500 {
		preview = preview[:500] + "\n...[dipotong untuk preview]"
	}

	var sb strings.Builder
	scopeLabel := "🌐 Global / Default"
	if channelID != "global" {
		scopeLabel = fmt.Sprintf("📢 Channel: %s", channelID)
	}

	sb.WriteString(fmt.Sprintf("📄 <b>FILE: <code>%s</code></b>\n", html.EscapeString(filename)))
	sb.WriteString(fmt.Sprintf("📍 <b>Lingkup:</b> %s\n", html.EscapeString(scopeLabel)))

	if channelID != "global" {
		if isCustom {
			sb.WriteString("🏷️ <b>Status:</b> 🟢 <b>KUSTOM KHUSUS</b> (Menimpa file global)\n")
		} else {
			sb.WriteString("🏷️ <b>Status:</b> ⚪ <b>MEWARISI GLOBAL</b> (Belum ada override khusus)\n")
		}
	}
	sb.WriteString(fmt.Sprintf("📏 <b>Panjang:</b> %d karakter\n\n", len(content)))

	sb.WriteString("<b>Cuplikan Konten:</b>\n")
	if len(strings.TrimSpace(content)) == 0 {
		sb.WriteString("<i>(File masih kosong)</i>\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n\n", html.EscapeString(preview)))
	}
	sb.WriteString("Pilih aksi:")

	menu := &tele.ReplyMarkup{}
	btnViewFull := menu.Data("📖 Baca Seluruh Isi", fmt.Sprintf("md_v:%s:%s", channelID, filename))
	btnEdit := menu.Data("✏️ Ganti Konten Baru", fmt.Sprintf("md_e:%s:%s", channelID, filename))
	btnAppend := menu.Data("➕ Tambah Catatan", fmt.Sprintf("md_a:%s:%s", channelID, filename))

	var rows []tele.Row
	rows = append(rows, menu.Row(btnViewFull))
	rows = append(rows, menu.Row(btnEdit, btnAppend))

	if channelID != "global" && isCustom {
		btnReset := menu.Data("🗑️ Reset ke Global Default", fmt.Sprintf("md_r:%s:%s", channelID, filename))
		rows = append(rows, menu.Row(btnReset))
	}

	btnBack := menu.Data("⬅️ Kembali ke Dashboard MD", fmt.Sprintf("md_scope:%s", channelID))
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// PromptEditContent sets session step to edit
func (ui *MDUI) PromptEditContent(c tele.Context, channelID, filename string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &MDSession{
		Step:      MDStepEditContent,
		ChannelID: channelID,
		Filename:  filename,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	scopeName := "Global"
	if channelID != "global" {
		scopeName = "Channel " + channelID
	}

	text := fmt.Sprintf("✏️ <b>EDIT FILE: <code>%s</code> (%s)</b>\n\n"+
		"Silakan kirimkan <b>seluruh teks baru</b> untuk menggantikan isi file ini.\n\n"+
		"<i>💡 Tips: Anda juga bisa mengunggah file dokumen .md langsung ke chat ini.</i>",
		html.EscapeString(filename), html.EscapeString(scopeName))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("md_f:%s:%s", channelID, filename))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// PromptAppendContent sets session step to append
func (ui *MDUI) PromptAppendContent(c tele.Context, channelID, filename string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &MDSession{
		Step:      MDStepAppendContent,
		ChannelID: channelID,
		Filename:  filename,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	scopeName := "Global"
	if channelID != "global" {
		scopeName = "Channel " + channelID
	}

	text := fmt.Sprintf("➕ <b>TAMBAH CATATAN KE: <code>%s</code> (%s)</b>\n\n"+
		"Silakan kirimkan teks tambahan yang ingin disisipkan ke baris paling bawah file ini:",
		html.EscapeString(filename), html.EscapeString(scopeName))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("md_f:%s:%s", channelID, filename))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// PromptNewFileName sets session step to create new file
func (ui *MDUI) PromptNewFileName(c tele.Context, channelID string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &MDSession{
		Step:      MDStepNewFileName,
		ChannelID: channelID,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := fmt.Sprintf("➕ <b>BUAT FILE .MD BARU (%s)</b>\n\n"+
		"Ketikkan nama file yang ingin dibuat (contoh: <code>RULES.md</code>, <code>FAQ.md</code>, atau <code>IDENTITY.md</code>):",
		html.EscapeString(channelID))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("md_scope:%s", channelID))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleResetChannelFile removes channel override
func (ui *MDUI) HandleResetChannelFile(c tele.Context, channelID, filename string) error {
	if err := ui.mdLoader.DeleteFileForChannel(channelID, filename); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mereset file: %v", err), tele.ModeHTML)
	}

	_ = c.Respond(&tele.CallbackResponse{Text: "✅ File berhasil di-reset ke global default!"})
	return ui.RenderMDFileDashboard(c, channelID, filename)
}

// HandleTextMessage handles input during MD editing
func (ui *MDUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID

	ui.mu.RLock()
	sess, exists := ui.sessions[userID]
	ui.mu.RUnlock()

	if !exists || sess.Step == MDStepNone {
		return false, nil
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") {
		chID := sess.ChannelID
		fname := sess.Filename
		ui.CancelWizard(userID)
		if fname != "" {
			return true, ui.RenderMDFileDashboard(c, chID, fname)
		}
		if chID != "" {
			return true, ui.RenderChannelDashboard(c, chID)
		}
		return true, c.Reply("❌ Operasi file .md dibatalkan.")
	}

	switch sess.Step {
	case MDStepCustomChannelID:
		chID := msgText
		ui.CancelWizard(userID)
		return true, ui.RenderChannelDashboard(c, chID)

	case MDStepNewFileName:
		fname := msgText
		if !strings.HasSuffix(strings.ToLower(fname), ".md") {
			fname += ".md"
		}
		chID := sess.ChannelID
		ui.mu.Lock()
		ui.sessions[userID] = &MDSession{
			Step:      MDStepEditContent,
			ChannelID: chID,
			Filename:  fname,
			UpdatedAt: time.Now(),
		}
		ui.mu.Unlock()
		return true, c.Reply(fmt.Sprintf("📝 File <code>%s</code> dipilih untuk scope <b>%s</b>.\n\nSilakan kirimkan konten teks untuk file ini:", html.EscapeString(fname), html.EscapeString(chID)), tele.ModeHTML)

	case MDStepEditContent:
		channelID := sess.ChannelID
		filename := sess.Filename
		if err := ui.mdLoader.SaveFileForChannel(channelID, filename, msgText); err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan file: %v", html.EscapeString(err.Error())))
		}
		ui.CancelWizard(userID)
		_ = c.Reply(fmt.Sprintf("✅ File <code>%s</code> berhasil diperbarui dan dimuat ulang!", html.EscapeString(filename)), tele.ModeHTML)
		return true, ui.RenderMDFileDashboard(c, channelID, filename)

	case MDStepAppendContent:
		channelID := sess.ChannelID
		filename := sess.Filename
		existing, _ := ui.mdLoader.GetFileForChannel(channelID, filename)
		newContent := strings.TrimSpace(existing) + "\n\n" + msgText
		if err := ui.mdLoader.SaveFileForChannel(channelID, filename, newContent); err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan file: %v", html.EscapeString(err.Error())))
		}
		ui.CancelWizard(userID)
		_ = c.Reply(fmt.Sprintf("✅ Berhasil menambahkan catatan baru ke <code>%s</code>!", html.EscapeString(filename)), tele.ModeHTML)
		return true, ui.RenderMDFileDashboard(c, channelID, filename)
	}

	return false, nil
}

// CancelWizard clears user session
func (ui *MDUI) CancelWizard(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.sessions, userID)
}

// RenderFullFile displays complete content of a markdown file with back button
func (ui *MDUI) RenderFullFile(c tele.Context, channelID, filename string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		channelID = "global"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename += ".md"
	}

	content, err := ui.mdLoader.GetFileForChannel(channelID, filename)
	if err != nil || content == "" {
		return c.Reply(fmt.Sprintf("❌ File <code>%s</code> tidak ditemukan atau kosong.", html.EscapeString(filename)), tele.ModeHTML)
	}

	displayContent := content
	if len(displayContent) > 3500 {
		displayContent = displayContent[:3500] + "\n...[dipotong karena batas pesan telegram]"
	}

	text := fmt.Sprintf("📄 <b>ISI LENGKAP: <code>%s</code> (%s)</b>\n\n<pre><code>%s</code></pre>", html.EscapeString(filename), html.EscapeString(channelID), html.EscapeString(displayContent))
	menu := &tele.ReplyMarkup{}
	btnBack := menu.Data("⬅️ Kembali ke Dashboard File", fmt.Sprintf("md_f:%s:%s", channelID, filename))
	menu.Inline(menu.Row(btnBack))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleViewMD processes `/viewmd`
func (ui *MDUI) HandleViewMD(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return ui.StartMDWizard(c)
	}

	filename := args[0]
	return ui.RenderFullFile(c, "global", filename)
}

// HandleEditMD processes `/editmd`
func (ui *MDUI) HandleEditMD(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return ui.StartMDWizard(c)
	}
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/editmd SOUL.md Selalu ramah dan panggil user dengan sebutan Bos.</code>", tele.ModeHTML)
	}

	filename := args[0]
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename += ".md"
	}

	content := strings.Join(args[1:], " ")
	if err := ui.mdLoader.SaveFile(filename, content); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan file: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("✅ File global <code>%s</code> berhasil diperbarui dan dimuat ulang (<i>Hot-Reloaded</i>)!", html.EscapeString(filename)), tele.ModeHTML)
}

// HandleDocumentUpload handles incoming document uploads (.md files)
func (ui *MDUI) HandleDocumentUpload(c tele.Context) error {
	doc := c.Message().Document
	if doc == nil {
		return nil
	}

	if !strings.HasSuffix(strings.ToLower(doc.FileName), ".md") {
		return nil
	}

	reader, err := ui.bot.File(&doc.File)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mendownload file dari Telegram: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}
	defer reader.Close()

	contentBytes, err := io.ReadAll(reader)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membaca isi file: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	targetChannel := "global"
	if c.Sender() != nil {
		ui.mu.RLock()
		sess, ok := ui.sessions[c.Sender().ID]
		if ok && sess.ChannelID != "" {
			targetChannel = sess.ChannelID
		}
		ui.mu.RUnlock()
	}

	filename := doc.FileName
	if err := ui.mdLoader.SaveFileForChannel(targetChannel, filename, string(contentBytes)); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan file %s: %v", html.EscapeString(filename), html.EscapeString(err.Error())), tele.ModeHTML)
	}

	scopeMsg := "Global / Default"
	if targetChannel != "global" {
		scopeMsg = fmt.Sprintf("Channel <code>%s</code>", html.EscapeString(targetChannel))
	}

	return c.Reply(fmt.Sprintf("🎉 File <code>%s</code> berhasil diunggah ke <b>%s</b> dan langsung aktif (<i>Hot-Reloaded</i>)!\nUkuran: %d bytes.", html.EscapeString(filename), scopeMsg, len(contentBytes)), tele.ModeHTML)
}
