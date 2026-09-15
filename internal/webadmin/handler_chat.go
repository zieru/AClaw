package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"goassistant/internal/agent"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

type ChatRequest struct {
	Message  string `json:"message"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
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

	sendSSE("start", map[string]string{"status": "Memproses permintaan..."})

	chatIDStr := fmt.Sprintf("web_%d", sess.TelegramID)
	userIDStr := fmt.Sprintf("%d", sess.TelegramID)
	userNameStr := fmt.Sprintf("Admin (%d)", sess.TelegramID)

	userReq := agent.UserRequest{
		ChannelType:    "web",
		ChannelID:      "webadmin",
		ChannelName:    "Web Admin Control Plane",
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

	agentResp, err := s.orchestrator.ProcessMessage(r.Context(), userReq)
	if err != nil {
		sendSSE("error", map[string]string{"error": err.Error()})
	} else {
		respText := ""
		if agentResp != nil {
			respText = agentResp.Text
		}
		sendSSE("done", map[string]string{"response": respText})
	}
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

	chatIDStr := fmt.Sprintf("web_%d", sess.TelegramID)
	userIDStr := fmt.Sprintf("%d", sess.TelegramID)

	activeSession, err := s.db.GetOrCreateSession("webadmin", chatIDStr, userIDStr)
	if err != nil || activeSession == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"messages": []storage.ChatMessageRecord{}})
		return
	}

	recentMessages, err := s.db.GetRecentMessages(activeSession.ID, 50)
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
