package admin

import (
	"fmt"
	"html"
	"io"
	"strings"

	"goassistant/internal/agent"
	tele "gopkg.in/telebot.v3"
)

type MDUI struct {
	mdLoader *agent.MDLoader
	bot      *tele.Bot
}

func NewMDUI(loader *agent.MDLoader, bot *tele.Bot) *MDUI {
	return &MDUI{mdLoader: loader, bot: bot}
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
	sb.WriteString("1. <b>Upload Langsung</b>: Kirimkan file dokumen <code>.md</code> (misal: <code>IDENTITY.md</code>) ke chat bot ini, file akan otomatis diperbarui (<i>Hot-Reloaded</i>).\n")
	sb.WriteString("2. <b>Lihat Isi</b>: <code>/viewmd &lt;nama_file.md&gt;</code>\n")
	sb.WriteString("3. <b>Edit Teks Singkat</b>: <code>/editmd &lt;nama_file.md&gt; &lt;konten_baru&gt;</code>\n")
	sb.WriteString("4. <b>Reload Cache</b>: <code>/reloadmd</code>\n")

	return sb.String()
}

// HandleViewMD processes `/viewmd`
func (ui *MDUI) HandleViewMD(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan nama file yang ingin dilihat.\nContoh: <code>/viewmd IDENTITY.md</code>", tele.ModeHTML)
	}

	filename := args[0]
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename += ".md"
	}

	content, err := ui.mdLoader.GetFile(filename)
	if err != nil || content == "" {
		return c.Reply(fmt.Sprintf("❌ File <code>%s</code> tidak ditemukan atau kosong.", html.EscapeString(filename)), tele.ModeHTML)
	}

	if len(content) > 3500 {
		content = content[:3500] + "\n...[dipotong karena batas pesan telegram]"
	}

	return c.Reply(fmt.Sprintf("📄 <b>Isi File <code>%s</code>:</b>\n\n<pre><code>%s</code></pre>", html.EscapeString(filename), html.EscapeString(content)), tele.ModeHTML)
}

// HandleEditMD processes `/editmd`
func (ui *MDUI) HandleEditMD(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/editmd SOUL.md Selalu ramah dan panggil user dengan sebutan Bos.</code>", tele.ModeHTML)
	}

	filename := args[0]
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename += ".md"
	}

	content := strings.Join(args[1:], " ")
	if err := ui.mdLoader.SaveFile(filename, content); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan file: %v", html.EscapeString(err.Error())), tele.ModeHTML)
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
		return nil // Ignore non-MD documents for admin config
	}

	// Download file from Telegram via telebot File reader
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
