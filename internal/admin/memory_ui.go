package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/memory"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type MemoryUI struct {
	db             *storage.DB
	memoryManager  *memory.Manager
	sessionManager *memory.SessionManager
}

func NewMemoryUI(db *storage.DB, mm *memory.Manager, sm *memory.SessionManager) *MemoryUI {
	return &MemoryUI{db: db, memoryManager: mm, sessionManager: sm}
}

// RenderMemorySummary returns summary of stored memories in HTML format
func (ui *MemoryUI) RenderMemorySummary() string {
	globals, _ := ui.db.ListMemoryItems("global", "system")

	var sb strings.Builder
	sb.WriteString("🧠 <b>MANAJEMEN MEMORI & SESI PERCAKAPAN</b>\n\n")

	sb.WriteString("🌐 <b>Fakta Global / SOP Sistem:</b>\n")
	if len(globals) == 0 {
		sb.WriteString("(Belum ada memori global yang tersimpan)\n\n")
	} else {
		for i, g := range globals {
			sb.WriteString(fmt.Sprintf("%d. [<code>%s</code>] %s\n", i+1, html.EscapeString(g.KeyTag), html.EscapeString(g.Content)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("📋 <b>Perintah Manajemen Memori:</b>\n")
	sb.WriteString("• <code>/savefact &lt;global|user&gt; &lt;id&gt; &lt;tag&gt; &lt;content&gt;</code>\n")
	sb.WriteString("• <code>/clearmemory &lt;user_id&gt;</code> (Menghapus memori preferensi user)\n")
	sb.WriteString("• <code>/resetsession &lt;chat_id&gt;</code> (Reset riwayat chat aktif)\n\n")
	sb.WriteString("<b>Contoh Simpan SOP Global:</b>\n")
	sb.WriteString("<code>/savefact global system sop Jangan berikan informasi sensitif server kepada pengguna umum.</code>\n")

	return sb.String()
}

// HandleSaveFact processes `/savefact`
func (ui *MemoryUI) HandleSaveFact(c tele.Context) error {
	args := c.Args()
	if len(args) < 4 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/savefact global system company_name Perusahaan XYZ</code>", tele.ModeHTML)
	}

	scope := strings.ToLower(args[0])
	scopeID := args[1]
	tag := args[2]
	content := strings.Join(args[3:], " ")

	if err := ui.memoryManager.SaveFact(scope, scopeID, tag, content, "fact"); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan memori: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Fakta [<code>%s</code>] berhasil disimpan ke scope <code>%s:%s</code>!", html.EscapeString(tag), html.EscapeString(scope), html.EscapeString(scopeID)), tele.ModeHTML)
}

// HandleResetSession processes `/resetsession`
func (ui *MemoryUI) HandleResetSession(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan chat ID yang ingin di-reset.")
	}

	chatID := args[0]
	session, err := ui.sessionManager.GetOrCreate("admin", chatID, "admin")
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error: %v", html.EscapeString(err.Error())))
	}

	if err := ui.sessionManager.ResetSession(session.ID); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mereset sesi: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("🧹 Riwayat percakapan untuk chat <code>%s</code> berhasil dibersihkan!", html.EscapeString(chatID)), tele.ModeHTML)
}
