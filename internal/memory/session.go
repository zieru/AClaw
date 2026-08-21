package memory

import (
	"context"
	"fmt"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

// SessionManager manages conversation messages per session
type SessionManager struct {
	db *storage.DB
}

func NewSessionManager(db *storage.DB) *SessionManager {
	return &SessionManager{db: db}
}

// GetOrCreate gets an existing session or creates a new one
func (sm *SessionManager) GetOrCreate(channelID, chatID, userID string) (*storage.ChatSessionRecord, error) {
	return sm.db.GetOrCreateSession(channelID, chatID, userID)
}

// AddMessage appends a message to session
func (sm *SessionManager) AddMessage(sessionID, role, content string, tokens int) error {
	return sm.db.AddMessage(sessionID, role, content, tokens)
}

// GetHistory retrieves the recent messages converted into Provider ChatMessage format
func (sm *SessionManager) GetHistory(sessionID string, maxTurns int) ([]provider.ChatMessage, error) {
	if maxTurns <= 0 {
		maxTurns = 20
	}
	dbMsgs, err := sm.db.GetRecentMessages(sessionID, maxTurns)
	if err != nil {
		return nil, err
	}

	var chatMsgs []provider.ChatMessage
	for _, m := range dbMsgs {
		chatMsgs = append(chatMsgs, provider.ChatMessage{
			Role:    provider.MessageRole(m.Role),
			Content: m.Content,
		})
	}
	return chatMsgs, nil
}

// ResetSession clears all history in the session
func (sm *SessionManager) ResetSession(sessionID string) error {
	return sm.db.ClearSessionMessages(sessionID)
}

// ResetChatSessions clears all history across any session for the given chat ID
func (sm *SessionManager) ResetChatSessions(chatID string) error {
	return sm.db.ClearSessionsByChatID(chatID)
}

// SummarizeSession uses LLM to compress earlier conversation history
func (sm *SessionManager) SummarizeSession(ctx context.Context, sessionID string, p provider.Provider) (string, error) {
	msgs, err := sm.db.GetRecentMessages(sessionID, 30)
	if err != nil {
		return "", err
	}
	if len(msgs) < 10 {
		return "", nil // Not enough messages to warrant summarization
	}

	var convText string
	for _, m := range msgs {
		convText += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}

	req := provider.ChatRequest{
		Temperature: 0.3,
		MaxTokens:   500,
		Messages: []provider.ChatMessage{
			{
				Role:    provider.RoleSystem,
				Content: "Kamu adalah asisten perangkum percakapan. Rangkumlah poin-poin penting, preferensi pengguna, dan konteks percakapan di bawah ini secara padat dalam 2-4 kalimat ringkas.",
			},
			{
				Role:    provider.RoleUser,
				Content: convText,
			},
		},
	}

	resp, err := p.GenerateChat(ctx, req)
	if err != nil {
		return "", err
	}

	if resp.Content != "" {
		_ = sm.db.UpdateSessionSummary(sessionID, resp.Content)
	}

	return resp.Content, nil
}
