package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
	"goassistant/internal/tgformat"
	"goassistant/internal/tools"
	tele "gopkg.in/telebot.v3"
)

type BotAdapter struct {
	channelID    string
	name         string
	token        string
	bot          *tele.Bot
	orchestrator *agent.Orchestrator
	db           *storage.DB
	activeTasks  sync.Map
	stopChan     chan struct{}
}

func NewBotAdapter(channelID, name, token string, orch *agent.Orchestrator, db *storage.DB) (*BotAdapter, error) {
	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("gagal inisialisasi telebot %s: %w", name, err)
	}

	return &BotAdapter{
		channelID:    channelID,
		name:         name,
		token:        token,
		bot:          bot,
		orchestrator: orch,
		db:           db,
		stopChan:     make(chan struct{}),
	}, nil
}

func (a *BotAdapter) ID() string   { return a.channelID }
func (a *BotAdapter) Type() string { return "telegram" }
func (a *BotAdapter) Name() string { return a.name }

func (a *BotAdapter) Start(ctx context.Context) error {
	a.registerHandlers()
	go a.bot.Start()

	// Register Bot Commands for Telegram autocomplete
	commands := []tele.Command{
		{Text: "retry", Description: "Coba lagi pesan/pertanyaan terakhir"},
		{Text: "new", Description: "Mulai sesi percakapan baru (reset konteks)"},
		{Text: "reset", Description: "Reset riwayat percakapan"},
		{Text: "stop", Description: "Hentikan respon AI yang sedang diproses"},
		{Text: "clearsudo", Description: "Hapus sesi password sudo dari memori"},
		{Text: "status", Description: "Cek status bot & sesi percakapan"},
		{Text: "help", Description: "Bantuan & panduan penggunaan bot"},
	}
	if err := a.bot.SetCommands(commands); err != nil {
		log.Printf("⚠️ [Channel-TG] Gagal mendaftarkan menu command untuk '%s': %v", a.name, err)
	}

	log.Printf("[Channel-TG] Bot '%s' (@%s) aktif dan siap menerima pesan.", a.name, a.bot.Me.Username)
	return nil
}

func (a *BotAdapter) Stop() error {
	a.bot.Stop()
	return nil
}

func (a *BotAdapter) SendMessage(targetID, text string) error {
	chatID, err := strconv.ParseInt(targetID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID %s: %w", targetID, err)
	}
	_, err = a.bot.Send(&tele.Chat{ID: chatID}, text)
	return err
}

func (a *BotAdapter) registerHandlers() {
	// Command Handlers
	a.bot.Handle("/start", a.handleHelp)
	a.bot.Handle("/help", a.handleHelp)
	a.bot.Handle("/new", a.handleNew)
	a.bot.Handle("/reset", a.handleNew)
	a.bot.Handle("/clear", a.handleNew)
	a.bot.Handle("/clearsudo", a.handleClearSudo)
	a.bot.Handle("/retry", a.handleRetry)
	a.bot.Handle("/stop", a.handleStop)
	a.bot.Handle("/cancel", a.handleStop)

	// Topic Management Commands
	a.bot.Handle("/topic", a.handleTopic)
	a.bot.Handle("/topics", a.handleTopic)
	a.bot.Handle("/threads", a.handleTopic)
	a.bot.Handle("/newtopic", a.handleNewTopic)
	a.bot.Handle("/switchtopic", a.handleSwitchTopic)
	a.bot.Handle("/renametopic", a.handleRenameTopic)
	a.bot.Handle("/deltopic", a.handleDeleteTopic)

	a.bot.Handle(tele.OnCallback, func(c tele.Context) error {
		if c.Callback() == nil {
			return nil
		}
		data := strings.TrimPrefix(c.Callback().Data, "\f")
		if strings.HasPrefix(data, "top_") {
			return a.handleTopicCallback(c, data)
		}
		if strings.HasPrefix(data, "cancel_task") {
			_ = c.Respond(&tele.CallbackResponse{Text: "Membatalkan proses AI..."})
			return a.handleStop(c)
		}
		if strings.HasPrefix(data, "retry_task") {
			_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Mencoba ulang..."})
			return a.handleRetry(c)
		}
		if strings.HasPrefix(data, "reset_session") {
			_ = c.Respond(&tele.CallbackResponse{Text: "✨ Mereset sesi percakapan..."})
			return a.handleNew(c)
		}
		return nil
	})
	a.bot.Handle("/status", a.handleStatus)

	// Text message handler
	a.bot.Handle(tele.OnText, func(c tele.Context) error {
		msg := c.Message()
		if msg == nil {
			return nil
		}

		// If in group, check if bot is mentioned or if direct
		if c.Chat().Type == tele.ChatGroup || c.Chat().Type == tele.ChatSuperGroup {
			botUsername := "@" + a.bot.Me.Username
			if !strings.Contains(msg.Text, botUsername) && (msg.ReplyTo == nil || msg.ReplyTo.Sender.ID != a.bot.Me.ID) {
				return nil // Ignore non-mentioned group messages
			}
		}

		userPrompt := strings.TrimSpace(strings.ReplaceAll(msg.Text, "@"+a.bot.Me.Username, ""))
		if userPrompt == "" {
			return nil
		}

		return a.executePrompt(c, msg, userPrompt, nil, 0)
	})

	// Photo / Image handler
	a.bot.Handle(tele.OnPhoto, func(c tele.Context) error {
		photo := c.Message().Photo
		if photo == nil {
			return nil
		}

		caption := strings.TrimSpace(c.Message().Caption)
		if caption == "" {
			caption = "Tolong analisis dan jelaskan gambar/foto terlampir."
		}

		var images []string
		var fileMB float64
		if reader, err := a.bot.File(&photo.File); err == nil {
			defer reader.Close()
			if imgBytes, err := io.ReadAll(reader); err == nil && len(imgBytes) > 0 {
				fileMB = float64(len(imgBytes)) / (1024 * 1024)
				images = append(images, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(imgBytes))
			}
		}

		return a.executePrompt(c, c.Message(), caption, images, fileMB)
	})

	// Document / File handler
	a.bot.Handle(tele.OnDocument, func(c tele.Context) error {
		doc := c.Message().Document
		if doc == nil {
			return nil
		}

		fileMB := float64(doc.FileSize) / (1024 * 1024)
		caption := strings.TrimSpace(c.Message().Caption)
		if caption == "" {
			caption = fmt.Sprintf("Analisis dokumen terlampir: %s", doc.FileName)
		}

		var images []string
		prompt := caption
		if reader, err := a.bot.File(&doc.File); err == nil {
			defer reader.Close()
			if docBytes, err := io.ReadAll(reader); err == nil {
				ext := strings.ToLower(filepath.Ext(doc.FileName))
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
					mime := "image/jpeg"
					if ext == ".png" {
						mime = "image/png"
					} else if ext == ".webp" {
						mime = "image/webp"
					}
					images = append(images, fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(docBytes)))
				} else if isTextExt(ext) && len(docBytes) <= 150*1024 {
					prompt = fmt.Sprintf("[📎 Berkas: %s]\n```\n%s\n```\n\n%s", doc.FileName, string(docBytes), caption)
				}
			}
		}

		return a.executePrompt(c, c.Message(), prompt, images, fileMB)
	})
}

func isTextExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".txt", ".md", ".json", ".csv", ".tsv", ".yaml", ".yml", ".xml", ".html", ".htm",
		".css", ".js", ".ts", ".jsx", ".tsx", ".go", ".py", ".java", ".c", ".cpp", ".h",
		".sql", ".sh", ".bat", ".ps1", ".log", ".ini", ".conf", ".env", ".toml", ".php":
		return true
	default:
		return false
	}
}

func (a *BotAdapter) executePrompt(c tele.Context, replyTo *tele.Message, userPrompt string, images []string, fileMB float64) error {
	_ = c.Notify(tele.Typing)
	cancelMenu := &tele.ReplyMarkup{}
	cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
	cancelMenu.Inline(cancelMenu.Row(cancelBtn))

	var thinkingMsg *tele.Message
	if replyTo != nil {
		thinkingMsg, _ = a.bot.Reply(replyTo, "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML, cancelMenu)
	} else if c.Message() != nil {
		thinkingMsg, _ = a.bot.Reply(c.Message(), "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML, cancelMenu)
	} else {
		thinkingMsg, _ = a.bot.Send(c.Chat(), "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML, cancelMenu)
	}

	policy := a.db.GetResolvedPolicy(a.channelID, strconv.FormatInt(c.Chat().ID, 10))

	var stopUpdater func()
	var onProgressStatus func(string)
	var onStreamChunk func(provider.StreamChunk)

	if policy.StreamingEnabled {
		stopUpdater, onProgressStatus, onStreamChunk = createProgressiveThinkingManager(a.bot, thinkingMsg, "🤔 <i>Sedang berpikir...</i>")
	} else {
		stopUpdater, onProgressStatus, _ = createProgressiveThinkingManager(a.bot, thinkingMsg, "🤔 <i>Sedang memproses respon...</i>")
		onStreamChunk = nil
	}
	defer stopUpdater()

	timeoutSec := config.Get().Timeouts.HandlerSeconds
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	a.activeTasks.Store(c.Chat().ID, cancel)
	defer func() {
		a.activeTasks.Delete(c.Chat().ID)
		cancel()
	}()

	resp, err := a.orchestrator.ProcessMessage(ctx, agent.UserRequest{
		ChannelType:    "telegram",
		ChannelID:      a.channelID,
		ChannelName:    a.name,
		ChatID:         strconv.FormatInt(c.Chat().ID, 10),
		UserID:         strconv.FormatInt(c.Sender().ID, 10),
		UserName:       c.Sender().Username,
		UserPrompt:     userPrompt,
		AttachedFileMB: fileMB,
		AttachedImages: images,
		OnProgress: func(status string) {
			onProgressStatus(status)
		},
		OnStreamChunk: func(chunk provider.StreamChunk) {
			if onStreamChunk != nil {
				onStreamChunk(chunk)
			}
		},
	})

	stopUpdater()

	if err != nil {
		log.Printf("⚠️ [Telegram Bot] Request gagal/timeout (Chat: %d, User: %s, Prompt: %q): %v",
			c.Chat().ID, c.Sender().Username, userPrompt, err)
		if ctx.Err() == context.Canceled {
			text := "🛑 <b>PROSES DIBATALKAN</b>\n\nRespon AI berhasil dihentikan atas permintaan pengguna."
			if thinkingMsg != nil {
				_, _ = a.bot.Edit(thinkingMsg, text, tele.ModeHTML)
				return nil
			}
			return c.Reply(text, tele.ModeHTML)
		}

		friendlyErr := agent.FormatUserFriendlyError(err)
		errMenu := &tele.ReplyMarkup{}
		retryBtn := errMenu.Data("🔄 Coba Lagi", "retry_task")
		newBtn := errMenu.Data("✨ Reset Sesi", "reset_session")
		errMenu.Inline(errMenu.Row(retryBtn, newBtn))

		if thinkingMsg != nil {
			_, _ = a.bot.Edit(thinkingMsg, friendlyErr, tele.ModeHTML, errMenu)
			return nil
		}
		return c.Reply(friendlyErr, tele.ModeHTML, errMenu)
	}

	return sendOrEditResponse(c, thinkingMsg, resp.Text, resp.MediaFiles)
}

func (a *BotAdapter) handleRetry(c tele.Context) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)
	lastPrompt := a.orchestrator.GetLastPrompt(a.channelID, chatID, userID)
	if strings.TrimSpace(lastPrompt) == "" {
		text := "⚠️ <b>Tidak ada pesan sebelumnya yang dapat dicoba lagi.</b>\nSilakan kirimkan pertanyaan atau pesan baru Anda."
		if c.Callback() != nil && c.Message() != nil {
			_, _ = a.bot.Edit(c.Message(), text, tele.ModeHTML)
			return nil
		}
		return c.Send(text, tele.ModeHTML)
	}

	return a.executePrompt(c, c.Message(), lastPrompt, nil, 0)
}

// createProgressiveThinkingManager runs a periodic ticker that updates thinking and streaming text dynamically
func createProgressiveThinkingManager(bot *tele.Bot, targetMsg *tele.Message, initialPrefix string) (stopFunc func(), updateStatus func(string), onChunk func(chunk provider.StreamChunk)) {
	if targetMsg == nil {
		return func() {}, func(string) {}, func(provider.StreamChunk) {}
	}

	cancelMenu := &tele.ReplyMarkup{}
	cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
	cancelMenu.Inline(cancelMenu.Row(cancelBtn))

	var mu sync.Mutex
	var thinkingBuf strings.Builder
	var contentBuf strings.Builder
	customStatus := ""
	lastSentText := ""
	stopped := false
	doneChan := make(chan struct{})
	startTime := time.Now()

	updateStatus = func(status string) {
		mu.Lock()
		customStatus = status
		mu.Unlock()
		if targetMsg != nil {
			_, _ = bot.Edit(targetMsg, status, tele.ModeHTML, cancelMenu)
		}
	}

	onChunk = func(chunk provider.StreamChunk) {
		mu.Lock()
		defer mu.Unlock()
		if chunk.Thinking != "" {
			thinkingBuf.WriteString(chunk.Thinking)
		}
		if chunk.Content != "" {
			contentBuf.WriteString(chunk.Content)
		}
	}

	stopFunc = func() {
		mu.Lock()
		if stopped {
			mu.Unlock()
			return
		}
		stopped = true
		mu.Unlock()
		close(doneChan)
	}

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-doneChan:
				return
			case <-ticker.C:
				mu.Lock()
				if stopped {
					mu.Unlock()
					return
				}
				elapsedSec := int(time.Since(startTime).Seconds())
				curThinking := strings.TrimSpace(thinkingBuf.String())
				curContent := strings.TrimSpace(contentBuf.String())
				status := customStatus

				var text string
				if curThinking != "" || curContent != "" {
					// Live streaming preview mode
					if curContent == "" && curThinking != "" {
						// Only thinking so far
						previewThink := curThinking
						if len(previewThink) > 3500 {
							previewThink = previewThink[len(previewThink)-3500:]
						}
						text = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote>%s ▌</blockquote>", html.EscapeString(previewThink))
					} else if curThinking != "" && curContent != "" {
						// Thinking + content streaming
						previewThink := curThinking
						if len(previewThink) > 1500 {
							previewThink = previewThink[:1500] + "..."
						}
						previewContent := curContent
						if len(previewContent) > 2000 {
							previewContent = previewContent[len(previewContent)-2000:]
						}
						formattedContent := tgformat.MarkdownToTelegramHTML(previewContent)
						text = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote>%s</blockquote>\n\n%s ▌", html.EscapeString(previewThink), formattedContent)
					} else {
						// Only content
						previewContent := curContent
						if len(previewContent) > 3800 {
							previewContent = previewContent[len(previewContent)-3800:]
						}
						text = tgformat.MarkdownToTelegramHTML(previewContent) + " ▌"
					}
				} else if status != "" {
					text = fmt.Sprintf("%s <i>(%dd)</i>", status, elapsedSec)
				} else {
					text = fmt.Sprintf("💭 <i>Sedang berpikir... (%dd)</i>", elapsedSec)
				}

				if text == lastSentText {
					mu.Unlock()
					continue
				}
				lastSentText = text
				mu.Unlock()

				if targetMsg != nil {
					_, _ = bot.Edit(targetMsg, text, tele.ModeHTML, cancelMenu)
				}
			}
		}
	}()

	return stopFunc, updateStatus, onChunk
}

func (a *BotAdapter) handleNew(c tele.Context) error {
	if cancelVal, loaded := a.activeTasks.LoadAndDelete(c.Chat().ID); loaded {
		if cancel, ok := cancelVal.(context.CancelFunc); ok {
			cancel()
		}
	}

	session, err := a.db.GetOrCreateSession(a.channelID, strconv.FormatInt(c.Chat().ID, 10), strconv.FormatInt(c.Sender().ID, 10))
	if err == nil && session != nil {
		_ = a.db.ClearSessionMessages(session.ID)
	}

	tools.ClearSudoSession(strconv.FormatInt(c.Chat().ID, 10))
	tools.ClearSudoSession(strconv.FormatInt(c.Sender().ID, 10))

	text := "✨ <b>SESI BARU DIMULAI</b>\n\n" +
		"Konteks percakapan dan riwayat pesan Anda telah direset.\n" +
		"Silakan ajukan pertanyaan atau perintah baru!"
	return c.Send(text, tele.ModeHTML)
}

func (a *BotAdapter) handleClearSudo(c tele.Context) error {
	tools.ClearSudoSession(strconv.FormatInt(c.Chat().ID, 10))
	tools.ClearSudoSession(strconv.FormatInt(c.Sender().ID, 10))
	return c.Send("🔒 <b>Sesi Sudo Dibersihkan</b>\n\nPassword sudo yang tersimpan di memori telah dihapus.", tele.ModeHTML)
}

func (a *BotAdapter) handleStop(c tele.Context) error {
	if cancelVal, loaded := a.activeTasks.LoadAndDelete(c.Chat().ID); loaded {
		if cancel, ok := cancelVal.(context.CancelFunc); ok {
			cancel()
		}
	}
	text := "🛑 <b>PROSES DIHENTIKAN</b>\n\n" +
		"Generasi respon AI untuk pesan terakhir Anda telah dihentikan."
	if c.Callback() != nil && c.Message() != nil {
		_, _ = a.bot.Edit(c.Message(), text, tele.ModeHTML)
		return nil
	}
	return c.Send(text, tele.ModeHTML)
}

func (a *BotAdapter) handleStatus(c tele.Context) error {
	session, _ := a.db.GetOrCreateSession(a.channelID, strconv.FormatInt(c.Chat().ID, 10), strconv.FormatInt(c.Sender().ID, 10))
	msgCount := 0
	if session != nil {
		msgCount, _ = a.db.CountSessionMessages(session.ID)
	}
	policy := a.db.GetResolvedPolicy(a.channelID, strconv.FormatInt(c.Chat().ID, 10))

	var sb strings.Builder
	sb.WriteString("🤖 <b>STATUS ASISTEN AI</b>\n\n")
	sb.WriteString(fmt.Sprintf("• Channel: <b>%s</b>\n", html.EscapeString(a.name)))
	sb.WriteString(fmt.Sprintf("• Chat ID: <code>%d</code>\n", c.Chat().ID))
	sb.WriteString(fmt.Sprintf("• User ID: <code>%d</code>\n", c.Sender().ID))
	if session != nil {
		sb.WriteString(fmt.Sprintf("• Sesi ID: <code>%s</code>\n", html.EscapeString(session.ID)))
		sb.WriteString(fmt.Sprintf("• Riwayat Aktif: <code>%d pesan</code> (Maks: <code>%d</code>)\n", msgCount, policy.MaxHistoryTurns))
	}
	sb.WriteString(fmt.Sprintf("• Mode Penghemat Token: <code>%s</code>\n\n", html.EscapeString(policy.TokenSaverMode)))
	sb.WriteString("💡 <b>Perintah Konteks:</b>\n")
	sb.WriteString("• <code>/retry</code> - Coba lagi respon yang gagal atau terhenti\n")
	sb.WriteString("• <code>/new</code> - Mulai percakapan baru & bersihkan konteks\n")
	sb.WriteString("• <code>/stop</code> - Hentikan generasi jawaban yang sedang berjalan")

	return c.Send(sb.String(), tele.ModeHTML)
}

func (a *BotAdapter) handleHelp(c tele.Context) error {
	text := "👋 <b>HALO! SAYA ASISTEN AI GOASSISTANT</b>\n\n" +
		"Silakan kirimkan pertanyaan atau permintaan Anda langsung di chat ini.\n\n" +
		"📌 <b>Daftar Perintah:</b>\n" +
		"• <code>/topic</code> - Kelola & ganti topik percakapan\n" +
		"• <code>/newtopic [nama]</code> - Mulai topik percakapan baru\n" +
		"• <code>/retry</code> - Coba lagi permintaan atau pesan terakhir\n" +
		"• <code>/new</code> - Mulai sesi baru & reset riwayat percakapan aktif\n" +
		"• <code>/stop</code> - Batalkan atau hentikan proses respon AI\n" +
		"• <code>/status</code> - Cek status percakapan dan konfigurasi sesi\n" +
		"• <code>/help</code> - Tampilkan panduan ini"
	return c.Send(text, tele.ModeHTML)
}

func (a *BotAdapter) renderTopicDashboard(chatID, userID string) (string, *tele.ReplyMarkup, error) {
	topics, err := a.db.ListChatSessions(a.channelID, chatID)
	if err != nil {
		return "", nil, err
	}

	if len(topics) == 0 {
		initSess, err := a.db.GetOrCreateSession(a.channelID, chatID, userID)
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
		_, _ = a.db.SwitchChatSession(a.channelID, chatID, activeTopic.ID)
	}

	activeMsgCount := 0
	if activeTopic != nil {
		activeMsgCount, _ = a.db.CountSessionMessages(activeTopic.ID)
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
		msgCount, _ := a.db.CountSessionMessages(t.ID)
		sb.WriteString(fmt.Sprintf("<b>#%d.</b> %s <b>%s</b> (<code>%d pesan</code>)%s\n",
			i+1, statusMarker, html.EscapeString(t.Title), msgCount, activeLabel))
	}

	sb.WriteString("\n💡 <i>Gunakan tombol di bawah untuk beralih atau membuat topik baru:</i>")

	menu := &tele.ReplyMarkup{}
	var allRows []tele.Row

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

	btnNew := menu.Data("➕ Topik Baru", "top_new")
	btnRefresh := menu.Data("🔄 Refresh", "top_refresh")
	allRows = append(allRows, menu.Row(btnNew, btnRefresh))

	menu.Inline(allRows...)
	return sb.String(), menu, nil
}

func (a *BotAdapter) handleTopic(c tele.Context) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)
	text, menu, err := a.renderTopicDashboard(chatID, userID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memuat daftar topik: %v", err))
	}
	if c.Callback() != nil && c.Message() != nil {
		_, _ = a.bot.Edit(c.Message(), text, tele.ModeHTML, menu)
		return nil
	}
	return c.Send(text, tele.ModeHTML, menu)
}

func (a *BotAdapter) handleNewTopic(c tele.Context) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)
	title := strings.TrimSpace(c.Message().Payload)
	if title == "" {
		topics, _ := a.db.ListChatSessions(a.channelID, chatID)
		title = fmt.Sprintf("Topik #%d (%s)", len(topics)+1, time.Now().Format("02/01 15:04"))
	}

	newTopic, err := a.db.CreateChatSession(a.channelID, chatID, userID, title, true)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membuat topik baru: %v", err))
	}

	text := fmt.Sprintf("✨ <b>TOPIK BARU DIMULAI: %s</b>\n\n"+
		"Riwayat percakapan topik sebelumnya tersimpan rapi.\n"+
		"Silakan ajukan pertanyaan atau perintah baru!",
		html.EscapeString(newTopic.Title))

	menu := &tele.ReplyMarkup{}
	btnList := menu.Data("🗂️ Lihat Daftar Topik", "top_refresh")
	menu.Inline(menu.Row(btnList))
	return c.Send(text, tele.ModeHTML, menu)
}

func (a *BotAdapter) handleSwitchTopic(c tele.Context) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	payload := strings.TrimSpace(c.Message().Payload)
	if payload == "" {
		return a.handleTopic(c)
	}

	topics, err := a.db.ListChatSessions(a.channelID, chatID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memuat topik: %v", err))
	}

	var targetTopic *storage.ChatSessionRecord
	if idx, err := strconv.Atoi(payload); err == nil && idx >= 1 && idx <= len(topics) {
		targetTopic = topics[idx-1]
	} else {
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

	switched, err := a.db.SwitchChatSession(a.channelID, chatID, targetTopic.ID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal beralih topik: %v", err))
	}

	msgCount, _ := a.db.CountSessionMessages(switched.ID)
	text := fmt.Sprintf("🔄 <b>BERALIH KE TOPIK: %s</b>\n\n"+
		"• Riwayat Topik Ini: <code>%d pesan</code>\n"+
		"• Status: 🟢 <b>Aktif</b>\n\n"+
		"Percakapan selanjutnya akan menggunakan konteks topik ini.",
		html.EscapeString(switched.Title), msgCount)

	menu := &tele.ReplyMarkup{}
	btnTopics := menu.Data("🗂️ Menu Topik", "top_refresh")
	menu.Inline(menu.Row(btnTopics))
	return c.Send(text, tele.ModeHTML, menu)
}

func (a *BotAdapter) handleRenameTopic(c tele.Context) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)
	newTitle := strings.TrimSpace(c.Message().Payload)
	if newTitle == "" {
		return c.Reply("⚠️ Format perintah: <code>/renametopic &lt;nama_baru&gt;</code>", tele.ModeHTML)
	}

	activeTopic, err := a.db.GetOrCreateSession(a.channelID, chatID, userID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mendapatkan topik aktif: %v", err))
	}

	if err := a.db.RenameChatSession(activeTopic.ID, newTitle); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mengubah nama topik: %v", err))
	}

	return c.Reply(fmt.Sprintf("✅ Judul topik aktif berhasil diubah menjadi: <b>%s</b>", html.EscapeString(newTitle)), tele.ModeHTML)
}

func (a *BotAdapter) handleDeleteTopic(c tele.Context) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	topics, err := a.db.ListChatSessions(a.channelID, chatID)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal memuat topik: %v", err))
	}
	if len(topics) <= 1 {
		return c.Reply("⚠️ Chat ini hanya memiliki 1 topik aktif. Gunakan <code>/new</code> jika ingin membersihkan riwayatnya.", tele.ModeHTML)
	}

	var sb strings.Builder
	sb.WriteString("🗑️ <b>PILIH TOPIK YANG AKAN DIHAPUS:</b>\n\n")

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
		btnDel := menu.Data(fmt.Sprintf("🗑️ %s", label), fmt.Sprintf("top_del_%s", t.ID))
		rows = append(rows, menu.Row(btnDel))
	}
	btnBack := menu.Data("⬅️ Kembali", "top_refresh")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.Send(sb.String(), tele.ModeHTML, menu)
}

func (a *BotAdapter) handleTopicCallback(c tele.Context, data string) error {
	chatID := strconv.FormatInt(c.Chat().ID, 10)
	userID := strconv.FormatInt(c.Sender().ID, 10)

	switch {
	case data == "top_refresh":
		_ = c.Respond(&tele.CallbackResponse{})
		return a.handleTopic(c)

	case strings.HasPrefix(data, "top_sw_"):
		targetID := strings.TrimPrefix(data, "top_sw_")
		switched, err := a.db.SwitchChatSession(a.channelID, chatID, targetID)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal beralih topik"})
			return err
		}
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🟢 Beralih ke: %s", switched.Title)})
		return a.handleTopic(c)

	case data == "top_new":
		topics, _ := a.db.ListChatSessions(a.channelID, chatID)
		newTitle := fmt.Sprintf("Topik #%d (%s)", len(topics)+1, time.Now().Format("02/01 15:04"))
		newTopic, err := a.db.CreateChatSession(a.channelID, chatID, userID, newTitle, true)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal membuat topik"})
			return err
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "✨ Topik baru dibuat & aktif!"})
		return c.Send(fmt.Sprintf("✨ <b>TOPIK BARU AKTIF: %s</b>\n\nRiwayat sebelumnya tersimpan rapi. Silakan kirim pesan baru!", html.EscapeString(newTopic.Title)), tele.ModeHTML)

	case strings.HasPrefix(data, "top_del_"):
		targetID := strings.TrimPrefix(data, "top_del_")
		nextActive, err := a.db.DeleteChatSession(a.channelID, chatID, targetID)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal menghapus topik"})
			return err
		}
		activeInfo := ""
		if nextActive != nil {
			activeInfo = fmt.Sprintf(" Topik aktif sekarang: %s", nextActive.Title)
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🗑️ Topik dihapus." + activeInfo})
		return a.handleTopic(c)
	}

	return nil
}

func sendOrEditResponse(c tele.Context, thinkingMsg *tele.Message, text string, mediaFiles []agent.MediaAttachment) error {
	if strings.TrimSpace(text) == "" {
		text = "(Tidak ada respon dari model)"
	}

	chunks := splitMessage(text, 4000)
	if len(chunks) > 0 {
		formattedFirst := tgformat.MarkdownToTelegramHTML(chunks[0])
		if thinkingMsg != nil {
			_, err := c.Bot().Edit(thinkingMsg, formattedFirst, tele.ModeHTML)
			if err != nil {
				// Fallback to plain text edit if HTML fails
				_, err = c.Bot().Edit(thinkingMsg, chunks[0])
				if err != nil {
					// Fallback to reply HTML, then plain text
					if err := c.Reply(formattedFirst, tele.ModeHTML); err != nil {
						_ = c.Reply(chunks[0])
					}
				}
			}
			for _, chunk := range chunks[1:] {
				formattedChunk := tgformat.MarkdownToTelegramHTML(chunk)
				if err := c.Reply(formattedChunk, tele.ModeHTML); err != nil {
					_ = c.Reply(chunk)
				}
			}
		} else {
			for _, chunk := range chunks {
				formattedChunk := tgformat.MarkdownToTelegramHTML(chunk)
				if err := c.Reply(formattedChunk, tele.ModeHTML); err != nil {
					_ = c.Reply(chunk)
				}
			}
		}
	}

	// Dispatch media attachments
	for _, mf := range mediaFiles {
		ext := strings.ToLower(filepath.Ext(mf.FilePath))
		isPhoto := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"

		if isPhoto {
			photo := &tele.Photo{
				File:    tele.FromDisk(mf.FilePath),
				Caption: mf.Caption,
			}
			_ = c.Send(photo)
		} else {
			doc := &tele.Document{
				File:     tele.FromDisk(mf.FilePath),
				Caption:  mf.Caption,
				FileName: filepath.Base(mf.FilePath),
			}
			_ = c.Send(doc)
		}
	}

	return nil
}

func splitMessage(text string, maxLen int) []string {
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		chunkSize := maxLen
		lastNL := strings.LastIndex(text[:chunkSize], "\n")
		if lastNL > maxLen/2 {
			chunkSize = lastNL
		}
		chunks = append(chunks, text[:chunkSize])
		text = strings.TrimPrefix(text[chunkSize:], "\n")
	}
	return chunks
}
