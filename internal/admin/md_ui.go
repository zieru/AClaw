package admin

import (
	"fmt"
	"html"
	"io"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	tele "gopkg.in/telebot.v3"
)

type MDStep int

const (
	MDStepNone MDStep = iota
	MDStepEditContent
	MDStepAppendContent
)

type MDSession struct {
	Step      MDStep
	Filename  string
	UpdatedAt time.Time
}

type MDUI struct {
	mdLoader *agent.MDLoader
	bot      *tele.Bot
	mu       sync.RWMutex
	sessions map[int64]*MDSession
}

func NewMDUI(loader *agent.MDLoader, bot *tele.Bot) *MDUI {
	return &MDUI{
		mdLoader: loader,
		bot:      bot,
		sessions: make(map[int64]*MDSession),
	}
}

// RenderMDList returns summary of all markdown bot files in HTML format
func (ui *MDUI) RenderMDList() string {
	files, err := ui.mdLoader.ListFiles()
	if err != nil {
		return fmt.Sprintf("❌ Error membaca daftar file MD: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("📝 <b>MANAJEMEN FILE MARKDOWN (.MD) BOT</b>\n\n")

	if len(files) == 0 {
		sb.WriteString("(Belum ada file .md ditemukan di direktori data/md)\n\n")
	} else {
		for i, f := range files {
			content, _ := ui.mdLoader.GetFile(f)
			sb.WriteString(fmt.Sprintf("%d. 📄 <b>%s</b> (%d karakter)\n", i+1, html.EscapeString(f), len(content)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("💡 <b>Cara Mengelola File MD:</b>\n")
	sb.WriteString("• <b>Upload Langsung</b>: Kirimkan file dokumen <code>.md</code> (misal: <code>IDENTITY.md</code>) ke chat bot ini.\n")
	sb.WriteString("• <b>Wizard Editor</b>: Klik tombol <b>🧙‍♂️ Kelola File .MD (Wizard)</b>.\n")
	sb.WriteString("• <b>Command Manual</b>: <code>/viewmd &lt;file&gt;</code>, <code>/editmd &lt;file&gt; &lt;konten&gt;</code>, <code>/reloadmd</code>\n")

	return sb.String()
}

// MDMenuKeyboard returns keyboard for markdown management
func (ui *MDUI) MDMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnWizard := menu.Data("🧙‍♂️ Kelola File .MD (Wizard)", "md_wiz_start")
	btnReload := menu.Data("🔄 Reload Semua Cache", "md_reload_all")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnWizard, btnReload),
		menu.Row(btnBack),
	)
	return menu
}

// StartMDWizard starts interactive markdown file selector
func (ui *MDUI) StartMDWizard(c tele.Context) error {
	files, err := ui.mdLoader.ListFiles()
	if err != nil || len(files) == 0 {
		return c.Reply("⚠️ Tidak ada file .md yang ditemukan.", tele.ModeHTML)
	}

	text := "📝 <b>WIZARD PENGELOLAAN FILE MARKDOWN (.MD)</b>\n\n" +
		"Pilih file persona, SOP, atau knowledge bot yang ingin Anda kelola:"

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, f := range files {
		fCopy := f
		btn := menu.Data(fmt.Sprintf("📄 %s", fCopy), fmt.Sprintf("md_pick_%s", fCopy))
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu MD", "menu_md")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderMDFileDashboard displays file view and options
func (ui *MDUI) RenderMDFileDashboard(c tele.Context, filename string) error {
	content, err := ui.mdLoader.GetFile(filename)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membaca file %s: %v", html.EscapeString(filename), err))
	}

	preview := content
	if len(preview) > 600 {
		preview = preview[:600] + "\n...[dipotong untuk preview]"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📄 <b>FILE: <code>%s</code></b> (%d karakter)\n\n", html.EscapeString(filename), len(content)))
	sb.WriteString("<b>Cuplikan Konten:</b>\n")
	sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n\n", html.EscapeString(preview)))
	sb.WriteString("Pilih aksi untuk file ini:")

	menu := &tele.ReplyMarkup{}
	btnViewFull := menu.Data("📖 Baca Seluruh Isi", fmt.Sprintf("md_view_full_%s", filename))
	btnEdit := menu.Data("✏️ Ganti Konten Baru", fmt.Sprintf("md_edit_prompt_%s", filename))
	btnAppend := menu.Data("➕ Tambah Catatan (Append)", fmt.Sprintf("md_app_prompt_%s", filename))
	btnBack := menu.Data("⬅️ Kembali ke Daftar File", "md_wiz_start")

	menu.Inline(
		menu.Row(btnViewFull),
		menu.Row(btnEdit, btnAppend),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// PromptEditContent sets session step to edit
func (ui *MDUI) PromptEditContent(c tele.Context, filename string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &MDSession{
		Step:      MDStepEditContent,
		Filename:  filename,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := fmt.Sprintf("✏️ <b>EDIT FILE: <code>%s</code></b>\n\n"+
		"Silakan kirimkan <b>seluruh teks baru</b> untuk menggantikan isi file ini.\n\n"+
		"<i>💡 Tips: Anda juga bisa mengirim langsung file .md ke chat bot untuk pembaruan instan.</i>",
		html.EscapeString(filename))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("md_pick_%s", filename))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// PromptAppendContent sets session step to append
func (ui *MDUI) PromptAppendContent(c tele.Context, filename string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &MDSession{
		Step:      MDStepAppendContent,
		Filename:  filename,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := fmt.Sprintf("➕ <b>TAMBAH CATATAN KE: <code>%s</code></b>\n\n"+
		"Silakan kirimkan teks tambahan yang ingin disisipkan ke baris paling bawah file ini:",
		html.EscapeString(filename))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("md_pick_%s", filename))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
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
		fname := sess.Filename
		ui.CancelWizard(userID)
		if fname != "" {
			return true, ui.RenderMDFileDashboard(c, fname)
		}
		return true, c.Reply("❌ Operasi file .md dibatalkan.")
	}

	switch sess.Step {
	case MDStepEditContent:
		filename := sess.Filename
		if err := ui.mdLoader.SaveFile(filename, msgText); err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan file: %v", html.EscapeString(err.Error())))
		}
		ui.CancelWizard(userID)
		_ = c.Reply(fmt.Sprintf("✅ File <code>%s</code> berhasil diperbarui dan dimuat ulang!", html.EscapeString(filename)), tele.ModeHTML)
		return true, ui.RenderMDFileDashboard(c, filename)

	case MDStepAppendContent:
		filename := sess.Filename
		existing, _ := ui.mdLoader.GetFile(filename)
		newContent := strings.TrimSpace(existing) + "\n\n" + msgText
		if err := ui.mdLoader.SaveFile(filename, newContent); err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan file: %v", html.EscapeString(err.Error())))
		}
		ui.CancelWizard(userID)
		_ = c.Reply(fmt.Sprintf("✅ Berhasil menambahkan catatan baru ke <code>%s</code>!", html.EscapeString(filename)), tele.ModeHTML)
		return true, ui.RenderMDFileDashboard(c, filename)
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
func (ui *MDUI) RenderFullFile(c tele.Context, filename string) error {
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename += ".md"
	}

	content, err := ui.mdLoader.GetFile(filename)
	if err != nil || content == "" {
		return c.Reply(fmt.Sprintf("❌ File <code>%s</code> tidak ditemukan atau kosong.", html.EscapeString(filename)), tele.ModeHTML)
	}

	displayContent := content
	if len(displayContent) > 3500 {
		displayContent = displayContent[:3500] + "\n...[dipotong karena batas pesan telegram]"
	}

	text := fmt.Sprintf("📄 <b>ISI LENGKAP: <code>%s</code></b>\n\n<pre><code>%s</code></pre>", html.EscapeString(filename), html.EscapeString(displayContent))
	menu := &tele.ReplyMarkup{}
	btnBack := menu.Data("⬅️ Kembali ke Dashboard File", fmt.Sprintf("md_pick_%s", filename))
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
	return ui.RenderFullFile(c, filename)
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

	return c.Reply(fmt.Sprintf("✅ File <code>%s</code> berhasil diperbarui dan dimuat ulang (<i>Hot-Reloaded</i>)!", html.EscapeString(filename)), tele.ModeHTML)
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

	filename := doc.FileName
	if err := ui.mdLoader.SaveFile(filename, string(contentBytes)); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan file %s: %v", html.EscapeString(filename), html.EscapeString(err.Error())), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("🎉 File <code>%s</code> berhasil diunggah dan langsung aktif (<i>Hot-Reloaded</i>)!\nUkuran: %d bytes.", html.EscapeString(filename), len(contentBytes)), tele.ModeHTML)
}

