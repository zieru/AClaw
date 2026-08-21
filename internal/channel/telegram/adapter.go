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
	"goassistant/internal/storage"
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
		{Text: "new", Description: "Mulai sesi percakapan baru (reset konteks)"},
		{Text: "reset", Description: "Reset riwayat percakapan"},
		{Text: "stop", Description: "Hentikan respon AI yang sedang diproses"},
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
	a.bot.Handle("/stop", a.handleStop)
	a.bot.Handle("/cancel", a.handleStop)
	a.bot.Handle(tele.OnCallback, func(c tele.Context) error {
		if c.Callback() == nil {
			return nil
		}
		if strings.HasPrefix(c.Callback().Data, "cancel_task") {
			_ = a.handleStop(c)
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

		_ = c.Notify(tele.Typing)
	cancelMenu := &tele.ReplyMarkup{}
	cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
	cancelMenu.Inline(cancelMenu.Row(cancelBtn))
	thinkingMsg, _ := a.bot.Reply(msg, "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML, cancelMenu)

		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(config.Get().Timeouts.HandlerSeconds)*time.Second)
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
			AttachedFileMB: 0,
			OnProgress: func(status string) {
				if thinkingMsg != nil {
					_, _ = a.bot.Edit(thinkingMsg, status, tele.ModeHTML)
				}
			},
		})

		if err != nil {
			friendlyErr := agent.FormatUserFriendlyError(err)
			if thinkingMsg != nil {
				_, _ = a.bot.Edit(thinkingMsg, friendlyErr, tele.ModeHTML)
				return nil
			}
			return c.Reply(friendlyErr, tele.ModeHTML)
		}

		return sendOrEditResponse(c, thinkingMsg, resp.Text, resp.MediaFiles)
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

		_ = c.Notify(tele.Typing)
		thinkingMsg, _ := a.bot.Reply(c.Message(), "📄 <i>Menganalisis dokumen & berpikir...</i>", tele.ModeHTML)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
			UserPrompt:     caption,
			AttachedFileMB: fileMB,
			OnProgress: func(status string) {
				if thinkingMsg != nil {
					_, _ = a.bot.Edit(thinkingMsg, status, tele.ModeHTML)
				}
			},
		})

		if err != nil {
			friendlyErr := agent.FormatUserFriendlyError(err)
			if thinkingMsg != nil {
				_, _ = a.bot.Edit(thinkingMsg, friendlyErr, tele.ModeHTML)
				return nil
			}
			return c.Reply(friendlyErr, tele.ModeHTML)
		}

		return sendOrEditResponse(c, thinkingMsg, resp.Text, resp.MediaFiles)
	})
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

	text := "✨ <b>SESI BARU DIMULAI</b>\n\n" +
		"Konteks percakapan dan riwayat pesan Anda telah direset.\n" +
		"Silakan ajukan pertanyaan atau perintah baru!"
	return c.Send(text, tele.ModeHTML)
}

func (a *BotAdapter) handleStop(c tele.Context) error {
	if cancelVal, loaded := a.activeTasks.LoadAndDelete(c.Chat().ID); loaded {
		if cancel, ok := cancelVal.(context.CancelFunc); ok {
			cancel()
		}
	}
	text := "🛑 <b>PROSES DIHENTIKAN</b>\n\n" +
		"Generasi respon AI untuk pesan terakhir Anda telah dihentikan."
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
	sb.WriteString("• <code>/new</code> - Mulai percakapan baru & bersihkan konteks\n")
	sb.WriteString("• <code>/stop</code> - Hentikan generasi jawaban yang sedang berjalan")

	return c.Send(sb.String(), tele.ModeHTML)
}

func (a *BotAdapter) handleHelp(c tele.Context) error {
	text := "👋 <b>HALO! SAYA ASISTEN AI GOASSISTANT</b>\n\n" +
		"Silakan kirimkan pertanyaan atau permintaan Anda langsung di chat ini.\n\n" +
		"📌 <b>Daftar Perintah:</b>\n" +
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
		if thinkingMsg != nil {
			_, err := c.Bot().Edit(thinkingMsg, chunks[0])
			if err != nil {
				// Fallback to reply if edit fails
				_ = c.Reply(chunks[0])
			}
			for _, chunk := range chunks[1:] {
				if err := c.Reply(chunk); err != nil {
					_ = err
				}
			}
		} else {
			for _, chunk := range chunks {
				if err := c.Reply(chunk); err != nil {
					_ = err
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
