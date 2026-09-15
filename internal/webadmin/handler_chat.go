package webadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"goassistant/internal/agent"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

type ChatRequest struct {
	Message   string `json:"message"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	ChannelID string `json:"channel_id"`
	ChatID    string `json:"chat_id"`
	SessionID string `json:"session_id"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var chatReq ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	prompt := strings.TrimSpace(chatReq.Message)
	if prompt == "" {
		http.Error(w, "Message tidak boleh kosong", http.StatusBadRequest)
		return
	}

	channelID := strings.TrimSpace(chatReq.ChannelID)
	if channelID == "" {
		channelID = "admin" // Default ke percakapan admin Telegram yang tersinkronisasi
	}

	chatIDStr := strings.TrimSpace(chatReq.ChatID)
	if chatIDStr == "" {
		chatIDStr = fmt.Sprintf("%d", sess.TelegramID)
	}

	userIDStr := fmt.Sprintf("%d", sess.TelegramID)
	userNameStr := fmt.Sprintf("Admin (%d)", sess.TelegramID)

	channelType := "web"
	channelName := "Web Admin Control Plane"
	if channelID == "admin" {
		channelType = "telegram_admin"
		channelName = "Telegram Admin PM"
	} else if channelID == "telegram" {
		channelType = "telegram"
		channelName = "Telegram Channel"
	} else if channelID == "whatsapp" {
		channelType = "whatsapp"
		channelName = "WhatsApp Channel"
	}

	// Switch session if specific topic ID requested
	if chatReq.SessionID != "" {
		_, _ = s.db.SwitchChatSession(channelID, chatIDStr, chatReq.SessionID)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Set SSE Headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sendSSE := func(eventType string, data interface{}) {
		payload, err := json.Marshal(data)
		if err == nil {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
			flusher.Flush()
		}
	}

	// Handle direct /stop or /cancel command
	if prompt == "/stop" || prompt == "/cancel" {
		s.stopActiveChat(chatReq.SessionID, sess.TelegramID)
		sendSSE("start", map[string]string{"status": "Menghentikan proses..."})
		sendSSE("chunk", map[string]string{"text": "🛑 **[Perintah /stop diterima]** Seluruh proses komputasi AI telah dihentikan."})
		sendSSE("done", map[string]string{"response": "🛑 [Perintah /stop diterima] Seluruh proses komputasi AI telah dihentikan."})
		return
	}

	// Setup cancellable context for this active chat
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	chatKey := fmt.Sprintf("%d", sess.TelegramID)
	if chatReq.SessionID != "" {
		chatKey = chatReq.SessionID
	}
	s.activeChats.Store(chatKey, cancel)
	defer s.activeChats.Delete(chatKey)

	sendSSE("start", map[string]string{"status": "Memproses permintaan..."})

	userReq := agent.UserRequest{
		ChannelType:    channelType,
		ChannelID:      channelID,
		ChannelName:    channelName,
		ChatID:         chatIDStr,
		UserID:         userIDStr,
		UserName:       userNameStr,
		UserPrompt:     prompt,
		PreferredModel: chatReq.Model,
		PreferredProv:  chatReq.Provider,
		OnProgress: func(status string) {
			sendSSE("progress", map[string]string{"status": status})
		},
		OnStreamChunk: func(chunk provider.StreamChunk) {
			if chunk.Thinking != "" {
				sendSSE("thinking", map[string]string{"text": chunk.Thinking})
			}
			if chunk.Content != "" {
				sendSSE("chunk", map[string]string{"text": chunk.Content})
			}
		},
	}

	if s.orchestrator == nil {
		sendSSE("error", map[string]string{"error": "Orchestrator engine belum diinisialisasi"})
		return
	}

	agentResp, err := s.orchestrator.ProcessMessage(ctx, userReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) || strings.Contains(err.Error(), "canceled") {
			sendSSE("chunk", map[string]string{"text": "\n\n🛑 *[Proses dihentikan oleh pengguna /stop]*"})
			sendSSE("done", map[string]string{"response": "\n\n🛑 *[Proses dihentikan oleh pengguna /stop]*", "status": "stopped"})
		} else {
			sendSSE("error", map[string]string{"error": err.Error()})
		}
	} else {
		respText := ""
		if agentResp != nil {
			respText = agentResp.Text
		}
		sendSSE("done", map[string]string{"response": respText})
	}
}

func (s *Server) stopActiveChat(sessionID string, telegramID int64) bool {
	stopped := false
	if sessionID != "" {
		if cancelVal, ok := s.activeChats.LoadAndDelete(sessionID); ok {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
				stopped = true
			}
		}
	}
	userKey := fmt.Sprintf("%d", telegramID)
	if cancelVal, ok := s.activeChats.LoadAndDelete(userKey); ok {
		if cancel, ok := cancelVal.(context.CancelFunc); ok {
			cancel()
			stopped = true
		}
	}
	return stopped
}

func (s *Server) handleChatStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	stopped := s.stopActiveChat(req.SessionID, sess.TelegramID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"stopped": stopped,
		"message": "Perintah /stop berhasil dikirimkan.",
	})
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	activeModel := "gemini-2.5-flash"
	activeProv := "gemini"
	if s.db != nil {
		if pol, err := s.db.GetPolicy("global", "system"); err == nil && pol != nil {
			if pol.ModelOverride != "" {
				activeModel = pol.ModelOverride
			}
		}
	}

	type ModelOption struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}

	models := []ModelOption{}

	if s.db != nil {
		if provRecords, err := s.db.ListProviders(); err == nil {
			for _, pr := range provRecords {
				if !pr.IsActive {
					continue
				}
				for _, m := range pr.Models {
					models = append(models, ModelOption{
						ID:       m,
						Name:     fmt.Sprintf("%s (%s)", m, pr.Name),
						Provider: pr.Type,
					})
				}
				if len(pr.Models) == 0 && pr.DefaultModel != "" {
					models = append(models, ModelOption{
						ID:       pr.DefaultModel,
						Name:     fmt.Sprintf("%s (%s)", pr.DefaultModel, pr.Name),
						Provider: pr.Type,
					})
				}
			}
		}

		if combos, err := s.db.ListCombos(); err == nil {
			for _, c := range combos {
				models = append(models, ModelOption{
					ID:       c.Name,
					Name:     fmt.Sprintf("🔀 Combo: %s (%s)", c.Name, c.Strategy),
					Provider: "combo",
				})
			}
		}
	}

	if len(models) == 0 {
		models = []ModelOption{
			{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Provider: "gemini"},
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Provider: "gemini"},
			{ID: "gpt-4o-mini", Name: "GPT-4o Mini", Provider: "openai"},
			{ID: "gpt-4o", Name: "GPT-4o", Provider: "openai"},
			{ID: "claude-3-5-sonnet-20241022", Name: "Claude 3.5 Sonnet", Provider: "anthropic"},
			{ID: "free", Name: "OpenCode Free AI", Provider: "free_openai"},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"active_model":    activeModel,
		"active_provider": activeProv,
		"models":          models,
	})
}

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	if channelID == "" {
		channelID = "admin"
	}
	chatIDStr := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	if chatIDStr == "" {
		chatIDStr = fmt.Sprintf("%d", sess.TelegramID)
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	userIDStr := fmt.Sprintf("%d", sess.TelegramID)

	var activeSession *storage.ChatSessionRecord
	var err error
	if sessionID != "" {
		activeSession, err = s.db.GetChatSession(sessionID)
	}
	if activeSession == nil {
		activeSession, err = s.db.GetOrCreateSession(channelID, chatIDStr, userIDStr)
	}

	if err != nil || activeSession == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"messages": []storage.ChatMessageRecord{}})
		return
	}

	recentMessages, err := s.db.GetRecentMessages(activeSession.ID, 100)
	if err != nil || recentMessages == nil {
		recentMessages = []storage.ChatMessageRecord{}
	}

	// Reverse so chronological order (oldest to newest)
	orderedMessages := make([]storage.ChatMessageRecord, len(recentMessages))
	for i, m := range recentMessages {
		orderedMessages[len(recentMessages)-1-i] = m
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id": activeSession.ID,
		"channel_id": activeSession.ChannelID,
		"chat_id":    activeSession.ChatID,
		"title":      activeSession.Title,
		"messages":   orderedMessages,
	})
}

func (s *Server) handleClearChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	chatIDStr := fmt.Sprintf("web_%d", sess.TelegramID)
	_ = s.db.ClearActiveSessionMessages("webadmin", chatIDStr)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Riwayat chat berhasil direset"})
}
