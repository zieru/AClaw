package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"goassistant/internal/storage"
)

type ListTopicsResponse struct {
	Channels          []string                     `json:"channels"`
	AdminTopics       []*storage.ChatSessionRecord `json:"admin_topics"`
	WebTopics         []*storage.ChatSessionRecord `json:"web_topics"`
	AllTopics         []*storage.ChatSessionRecord `json:"all_topics"`
	ActiveAdminChatID string                       `json:"active_admin_chat_id"`
}

func (s *Server) handleListTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	channelFilter := strings.TrimSpace(r.URL.Query().Get("channel_id"))

	adminChatID := fmt.Sprintf("%d", sess.TelegramID)
	webChatID := fmt.Sprintf("web_%d", sess.TelegramID)

	adminTopics, _ := s.db.ListChatSessions("admin", adminChatID)
	if adminTopics == nil {
		adminTopics = []*storage.ChatSessionRecord{}
	}

	webTopics, _ := s.db.ListChatSessions("webadmin", webChatID)
	if webTopics == nil {
		webTopics = []*storage.ChatSessionRecord{}
	}

	allTopics, _ := s.db.ListAllChatSessions(channelFilter, 100)
	if allTopics == nil {
		allTopics = []*storage.ChatSessionRecord{}
	}

	channels, _ := s.db.ListChannelsWithTopics()
	channelSet := make(map[string]bool)
	for _, ch := range channels {
		channelSet[ch] = true
	}
	for _, defCh := range []string{"admin", "webadmin", "telegram", "whatsapp"} {
		if !channelSet[defCh] {
			channels = append(channels, defCh)
			channelSet[defCh] = true
		}
	}

	resp := ListTopicsResponse{
		Channels:          channels,
		AdminTopics:       adminTopics,
		WebTopics:         webTopics,
		AllTopics:         allTopics,
		ActiveAdminChatID: adminChatID,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type SwitchTopicRequest struct {
	ChannelID string `json:"channel_id"`
	ChatID    string `json:"chat_id"`
	SessionID string `json:"session_id"`
}

func (s *Server) handleSwitchTopic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req SwitchTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		http.Error(w, "Session ID diperlukan", http.StatusBadRequest)
		return
	}

	if req.ChannelID == "" {
		req.ChannelID = "admin"
	}
	if req.ChatID == "" {
		req.ChatID = fmt.Sprintf("%d", sess.TelegramID)
	}

	switched, err := s.db.SwitchChatSession(req.ChannelID, req.ChatID, req.SessionID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"session": switched,
	})
}

type NewTopicRequest struct {
	ChannelID string `json:"channel_id"`
	ChatID    string `json:"chat_id"`
	Title     string `json:"title"`
}

func (s *Server) handleNewTopic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := GetSessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req NewTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.ChannelID == "" {
		req.ChannelID = "admin"
	}
	if req.ChatID == "" {
		req.ChatID = fmt.Sprintf("%d", sess.TelegramID)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Topik Baru"
	}

	userIDStr := fmt.Sprintf("%d", sess.TelegramID)
	newSess, err := s.db.CreateChatSession(req.ChannelID, req.ChatID, userIDStr, title, true)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"session": newSess,
	})
}

func (s *Server) handleGetTopicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id parameter diperlukan", http.StatusBadRequest)
		return
	}

	recentMessages, err := s.db.GetRecentMessages(sessionID, 100)
	if err != nil || recentMessages == nil {
		recentMessages = []storage.ChatMessageRecord{}
	}

	// Reverse chronological order (oldest to newest)
	ordered := make([]storage.ChatMessageRecord, len(recentMessages))
	for i, m := range recentMessages {
		ordered[len(recentMessages)-1-i] = m
	}

	sessRec, _ := s.db.GetChatSession(sessionID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id": sessionID,
		"session":    sessRec,
		"messages":   ordered,
	})
}
