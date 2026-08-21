package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/storage"
)

// BridgeAdapter manages a WhatsApp channel via webhook bridge or native connection
type BridgeAdapter struct {
	channelID    string
	name         string
	endpointURL  string
	apiKey       string
	orchestrator *agent.Orchestrator
	db           *storage.DB
	httpClient   *http.Client
	activeTasks  sync.Map // chatID -> context.CancelFunc
}

func NewBridgeAdapter(channelID, name, endpointURL, apiKey string, orch *agent.Orchestrator, db *storage.DB) *BridgeAdapter {
	return &BridgeAdapter{
		channelID:    channelID,
		name:         name,
		endpointURL:  endpointURL,
		apiKey:       apiKey,
		orchestrator: orch,
		db:           db,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *BridgeAdapter) ID() string   { return a.channelID }
func (a *BridgeAdapter) Type() string { return "whatsapp" }
func (a *BridgeAdapter) Name() string { return a.name }

func (a *BridgeAdapter) Start(ctx context.Context) error {
	log.Printf("[Channel-WA] WhatsApp Bridge '%s' aktif (Endpoint: %s)", a.name, a.endpointURL)
	return nil
}

func (a *BridgeAdapter) Stop() error {
	return nil
}

// SendMessage sends an outgoing text message to a WhatsApp number/JID
func (a *BridgeAdapter) SendMessage(targetID, text string) error {
	if a.endpointURL == "" {
		return fmt.Errorf("whatsapp endpoint URL belum dikonfigurasi")
	}

	payload := map[string]interface{}{
		"number":  targetID,
		"text":    text,
		"message": text,
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", a.endpointURL+"/message/sendText", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("apikey", a.apiKey)
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gagal mengirim pesan WA: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp bridge error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// IncomingMessagePayload represents a webhook body from WhatsApp bridge
type IncomingMessagePayload struct {
	From       string  `json:"from"`
	SenderName string  `json:"sender_name"`
	Text       string  `json:"text"`
	IsGroup    bool    `json:"is_group"`
	GroupID    string  `json:"group_id"`
	FileSizeMB float64 `json:"file_size_mb"`
}

// HandleIncomingWebhook handles an HTTP webhook call from WhatsApp gateway
func (a *BridgeAdapter) HandleIncomingWebhook(w http.ResponseWriter, r *http.Request) {
	var payload IncomingMessagePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	chatID := payload.From
	if payload.IsGroup && payload.GroupID != "" {
		chatID = payload.GroupID
	}

	cleanText := strings.TrimSpace(payload.Text)
	lowerText := strings.ToLower(cleanText)

	// 1. Handle Command: /stop or stop
	if lowerText == "/stop" || lowerText == "!stop" || lowerText == "/cancel" || lowerText == "stop" || lowerText == "batal" {
		if cancelVal, loaded := a.activeTasks.LoadAndDelete(chatID); loaded {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
			}
			go func() {
				_ = a.SendMessage(chatID, "🛑 *PROSES DIHENTIKAN*\n\nRespon AI yang sedang diproses telah berhasil dibatalkan.")
			}()
		} else {
			go func() {
				_ = a.SendMessage(chatID, "ℹ️ Tidak ada respon AI yang sedang diproses saat ini.")
			}()
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"stopped"}`))
		return
	}

	// 2. Handle Command: /new or /reset
	if lowerText == "/new" || lowerText == "/reset" || lowerText == "!reset" || lowerText == "/clear" {
		if cancelVal, loaded := a.activeTasks.LoadAndDelete(chatID); loaded {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
			}
		}

		session, err := a.db.GetOrCreateSession(a.channelID, chatID, payload.From)
		if err == nil && session != nil {
			_ = a.db.ClearSessionMessages(session.ID)
		}

		go func() {
			_ = a.SendMessage(chatID, "✨ *SESI BARU DIMULAI*\n\nKonteks percakapan dan riwayat pesan Anda telah direset.\nSilakan ajukan pertanyaan atau perintah baru!")
		}()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"cleared"}`))
		return
	}

	// 3. Handle Command: /help
	if lowerText == "/help" || lowerText == "!help" {
		go func() {
			helpText := "👋 *PANDUAN BOT WHATSAPP AI*\n\n" +
				"Silakan kirimkan pertanyaan atau perintah langsung di chat ini.\n\n" +
				"📌 *Daftar Perintah:*\n" +
				"• */new* - Mulai sesi baru & reset riwayat percakapan\n" +
				"• */stop* - Hentikan atau batalkan proses jawaban AI\n" +
				"• */status* - Cek status sesi & konfigurasi bot\n" +
				"• */help* - Tampilkan menu bantuan ini"
			_ = a.SendMessage(chatID, helpText)
		}()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"help"}`))
		return
	}

	// 4. Handle Command: /status
	if lowerText == "/status" || lowerText == "!status" {
		go func() {
			session, _ := a.db.GetOrCreateSession(a.channelID, chatID, payload.From)
			msgCount := 0
			if session != nil {
				msgCount, _ = a.db.CountSessionMessages(session.ID)
			}
			policy := a.db.GetResolvedPolicy(a.channelID, chatID)

			statusText := fmt.Sprintf("🤖 *STATUS ASISTEN AI*\n\n"+
				"• Channel: *%s*\n"+
				"• Chat ID: `%s`\n"+
				"• Riwayat Aktif: `%d pesan` (Maks: `%d`)\n"+
				"• Token Saver: `%s`\n\n"+
				"💡 Gunakan */stop* untuk membatalkan proses atau */new* untuk reset konteks.",
				a.name, chatID, msgCount, policy.MaxHistoryTurns, policy.TokenSaverMode)
			_ = a.SendMessage(chatID, statusText)
		}()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"status"}`))
		return
	}

	if cleanText == "" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"empty"}`))
		return
	}

	// Process Regular Message
	go func() {
		// Cancel any previous running task for this chat ID
		if cancelVal, loaded := a.activeTasks.LoadAndDelete(chatID); loaded {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(config.Get().Timeouts.HandlerSeconds)*time.Second)
		a.activeTasks.Store(chatID, cancel)
		defer func() {
			a.activeTasks.Delete(chatID)
			cancel()
		}()

		resp, err := a.orchestrator.ProcessMessage(ctx, agent.UserRequest{
			ChannelType:    "whatsapp",
			ChannelID:      a.channelID,
			ChannelName:    a.name,
			ChatID:         chatID,
			UserID:         payload.From,
			UserName:       payload.SenderName,
			UserPrompt:     payload.Text,
			AttachedFileMB: payload.FileSizeMB,
		})

		if err != nil {
			if ctx.Err() == context.Canceled {
				// Process was canceled by user (/stop)
				return
			}
			friendlyErr := agent.FormatUserFriendlyError(err)
			_ = a.SendMessage(chatID, friendlyErr)
			return
		}

		if resp != nil && resp.Text != "" {
			_ = a.SendMessage(chatID, resp.Text)
		}
	}()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"queued"}`))
}

