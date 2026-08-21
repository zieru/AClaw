package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	From        string  `json:"from"`
	SenderName  string  `json:"sender_name"`
	Text        string  `json:"text"`
	IsGroup     bool    `json:"is_group"`
	GroupID     string  `json:"group_id"`
	FileSizeMB  float64 `json:"file_size_mb"`
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

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(config.Get().Timeouts.HandlerSeconds)*time.Second)
		defer cancel()

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
