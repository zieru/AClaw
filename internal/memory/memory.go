package memory

import (
	"fmt"
	"strings"

	"goassistant/internal/storage"
)

// Manager manages short-term and long-term memory
type Manager struct {
	db *storage.DB
}

// NewManager creates a new memory manager
func NewManager(db *storage.DB) *Manager {
	return &Manager{db: db}
}

// SaveFact saves a learned fact or profile item
func (m *Manager) SaveFact(scope, scopeID, key, content, category string) error {
	return m.db.AddMemoryItem(scope, scopeID, key, content, category)
}

// GetContextMemory retrieves formatted memory context for system prompt injection
func (m *Manager) GetContextMemory(channelID, userID string) (string, error) {
	var sb strings.Builder

	// 1. Global memories
	globals, _ := m.db.ListMemoryItems("global", "system")
	if len(globals) > 0 {
		sb.WriteString("### Global Memories / Knowledge:\n")
		for _, item := range globals {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.KeyTag, item.Content))
		}
		sb.WriteString("\n")
	}

	// 2. Channel memories
	if channelID != "" {
		chanMems, _ := m.db.ListMemoryItems("channel", channelID)
		if len(chanMems) > 0 {
			sb.WriteString("### Channel Context:\n")
			for _, item := range chanMems {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.KeyTag, item.Content))
			}
			sb.WriteString("\n")
		}
	}

	// 3. User memories
	if userID != "" {
		userMems, _ := m.db.ListMemoryItems("user", userID)
		if len(userMems) > 0 {
			sb.WriteString("### User Profile & Preferences:\n")
			for _, item := range userMems {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.KeyTag, item.Content))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String(), nil
}

// ClearUserMemory deletes all memories associated with a user
func (m *Manager) ClearUserMemory(userID string) error {
	items, err := m.db.ListMemoryItems("user", userID)
	if err != nil {
		return err
	}
	for _, item := range items {
		_ = m.db.DeleteMemoryItem(item.ID)
	}
	return nil
}

// ClearChannelMemory deletes all memories associated with a channel
func (m *Manager) ClearChannelMemory(channelID string) error {
	items, err := m.db.ListMemoryItems("channel", channelID)
	if err != nil {
		return err
	}
	for _, item := range items {
		_ = m.db.DeleteMemoryItem(item.ID)
	}
	return nil
}
