package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/omniroute"
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
	err := m.db.AddMemoryItem(scope, scopeID, key, content, category)
	if err == nil {
		m.syncUpstreamMemory(scope, scopeID, key, content, category)
	}
	return err
}

// UpsertFact saves or updates a learned fact or profile item (prevents duplicate keys)
func (m *Manager) UpsertFact(scope, scopeID, key, content, category string) error {
	err := m.db.UpsertMemoryItem(scope, scopeID, key, content, category)
	if err == nil {
		m.syncUpstreamMemory(scope, scopeID, key, content, category)
	}
	return err
}

func (m *Manager) syncUpstreamMemory(scope, scopeID, key, content, category string) {
	if cfg := config.Get(); cfg != nil && cfg.OmniRoute.Enabled && cfg.OmniRoute.UseUpstreamMemory {
		if client := omniroute.GetClient(); client != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				meta := map[string]interface{}{
					"scope":    scope,
					"scope_id": scopeID,
					"category": category,
					"source":   "goassistant",
				}
				_ = client.SaveMemory(ctx, key, content, "factual", meta)
			}()
		}
	}
}

// ListMemories returns all memory items for a specific scope and scope ID
func (m *Manager) ListMemories(scope, scopeID string) ([]storage.MemoryRecord, error) {
	return m.db.ListMemoryItems(scope, scopeID)
}

// SearchMemories searches memory items by keyword/query
func (m *Manager) SearchMemories(scope, scopeID, query string) ([]storage.MemoryRecord, error) {
	return m.db.SearchMemoryItems(scope, scopeID, query)
}

// DeleteMemoryItem deletes a memory item by its ID
func (m *Manager) DeleteMemoryItem(id string) error {
	return m.db.DeleteMemoryItem(id)
}

// DeleteMemoryByKey deletes a memory item by its key
func (m *Manager) DeleteMemoryByKey(scope, scopeID, key string) error {
	return m.db.DeleteMemoryByKey(scope, scopeID, key)
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

	// 1b. OmniRoute Centralized Shared Memory
	if cfg := config.Get(); cfg != nil && cfg.OmniRoute.Enabled && cfg.OmniRoute.UseUpstreamMemory {
		if client := omniroute.GetClient(); client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if omniMems, err := client.ListMemories(ctx); err == nil && len(omniMems) > 0 {
				sb.WriteString("### OmniRoute Shared Facts & Knowledge:\n")
				for _, om := range omniMems {
					sb.WriteString(fmt.Sprintf("- [%s] %s\n", om.Key, om.Content))
				}
				sb.WriteString("\n")
			}
			cancel()
		}
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
