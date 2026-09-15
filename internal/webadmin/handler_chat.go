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
	Message        string `json:"message"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	ChannelID      string `json:"channel_id"`
	ChatID         string `json:"chat_id"`
	SessionID      string `json:"session_id"`
	ThinkingLevel  string `json:"thinking_level"`
	ThinkingBudget int    `json:"thinking_budget"`
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
		ThinkingLevel:  chatReq.ThinkingLevel,
		ThinkingBudget: chatReq.ThinkingBudget,
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

	activeModel := "gemini-3.8-flash"
	activeProv := "google"
	if s.db != nil {
		if pol, err := s.db.GetPolicy("global", "system"); err == nil && pol != nil {
			if pol.ModelOverride != "" {
				activeModel = pol.ModelOverride
			}
		}
	}

	// 1. Built-in standard providers & models according to user specifications
	standardProviders := []ProviderWithModels{
		{
			ID:   "google",
			Name: "Google (Gemini)",
			Type: "gemini",
			Models: []ModelDetail{
				{ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash (Recommended)", ProviderID: "google", ThinkingConfig: ResolveThinkingForModel("gemini-3.8-flash")},
				{ID: "gemini-3.7-flash", Name: "Gemini 3.7 Flash", ProviderID: "google", ThinkingConfig: ResolveThinkingForModel("gemini-3.7-flash")},
				{ID: "gemini-3.1-pro", Name: "Gemini 3.1 Pro (Deep Thinking)", ProviderID: "google", ThinkingConfig: ResolveThinkingForModel("gemini-3.1-pro")},
				{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", ProviderID: "google", ThinkingConfig: ResolveThinkingForModel("gemini-2.5-flash")},
				{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", ProviderID: "google", ThinkingConfig: ResolveThinkingForModel("gemini-2.5-pro")},
			},
		},
		{
			ID:   "openai",
			Name: "OpenAI",
			Type: "openai",
			Models: []ModelDetail{
				{ID: "o3-mini", Name: "o3-mini (Reasoning)", ProviderID: "openai", ThinkingConfig: ResolveThinkingForModel("o3-mini")},
				{ID: "o1", Name: "o1 (High Reasoning)", ProviderID: "openai", ThinkingConfig: ResolveThinkingForModel("o1")},
				{ID: "o1-mini", Name: "o1-mini", ProviderID: "openai", ThinkingConfig: ResolveThinkingForModel("o1-mini")},
				{ID: "gpt-4o", Name: "GPT-4o (Omni)", ProviderID: "openai", ThinkingConfig: nil},
				{ID: "gpt-4o-mini", Name: "GPT-4o Mini", ProviderID: "openai", ThinkingConfig: nil},
			},
		},
		{
			ID:   "deepseek",
			Name: "DeepSeek",
			Type: "deepseek",
			Models: []ModelDetail{
				{ID: "deepseek-r1", Name: "DeepSeek R1 (Reasoning)", ProviderID: "deepseek", ThinkingConfig: ResolveThinkingForModel("deepseek-r1")},
				{ID: "deepseek-v4", Name: "DeepSeek V4", ProviderID: "deepseek", ThinkingConfig: ResolveThinkingForModel("deepseek-v4")},
				{ID: "deepseek-chat", Name: "DeepSeek Chat (V3)", ProviderID: "deepseek", ThinkingConfig: nil},
			},
		},
		{
			ID:   "anthropic",
			Name: "Anthropic",
			Type: "anthropic",
			Models: []ModelDetail{
				{ID: "claude-4-sonnet", Name: "Claude 4 Sonnet", ProviderID: "anthropic", ThinkingConfig: ResolveThinkingForModel("claude-4-sonnet")},
				{ID: "claude-4-opus", Name: "Claude 4 Opus", ProviderID: "anthropic", ThinkingConfig: ResolveThinkingForModel("claude-4-opus")},
				{ID: "claude-3-7-sonnet", Name: "Claude 3.7 Sonnet (Hybrid)", ProviderID: "anthropic", ThinkingConfig: ResolveThinkingForModel("claude-3-7-sonnet")},
				{ID: "claude-3-5-sonnet-20241022", Name: "Claude 3.5 Sonnet", ProviderID: "anthropic", ThinkingConfig: ResolveThinkingForModel("claude-3-5-sonnet-20241022")},
			},
		},
		{
			ID:   "xai",
			Name: "xAI (Grok)",
			Type: "xai",
			Models: []ModelDetail{
				{ID: "grok-4.6-reasoning", Name: "Grok 4.6 Reasoning", ProviderID: "xai", ThinkingConfig: ResolveThinkingForModel("grok-4.6-reasoning")},
				{ID: "grok-4.6", Name: "Grok 4.6", ProviderID: "xai", ThinkingConfig: ResolveThinkingForModel("grok-4.6")},
				{ID: "grok-4.5", Name: "Grok 4.5", ProviderID: "xai", ThinkingConfig: ResolveThinkingForModel("grok-4.5")},
			},
		},
		{
			ID:   "alibaba",
			Name: "Alibaba Cloud (Qwen)",
			Type: "alibaba",
			Models: []ModelDetail{
				{ID: "qwen3.8-max", Name: "Qwen 3.8 Max", ProviderID: "alibaba", ThinkingConfig: ResolveThinkingForModel("qwen3.8-max")},
				{ID: "qwen3.8-max-preview", Name: "Qwen 3.8 Max Preview", ProviderID: "alibaba", ThinkingConfig: ResolveThinkingForModel("qwen3.8-max-preview")},
				{ID: "qwen3.5-instruct", Name: "Qwen 3.5 Instruct", ProviderID: "alibaba", ThinkingConfig: ResolveThinkingForModel("qwen3.5-instruct")},
			},
		},
		{
			ID:   "mistral",
			Name: "Mistral AI",
			Type: "mistral",
			Models: []ModelDetail{
				{ID: "mistral-small-4", Name: "Mistral Small 4", ProviderID: "mistral", ThinkingConfig: ResolveThinkingForModel("mistral-small-4")},
				{ID: "mistral-medium-3.5", Name: "Mistral Medium 3.5", ProviderID: "mistral", ThinkingConfig: ResolveThinkingForModel("mistral-medium-3.5")},
			},
		},
		{
			ID:   "minimax",
			Name: "MiniMax",
			Type: "minimax",
			Models: []ModelDetail{
				{ID: "minimax-m1", Name: "MiniMax M1", ProviderID: "minimax", ThinkingConfig: ResolveThinkingForModel("minimax-m1")},
				{ID: "minimax-m2", Name: "MiniMax M2", ProviderID: "minimax", ThinkingConfig: ResolveThinkingForModel("minimax-m2")},
				{ID: "minimax-m3", Name: "MiniMax M3", ProviderID: "minimax", ThinkingConfig: ResolveThinkingForModel("minimax-m3")},
			},
		},
	}

	providerMap := make(map[string]*ProviderWithModels)
	for i := range standardProviders {
		providerMap[standardProviders[i].ID] = &standardProviders[i]
	}

	// 2. Append DB configured providers & custom models
	if s.db != nil {
		if provRecords, err := s.db.ListProviders(); err == nil {
			for _, pr := range provRecords {
				if !pr.IsActive {
					continue
				}
				provID := strings.ToLower(pr.Type)
				if provID == "" {
					provID = strings.ToLower(pr.Name)
				}
				target, exists := providerMap[provID]
				if !exists {
					newProv := &ProviderWithModels{
						ID:     provID,
						Name:   pr.Name,
						Type:   pr.Type,
						Models: []ModelDetail{},
					}
					providerMap[provID] = newProv
					standardProviders = append(standardProviders, *newProv)
					target = providerMap[provID]
				}

				for _, m := range pr.Models {
					already := false
					for _, existingM := range target.Models {
						if strings.EqualFold(existingM.ID, m) {
							already = true
							break
						}
					}
					if !already {
						target.Models = append(target.Models, ModelDetail{
							ID:             m,
							Name:           m,
							ProviderID:     provID,
							ThinkingConfig: ResolveThinkingForModel(m),
						})
					}
				}
			}
		}

		// 3. Append Model Combos
		if combos, err := s.db.ListCombos(); err == nil && len(combos) > 0 {
			comboProv := ProviderWithModels{
				ID:     "combo",
				Name:   "Model Combos (Fallback)",
				Type:   "combo",
				Models: []ModelDetail{},
			}
			for _, c := range combos {
				comboProv.Models = append(comboProv.Models, ModelDetail{
					ID:             c.Name,
					Name:           fmt.Sprintf("🔀 Combo: %s (%s)", c.Name, c.Strategy),
					ProviderID:     "combo",
					ThinkingConfig: nil,
				})
			}
			standardProviders = append(standardProviders, comboProv)
		}
	}

	// 4. Flatten models list for backward compatibility
	flatModels := []map[string]interface{}{}
	for _, p := range standardProviders {
		for _, m := range p.Models {
			flatModels = append(flatModels, map[string]interface{}{
				"id":              m.ID,
				"name":            fmt.Sprintf("%s (%s)", m.Name, p.Name),
				"provider":        p.ID,
				"thinking_config": m.ThinkingConfig,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"active_model":     activeModel,
		"active_provider":  activeProv,
		"providers":        standardProviders,
		"models":           flatModels,
		"thinking_catalog": GetThinkingCatalog(),
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
