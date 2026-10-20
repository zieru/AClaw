package admin

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/memory"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type TopicUI struct {
	db             *storage.DB
	sessionManager *memory.SessionManager
	bot            *tele.Bot
	mu             sync.RWMutex
	awaitingRename map[int64]string // userID -> sessionID
	awaitingNew    map[int64]bool   // userID -> true
}

func NewTopicUI(db *storage.DB, sm *memory.SessionManager, bot *tele.Bot) *TopicUI {
	return &TopicUI{
		db:             db,
		sessionManager: sm,
		bot:            bot,
		awaitingRename: make(map[int64]string),
		awaitingNew:    make(map[int64]bool),
	}
}

func (ui *TopicUI) CancelSession(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.awaitingRename, userID)
	delete(ui.awaitingNew, userID)
}

func (ui *TopicUI) resolveChannelID(c tele.Context) string {
	return "admin"
}

// RenderTopicDashboard generates HTML summary and keyboard for chat topics
func (ui *TopicUI) RenderTopicDashboard(channelID, chatID, userID string) (string, *tele.ReplyMarkup, error) {
	topics, err := ui.db.ListChatSessions(channelID, chatID)
	if err != nil {
		return "", nil, err
	}

	if len(topics) == 0 {
		// Create initial session
		initSess, err := ui.db.GetOrCreateSession(channelID, chatID, userID)
		if err != nil {
			return "", nil, err
		}
		topics = []*storage.ChatSessionRecord{initSess}
	}

	var activeTopic *storage.ChatSessionRecord
	for _, t := range topics {
		if t.IsActive {
			activeTopic = t
			break
		}
	}
	if activeTopic == nil && len(topics) > 0 {
		topics[0].IsActive = true
		activeTopic = topics[0]
		_, _ = ui.db.SwitchChatSession(channelID, chatID, activeTopic.ID)
	}

	activeMsgCount := 0
	if activeTopic != nil {
		activeMsgCount, _ = ui.db.CountSessionMessages(activeTopic.ID)
	}

	var sb strings.Builder
	sb.WriteString("🧵 <b>MANAJEMEN TOPIK PERCAKAPAN</b>\n\n")

	if activeTopic != nil {
		sb.WriteString("📌 <b>Topik Aktif Saat Ini:</b>\n")
		sb.WriteString(fmt.Sprintf("🟢 <b>%s</b>\n", html.EscapeString(activeTopic.Title)))
		sb.WriteString(fmt.Sprintf("   • Riwayat: <code>%d pesan</code>\n", activeMsgCount))
		sb.WriteString(fmt.Sprintf("   • Terakhir aktif: <code>%s</code>\n\n", activeTopic.UpdatedAt.Format("02 Jan 15:04 WIB")))
	}

	sb.WriteString(fmt.Sprintf("🗂️ <b>Daftar Topik di Chat Ini (%d topik):</b>\n", len(topics)))
	for i, t := range topics {
		statusMarker := "⚪️"
		activeLabel := ""
		if t.IsActive {
			statusMarker = "🟢"
			activeLabel = " <i>[AKTIF]</i>"
		}
		msgCount, _ := ui.db.CountSessionMessages(t.ID)
		sb.WriteString(fmt.Sprintf("<b>#%d.</b> %s <b>%s</b> (<code>%d pesan</code>)%s\n",
			i+1, statusMarker, html.EscapeString(t.Title), msgCount, activeLabel))
	}

	sb.WriteString("\n💡 <i>Gunakan tombol di bawah untuk beralih, membuat, atau mengelola topik:</i>")

	menu := &tele.ReplyMarkup{}
	var allRows []tele.Row

	// 1. Switch topic buttons
	var switchBtns []tele.Btn
	for i, t := range topics {
		btnText := fmt.Sprintf("#%d", i+1)
		if t.IsActive {
			btnText = fmt.Sprintf("🔘 #%d", i+1)
		} else {
			btnText = fmt.Sprintf("🔄 #%d", i+1)
		}
		btn := menu.Data(btnText, fmt.Sprintf("top_sw_%s", t.ID))
		switchBtns = append(switchBtns, btn)
		if len(switchBtns) == 4 {
			allRows = append(allRows, menu.Row(switchBtns...))
			switchBtns = nil
		}
	}
	if len(switchBtns) > 0 {
		allRows = append(allRows, menu.Row(switchBtns...))
	}

	// 2. Actions: New Topic & Rename
	btnNew := menu.Data("➕ Topik Baru", "top_new")
	btnRename := menu.Data("✏️ Ganti Nama", "top_ren")
	allRows = append(allRows, menu.Row(btnNew, btnRename))

	// 3. Actions: Delete & Reset
	btnDelete := menu.Data("🗑️ Hapus Topik", "top_del_menu")
	btnReset := menu.Data("🧹 Bersihkan Topik Ini", "top_reset")
	allRows = append(allRows, menu.Row(btnDelete, btnReset))

	// 4. Back button
	btnRefresh := menu.Data("🔄 Refresh", "top_refresh")
	btnMain := menu.Data("⬅️ Menu Utama", "menu_main")
	allRows = append(allRows, menu.Row(btnRefresh, btnMain))

	menu.Inline(allRows...)
	return sb.String(), menu, nil
}

// HandleTopicDashboard displays the interactive topic dashboard
func (ui *TopicUI) HandleTopicDashboard(c tele.Context) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)

	text, menu, err := ui.RenderTopicDashboard(channelID, chatID, userID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memuat daftar topik: %v", err))
	}
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleNewTopicCmd processes `/newtopic [title]`
func (ui *TopicUI) HandleNewTopicCmd(c tele.Context) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)

	title := strings.TrimSpace(c.Message().Payload)
	if title == "" {
		topics, _ := ui.db.ListChatSessions(channelID, chatID)
		title = fmt.Sprintf("Topik #%d (%s)", len(topics)+1, time.Now().Format("02/01 15:04"))
	}

	newTopic, err := ui.sessionManager.CreateNewTopic(channelID, chatID, userID, title)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membuat topik baru: %v", err))
	}

	text := fmt.Sprintf("✨ <b>TOPIK BARU DIMULAI</b>\n\n"+
		"Topik Aktif: 🟢 <b>%s</b>\n\n"+
		"Riwayat percakapan sebelumnya tersimpan rapi.\n"+
		"Silakan ajukan pertanyaan atau perintah baru untuk topik ini!",
		html.EscapeString(newTopic.Title))

	menu := &tele.ReplyMarkup{}
	btnList := menu.Data("🗂️ Lihat Semua Topik", "top_refresh")
	menu.Inline(menu.Row(btnList))

	return c.Reply(text, menu, tele.ModeHTML)
}

// HandleSwitchTopicCmd processes `/switchtopic [index or name]`
func (ui *TopicUI) HandleSwitchTopicCmd(c tele.Context) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	payload := strings.TrimSpace(c.Message().Payload)

	if payload == "" {
		return ui.HandleTopicDashboard(c)
	}

	topics, err := ui.db.ListChatSessions(channelID, chatID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memuat topik: %v", err))
	}

	var targetTopic *storage.ChatSessionRecord

	// Check if integer index (1-based)
	if idx, err := strconv.Atoi(payload); err == nil && idx >= 1 && idx <= len(topics) {
		targetTopic = topics[idx-1]
	} else {
		// Search by title substring
		pLower := strings.ToLower(payload)
		for _, t := range topics {
			if strings.Contains(strings.ToLower(t.Title), pLower) {
				targetTopic = t
				break
			}
		}
	}

	if targetTopic == nil {
		return c.Reply(fmt.Sprintf("⚠️ Topik <i>%q</i> tidak ditemukan.\nGunakan <code>/topic</code> untuk melihat daftar topik yang tersedia.", html.EscapeString(payload)), tele.ModeHTML)
	}

	switched, err := ui.sessionManager.SwitchTopic(channelID, chatID, targetTopic.ID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal beralih topik: %v", err))
	}

	msgCount, _ := ui.db.CountSessionMessages(switched.ID)
	text := fmt.Sprintf("🔄 <b>BERALIH KE TOPIK: %s</b>\n\n"+
		"• Riwayat Topik Ini: <code>%d pesan</code>\n"+
		"• Status: 🟢 <b>Aktif</b>\n\n"+
		"Percakapan selanjutnya akan menggunakan konteks topik ini.",
		html.EscapeString(switched.Title), msgCount)

	menu := &tele.ReplyMarkup{}
	btnTopics := menu.Data("🗂️ Menu Topik", "top_refresh")
	menu.Inline(menu.Row(btnTopics))

	return c.Reply(text, menu, tele.ModeHTML)
}

// HandleRenameTopicCmd processes `/renametopic <new_title>`
func (ui *TopicUI) HandleRenameTopicCmd(c tele.Context) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)
	newTitle := strings.TrimSpace(c.Message().Payload)

	if newTitle == "" {
		ui.mu.Lock()
		activeTopic, _ := ui.db.GetOrCreateSession(channelID, chatID, userID)
		if activeTopic != nil {
			ui.awaitingRename[c.Sender().ID] = activeTopic.ID
		}
		ui.mu.Unlock()
		return c.Reply("✍️ <b>Ganti Nama Topik Aktif</b>\n\nSilakan ketikkan nama/judul baru untuk topik ini sekarang (atau kirim <code>/cancel</code> untuk batal):", tele.ModeHTML)
	}

	activeTopic, err := ui.db.GetOrCreateSession(channelID, chatID, userID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mendapatkan topik aktif: %v", err))
	}

	if err := ui.sessionManager.RenameTopic(activeTopic.ID, newTitle); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mengubah nama topik: %v", err))
	}

	return c.Reply(fmt.Sprintf("✅ Judul topik aktif berhasil diubah menjadi: <b>%s</b>", html.EscapeString(newTitle)), tele.ModeHTML)
}

// HandleDeleteTopicCmd processes `/deltopic`
func (ui *TopicUI) HandleDeleteTopicCmd(c tele.Context) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)

	topics, err := ui.db.ListChatSessions(channelID, chatID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memuat topik: %v", err))
	}

	if len(topics) <= 1 {
		return c.Reply("⚠️ Chat ini hanya memiliki 1 topik. Anda tidak dapat menghapus topik satu-satunya (gunakan <code>/reset</code> jika ingin membersihkan riwayatnya).", tele.ModeHTML)
	}

	var sb strings.Builder
	sb.WriteString("🗑️ <b>PILIH TOPIK YANG AKAN DIHAPUS:</b>\n\n")
	sb.WriteString("⚠️ <i>Menghapus topik akan menghapus seluruh riwayat pesan di topik tersebut secara permanen.</i>\n\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, t := range topics {
		label := fmt.Sprintf("#%d. %s", i+1, t.Title)
		if len(label) > 25 {
			label = label[:25] + "..."
		}
		if t.IsActive {
			label += " [Aktif]"
		}
		btnDel := menu.Data(fmt.Sprintf("🗑️ %s", label), fmt.Sprintf("top_del_conf_%s", t.ID))
		rows = append(rows, menu.Row(btnDel))
	}

	btnBatal := menu.Data("⬅️ Kembali ke Menu Topik", "top_refresh")
	rows = append(rows, menu.Row(btnBatal))
	menu.Inline(rows...)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleResetTopicCmd processes `/resettopic`
func (ui *TopicUI) HandleResetTopicCmd(c tele.Context) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)

	activeTopic, err := ui.db.GetOrCreateSession(channelID, chatID, userID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mereset topik: %v", err))
	}

	_ = ui.db.ClearSessionMessages(activeTopic.ID)

	text := fmt.Sprintf("✨ <b>RIWAYAT TOPIK DIBERSIHKAN</b>\n\n"+
		"Konteks pesan pada topik <b>%s</b> telah direset.\n"+
		"Topik Anda yang lain tetap aman tersimpan.",
		html.EscapeString(activeTopic.Title))

	menu := &tele.ReplyMarkup{}
	btnList := menu.Data("🗂️ Menu Topik", "top_refresh")
	menu.Inline(menu.Row(btnList))

	return c.Reply(text, menu, tele.ModeHTML)
}

// HandleTextMessage intercepts text inputs for awaitingRename or awaitingNew
func (ui *TopicUI) HandleTextMessage(c tele.Context) (bool, error) {
	ui.mu.Lock()
	renameSessID, isRename := ui.awaitingRename[c.Sender().ID]
	isNew := ui.awaitingNew[c.Sender().ID]
	ui.mu.Unlock()

	if !isRename && !isNew {
		return false, nil
	}

	text := strings.TrimSpace(c.Message().Text)
	if text == "/cancel" {
		ui.CancelSession(c.Sender().ID)
		return true, c.Reply("🛑 Tindakan pengelolaan topik dibatalkan.")
	}

	if isRename {
		ui.CancelSession(c.Sender().ID)
		if text == "" {
			return true, c.Reply("⚠️ Nama topik tidak boleh kosong.")
		}
		if err := ui.sessionManager.RenameTopic(renameSessID, text); err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal mengubah nama topik: %v", err))
		}
		return true, c.Reply(fmt.Sprintf("✅ Judul topik berhasil diperbarui menjadi: <b>%s</b>", html.EscapeString(text)), tele.ModeHTML)
	}

	if isNew {
		ui.CancelSession(c.Sender().ID)
		if text == "" {
			text = "Topik Baru"
		}
		channelID := ui.resolveChannelID(c)
		chatID := strconv.FormatInt(c.Chat().ID, 10)
		userID := strconv.FormatInt(c.Sender().ID, 10)

		newTopic, err := ui.sessionManager.CreateNewTopic(channelID, chatID, userID, text)
		if err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal membuat topik: %v", err))
		}

		respText := fmt.Sprintf("✨ <b>TOPIK BARU AKTIF: %s</b>\n\nSilakan kirimkan pertanyaan atau instruksi Anda!", html.EscapeString(newTopic.Title))
		return true, c.Reply(respText, tele.ModeHTML)
	}

	return false, nil
}

// HandleCallback processes inline keyboard callbacks for topics
func (ui *TopicUI) HandleCallback(c tele.Context, data string) error {
	channelID := ui.resolveChannelID(c)
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)

	switch {
	case data == "top_refresh" || data == "top_dashboard":
		return ui.HandleTopicDashboard(c)

	case strings.HasPrefix(data, "top_sw_"):
		targetID := strings.TrimPrefix(data, "top_sw_")
		switched, err := ui.sessionManager.SwitchTopic(channelID, chatID, targetID)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal beralih topik"})
			return err
		}
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🟢 Beralih ke: %s", switched.Title)})
		return ui.HandleTopicDashboard(c)

	case data == "top_new":
		// Quick create a new topic with timestamp and switch to it immediately
		topics, _ := ui.db.ListChatSessions(channelID, chatID)
		newTitle := fmt.Sprintf("Topik #%d (%s)", len(topics)+1, time.Now().Format("02/01 15:04"))
		newTopic, err := ui.sessionManager.CreateNewTopic(channelID, chatID, userID, newTitle)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal membuat topik"})
			return err
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "✨ Topik baru dibuat & aktif!"})
		text, menu, err := ui.RenderTopicDashboard(channelID, chatID, userID)
		if err == nil {
			_ = c.EditOrSend(text, menu, tele.ModeHTML)
		}
		return c.Send(fmt.Sprintf("✨ <b>TOPIK BARU AKTIF: %s</b>\n\nRiwayat sebelumnya tersimpan aman. Silakan mulai percakapan baru di topik ini!", html.EscapeString(newTopic.Title)), tele.ModeHTML)

	case data == "top_ren":
		activeTopic, _ := ui.db.GetOrCreateSession(channelID, chatID, userID)
		if activeTopic == nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "Topik tidak ditemukan"})
			return nil
		}
		ui.mu.Lock()
		ui.awaitingRename[c.Sender().ID] = activeTopic.ID
		ui.mu.Unlock()
		_ = c.Respond(&tele.CallbackResponse{})
		return c.Send(fmt.Sprintf("✍️ <b>UBAH NAMA TOPIK AKTIF</b>\n\nTopik saat ini: <b>%s</b>\n\nKetikkan nama baru untuk topik ini sekarang (atau ketik <code>/cancel</code> untuk membatalkan):", html.EscapeString(activeTopic.Title)), tele.ModeHTML)

	case data == "top_del_menu":
		_ = c.Respond(&tele.CallbackResponse{})
		return ui.HandleDeleteTopicCmd(c)

	case strings.HasPrefix(data, "top_del_conf_"):
		targetID := strings.TrimPrefix(data, "top_del_conf_")
		sess, err := ui.db.GetSessionByID(targetID)
		if err != nil || sess == nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "Topik tidak ditemukan"})
			return ui.HandleTopicDashboard(c)
		}

		msgCount, _ := ui.db.CountSessionMessages(sess.ID)
		text := fmt.Sprintf("⚠️ <b>KONFIRMASI HAPUS TOPIK</b>\n\n"+
			"Apakah Anda yakin ingin menghapus topik ini secara permanen?\n\n"+
			"• Judul: <b>%s</b>\n"+
			"• Riwayat: <code>%d pesan</code>\n\n"+
			"<i>Tindakan ini tidak dapat dibatalkan.</i>",
			html.EscapeString(sess.Title), msgCount)

		menu := &tele.ReplyMarkup{}
		btnYes := menu.Data("🗑️ Ya, Hapus Sekarang", fmt.Sprintf("top_do_del_%s", sess.ID))
		btnNo := menu.Data("❌ Batal", "top_refresh")
		menu.Inline(menu.Row(btnYes, btnNo))

		_ = c.Respond(&tele.CallbackResponse{})
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case strings.HasPrefix(data, "top_do_del_"):
		targetID := strings.TrimPrefix(data, "top_do_del_")
		nextActive, err := ui.sessionManager.DeleteTopic(channelID, chatID, targetID)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal menghapus topik"})
			return err
		}
		activeInfo := ""
		if nextActive != nil {
			activeInfo = fmt.Sprintf(" Topik aktif sekarang: %s", nextActive.Title)
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🗑️ Topik berhasil dihapus." + activeInfo})
		return ui.HandleTopicDashboard(c)

	case data == "top_reset":
		_ = c.Respond(&tele.CallbackResponse{})
		return ui.HandleResetTopicCmd(c)
	}

	return nil
}
