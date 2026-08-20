package telegram

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

// BotAdapter manages a Telegram bot channel instance
type BotAdapter struct {
	channelID    string
	name         string
	token        string
	bot          *tele.Bot
	orchestrator *agent.Orchestrator
	db           *storage.DB
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

		// Send typing action & initial thinking message
		_ = c.Notify(tele.Typing)
		thinkingMsg, _ := a.bot.Reply(msg, "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

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

		return sendOrEditResponse(c, thinkingMsg, resp.Text)
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
		defer cancel()

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

		return sendOrEditResponse(c, thinkingMsg, resp.Text)
	})
}

func sendOrEditResponse(c tele.Context, thinkingMsg *tele.Message, text string) error {
	if strings.TrimSpace(text) == "" {
		text = "(Tidak ada respon dari model)"
	}

	chunks := splitMessage(text, 4000)
	if len(chunks) == 0 {
		return nil
	}

	if thinkingMsg != nil {
		_, err := c.Bot().Edit(thinkingMsg, chunks[0])
		if err != nil {
			// Fallback to reply if edit fails
			_ = c.Reply(chunks[0])
		}
		for _, chunk := range chunks[1:] {
			if err := c.Reply(chunk); err != nil {
				return err
			}
		}
		return nil
	}

	for _, chunk := range chunks {
		if err := c.Reply(chunk); err != nil {
			return err
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
