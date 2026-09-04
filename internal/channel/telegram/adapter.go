package telegram

import (
	"context"
	"fmt"
	"html"
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
	a.bot.Handle(tele.OnCallback, func(c tele.Context) error {
		if c.Callback() == nil {
			return nil
		}
		data := strings.TrimPrefix(c.Callback().Data, "\f")
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

		return a.executePrompt(c, msg, userPrompt, 0)
	})

	// Document / File handler
	a.bot.Handle(tele.OnDocument, func(c tele.Context) error {
		doc := c.Message().Document
		if doc == nil {
			return nil
		}

		fileMB := float64(doc.FileSize) / (1024 * 1024)
		caption := c.Message().Caption
		if caption == "" {
			caption = fmt.Sprintf("Analisis dokumen terlampir: %s", doc.FileName)
		}

		return a.executePrompt(c, c.Message(), caption, fileMB)
	})
}

func (a *BotAdapter) executePrompt(c tele.Context, replyTo *tele.Message, userPrompt string, fileMB float64) error {
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

	return a.executePrompt(c, c.Message(), lastPrompt, 0)
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
		"• <code>/retry</code> - Coba lagi permintaan atau pesan terakhir\n" +
		"• <code>/new</code> - Mulai sesi baru & reset riwayat percakapan\n" +
		"• <code>/stop</code> - Batalkan atau hentikan proses respon AI\n" +
		"• <code>/status</code> - Cek status percakapan dan konfigurasi sesi\n" +
		"• <code>/help</code> - Tampilkan panduan ini"
	return c.Send(text, tele.ModeHTML)
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
