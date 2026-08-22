package admin

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/tgformat"
	tele "gopkg.in/telebot.v3"
)

// handleDirectChat processes direct PM messages to the admin bot using the Agent Orchestrator
func (a *AdminBot) handleDirectChat(c tele.Context, msg string) error {
	_ = c.Notify(tele.Typing)
	cancelMenu := &tele.ReplyMarkup{}
	cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
	cancelMenu.Inline(cancelMenu.Row(cancelBtn))

	thinkingMsg, _ := a.bot.Reply(c.Message(), "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML, cancelMenu)

	stopUpdater, onProgressStatus := startAdminProgressiveThinking(a.bot, thinkingMsg)
	defer stopUpdater()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	a.activeTasks.Store(c.Chat().ID, cancel)
	defer func() {
		a.activeTasks.Delete(c.Chat().ID)
		cancel()
	}()

	resp, err := a.orchestrator.ProcessMessage(ctx, agent.UserRequest{
		ChannelType: "telegram_admin",
		ChannelID:   "admin",
		ChannelName: "Telegram Admin PM",
		ChatID:      fmt.Sprintf("%d", c.Chat().ID),
		UserID:      fmt.Sprintf("%d", c.Sender().ID),
		UserName:    c.Sender().Username,
		UserPrompt:  msg,
		OnProgress: func(status string) {
			onProgressStatus(status)
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
		if thinkingMsg != nil {
			_, _ = a.bot.Edit(thinkingMsg, friendlyErr, tele.ModeHTML)
			return nil
		}
		return c.Reply(friendlyErr, tele.ModeHTML)
	}

	return sendOrEditSplitMessage(c, thinkingMsg, resp.Text, resp.MediaFiles...)
}

func sendSplitMessage(c tele.Context, text string) error {
	return sendOrEditSplitMessage(c, nil, text)
}

func sendOrEditSplitMessage(c tele.Context, thinkingMsg *tele.Message, text string, mediaFiles ...agent.MediaAttachment) error {
	if strings.TrimSpace(text) == "" {
		text = "(Tidak ada respon dari model)"
	}

	chunks := splitText(text, 4000)
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

func splitText(text string, maxLen int) []string {
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

// startAdminProgressiveThinking periodically updates the thinking indicator with elapsed time & dynamic messages
func startAdminProgressiveThinking(bot *tele.Bot, targetMsg *tele.Message) (stopFunc func(), updateStatus func(string)) {
	if targetMsg == nil {
		return func() {}, func(string) {}
	}

	cancelMenu := &tele.ReplyMarkup{}
	cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
	cancelMenu.Inline(cancelMenu.Row(cancelBtn))

	var mu sync.Mutex
	customStatus := ""
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
				elapsedSec := int(time.Since(startTime).Seconds())
				var text string
				if customStatus != "" {
					text = fmt.Sprintf("%s <i>(%dd)</i>", customStatus, elapsedSec)
				} else {
					switch {
					case elapsedSec < 6:
						text = fmt.Sprintf("⚡ <i>Masih menganalisis pertanyaan & konteks... (%dd)</i>", elapsedSec)
					case elapsedSec < 22:
						text = fmt.Sprintf("🔍 <i>Sedang memproses instruksi secara mendalam... (%dd)</i>", elapsedSec)
					default:
						text = fmt.Sprintf("⏳ <i>Hampir selesai, memvalidasi & menyusun format output... (%dd)</i>", elapsedSec)
					}
				}
				mu.Unlock()

				if targetMsg != nil {
					_, _ = bot.Edit(targetMsg, text, tele.ModeHTML, cancelMenu)
				}
			}
		}
	}()

	return stopFunc, updateStatus
}
