package tgprompt

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/tools"
	tele "gopkg.in/telebot.v3"
)

type pendingRequest struct {
	chatID     int64
	userID     int64
	promptMsg  *tele.Message
	resultChan chan string
	cancelChan chan struct{}
	createdAt  time.Time
}

// BotAPI abstracts telebot Bot methods needed by PromptManager for testability
type BotAPI interface {
	Send(to tele.Recipient, what interface{}, opts ...interface{}) (*tele.Message, error)
	Delete(msg tele.Editable) error
	Edit(msg tele.Editable, what interface{}, opts ...interface{}) (*tele.Message, error)
}

// PromptManager handles interactive, secure Telegram dialogs for credentials/passwords.
// It intercepts user input before it reaches the AI orchestrator, deletes sensitive messages,
// and removes the prompt message once the task completes.
type PromptManager struct {
	bot     BotAPI
	pending sync.Map // key: string "chatID:userID" -> *pendingRequest
}

// NewPromptManager creates a new instance of PromptManager
func NewPromptManager(bot BotAPI) *PromptManager {
	return &PromptManager{
		bot: bot,
	}
}

func (p *PromptManager) key(chatID, userID int64) string {
	return fmt.Sprintf("%d:%d", chatID, userID)
}

// PromptPassword sends a ForceReply message to the user asking for a password,
// waits for their response, and returns the password alongside a cleanup func
// that deletes the prompt message when called.
func (p *PromptManager) PromptPassword(ctx context.Context, chatID int64, userID int64, title, description string) (string, func(), error) {
	if p.bot == nil {
		return "", nil, errors.New("telegram bot belum terinisialisasi")
	}
	if ctx != nil && ctx.Err() != nil {
		return "", nil, ctx.Err()
	}

	key := p.key(chatID, userID)

	// Cancel previous pending prompt if exists
	if prev, ok := p.pending.LoadAndDelete(key); ok {
		if req, ok := prev.(*pendingRequest); ok {
			close(req.cancelChan)
			if req.promptMsg != nil {
				_ = p.bot.Delete(req.promptMsg)
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔐 <b>%s</b>\n\n", html.EscapeString(title)))
	if strings.TrimSpace(description) != "" {
		sb.WriteString(description)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Silakan balas pesan ini dengan password Anda.\n")
	sb.WriteString("💡 <i>Ketik <code>/cancel</code> atau <code>batal</code> untuk membatalkan.</i>\n\n")
	sb.WriteString("🔒 <i>Password Anda tidak akan pernah dikirimkan ke AI dan pesan akan otomatis dihapus segera setelah selesai.</i>")

	replyMarkup := &tele.ReplyMarkup{
		ForceReply:  true,
		Selective:   true,
		Placeholder: "Masukkan password...",
	}

	targetChat := &tele.Chat{ID: chatID}
	msg, err := p.bot.Send(targetChat, sb.String(), tele.ModeHTML, replyMarkup)
	if err != nil {
		return "", nil, fmt.Errorf("gagal mengirim dialog input password ke Telegram: %w", err)
	}

	req := &pendingRequest{
		chatID:     chatID,
		userID:     userID,
		promptMsg:  msg,
		resultChan: make(chan string, 1),
		cancelChan: make(chan struct{}),
		createdAt:  time.Now(),
	}
	p.pending.Store(key, req)

	// Wait with timeout
	timeout := 90 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case pass := <-req.resultChan:
		finish := func() {
			if msg != nil {
				_ = p.bot.Delete(msg)
			}
		}
		return pass, finish, nil

	case <-req.cancelChan:
		if msg != nil {
			_ = p.bot.Delete(msg)
		}
		return "", nil, errors.New("input password dibatalkan oleh pengguna")

	case <-ctx.Done():
		p.pending.Delete(key)
		if msg != nil {
			_ = p.bot.Delete(msg)
		}
		return "", nil, ctx.Err()

	case <-timer.C:
		p.pending.Delete(key)
		if msg != nil {
			_ = p.bot.Delete(msg)
		}
		return "", nil, errors.New("waktu input password habis (timeout)")
	}
}

// HandleTextMessage intercepts incoming text messages from Telegram.
// If the sender has a pending password prompt or replies to the prompt message,
// it extracts the password, deletes the user's message immediately from Telegram,
// and delivers the password out-of-band without passing to the AI.
func (p *PromptManager) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Chat() == nil || c.Sender() == nil {
		return false, nil
	}

	chatID := c.Chat().ID
	userID := c.Sender().ID
	key := p.key(chatID, userID)

	val, ok := p.pending.Load(key)
	if !ok {
		// Also check if user replied directly to a pending prompt message
		if c.Message() != nil && c.Message().ReplyTo != nil {
			replyID := c.Message().ReplyTo.ID
			p.pending.Range(func(k, v interface{}) bool {
				req, isReq := v.(*pendingRequest)
				if isReq && req.promptMsg != nil && req.promptMsg.ID == replyID {
					val = v
					key, _ = k.(string)
					ok = true
					return false
				}
				return true
			})
		}
	}

	if !ok {
		return false, nil
	}

	req, ok := val.(*pendingRequest)
	if !ok {
		p.pending.Delete(key)
		return false, nil
	}

	msg := c.Message()
	userText := ""
	if msg != nil {
		userText = strings.TrimSpace(msg.Text)
		// Delete the user's password message immediately from chat for security
		_ = p.bot.Delete(msg)
	}

	p.pending.Delete(key)

	// Check if user requested cancellation
	if userText == "/cancel" || strings.EqualFold(userText, "batal") || strings.EqualFold(userText, "cancel") {
		close(req.cancelChan)
		if req.promptMsg != nil {
			_ = p.bot.Delete(req.promptMsg)
		}
		_, _ = p.bot.Send(c.Chat(), "❌ <b>Input password dibatalkan.</b>", tele.ModeHTML)
		return true, nil
	}

	// Update prompt message status to show active progress
	if req.promptMsg != nil {
		_, _ = p.bot.Edit(req.promptMsg, "⏳ <i>Password diterima. Menjalankan perintah...</i>", tele.ModeHTML)
	}

	// Send password to waiting PromptPassword goroutine
	req.resultChan <- userText
	return true, nil
}

// Cancel cancels any pending prompt for the specified chat and user
func (p *PromptManager) Cancel(chatID, userID int64) {
	key := p.key(chatID, userID)
	if val, ok := p.pending.LoadAndDelete(key); ok {
		if req, ok := val.(*pendingRequest); ok {
			close(req.cancelChan)
			if req.promptMsg != nil {
				_ = p.bot.Delete(req.promptMsg)
			}
		}
	}
}

// ForUser returns a tools.PasswordPrompter adapter bound to specific chatID and userID
func (p *PromptManager) ForUser(chatID, userID int64) tools.PasswordPrompter {
	return &userPrompter{
		pm:     p,
		chatID: chatID,
		userID: userID,
	}
}

type userPrompter struct {
	pm     *PromptManager
	chatID int64
	userID int64
}

func (u *userPrompter) PromptPassword(ctx context.Context, title, description string) (string, func(), error) {
	return u.pm.PromptPassword(ctx, u.chatID, u.userID, title, description)
}
