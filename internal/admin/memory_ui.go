package admin

import (
	"fmt"
	"html"
	"strconv"
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
func (ui *MemoryUI) RenderMemorySummary(currentUserID int64) string {
	userIDStr := "399999658"
	if currentUserID > 0 {
		userIDStr = strconv.FormatInt(currentUserID, 10)
	}

	globals, _ := ui.memoryManager.ListMemoryItems("global", "system", "", 50)
	userMems, _ := ui.memoryManager.ListMemoryItems("user", userIDStr, "", 100)

	// Count by type
	factualCount := 0
	proceduralCount := 0
	episodicCount := 0
	semanticCount := 0

	for _, m := range append(globals, userMems...) {
		switch strings.ToLower(m.Type) {
		case "procedural":
			proceduralCount++
		case "episodic":
			episodicCount++
		case "semantic":
			semanticCount++
		default:
			factualCount++
		}
	}

	var sb strings.Builder
	sb.WriteString("🧠 <b>MANAJEMEN MEMORI GOASSISTANT (SQLite FTS5)</b>\n\n")

	sb.WriteString(fmt.Sprintf("📊 <b>Statistik Kategori Memori (User: <code>%s</code>):</b>\n", userIDStr))
	sb.WriteString(fmt.Sprintf("• 📌 <b>Faktual:</b> %d catatan (profil & preferensi)\n", factualCount))
	sb.WriteString(fmt.Sprintf("• 📜 <b>Prosedural:</b> %d catatan (SOP & panduan teknis)\n", proceduralCount))
	sb.WriteString(fmt.Sprintf("• 🔄 <b>Episodik:</b> %d catatan (progres & riwayat tugas)\n", episodicCount))
	sb.WriteString(fmt.Sprintf("• 💡 <b>Semantik:</b> %d catatan (konsep & pemahaman sistem)\n\n", semanticCount))

	if len(globals) > 0 {
		sb.WriteString("🌐 <b>SOP Global Sistem:</b>\n")
		for i, g := range globals {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("<i>...dan %d SOP lainnya</i>\n", len(globals)-5))
				break
			}
			sb.WriteString(fmt.Sprintf("%d. [<code>%s</code>] %s\n", i+1, html.EscapeString(g.Key), html.EscapeString(g.Content)))
		}
		sb.WriteString("\n")
	}

	if len(userMems) > 0 {
		sb.WriteString("👤 <b>Catatan Terbaru Pengguna:</b>\n")
		count := 0
		for _, u := range userMems {
			if count >= 6 {
				sb.WriteString(fmt.Sprintf("<i>...dan %d memori lainnya</i>\n", len(userMems)-6))
				break
			}
			sb.WriteString(fmt.Sprintf("• [<code>%s</code>|%s] %s\n", html.EscapeString(u.Key), u.Type, html.EscapeString(u.Content)))
			count++
		}
		sb.WriteString("\n")
	}

	sb.WriteString("📋 <b>Perintah Manajemen Memori:</b>\n")
	sb.WriteString("• <code>/searchmemory &lt;kata kunci&gt;</code> (Uji pencarian hybrid FTS5)\n")
	sb.WriteString("• <code>/savefact &lt;global|user&gt; &lt;id&gt; &lt;tag&gt; &lt;content&gt;</code>\n")
	sb.WriteString("• <code>/clearmemory &lt;user_id&gt;</code> (Hapus memori preferensi user)\n")
	sb.WriteString("• <code>/resetsession &lt;chat_id&gt;</code> (Reset riwayat chat topik ini)\n")

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
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan memori ke SQLite: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Fakta [<code>%s</code>] berhasil disimpan ke SQLite Memory (scope <code>%s:%s</code>)!", html.EscapeString(tag), html.EscapeString(scope), html.EscapeString(scopeID)), tele.ModeHTML)
}

// HandleSearchMemory processes `/searchmemory <query>`
func (ui *MemoryUI) HandleSearchMemory(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Masukkan kata kunci atau pertanyaan pencarian.\nContoh: <code>/searchmemory bypass ssl</code>", tele.ModeHTML)
	}

	query := strings.Join(args, " ")
	userIDStr := "399999658"
	if c.Sender() != nil {
		userIDStr = strconv.FormatInt(c.Sender().ID, 10)
	}

	results, err := ui.memoryManager.SearchMemoriesAdvanced("user", userIDStr, query, "hybrid", 5)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error pencarian memori: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	if len(results) == 0 {
		return c.Reply(fmt.Sprintf("🔍 Tidak ditemukan memori dengan kata kunci <i>\"%s\"</i> untuk user <code>%s</code>.", html.EscapeString(query), userIDStr), tele.ModeHTML)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 <b>Hasil Pencarian Memori Hybrid (\"%s\"):</b>\n\n", html.EscapeString(query)))
	for i, r := range results {
		scoreStr := ""
		if r.Score > 0 {
			scoreStr = fmt.Sprintf(" <i>(skor: %.2f)</i>", r.Score)
		}
		sb.WriteString(fmt.Sprintf("%d. <b>[%s|%s]</b> <code>%s</code>%s\n  %s\n\n", i+1, r.Type, r.Category, html.EscapeString(r.Key), scoreStr, html.EscapeString(r.Content)))
	}

	return c.Reply(sb.String(), tele.ModeHTML)
}

// HandleClearMemory processes `/clearmemory <user_id>`
func (ui *MemoryUI) HandleClearMemory(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan user ID yang ingin dibersihkan memorinya.\nContoh: <code>/clearmemory 12345678</code>", tele.ModeHTML)
	}

	userID := args[0]
	if err := ui.memoryManager.ClearUserMemory(userID); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membersihkan memori user di SQLite: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("🧹 Memori untuk user <code>%s</code> di SQLite berhasil dibersihkan!", html.EscapeString(userID)), tele.ModeHTML)
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
