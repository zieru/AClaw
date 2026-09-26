package admin

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
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/provider"
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

	policy := a.db.GetResolvedPolicy("admin", fmt.Sprintf("%d", c.Chat().ID))

	var stopUpdater func()
	var onProgressStatus func(string)
	var onStreamChunk func(provider.StreamChunk)
	var getStreamThinking func() string

	if policy.StreamingEnabled {
		stopUpdater, onProgressStatus, onStreamChunk, getStreamThinking = startAdminProgressiveThinking(a.bot, thinkingMsg)
	} else {
		stopUpdater, onProgressStatus, _, getStreamThinking = startAdminProgressiveThinking(a.bot, thinkingMsg)
		onStreamChunk = nil
	}
	defer stopUpdater()

	timeoutSec := 180
	if cfg := config.Get(); cfg != nil && cfg.Timeouts.HandlerSeconds > 0 {
		timeoutSec = cfg.Timeouts.HandlerSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
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
		OnStreamChunk: func(chunk provider.StreamChunk) {
			if onStreamChunk != nil {
				onStreamChunk(chunk)
			}
		},
	})

	stopUpdater()

	if err != nil {
		log.Printf("⚠️ [Telegram Admin PM] Request gagal/timeout (User: %s, Prompt: %q): %v",
			c.Sender().Username, msg, err)
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
		retryBtn := errMenu.Data("🔄 Coba Lagi", "retry_admin_task")
		errMenu.Inline(errMenu.Row(retryBtn))

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
	// Pastikan hasil stream thinking tidak dihapus dari tampilan pesan Telegram
	if streamThink != "" && !strings.Contains(finalText, "Proses Berpikir") {
		finalText = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s</blockquote>\n\n%s", streamThink, finalText)
	}

	return sendOrEditSplitMessage(c, thinkingMsg, finalText, resp.MediaFiles...)
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
				restChunks := splitTextSimple(afterBlock, maxLen)
				chunks = append(chunks, restChunks...)
			}
			return chunks
		}
	}

	return splitTextSimple(text, maxLen)
}

func splitTextSimple(text string, maxLen int) []string {
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

// startAdminProgressiveThinking periodically updates the thinking indicator with elapsed time, tool progress & streaming thoughts
func startAdminProgressiveThinking(bot *tele.Bot, targetMsg *tele.Message) (stopFunc func(), updateStatus func(string), onChunk func(chunk provider.StreamChunk), getThinking func() string) {
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
		// Minimum 3 seconds throttle to strictly avoid Telegram flood limits (429)
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
					if curThinking != "" {
						previewThink := curThinking
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
						if len(previewContent) > 3500 {
							previewContent = previewContent[len(previewContent)-3500:]
						}
						formattedContent := tgformat.MarkdownToTelegramHTML(previewContent)
						text = fmt.Sprintf("%s ▌", formattedContent)
					}
				} else if status != "" {
					if curThinking != "" {
						previewThink := curThinking
						if len(previewThink) > 1200 {
							previewThink = previewThink[len(previewThink)-1200:]
						}
						text = fmt.Sprintf("%s\n\n💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s ▌</blockquote>\n\n⏱️ <i>(%dd)</i>", status, html.EscapeString(previewThink), elapsedSec)
					} else {
						text = fmt.Sprintf("%s <i>(%dd)</i>", status, elapsedSec)
					}
				} else if curThinking != "" {
					previewThink := curThinking
					if len(previewThink) > 3500 {
						previewThink = previewThink[len(previewThink)-3500:]
					}
					text = fmt.Sprintf("💭 <b>Proses Berpikir:</b>\n<blockquote expandable>%s ▌</blockquote>", html.EscapeString(previewThink))
				} else {
					text = fmt.Sprintf("🤔 <i>Sedang berpikir... (%dd)</i>", elapsedSec)
				}
				mu.Unlock()

				if targetMsg != nil && text != lastSentText {
					lastSentText = text
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
