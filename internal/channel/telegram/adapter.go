package telegram

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
	"goassistant/internal/tgformat"
	"goassistant/internal/tgprompt"
	"goassistant/internal/tools"
	"goassistant/internal/util"
	tele "gopkg.in/telebot.v3"
)

type BotAdapter struct {
	channelID      string
	name           string
	token          string
	bot            *tele.Bot
	orchestrator   *agent.Orchestrator
	db             *storage.DB
	promptManager  *tgprompt.PromptManager
	activeTasks    sync.Map
	pendingOptions sync.Map
	stopChan       chan struct{}
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
		channelID:     channelID,
		name:          name,
		token:         token,
		bot:           bot,
		orchestrator:  orch,
		db:            db,
		promptManager: tgprompt.NewPromptManager(bot),
		stopChan:      make(chan struct{}),
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
		{Text: "memory", Description: "Pengaturan & status memory engine AI"},
		{Text: "setsudo", Description: "Atur password sudo di memori aman (5 menit)"},
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
	a.bot.Handle("/setsudo", a.handleSetSudo)
	a.bot.Handle("/password", a.handleSetSudo)
	a.bot.Handle("/clearsudo", a.handleClearSudo)
	a.bot.Handle("/retry", a.handleRetry)
	a.bot.Handle("/stop", a.handleStop)
	a.bot.Handle("/cancel", a.handleStop)

	// Memory Engine Command
	a.bot.Handle("/memory", a.handleMemory)
	a.bot.Handle("/memories", a.handleMemory)

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
		if strings.HasPrefix(data, "mem_") {
			return a.handleMemoryCallback(c, data)
		}
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
		if strings.HasPrefix(data, "opt_") {
			return a.handleOptionCallback(c, data)
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

		// Intercept secure password prompt input before anything else (zero-leakage to AI)
		if handled, err := a.promptManager.HandleTextMessage(c); handled {
			return err
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
				optBytes, mime, _ := util.OptimizeImageBytes(imgBytes, util.DefaultMaxDimension, util.DefaultJPEGQuality)
				fileMB = float64(len(optBytes)) / (1024 * 1024)
				images = append(images, fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(optBytes)))
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
					optBytes, mime, _ := util.OptimizeImageBytes(docBytes, util.DefaultMaxDimension, util.DefaultJPEGQuality)
					images = append(images, fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(optBytes)))
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
	var getStreamThinking func() string

	if policy.StreamingEnabled {
		stopUpdater, onProgressStatus, onStreamChunk, getStreamThinking = createProgressiveThinkingManager(a.bot, thinkingMsg, "🤔 <i>Sedang berpikir...</i>")
	} else {
		stopUpdater, onProgressStatus, _, getStreamThinking = createProgressiveThinkingManager(a.bot, thinkingMsg, "🤔 <i>Sedang memproses respon...</i>")
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

	prompter := a.promptManager.ForUser(c.Chat().ID, c.Sender().ID)
	ctx = tools.WithPasswordPrompter(ctx, prompter)

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
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			text := "🛑 <b>PROSES DIBATALKAN</b>\n\nRespon AI berhasil dihentikan atas permintaan pengguna."
			if thinkingMsg != nil {
				_, _ = a.bot.Edit(thinkingMsg, text, tele.ModeHTML)
				return nil
			}
			return c.Reply(text, tele.ModeHTML)
		}

		friendlyErr := tgformat.MarkdownToTelegramHTML(agent.FormatUserFriendlyError(err))
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

	finalText := resp.Text
	streamThink := ""
	if getStreamThinking != nil {
		streamThink = strings.TrimSpace(getStreamThinking())
	}
	if streamThink == "" && resp != nil {
		streamThink = strings.TrimSpace(resp.ThinkingContent)
	}
	// Pastikan hasil stream thinking tidak dihapus dari tampilan pesan Telegram dan tidak dibungkus dalam code
	if streamThink != "" && !strings.Contains(finalText, "Proses Berpikir") {
		cleanThink := tgformat.CleanThinkingForTelegram(streamThink)
		finalText = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s</blockquote>\n\n%s", cleanThink, finalText)
	}

	return a.sendOrEditResponse(c, thinkingMsg, finalText, resp.MediaFiles)
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

func createProgressiveThinkingManager(bot *tele.Bot, targetMsg *tele.Message, initialPrefix string) (stopFunc func(), updateStatus func(string), onChunk func(chunk provider.StreamChunk), getThinking func() string) {
	if targetMsg == nil {
		return func() {}, func(string) {}, func(provider.StreamChunk) {}, func() string { return "" }
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
	var floodWaitUntil time.Time

	updateStatus = func(status string) {
		mu.Lock()
		customStatus = status
		mu.Unlock()
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
		// Minimum 3 seconds throttle to avoid Telegram rate limits
		ticker := time.NewTicker(3 * time.Second)
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
				if time.Now().Before(floodWaitUntil) {
					mu.Unlock()
					continue
				}

				elapsedSec := int(time.Since(startTime).Seconds())
				curThinking := strings.TrimSpace(thinkingBuf.String())
				curContent := strings.TrimSpace(contentBuf.String())
				status := strings.TrimSpace(customStatus)

				var text string
				if curContent != "" {
					// Final answer is actively streaming
					if curThinking != "" {
						previewThink := tgformat.CleanThinkingForTelegram(curThinking)
						if len(previewThink) > 1200 {
							previewThink = previewThink[:1200] + "..."
						}
						previewContent := curContent
						if len(previewContent) > 2200 {
							previewContent = previewContent[len(previewContent)-2200:]
						}
						formattedContent := tgformat.MarkdownToTelegramHTML(previewContent)
						text = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s</blockquote>\n\n%s ▌", html.EscapeString(previewThink), formattedContent)
					} else {
						previewContent := curContent
						if len(previewContent) > 3800 {
							previewContent = previewContent[len(previewContent)-3800:]
						}
						text = tgformat.MarkdownToTelegramHTML(previewContent) + " ▌"
					}
				} else if status != "" {
					// Tool execution / planning / checklist in progress
					if curThinking != "" {
						previewThink := tgformat.CleanThinkingForTelegram(curThinking)
						if len(previewThink) > 1000 {
							previewThink = previewThink[:1000] + "..."
						}
						text = fmt.Sprintf("%s\n\n💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s</blockquote>\n\n⏱️ <i>(%dd)</i>", status, html.EscapeString(previewThink), elapsedSec)
					} else {
						text = fmt.Sprintf("%s\n\n⏱️ <i>(%dd)</i>", status, elapsedSec)
					}
				} else if curThinking != "" {
					// Only thinking so far
					previewThink := tgformat.CleanThinkingForTelegram(curThinking)
					if len(previewThink) > 3500 {
						previewThink = previewThink[len(previewThink)-3500:]
					}
					text = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s ▌</blockquote>\n\n⏱️ <i>(%dd)</i>", html.EscapeString(previewThink), elapsedSec)
				} else {
					text = fmt.Sprintf("💭 <i>Sedang berpikir... (%dd)</i>", elapsedSec)
				}

				if len(text) > 4000 {
					text = text[:3990] + "..."
				}

				if text == lastSentText {
					mu.Unlock()
					continue
				}
				lastSentText = text
				mu.Unlock()

				if targetMsg != nil {
					_, err := bot.Edit(targetMsg, text, tele.ModeHTML, cancelMenu)
					if err != nil {
						errStr := strings.ToLower(err.Error())
						if strings.Contains(errStr, "flood") || strings.Contains(errStr, "429") {
							mu.Lock()
							floodWaitUntil = time.Now().Add(5 * time.Second)
							mu.Unlock()
						} else {
							// If HTML parsing failed on partial stream chunk, retry edit as plain text
							cleanText := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
							_, _ = bot.Edit(targetMsg, cleanText, cancelMenu)
						}
					}
				}
			}
		}
	}()

	getThinking = func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.TrimSpace(thinkingBuf.String())
	}

	return stopFunc, updateStatus, onChunk, getThinking
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

func (a *BotAdapter) handleSetSudo(c tele.Context) error {
	msg := c.Message()
	payload := ""
	if msg != nil {
		payload = strings.TrimSpace(msg.Payload)
	}
	chatIDStr := strconv.FormatInt(c.Chat().ID, 10)
	userIDStr := strconv.FormatInt(c.Sender().ID, 10)

	// If password is provided directly via command payload: e.g. /setsudo <password>
	if payload != "" {
		_ = a.bot.Delete(msg) // Delete user message immediately for security
		tools.SetSudoSession(chatIDStr, payload)
		tools.SetSudoSession(userIDStr, payload)
		return c.Send("✅ <b>Password Sudo Disimpan</b>\n\nPassword sudo berhasil disimpan di memori aman (aktif selama 5 menit). Pesan Anda telah dihapus demi keamanan.", tele.ModeHTML)
	}

	// Interactive password prompt
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		pass, finish, err := a.promptManager.PromptPassword(ctx, c.Chat().ID, c.Sender().ID, "Atur Password Sudo Server", "Password akan disimpan di memori aman selama 5 menit untuk eksekusi perintah administratif.")
		if err != nil {
			return
		}
		if finish != nil {
			defer finish()
		}
		if pass != "" {
			tools.SetSudoSession(chatIDStr, pass)
			tools.SetSudoSession(userIDStr, pass)
			_, _ = a.bot.Send(c.Chat(), "✅ <b>Password Sudo Disimpan</b>\n\nPassword sudo berhasil disimpan di memori aman (aktif selama 5 menit). Anda kini dapat meminta AI menjalankan perintah administratif tanpa perlu memasukkan password lagi.", tele.ModeHTML)
		}
	}()
	return nil
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

func (a *BotAdapter) handleMemory(c tele.Context) error {
	if a.orchestrator == nil || a.orchestrator.MemoryManager() == nil {
		return c.Send("⚠️ <b>Engine Memory GoAssistant belum aktif.</b>\nPastikan <code>memory.enabled: true</code> di konfigurasi.", tele.ModeHTML)
	}
	memMgr := a.orchestrator.MemoryManager()
	payload := ""
	if c.Message() != nil {
		payload = strings.TrimSpace(c.Message().Payload)
	}

	parts := strings.Fields(payload)
	if len(parts) == 0 || parts[0] == "status" || parts[0] == "dashboard" {
		text, menu := a.renderMemoryDashboard(strconv.FormatInt(c.Sender().ID, 10))
		return c.Send(text, menu, tele.ModeHTML)
	}

	subCmd := strings.ToLower(parts[0])
	switch subCmd {
	case "enable", "on":
		memMgr.SetEnabled(true)
		return c.Send("✅ <b>Engine Memory GoAssistant diaktifkan.</b>", tele.ModeHTML)

	case "disable", "off":
		memMgr.SetEnabled(false)
		return c.Send("🛑 <b>Engine Memory GoAssistant dinonaktifkan.</b>", tele.ModeHTML)

	case "autoextract", "extract":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			st := "Nonaktif"
			if cfg.AutoExtract {
				st = "Aktif"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Auto-Extract Percakapan:</b> <code>%s</code>\n\nGunakan: <code>/memory autoextract on</code> atau <code>/memory autoextract off</code>", st), tele.ModeHTML)
		}
		arg := strings.ToLower(parts[1])
		if arg == "on" || arg == "true" || arg == "1" {
			memMgr.SetAutoExtract(true)
			return c.Send("✅ <b>Auto-Extract percakapan diaktifkan.</b> Bot akan secara otomatis mengekstrak fakta penting dari percakapan.", tele.ModeHTML)
		} else if arg == "off" || arg == "false" || arg == "0" {
			memMgr.SetAutoExtract(false)
			return c.Send("🛑 <b>Auto-Extract percakapan dinonaktifkan.</b>", tele.ModeHTML)
		}
		return c.Send("⚠️ Gunakan <code>/memory autoextract on</code> atau <code>/memory autoextract off</code>", tele.ModeHTML)

	case "strategy", "strat":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Strategi Memory Saat Ini:</b> <code>%s</code>\n\nPilihan: <code>recent</code>, <code>semantic</code>, <code>hybrid</code>\nUbah dengan: <code>/memory strategy &lt;pilihan&gt;</code>", memMgr.GetStrategy()), tele.ModeHTML)
		}
		newStrat := strings.ToLower(parts[1])
		if newStrat != "recent" && newStrat != "semantic" && newStrat != "hybrid" {
			return c.Send("⚠️ Pilihan strategi tidak valid. Gunakan salah satu:\n• <code>recent</code> (kronologis waktu)\n• <code>semantic</code> (vektor makna)\n• <code>hybrid</code> (BM25 + Vektor + Recency)", tele.ModeHTML)
		}
		memMgr.SetStrategy(newStrat)
		return c.Send(fmt.Sprintf("✅ <b>Strategi memory engine berhasil diubah ke:</b> <code>%s</code>", newStrat), tele.ModeHTML)

	case "model", "embedding_model", "setmodel":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			currModel := emb.Model
			if currModel == "" {
				currModel = "(belum disetel)"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Model Embedding Saat Ini:</b> <code>%s</code> (Provider: <code>%s</code>)\n\n"+
				"Ubah dengan: <code>/memory model &lt;nama_model&gt;</code>\n\n"+
				"Contoh populer:\n"+
				"• <code>/memory model text-embedding-3-small</code> (OpenAI)\n"+
				"• <code>/memory model text-embedding-004</code> (Gemini)\n"+
				"• <code>/memory model nomic-embed-text</code> (Ollama)\n"+
				"• <code>/memory model mxbai-embed-large</code> (Ollama)",
				html.EscapeString(currModel), html.EscapeString(emb.Provider)), tele.ModeHTML)
		}
		newModel := strings.TrimSpace(parts[1])
		memMgr.SetEmbeddingModel(newModel)
		return c.Send(fmt.Sprintf("✅ <b>Model embedding berhasil diubah ke:</b> <code>%s</code>\nStatus Remote Embedding kini <b>Aktif</b>.", html.EscapeString(newModel)), tele.ModeHTML)

	case "provider", "prov":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Provider Embedding Saat Ini:</b> <code>%s</code>\n\nPilihan: <code>openai</code>, <code>gemini</code>, <code>ollama</code>, <code>custom</code>\nUbah dengan: <code>/memory provider &lt;nama_provider&gt;</code>", html.EscapeString(emb.Provider)), tele.ModeHTML)
		}
		newProv := strings.ToLower(strings.TrimSpace(parts[1]))
		if newProv != "openai" && newProv != "gemini" && newProv != "ollama" && newProv != "custom" {
			return c.Send("⚠️ Provider tidak valid. Gunakan: <code>openai</code>, <code>gemini</code>, <code>ollama</code>, atau <code>custom</code>.", tele.ModeHTML)
		}
		memMgr.SetEmbeddingProvider(newProv)
		return c.Send(fmt.Sprintf("✅ <b>Provider embedding berhasil diubah ke:</b> <code>%s</code>", html.EscapeString(newProv)), tele.ModeHTML)

	case "embed", "embedding":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			st := "Nonaktif"
			if emb.Enabled {
				st = "Aktif"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Status Vector Embedding:</b> <code>%s</code>\n\nGunakan: <code>/memory embed on</code> atau <code>/memory embed off</code>", st), tele.ModeHTML)
		}
		toggle := strings.ToLower(parts[1])
		if toggle == "on" || toggle == "enable" || toggle == "1" || toggle == "true" {
			memMgr.SetEmbeddingEnabled(true)
			return c.Send("✅ <b>Vector Embedding berhasil diaktifkan.</b>", tele.ModeHTML)
		} else if toggle == "off" || toggle == "disable" || toggle == "0" || toggle == "false" {
			memMgr.SetEmbeddingEnabled(false)
			return c.Send("✅ <b>Vector Embedding dinonaktifkan</b> (Fallback ke FTS5 BM25).", tele.ModeHTML)
		} else {
			return c.Send("⚠️ Gunakan <code>/memory embed on</code> atau <code>/memory embed off</code>", tele.ModeHTML)
		}

	case "baseurl", "url":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Embedding Base URL Saat Ini:</b> <code>%s</code>\n\nUbah dengan: <code>/memory baseurl &lt;url&gt;</code>\nContoh untuk Ollama: <code>/memory baseurl http://localhost:11434/v1</code>", html.EscapeString(emb.BaseURL)), tele.ModeHTML)
		}
		newURL := strings.TrimSpace(parts[1])
		if newURL == "default" || newURL == "none" || newURL == "-" {
			newURL = ""
		}
		memMgr.SetEmbeddingBaseURL(newURL)
		return c.Send(fmt.Sprintf("✅ <b>Base URL embedding berhasil diatur ke:</b> <code>%s</code>", html.EscapeString(newURL)), tele.ModeHTML)

	case "key", "apikey":
		if len(parts) < 2 {
			return c.Send("Ubah API key embedding dengan: <code>/memory key &lt;api_key&gt;</code>", tele.ModeHTML)
		}
		newKey := strings.TrimSpace(parts[1])
		memMgr.SetEmbeddingAPIKey(newKey)
		return c.Send("✅ <b>API Key untuk embedding berhasil diperbarui.</b>", tele.ModeHTML)

	case "dimensions", "dims":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Dimensi Vektor Saat Ini:</b> <code>%d</code>\n\nUbah dengan: <code>/memory dimensions &lt;jumlah&gt;</code> (contoh: <code>/memory dimensions 1536</code>)", cfg.Embedding.Dimensions), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 64 || val > 16384 {
			return c.Send("⚠️ Nilai dimensi vektor harus antara 64 sampai 16384.", tele.ModeHTML)
		}
		memMgr.SetEmbeddingDimensions(val)
		return c.Send(fmt.Sprintf("✅ <b>Dimensi vektor embedding berhasil diatur ke:</b> <code>%d</code>", val), tele.ModeHTML)

	case "similarity", "threshold", "sim":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Ambang Batas Similarity Saat Ini:</b> <code>%.2f</code>\n\nUbah dengan: <code>/memory similarity &lt;0.10 - 1.00&gt;</code> (contoh: <code>/memory similarity 0.60</code>)", memMgr.GetSimilarityThreshold()), tele.ModeHTML)
		}
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || val <= 0.0 || val > 1.0 {
			return c.Send("⚠️ Nilai similarity threshold harus berupa desimal antara 0.01 sampai 1.00 (misal: 0.60).", tele.ModeHTML)
		}
		memMgr.SetSimilarityThreshold(val)
		return c.Send(fmt.Sprintf("✅ <b>Similarity threshold berhasil diatur ke:</b> <code>%.2f</code>", val), tele.ModeHTML)

	case "tokens", "maxtokens", "token":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Anggaran Token Saat Ini:</b> <code>%d tokens</code>\n\nUbah dengan: <code>/memory tokens &lt;jumlah&gt;</code> (contoh: <code>/memory tokens 2000</code>)", memMgr.GetMaxTokens()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 100 || val > 32000 {
			return c.Send("⚠️ Nilai token harus berupa angka antara 100 dan 32000.", tele.ModeHTML)
		}
		memMgr.SetMaxTokens(val)
		return c.Send(fmt.Sprintf("✅ <b>Anggaran token injeksi memori berhasil diatur ke:</b> <code>%d tokens</code>", val), tele.ModeHTML)

	case "maxitems", "items":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Batas Maksimal Item Memori Saat Ini:</b> <code>%d item</code>\n\nUbah dengan: <code>/memory maxitems &lt;jumlah&gt;</code> (contoh: <code>/memory maxitems 20</code>)", memMgr.GetMaxContextItems()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 100 {
			return c.Send("⚠️ Nilai max items harus antara 1 sampai 100.", tele.ModeHTML)
		}
		memMgr.SetMaxContextItems(val)
		return c.Send(fmt.Sprintf("✅ <b>Batas item memori injeksi prompt berhasil diatur ke:</b> <code>%d item</code>", val), tele.ModeHTML)

	case "retention", "retention_days", "days":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Masa Retensi Saat Ini:</b> <code>%d hari</code>\n\nUbah dengan: <code>/memory retention &lt;hari&gt;</code> (contoh: <code>/memory retention 30</code>)", memMgr.GetRetentionDays()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 365 {
			return c.Send("⚠️ Nilai retensi harus antara 1 sampai 365 hari.", tele.ModeHTML)
		}
		memMgr.SetRetentionDays(val)
		return c.Send(fmt.Sprintf("✅ <b>Masa retensi memori berhasil diatur ke:</b> <code>%d hari</code>", val), tele.ModeHTML)

	case "promotion", "promo", "promotion_threshold":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Ambang Promosi Saat Ini:</b> <code>≥ %dx akses</code>\n\nUbah dengan: <code>/memory promotion &lt;kali&gt;</code> (contoh: <code>/memory promotion 3</code>)", memMgr.GetPromotionThreshold()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 50 {
			return c.Send("⚠️ Nilai ambang promosi harus antara 1 sampai 50.", tele.ModeHTML)
		}
		memMgr.SetPromotionThreshold(val)
		return c.Send(fmt.Sprintf("✅ <b>Ambang promosi memori berhasil diatur ke:</b> <code>≥ %dx akses</code>", val), tele.ModeHTML)

	case "compaction", "autocompact":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			st := "Nonaktif"
			if cfg.AutoCompaction {
				st = "Aktif"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Auto-Compaction:</b> <code>%s</code>\n\nGunakan: <code>/memory compaction on</code> atau <code>/memory compaction off</code>", st), tele.ModeHTML)
		}
		arg := strings.ToLower(parts[1])
		if arg == "on" || arg == "true" || arg == "1" {
			memMgr.SetAutoCompaction(true)
			return c.Send("✅ <b>Auto-Compaction diaktifkan.</b>", tele.ModeHTML)
		} else if arg == "off" || arg == "false" || arg == "0" {
			memMgr.SetAutoCompaction(false)
			return c.Send("🛑 <b>Auto-Compaction dinonaktifkan.</b>", tele.ModeHTML)
		}
		return c.Send("⚠️ Gunakan <code>/memory compaction on</code> atau <code>/memory compaction off</code>", tele.ModeHTML)

	case "compactinterval", "interval":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Interval Compaction Saat Ini:</b> <code>%d jam</code>\n\nUbah dengan: <code>/memory compactinterval &lt;jam&gt;</code> (contoh: <code>/memory compactinterval 24</code>)", cfg.CompactionIntervalHours), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 720 {
			return c.Send("⚠️ Nilai interval harus antara 1 sampai 720 jam.", tele.ModeHTML)
		}
		memMgr.SetCompactionIntervalHours(val)
		return c.Send(fmt.Sprintf("✅ <b>Interval auto-compaction berhasil diatur ke:</b> <code>%d jam</code>", val), tele.ModeHTML)

	case "compactthreshold", "cthreshold":
		if len(parts) < 2 {
			cfg := memMgr.GetConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Ambang Pemicu Compaction Saat Ini:</b> <code>%d item</code>\n\nUbah dengan: <code>/memory compactthreshold &lt;jumlah&gt;</code> (contoh: <code>/memory compactthreshold 100</code>)", cfg.CompactionThreshold), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 10 || val > 10000 {
			return c.Send("⚠️ Nilai ambang pemicu harus antara 10 sampai 10000 item.", tele.ModeHTML)
		}
		memMgr.SetCompactionThreshold(val)
		return c.Send(fmt.Sprintf("✅ <b>Ambang pemicu auto-compaction berhasil diatur ke:</b> <code>%d item</code>", val), tele.ModeHTML)

	case "compact", "prune":
		userID := strconv.FormatInt(c.Sender().ID, 10)
		rep, err := memMgr.Compact(context.Background(), "user", userID)
		if err != nil {
			return c.Send(fmt.Sprintf("❌ Gagal menjalankan compaction: %v", err), tele.ModeHTML)
		}
		msg := fmt.Sprintf("🧹 <b>HASIL AUTO-COMPACTION MEMORI</b>\n\n"+
			"• <b>Memori Usang Dihapus:</b> <code>%d catatan</code>\n"+
			"• <b>Indeks SQLite FTS5:</b> <code>Dioptimalkan (OK)</code>\n"+
			"• <b>Total Memori Aktif Anda:</b> <code>%d catatan</code>\n\n"+
			"Database memori Anda kini bersih dan optimal!", rep.PrunedExpired, rep.TotalActive)
		return c.Send(msg, tele.ModeHTML)

	case "list":
		userID := strconv.FormatInt(c.Sender().ID, 10)
		items, err := a.db.ListMemoriesByScope("user", userID, "", 15)
		if err != nil || len(items) == 0 {
			return c.Send("📭 <i>Belum ada catatan memori yang tersimpan untuk akun Anda.</i>", tele.ModeHTML)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 <b>DAFTAR MEMORI ANDA (%d item terbaru):</b>\n\n", len(items)))
		for i, it := range items {
			promTag := ""
			if it.IsPromoted {
				promTag = " ⭐️[PERMANEN]"
			}
			contentExcerpt := it.Content
			if len([]rune(contentExcerpt)) > 70 {
				contentExcerpt = string([]rune(contentExcerpt)[:70]) + "..."
			}
			sb.WriteString(fmt.Sprintf("<b>%d.</b> <code>[%s]</code>%s\n   %s\n\n",
				i+1, html.EscapeString(it.Key), promTag, html.EscapeString(contentExcerpt)))
		}
		return c.Send(sb.String(), tele.ModeHTML)

	case "search":
		if len(parts) < 2 {
			return c.Send("Gunakan format: <code>/memory search &lt;kata_kunci&gt;</code>", tele.ModeHTML)
		}
		query := strings.Join(parts[1:], " ")
		userID := strconv.FormatInt(c.Sender().ID, 10)
		results, err := memMgr.SearchMemoriesAdvanced("user", userID, query, "", 5)
		if err != nil || len(results) == 0 {
			return c.Send(fmt.Sprintf("🔍 <i>Tidak ditemukan memori yang cocok dengan: %s</i>", html.EscapeString(query)), tele.ModeHTML)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 <b>HASIL PENCARIAN MEMORI (%d ditemukan):</b>\n\n", len(results)))
		for i, it := range results {
			sb.WriteString(fmt.Sprintf("<b>%d.</b> <code>[%s]</code> (Score: <code>%.2f</code>)\n   %s\n\n",
				i+1, html.EscapeString(it.Key), it.Score, html.EscapeString(it.Content)))
		}
		return c.Send(sb.String(), tele.ModeHTML)

	case "clear":
		userID := strconv.FormatInt(c.Sender().ID, 10)
		if err := memMgr.ClearUserMemory(userID); err != nil {
			return c.Send(fmt.Sprintf("❌ Gagal menghapus memori: %v", err), tele.ModeHTML)
		}
		return c.Send("🗑️ <b>Semua catatan memori pribadi Anda berhasil dihapus.</b>", tele.ModeHTML)

	case "seed", "import":
		csvPath := ""
		if len(parts) > 1 {
			csvPath = strings.Join(parts[1:], " ")
		}
		userID := strconv.FormatInt(c.Sender().ID, 10)
		res, err := memMgr.ImportSeedCSV(context.Background(), csvPath, userID)
		if err != nil {
			return c.Send(fmt.Sprintf("❌ <b>Gagal mengimpor seed CSV:</b> %v", err), tele.ModeHTML)
		}
		return c.Send(fmt.Sprintf("📥 <b>HASIL IMPOR SEED CSV MEMORY</b>\n\n"+
			"• <b>Total Diproses:</b> <code>%d item</code>\n"+
			"• <b>Berhasil Diimpor:</b> <code>%d item</code>\n"+
			"• <b>Error / Dilewati:</b> <code>%d</code>\n\n"+
			"Catatan memori kini telah siap digunakan dalam pencarian!",
			res.TotalProcessed, res.Inserted, len(res.Errors)), tele.ModeHTML)

	case "reset":
		memMgr.ResetToDefaults()
		return c.Send("🔄 <b>Seluruh pengaturan memory engine telah di-reset ke nilai default pabrik.</b>", tele.ModeHTML)

	default:
		return c.Send("Perintah tidak dikenal. Ketik <code>/memory</code> untuk membuka dashboard lengkap.", tele.ModeHTML)
	}
}

// renderMemoryDashboard renders the top-level Hub dashboard
func (a *BotAdapter) renderMemoryDashboard(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	cfg := memMgr.GetConfig()
	activeStrategy := memMgr.GetStrategy()
	maxTokens := memMgr.GetMaxTokens()
	maxItems := memMgr.GetMaxContextItems()
	retentionDays := memMgr.GetRetentionDays()
	promotionThreshold := memMgr.GetPromotionThreshold()
	activeCount, _ := a.db.CountMemories("user", userID)

	engineStatus := "🟢 Aktif"
	if !cfg.Enabled {
		engineStatus = "🔴 Nonaktif"
	}
	extractStatus := "🟢 Aktif"
	if !cfg.AutoExtract {
		extractStatus = "🔴 Nonaktif"
	}

	embCfg := cfg.GetEmbeddingConfig()
	embStatus := "🔴 Nonaktif (BM25 FTS5 Cepat)"
	if embCfg.Enabled && embCfg.Model != "" {
		embStatus = fmt.Sprintf("🟢 Aktif (<code>%s</code> - <code>%s</code>)", html.EscapeString(embCfg.Provider), html.EscapeString(embCfg.Model))
	}

	stratDesc := "Hybrid (BM25 + Vektor + Recency)"
	if activeStrategy == "recent" {
		stratDesc = "Recent (Kronologis Waktu Terbaru)"
	} else if activeStrategy == "semantic" {
		stratDesc = "Semantic (Vektor Kemiripan Makna)"
	}

	var sb strings.Builder
	sb.WriteString("🧠 <b>PUSAT KONTROL & PENGATURAN MEMORY ENGINE</b>\n\n")
	sb.WriteString(fmt.Sprintf("• <b>Status Engine:</b> %s | <b>Auto-Extract:</b> %s\n", engineStatus, extractStatus))
	sb.WriteString(fmt.Sprintf("• <b>Strategi Aktif:</b> <code>%s</code> (%s)\n", activeStrategy, stratDesc))
	sb.WriteString(fmt.Sprintf("• <b>Anggaran Token Prompt:</b> <code>%d tokens</code> (Maks: <code>%d item</code>)\n", maxTokens, maxItems))
	sb.WriteString(fmt.Sprintf("• <b>Masa Retensi:</b> <code>%d hari</code> (Auto-promosi &ge; <code>%dx</code> pakai)\n", retentionDays, promotionThreshold))
	sb.WriteString(fmt.Sprintf("• <b>Auto-Compaction:</b> <code>Aktif</code> (Tiap %d jam, ambang: %d)\n", cfg.CompactionIntervalHours, cfg.CompactionThreshold))
	sb.WriteString(fmt.Sprintf("• <b>Similarity Threshold:</b> <code>%.2f</code>\n", cfg.SimilarityThreshold))
	sb.WriteString(fmt.Sprintf("• <b>Remote Embedding:</b> %s\n", embStatus))
	sb.WriteString(fmt.Sprintf("• <b>Memori Pribadi Anda:</b> <code>%d catatan</code>\n\n", activeCount))
	sb.WriteString("⚙️ <i>Pilih sub-menu di bawah untuk mengatur parameter secara detail:</i>")

	menu := &tele.ReplyMarkup{}
	btnGeneral := menu.Data("⚙️ Umum & Ekstrak", "mem_menu_general")
	btnStrategy := menu.Data("🎯 Strategi & Token", "mem_menu_strategy")
	btnRetention := menu.Data("⏳ Retensi & Promosi", "mem_menu_retention")
	btnCompaction := menu.Data("🧹 Auto-Compaction", "mem_menu_compaction")
	btnEmbedding := menu.Data("🔌 Remote Embedding", "mem_menu_embedding")
	btnData := menu.Data("📋 Kelola Catatan", "mem_menu_data")
	btnRefresh := menu.Data("🔄 Segarkan Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnGeneral, btnStrategy),
		menu.Row(btnRetention, btnCompaction),
		menu.Row(btnEmbedding, btnData),
		menu.Row(btnRefresh),
	)

	return sb.String(), menu
}

// renderMemoryGeneralMenu renders General & Auto-Extract sub-menu
func (a *BotAdapter) renderMemoryGeneralMenu(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	cfg := memMgr.GetConfig()

	engTag := "🟢 Engine: AKTIF"
	if !cfg.Enabled {
		engTag = "🔴 Engine: NONAKTIF"
	}
	extTag := "🟢 Auto-Extract: AKTIF"
	if !cfg.AutoExtract {
		extTag = "🔴 Auto-Extract: NONAKTIF"
	}

	text := "⚙️ <b>PENGATURAN UMUM & EKSTRAKSI MEMORY</b>\n\n" +
		"• <b>Master Engine:</b> " + engTag + "\n" +
		"  <i>Mengaktifkan penyimpanan & injeksi konteks memori ke sistem prompt.</i>\n\n" +
		"• <b>Auto-Extract:</b> " + extTag + "\n" +
		"  <i>Mengekstrak otomatis fakta profil, SOP, dan konsep dari chat secara background.</i>\n\n" +
		"• <b>Seed CSV Path:</b> <code>" + html.EscapeString(cfg.SeedCSVPath) + "</code>\n\n" +
		"<i>Klik tombol di bawah untuk toggle atau reset:</i>"

	menu := &tele.ReplyMarkup{}
	btnToggleEng := menu.Data(engTag, "mem_toggle_engine")
	btnToggleExt := menu.Data(extTag, "mem_toggle_autoextract")
	btnReset := menu.Data("🔄 Reset ke Default", "mem_reset_defaults")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnToggleEng),
		menu.Row(btnToggleExt),
		menu.Row(btnReset),
		menu.Row(btnBack),
	)

	return text, menu
}

// renderMemoryStrategyMenu renders Retrieval Strategy & Token Budget sub-menu
func (a *BotAdapter) renderMemoryStrategyMenu(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	activeStrat := memMgr.GetStrategy()
	maxTokens := memMgr.GetMaxTokens()
	maxItems := memMgr.GetMaxContextItems()

	text := fmt.Sprintf("🎯 <b>PENGATURAN STRATEGI & BUDGET TOKEN</b>\n\n"+
		"• <b>Strategi Saat Ini:</b> <code>%s</code>\n"+
		"  - <b>Hybrid:</b> FTS5 BM25 + Semantic Vector + Recency weighting (Direkomendasikan)\n"+
		"  - <b>Recent:</b> Urutkan berdasarkan waktu pembaruan terbaru\n"+
		"  - <b>Semantic:</b> Berdasarkan kemiripan vektor makna (Cosine Similarity)\n\n"+
		"• <b>Anggaran Token Prompt:</b> <code>%d tokens</code>\n"+
		"• <b>Batas Maksimal Item:</b> <code>%d item</code>\n\n"+
		"<i>Pilih strategi atau preset anggaran token / batas item:</i>",
		activeStrat, maxTokens, maxItems)

	menu := &tele.ReplyMarkup{}
	btnHybrid := menu.Data("🔘 Hybrid", "mem_strat_hybrid")
	if activeStrat == "hybrid" {
		btnHybrid = menu.Data("✅ Hybrid", "mem_strat_hybrid")
	}
	btnRecent := menu.Data("⚡ Recent", "mem_strat_recent")
	if activeStrat == "recent" {
		btnRecent = menu.Data("✅ Recent", "mem_strat_recent")
	}
	btnSemantic := menu.Data("🧠 Semantic", "mem_strat_semantic")
	if activeStrat == "semantic" {
		btnSemantic = menu.Data("✅ Semantic", "mem_strat_semantic")
	}

	btnTok500 := menu.Data(fmt.Sprintf("%s 500 Tok", checkmark(maxTokens == 500)), "mem_tok_500")
	btnTok1000 := menu.Data(fmt.Sprintf("%s 1000 Tok", checkmark(maxTokens == 1000)), "mem_tok_1000")
	btnTok2000 := menu.Data(fmt.Sprintf("%s 2000 Tok", checkmark(maxTokens == 2000)), "mem_tok_2000")
	btnTok4000 := menu.Data(fmt.Sprintf("%s 4000 Tok", checkmark(maxTokens == 4000)), "mem_tok_4000")

	btnItem10 := menu.Data(fmt.Sprintf("%s 10 Item", checkmark(maxItems == 10)), "mem_item_10")
	btnItem20 := menu.Data(fmt.Sprintf("%s 20 Item", checkmark(maxItems == 20)), "mem_item_20")
	btnItem50 := menu.Data(fmt.Sprintf("%s 50 Item", checkmark(maxItems == 50)), "mem_item_50")

	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnHybrid, btnRecent, btnSemantic),
		menu.Row(btnTok500, btnTok1000, btnTok2000, btnTok4000),
		menu.Row(btnItem10, btnItem20, btnItem50),
		menu.Row(btnBack),
	)

	return text, menu
}

// renderMemoryRetentionMenu renders Retention & Promotion Threshold sub-menu
func (a *BotAdapter) renderMemoryRetentionMenu(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	retDays := memMgr.GetRetentionDays()
	promThresh := memMgr.GetPromotionThreshold()

	text := fmt.Sprintf("⏳ <b>PENGATURAN RETENSI & PROMOSI MEMORI</b>\n\n"+
		"• <b>Masa Retensi:</b> <code>%d hari</code>\n"+
		"  <i>Batas usia memori sebelum kedaluwarsa jika tidak digunakan kembali.</i>\n\n"+
		"• <b>Ambang Promosi Permanen:</b> <code>&ge; %dx akses</code>\n"+
		"  <i>Memori yang sering diakses (&ge; ambang) otomatis dipromosikan menjadi permanen (long-term).</i>\n\n"+
		"<i>Pilih preset masa retensi atau ambang promosi:</i>",
		retDays, promThresh)

	menu := &tele.ReplyMarkup{}
	btnRet7 := menu.Data(fmt.Sprintf("%s 7h", checkmark(retDays == 7)), "mem_ret_7")
	btnRet14 := menu.Data(fmt.Sprintf("%s 14h", checkmark(retDays == 14)), "mem_ret_14")
	btnRet30 := menu.Data(fmt.Sprintf("%s 30h", checkmark(retDays == 30)), "mem_ret_30")
	btnRet60 := menu.Data(fmt.Sprintf("%s 60h", checkmark(retDays == 60)), "mem_ret_60")
	btnRet365 := menu.Data(fmt.Sprintf("%s 365h", checkmark(retDays == 365)), "mem_ret_365")

	btnProm1 := menu.Data(fmt.Sprintf("%s 1x", checkmark(promThresh == 1)), "mem_prom_1")
	btnProm2 := menu.Data(fmt.Sprintf("%s 2x", checkmark(promThresh == 2)), "mem_prom_2")
	btnProm3 := menu.Data(fmt.Sprintf("%s 3x", checkmark(promThresh == 3)), "mem_prom_3")
	btnProm5 := menu.Data(fmt.Sprintf("%s 5x", checkmark(promThresh == 5)), "mem_prom_5")

	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnRet7, btnRet14, btnRet30, btnRet60, btnRet365),
		menu.Row(btnProm1, btnProm2, btnProm3, btnProm5),
		menu.Row(btnBack),
	)

	return text, menu
}

// renderMemoryCompactionMenu renders Auto-Compaction sub-menu
func (a *BotAdapter) renderMemoryCompactionMenu(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	cfg := memMgr.GetConfig()

	compTag := "🟢 Auto-Compaction: AKTIF"
	if !cfg.AutoCompaction {
		compTag = "🔴 Auto-Compaction: NONAKTIF"
	}

	text := fmt.Sprintf("🧹 <b>PENGATURAN AUTO-COMPACTION & PEMBERSIHAN</b>\n\n"+
		"• <b>Status:</b> %s\n"+
		"• <b>Interval Eksekusi:</b> <code>Tiap %d jam</code>\n"+
		"• <b>Ambang Batas Jumlah:</b> <code>%d item</code> (Pemicu kompresi)\n\n"+
		"<i>Compaction merampingkan memori, menghapus catatan usang/expired, dan mengoptimalkan indeks SQLite FTS5.</i>",
		compTag, cfg.CompactionIntervalHours, cfg.CompactionThreshold)

	menu := &tele.ReplyMarkup{}
	btnToggleComp := menu.Data(compTag, "mem_toggle_compaction")

	btnCint6 := menu.Data(fmt.Sprintf("%s 6 Jam", checkmark(cfg.CompactionIntervalHours == 6)), "mem_cint_6")
	btnCint12 := menu.Data(fmt.Sprintf("%s 12 Jam", checkmark(cfg.CompactionIntervalHours == 12)), "mem_cint_12")
	btnCint24 := menu.Data(fmt.Sprintf("%s 24 Jam", checkmark(cfg.CompactionIntervalHours == 24)), "mem_cint_24")
	btnCint48 := menu.Data(fmt.Sprintf("%s 48 Jam", checkmark(cfg.CompactionIntervalHours == 48)), "mem_cint_48")

	btnCthr50 := menu.Data(fmt.Sprintf("%s 50 Item", checkmark(cfg.CompactionThreshold == 50)), "mem_cthr_50")
	btnCthr100 := menu.Data(fmt.Sprintf("%s 100 Item", checkmark(cfg.CompactionThreshold == 100)), "mem_cthr_100")
	btnCthr200 := menu.Data(fmt.Sprintf("%s 200 Item", checkmark(cfg.CompactionThreshold == 200)), "mem_cthr_200")

	btnPruneNow := menu.Data("🧹 Bersihkan & Prune Sekarang", "mem_compact_now")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnToggleComp),
		menu.Row(btnCint6, btnCint12, btnCint24, btnCint48),
		menu.Row(btnCthr50, btnCthr100, btnCthr200),
		menu.Row(btnPruneNow),
		menu.Row(btnBack),
	)

	return text, menu
}

// renderMemoryEmbeddingMenu renders Remote Vector Embedding sub-menu
func (a *BotAdapter) renderMemoryEmbeddingMenu(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	cfg := memMgr.GetConfig()
	emb := cfg.GetEmbeddingConfig()

	embTag := "🟢 Vector Embedding: AKTIF"
	if !emb.Enabled {
		embTag = "🔴 Vector Embedding: NONAKTIF"
	}

	modelStr := emb.Model
	if modelStr == "" {
		modelStr = "(belum ditentukan)"
	}
	baseStr := emb.BaseURL
	if baseStr == "" {
		baseStr = "(default endpoint)"
	}

	text := fmt.Sprintf("🔌 <b>PENGATURAN REMOTE VECTOR EMBEDDING</b>\n\n"+
		"• <b>Status:</b> %s\n"+
		"• <b>Provider:</b> <code>%s</code>\n"+
		"• <b>Model:</b> <code>%s</code>\n"+
		"• <b>Dimensi Vektor:</b> <code>%d dims</code>\n"+
		"• <b>Similarity Threshold:</b> <code>%.2f</code> (Cosine)\n"+
		"• <b>Base URL:</b> <code>%s</code>\n\n"+
		"<i>Vektor embedding digunakan untuk strategi pencarian Semantic dan Hybrid.</i>",
		embTag, html.EscapeString(emb.Provider), html.EscapeString(modelStr),
		emb.Dimensions, cfg.SimilarityThreshold, html.EscapeString(baseStr))

	menu := &tele.ReplyMarkup{}
	btnToggle := menu.Data(embTag, "mem_toggle_embed")
	btnPickModel := menu.Data("🤖 Pilih Model Preset", "mem_menu_models")

	btnOAI := menu.Data(fmt.Sprintf("%s OpenAI", checkmark(emb.Provider == "openai")), "mem_prov_openai")
	btnGem := menu.Data(fmt.Sprintf("%s Gemini", checkmark(emb.Provider == "gemini")), "mem_prov_gemini")
	btnOll := menu.Data(fmt.Sprintf("%s Ollama", checkmark(emb.Provider == "ollama")), "mem_prov_ollama")
	btnCust := menu.Data(fmt.Sprintf("%s Custom", checkmark(emb.Provider == "custom")), "mem_prov_custom")

	btnDim768 := menu.Data(fmt.Sprintf("%s 768 Dims", checkmark(emb.Dimensions == 768)), "mem_dims_768")
	btnDim1536 := menu.Data(fmt.Sprintf("%s 1536 Dims", checkmark(emb.Dimensions == 1536)), "mem_dims_1536")
	btnDim3072 := menu.Data(fmt.Sprintf("%s 3072 Dims", checkmark(emb.Dimensions == 3072)), "mem_dims_3072")

	btnSim50 := menu.Data(fmt.Sprintf("%s 0.50", checkmark(cfg.SimilarityThreshold == 0.50)), "mem_sim_50")
	btnSim60 := menu.Data(fmt.Sprintf("%s 0.60", checkmark(cfg.SimilarityThreshold == 0.60)), "mem_sim_60")
	btnSim70 := menu.Data(fmt.Sprintf("%s 0.70", checkmark(cfg.SimilarityThreshold == 0.70)), "mem_sim_70")
	btnSim80 := menu.Data(fmt.Sprintf("%s 0.80", checkmark(cfg.SimilarityThreshold == 0.80)), "mem_sim_80")

	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnToggle, btnPickModel),
		menu.Row(btnOAI, btnGem, btnOll, btnCust),
		menu.Row(btnDim768, btnDim1536, btnDim3072),
		menu.Row(btnSim50, btnSim60, btnSim70, btnSim80),
		menu.Row(btnBack),
	)

	return text, menu
}

// renderMemoryDataMenu renders Data management sub-menu
func (a *BotAdapter) renderMemoryDataMenu(userID string) (string, *tele.ReplyMarkup) {
	memMgr := a.orchestrator.MemoryManager()
	cfg := memMgr.GetConfig()
	activeCount, _ := a.db.CountMemories("user", userID)

	text := fmt.Sprintf("📋 <b>KELOLA DATA & CATATAN MEMORI</b>\n\n"+
		"• <b>Total Catatan Memori Anda:</b> <code>%d item</code>\n"+
		"• <b>Lokasi File Seed CSV:</b>\n  <code>%s</code>\n\n"+
		"<i>Pilih tindakan di bawah untuk melihat, mengimpor, atau menghapus catatan:</i>",
		activeCount, html.EscapeString(cfg.SeedCSVPath))

	menu := &tele.ReplyMarkup{}
	btnList := menu.Data("📋 Lihat 15 Memori Terbaru", "mem_list_now")
	btnImport := menu.Data("📥 Impor Seed CSV OmniRoute", "mem_import_seed")
	btnClear := menu.Data("🗑️ Hapus Semua Memori Saya", "mem_clear_confirm")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnList),
		menu.Row(btnImport),
		menu.Row(btnClear),
		menu.Row(btnBack),
	)

	return text, menu
}

func (a *BotAdapter) renderModelPicker() (string, *tele.ReplyMarkup) {
	text := "🤖 <b>PILIH PRESET MODEL EMBEDDING REMOTE</b>\n\n" +
		"Pilih salah satu model embedding yang didukung:\n\n" +
		"1. <b>OpenAI text-embedding-3-small</b> (1536 dimensi, akurat & cepat)\n" +
		"2. <b>Google Gemini text-embedding-004</b> (768 dimensi)\n" +
		"3. <b>Ollama nomic-embed-text</b> (768 dimensi, lokal :11434)\n" +
		"4. <b>Ollama mxbai-embed-large</b> (1024 dimensi, lokal :11434)\n\n" +
		"<i>Atau gunakan perintah teks:</i>\n" +
		"<code>/memory model &lt;nama_model&gt;</code>\n" +
		"<code>/memory provider &lt;provider&gt;</code>\n" +
		"<code>/memory baseurl &lt;url&gt;</code>"

	menu := &tele.ReplyMarkup{}
	btnOAI := menu.Data("OpenAI text-embedding-3-small", "mem_set_model_openai_text-embedding-3-small")
	btnGemini := menu.Data("Gemini text-embedding-004", "mem_set_model_gemini_text-embedding-004")
	btnNomic := menu.Data("Ollama nomic-embed-text", "mem_set_model_ollama_nomic-embed-text")
	btnMxbai := menu.Data("Ollama mxbai-embed-large", "mem_set_model_ollama_mxbai-embed-large")
	btnBack := menu.Data("🔙 Kembali ke Menu Embedding", "mem_menu_embedding")

	menu.Inline(
		menu.Row(btnOAI),
		menu.Row(btnGemini),
		menu.Row(btnNomic),
		menu.Row(btnMxbai),
		menu.Row(btnBack),
	)

	return text, menu
}

func checkmark(active bool) string {
	if active {
		return "✅"
	}
	return "🔘"
}

func (a *BotAdapter) handleMemoryCallback(c tele.Context, data string) error {
	if a.orchestrator == nil || a.orchestrator.MemoryManager() == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "Memory manager belum siap"})
		return nil
	}
	memMgr := a.orchestrator.MemoryManager()
	userID := strconv.FormatInt(c.Sender().ID, 10)

	switch data {
	// Navigation Callbacks
	case "mem_refresh", "mem_menu_dashboard":
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Memperbarui dashboard..."})
		text, menu := a.renderMemoryDashboard(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_general":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderMemoryGeneralMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_strategy":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_retention":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_compaction":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_embedding":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_data":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderMemoryDataMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_menu_models":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := a.renderModelPicker()
		return c.Edit(text, menu, tele.ModeHTML)

	// General & Auto-Extract Toggles
	case "mem_toggle_engine":
		newState := !memMgr.IsEnabled()
		memMgr.SetEnabled(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "⚙️ Master engine " + statusTxt})
		text, menu := a.renderMemoryGeneralMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_toggle_autoextract":
		cfg := memMgr.GetConfig()
		newState := !cfg.AutoExtract
		memMgr.SetAutoExtract(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🧠 Auto-extract " + statusTxt})
		text, menu := a.renderMemoryGeneralMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_reset_defaults":
		memMgr.ResetToDefaults()
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Pengaturan di-reset ke default pabrik"})
		text, menu := a.renderMemoryGeneralMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Strategy Presets
	case "mem_strat_hybrid":
		memMgr.SetStrategy("hybrid")
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Strategi: Hybrid"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_strat_recent":
		memMgr.SetStrategy("recent")
		_ = c.Respond(&tele.CallbackResponse{Text: "⚡ Strategi: Recent"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_strat_semantic":
		memMgr.SetStrategy("semantic")
		_ = c.Respond(&tele.CallbackResponse{Text: "🧠 Strategi: Semantic"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Token Presets
	case "mem_tok_500":
		memMgr.SetMaxTokens(500)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 500"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_tok_1000":
		memMgr.SetMaxTokens(1000)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 1000"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_tok_2000":
		memMgr.SetMaxTokens(2000)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 2000"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_tok_4000":
		memMgr.SetMaxTokens(4000)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 4000"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Max Items Presets
	case "mem_item_10":
		memMgr.SetMaxContextItems(10)
		_ = c.Respond(&tele.CallbackResponse{Text: "Max items: 10"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_item_20":
		memMgr.SetMaxContextItems(20)
		_ = c.Respond(&tele.CallbackResponse{Text: "Max items: 20"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_item_50":
		memMgr.SetMaxContextItems(50)
		_ = c.Respond(&tele.CallbackResponse{Text: "Max items: 50"})
		text, menu := a.renderMemoryStrategyMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Retention Presets
	case "mem_ret_7":
		memMgr.SetRetentionDays(7)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 7 hari"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_ret_14":
		memMgr.SetRetentionDays(14)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 14 hari"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_ret_30":
		memMgr.SetRetentionDays(30)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 30 hari"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_ret_60":
		memMgr.SetRetentionDays(60)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 60 hari"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_ret_365":
		memMgr.SetRetentionDays(365)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 365 hari"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Promotion Threshold Presets
	case "mem_prom_1":
		memMgr.SetPromotionThreshold(1)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 1x akses"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prom_2":
		memMgr.SetPromotionThreshold(2)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 2x akses"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prom_3":
		memMgr.SetPromotionThreshold(3)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 3x akses"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prom_5":
		memMgr.SetPromotionThreshold(5)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 5x akses"})
		text, menu := a.renderMemoryRetentionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Compaction Presets & Toggles
	case "mem_toggle_compaction":
		cfg := memMgr.GetConfig()
		newState := !cfg.AutoCompaction
		memMgr.SetAutoCompaction(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🧹 Compaction " + statusTxt})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cint_6":
		memMgr.SetCompactionIntervalHours(6)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 6 jam"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cint_12":
		memMgr.SetCompactionIntervalHours(12)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 12 jam"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cint_24":
		memMgr.SetCompactionIntervalHours(24)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 24 jam"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cint_48":
		memMgr.SetCompactionIntervalHours(48)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 48 jam"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cthr_50":
		memMgr.SetCompactionThreshold(50)
		_ = c.Respond(&tele.CallbackResponse{Text: "Ambang batas: 50 item"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cthr_100":
		memMgr.SetCompactionThreshold(100)
		_ = c.Respond(&tele.CallbackResponse{Text: "Ambang batas: 100 item"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_cthr_200":
		memMgr.SetCompactionThreshold(200)
		_ = c.Respond(&tele.CallbackResponse{Text: "Ambang batas: 200 item"})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_compact_now":
		rep, err := memMgr.Compact(context.Background(), "user", userID)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal compaction"})
			return nil
		}
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🧹 Selesai: %d usang dihapus", rep.PrunedExpired)})
		text, menu := a.renderMemoryCompactionMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Embedding Toggles, Providers & Settings
	case "mem_toggle_embed":
		cfg := memMgr.GetConfig()
		emb := cfg.GetEmbeddingConfig()
		newState := !emb.Enabled
		memMgr.SetEmbeddingEnabled(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🔌 Embedding " + statusTxt})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prov_openai":
		memMgr.SetEmbeddingProvider("openai")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: OpenAI"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prov_gemini":
		memMgr.SetEmbeddingProvider("gemini")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: Gemini"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prov_ollama":
		memMgr.SetEmbeddingProvider("ollama")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: Ollama"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_prov_custom":
		memMgr.SetEmbeddingProvider("custom")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: Custom"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_dims_768":
		memMgr.SetEmbeddingDimensions(768)
		_ = c.Respond(&tele.CallbackResponse{Text: "Dimensi: 768"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_dims_1536":
		memMgr.SetEmbeddingDimensions(1536)
		_ = c.Respond(&tele.CallbackResponse{Text: "Dimensi: 1536"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_dims_3072":
		memMgr.SetEmbeddingDimensions(3072)
		_ = c.Respond(&tele.CallbackResponse{Text: "Dimensi: 3072"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_sim_50":
		memMgr.SetSimilarityThreshold(0.50)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.50"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_sim_60":
		memMgr.SetSimilarityThreshold(0.60)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.60"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_sim_70":
		memMgr.SetSimilarityThreshold(0.70)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.70"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_sim_80":
		memMgr.SetSimilarityThreshold(0.80)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.80"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Preset Embedding Models
	case "mem_set_model_openai_text-embedding-3-small":
		memMgr.SetEmbeddingProvider("openai")
		memMgr.SetEmbeddingModel("text-embedding-3-small")
		memMgr.SetEmbeddingDimensions(1536)
		memMgr.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ OpenAI text-embedding-3-small"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_set_model_gemini_text-embedding-004":
		memMgr.SetEmbeddingProvider("gemini")
		memMgr.SetEmbeddingModel("text-embedding-004")
		memMgr.SetEmbeddingDimensions(768)
		memMgr.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Gemini text-embedding-004"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_set_model_ollama_nomic-embed-text":
		memMgr.SetEmbeddingProvider("ollama")
		memMgr.SetEmbeddingModel("nomic-embed-text")
		memMgr.SetEmbeddingDimensions(768)
		memMgr.SetEmbeddingBaseURL("http://localhost:11434/v1")
		memMgr.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Ollama nomic-embed-text"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_set_model_ollama_mxbai-embed-large":
		memMgr.SetEmbeddingProvider("ollama")
		memMgr.SetEmbeddingModel("mxbai-embed-large")
		memMgr.SetEmbeddingDimensions(1024)
		memMgr.SetEmbeddingBaseURL("http://localhost:11434/v1")
		memMgr.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Ollama mxbai-embed-large"})
		text, menu := a.renderMemoryEmbeddingMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	// Data Management Callbacks
	case "mem_list_now":
		_ = c.Respond(&tele.CallbackResponse{})
		items, err := a.db.ListMemoriesByScope("user", userID, "", 15)
		if err != nil || len(items) == 0 {
			return c.Send("📭 <i>Belum ada catatan memori yang tersimpan untuk akun Anda.</i>", tele.ModeHTML)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 <b>DAFTAR MEMORI ANDA (%d item):</b>\n\n", len(items)))
		for i, it := range items {
			promTag := ""
			if it.IsPromoted {
				promTag = " ⭐️[PERMANEN]"
			}
			contentExcerpt := it.Content
			if len([]rune(contentExcerpt)) > 70 {
				contentExcerpt = string([]rune(contentExcerpt)[:70]) + "..."
			}
			sb.WriteString(fmt.Sprintf("<b>%d.</b> <code>[%s]</code>%s\n   %s\n\n",
				i+1, html.EscapeString(it.Key), promTag, html.EscapeString(contentExcerpt)))
		}
		return c.Send(sb.String(), tele.ModeHTML)

	case "mem_import_seed":
		_ = c.Respond(&tele.CallbackResponse{Text: "📥 Mengimpor seed CSV..."})
		res, err := memMgr.ImportSeedCSV(context.Background(), "", userID)
		if err != nil {
			return c.Send(fmt.Sprintf("❌ Gagal mengimpor seed CSV: %v", err), tele.ModeHTML)
		}
		msg := fmt.Sprintf("📥 <b>HASIL IMPOR SEED CSV MEMORY</b>\n\n"+
			"• <b>Total Diproses:</b> <code>%d item</code>\n"+
			"• <b>Berhasil Diimpor:</b> <code>%d item</code>\n"+
			"• <b>Error / Dilewati:</b> <code>%d</code>",
			res.TotalProcessed, res.Inserted, len(res.Errors))
		_ = c.Send(msg, tele.ModeHTML)
		text, menu := a.renderMemoryDataMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)

	case "mem_clear_confirm":
		if err := memMgr.ClearUserMemory(userID); err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal menghapus"})
			return c.Send(fmt.Sprintf("❌ Gagal menghapus memori: %v", err), tele.ModeHTML)
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🗑️ Memori dibersihkan"})
		_ = c.Send("🗑️ <b>Semua catatan memori pribadi Anda telah dihapus.</b>", tele.ModeHTML)
		text, menu := a.renderMemoryDataMenu(userID)
		return c.Edit(text, menu, tele.ModeHTML)
	}

	return nil
}

func extractInteractiveOptions(text string) (string, []string) {
	return tgformat.ExtractInteractiveOptions(text)
}

func (a *BotAdapter) handleOptionCallback(c tele.Context, data string) error {
	optKey := strings.TrimPrefix(data, "opt_")
	val, ok := a.pendingOptions.Load(optKey)
	if !ok {
		return c.Respond(&tele.CallbackResponse{Text: "⚠️ Pilihan sudah kadaluarsa atau tidak ditemukan."})
	}
	optPrompt, _ := val.(string)
	_ = c.Respond(&tele.CallbackResponse{Text: "✅ Dipilih: " + optPrompt})

	// Tampilkan pilihan pengguna di chat
	chosenMsg, _ := a.bot.Send(c.Chat(), fmt.Sprintf("👉 <b>%s</b>", html.EscapeString(optPrompt)), tele.ModeHTML)

	// Jalankan permintaan AI berdasarkan opsi yang dipilih
	return a.executePrompt(c, chosenMsg, optPrompt, nil, 0)
}

func (a *BotAdapter) sendOrEditResponse(c tele.Context, thinkingMsg *tele.Message, text string, mediaFiles []agent.MediaAttachment) error {
	if strings.TrimSpace(text) == "" {
		text = "(Tidak ada respon dari model)"
	}

	var menu *tele.ReplyMarkup
	cleanText, options := extractInteractiveOptions(text)
	if len(options) > 0 {
		text = cleanText
		menu = &tele.ReplyMarkup{}
		var rows []tele.Row
		var currentRow []tele.Btn

		count := 0
		a.pendingOptions.Range(func(k, v interface{}) bool {
			count++
			return true
		})
		if count > 500 {
			a.pendingOptions = sync.Map{}
		}

		for i, opt := range options {
			optKey := fmt.Sprintf("%d_%d", time.Now().UnixNano()%10000000, i)
			a.pendingOptions.Store(optKey, opt)

			btnLabel := opt
			if len(btnLabel) > 35 {
				btnLabel = btnLabel[:32] + "..."
			}
			btn := menu.Data(btnLabel, "opt_"+optKey)

			if len(btnLabel) > 18 || len(currentRow) == 2 {
				if len(currentRow) > 0 {
					rows = append(rows, menu.Row(currentRow...))
					currentRow = nil
				}
			}
			if len(btnLabel) > 18 {
				rows = append(rows, menu.Row(btn))
			} else {
				currentRow = append(currentRow, btn)
			}
		}
		if len(currentRow) > 0 {
			rows = append(rows, menu.Row(currentRow...))
		}
		if len(rows) > 0 {
			menu.Inline(rows...)
		} else {
			menu = nil
		}
	}

	chunks := splitMessage(text, 4000)
	if len(chunks) > 0 {
		if thinkingMsg != nil {
			isLast := len(chunks) == 1
			var firstOpts []interface{}
			firstOpts = append(firstOpts, tele.ModeHTML)
			if isLast && menu != nil {
				firstOpts = append(firstOpts, menu)
			}

			formattedFirst := tgformat.MarkdownToTelegramHTML(chunks[0])
			_, err := c.Bot().Edit(thinkingMsg, formattedFirst, firstOpts...)
			if err != nil {
				// Fallback to plain text edit if HTML fails
				var plainOpts []interface{}
				if isLast && menu != nil {
					plainOpts = append(plainOpts, menu)
				}
				_, err = c.Bot().Edit(thinkingMsg, chunks[0], plainOpts...)
				if err != nil {
					// Fallback to reply HTML, then plain text
					if err := c.Reply(formattedFirst, firstOpts...); err != nil {
						_ = c.Reply(chunks[0], plainOpts...)
					}
				}
			}

			for i, chunk := range chunks[1:] {
				isChunkLast := (i + 1) == len(chunks)-1
				var chunkOpts []interface{}
				chunkOpts = append(chunkOpts, tele.ModeHTML)
				if isChunkLast && menu != nil {
					chunkOpts = append(chunkOpts, menu)
				}
				formattedChunk := tgformat.MarkdownToTelegramHTML(chunk)
				if err := c.Reply(formattedChunk, chunkOpts...); err != nil {
					var plainChunkOpts []interface{}
					if isChunkLast && menu != nil {
						plainChunkOpts = append(plainChunkOpts, menu)
					}
					_ = c.Reply(chunk, plainChunkOpts...)
				}
			}
		} else {
			for i, chunk := range chunks {
				isChunkLast := i == len(chunks)-1
				var chunkOpts []interface{}
				chunkOpts = append(chunkOpts, tele.ModeHTML)
				if isChunkLast && menu != nil {
					chunkOpts = append(chunkOpts, menu)
				}
				formattedChunk := tgformat.MarkdownToTelegramHTML(chunk)
				if err := c.Reply(formattedChunk, chunkOpts...); err != nil {
					var plainChunkOpts []interface{}
					if isChunkLast && menu != nil {
						plainChunkOpts = append(plainChunkOpts, menu)
					}
					_ = c.Reply(chunk, plainChunkOpts...)
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

		// Hapus file screenshot sementara dari server segera setelah berhasil dikirim
		if isTempScreenshot(mf.FilePath) {
			_ = os.Remove(mf.FilePath)
		}
	}

	return nil
}

func isTempScreenshot(fPath string) bool {
	clean := filepath.ToSlash(fPath)
	return strings.Contains(clean, "/screenshots/") || strings.HasPrefix(clean, "screenshots/") || strings.Contains(clean, "data/screenshots/")
}

func splitMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = 4000
	}
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return []string{text}
	}

	// Check if text starts with an expandable blockquote (e.g. thinking block)
	bqStart := "<blockquote expandable>"
	bqEnd := "</blockquote>"
	if strings.Contains(text, bqStart) && strings.Contains(text, bqEnd) {
		startIdx := strings.Index(text, bqStart)
		endIdx := strings.Index(text, bqEnd)
		if startIdx < endIdx {
			blockPrefix := text[:startIdx] // e.g. "💭 <b>Proses Berpikir:</b>\n"
			blockContent := text[startIdx+len(bqStart) : endIdx]
			afterBlock := strings.TrimSpace(text[endIdx+len(bqEnd):])

			// If the blockquote alone is longer than maxLen - 200, cap blockContent
			maxBlockContent := maxLen - len(blockPrefix) - len(bqStart) - len(bqEnd) - 50
			if maxBlockContent > 500 && len(blockContent) > maxBlockContent {
				blockContent = blockContent[:maxBlockContent] + "..."
			}

			fullBlock := fmt.Sprintf("%s%s%s%s", blockPrefix, bqStart, blockContent, bqEnd)

			// If full block + afterBlock fits within maxLen, keep them together in one message!
			if len(fullBlock)+2+len(afterBlock) <= maxLen {
				return []string{fullBlock + "\n\n" + afterBlock}
			}

			// Otherwise, chunk 0 is the full blockquote, and remaining chunks are afterBlock
			var chunks []string
			chunks = append(chunks, fullBlock)
			if afterBlock != "" {
				restChunks := splitMessageSimple(afterBlock, maxLen)
				chunks = append(chunks, restChunks...)
			}
			return chunks
		}
	}

	return splitMessageSimple(text, maxLen)
}

func splitMessageSimple(text string, maxLen int) []string {
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
