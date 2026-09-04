package admin

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
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
	return a.handleDirectChatWithMedia(c, msg, nil, 0)
}

func (a *AdminBot) handleDirectChatWithMedia(c tele.Context, msg string, images []string, fileMB float64) error {
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
		ChannelType:    "telegram_admin",
		ChannelID:      "admin",
		ChannelName:    "Telegram Admin PM",
		ChatID:         fmt.Sprintf("%d", c.Chat().ID),
		UserID:         fmt.Sprintf("%d", c.Sender().ID),
		UserName:       c.Sender().Username,
		UserPrompt:     msg,
		AttachedFileMB: fileMB,
		AttachedImages: images,
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
		errMenu := &tele.ReplyMarkup{}
		retryBtn := errMenu.Data("🔄 Coba Lagi", "retry_admin_task")
		errMenu.Inline(errMenu.Row(retryBtn))

		if thinkingMsg != nil {
			_, _ = a.bot.Edit(thinkingMsg, friendlyErr, tele.ModeHTML, errMenu)
			return nil
		}
		return c.Reply(friendlyErr, tele.ModeHTML, errMenu)
	}

	return sendOrEditSplitMessage(c, thinkingMsg, resp.Text, resp.MediaFiles...)
}

func isTextDocument(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md", ".json", ".csv", ".tsv", ".yaml", ".yml", ".xml", ".html", ".htm",
		".css", ".js", ".ts", ".jsx", ".tsx", ".go", ".py", ".java", ".c", ".cpp", ".h",
		".sql", ".sh", ".bat", ".ps1", ".log", ".ini", ".conf", ".env", ".toml", ".php":
		return true
	default:
		return false
	}
}

func isImageDocument(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp":
		return true
	default:
		return false
	}
}

// handleDirectPhoto processes photo/image sent to admin bot
func (a *AdminBot) handleDirectPhoto(c tele.Context) error {
	msg := c.Message()
	if msg == nil || msg.Photo == nil {
		return nil
	}
	photo := msg.Photo

	uploadDir := filepath.Join(a.cfg.Server.DataDir, "uploads")
	_ = os.MkdirAll(uploadDir, 0755)

	caption := strings.TrimSpace(msg.Caption)
	if caption == "" {
		caption = "Tolong analisis dan jelaskan gambar/foto ini."
	}

	reader, err := a.bot.File(&photo.File)
	if err != nil {
		return a.handleDirectChat(c, caption)
	}
	defer reader.Close()

	imgBytes, err := io.ReadAll(reader)
	if err != nil || len(imgBytes) == 0 {
		return a.handleDirectChat(c, caption)
	}

	fileMB := float64(len(imgBytes)) / (1024 * 1024)
	fileName := fmt.Sprintf("photo_%d.jpg", time.Now().Unix())
	targetPath := filepath.Join(uploadDir, fileName)
	_ = os.WriteFile(targetPath, imgBytes, 0644)

	base64URL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imgBytes)
	fileHeader := fmt.Sprintf("[📸 Gambar Terlampir: %s (%.1f KB) | Tersimpan di: %s]\n\n", fileName, float64(len(imgBytes))/1024.0, targetPath)

	return a.handleDirectChatWithMedia(c, fileHeader+caption, []string{base64URL}, fileMB)
}

// handleDirectDocument processes document/file sent to admin bot
func (a *AdminBot) handleDirectDocument(c tele.Context) error {
	msg := c.Message()
	if msg == nil || msg.Document == nil {
		return nil
	}
	doc := msg.Document

	// If user is currently in MD wizard session and document is .md, let mdUI handle it
	if strings.HasSuffix(strings.ToLower(doc.FileName), ".md") {
		if a.mdUI != nil && a.mdUI.HasActiveSession(c.Sender().ID) {
			return a.mdUI.HandleDocumentUpload(c)
		}
	}

	uploadDir := filepath.Join(a.cfg.Server.DataDir, "uploads")
	_ = os.MkdirAll(uploadDir, 0755)

	caption := strings.TrimSpace(msg.Caption)
	if caption == "" {
		caption = fmt.Sprintf("Tolong analisis dan jelaskan berkas/file terlampir: %s", doc.FileName)
	}

	fileMB := float64(doc.FileSize) / (1024 * 1024)
	targetPath := filepath.Join(uploadDir, doc.FileName)

	reader, err := a.bot.File(&doc.File)
	if err != nil {
		return a.handleDirectChat(c, fmt.Sprintf("[📎 Berkas: %s]\n%s", doc.FileName, caption))
	}
	defer reader.Close()

	docBytes, err := io.ReadAll(reader)
	if err != nil {
		return a.handleDirectChat(c, fmt.Sprintf("[📎 Berkas: %s]\n%s", doc.FileName, caption))
	}
	_ = os.WriteFile(targetPath, docBytes, 0644)

	var images []string
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("[📎 Berkas Terlampir: %s (%.1f KB) | Tersimpan di: %s]\n\n", doc.FileName, float64(len(docBytes))/1024.0, targetPath))

	if isImageDocument(doc.FileName) {
		mime := "image/jpeg"
		if strings.HasSuffix(strings.ToLower(doc.FileName), ".png") {
			mime = "image/png"
		} else if strings.HasSuffix(strings.ToLower(doc.FileName), ".webp") {
			mime = "image/webp"
		}
		images = append(images, fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(docBytes)))
	} else if isTextDocument(doc.FileName) && len(docBytes) <= 150*1024 {
		promptBuilder.WriteString("Isi File:\n```\n")
		promptBuilder.WriteString(string(docBytes))
		promptBuilder.WriteString("\n```\n\n")
	}

	promptBuilder.WriteString(caption)
	return a.handleDirectChatWithMedia(c, promptBuilder.String(), images, fileMB)
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
					text = fmt.Sprintf("💭 <i>Sedang berpikir... (%dd)</i>", elapsedSec)
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
