package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/omniroute"
	"goassistant/internal/storage"
)

// Manager manages memory directly and exclusively via OmniRoute's centralized Memory API.
// Dual memory with SQLite has been eliminated to prevent state collision.
type Manager struct {
	client *omniroute.Client
}

// NewManager creates a new OmniRoute-backed memory manager
func NewManager(client ...*omniroute.Client) *Manager {
	var c *omniroute.Client
	if len(client) > 0 && client[0] != nil {
		c = client[0]
	} else {
		c = omniroute.GetClient()
	}
	return &Manager{client: c}
}

func (m *Manager) getClient() *omniroute.Client {
	if m.client != nil {
		return m.client
	}
	return omniroute.GetClient()
}

// SaveFact saves a learned fact or profile item to OmniRoute
func (m *Manager) SaveFact(scope, scopeID, key, content, category string) error {
	return m.UpsertFact(scope, scopeID, key, content, category)
}

// UpsertFact saves or updates a learned fact in OmniRoute (prevents duplicate keys)
func (m *Manager) UpsertFact(scope, scopeID, key, content, category string) error {
	client := m.getClient()
	if client == nil {
		return fmt.Errorf("omniroute client belum terhubung")
	}

	sessionID := formatSessionID(scope, scopeID)
	metadata := map[string]interface{}{
		"scope":    scope,
		"scope_id": scopeID,
		"category": category,
		"source":   "goassistant",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return client.UpsertMemory(ctx, key, content, "factual", sessionID, metadata)
}

// ListMemories returns all memory items from OmniRoute for a specific scope and scope ID
func (m *Manager) ListMemories(scope, scopeID string) ([]storage.MemoryRecord, error) {
	client := m.getClient()
	if client == nil {
		return nil, fmt.Errorf("omniroute client belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID := formatSessionID(scope, scopeID)
	items, err := client.ListMemories(ctx, omniroute.MemoryFilter{
		SessionID: sessionID,
		Limit:     200,
	})
	if err != nil {
		return nil, err
	}

	records := make([]storage.MemoryRecord, 0, len(items))
	for _, it := range items {
		if matchesScope(it, scope, scopeID) {
			records = append(records, memoryItemToRecord(it))
		}
	}
	return records, nil
}

// SearchMemories searches memory items in OmniRoute by keyword/query
func (m *Manager) SearchMemories(scope, scopeID, query string) ([]storage.MemoryRecord, error) {
	client := m.getClient()
	if client == nil {
		return nil, fmt.Errorf("omniroute client belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID := formatSessionID(scope, scopeID)
	items, err := client.SearchMemories(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}

	records := make([]storage.MemoryRecord, 0, len(items))
	for _, it := range items {
		if matchesScope(it, scope, scopeID) {
			records = append(records, memoryItemToRecord(it))
		}
	}
	return records, nil
}

// DeleteMemoryItem deletes a memory item in OmniRoute by its ID
func (m *Manager) DeleteMemoryItem(id string) error {
	client := m.getClient()
	if client == nil {
		return fmt.Errorf("omniroute client belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return client.DeleteMemory(ctx, id)
}

// DeleteMemoryByKey deletes a memory item in OmniRoute by its key within a scope
func (m *Manager) DeleteMemoryByKey(scope, scopeID, key string) error {
	client := m.getClient()
	if client == nil {
		return fmt.Errorf("omniroute client belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID := formatSessionID(scope, scopeID)
	items, err := client.ListMemories(ctx, omniroute.MemoryFilter{
		SessionID: sessionID,
		Q:         key,
		Limit:     50,
	})
	if err != nil {
		return err
	}

	for _, it := range items {
		if strings.EqualFold(it.Key, key) && matchesScope(it, scope, scopeID) {
			return client.DeleteMemory(ctx, it.ID)
		}
	}

	return fmt.Errorf("memori dengan kunci '%s' tidak ditemukan di OmniRoute", key)
}

// GetContextMemory retrieves formatted memory context from OmniRoute for system prompt injection
func (m *Manager) GetContextMemory(channelID, userID string) (string, error) {
	client := m.getClient()
	if client == nil {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	allMems, err := client.ListMemories(ctx, omniroute.MemoryFilter{Limit: 200})
	if err != nil || len(allMems) == 0 {
		return "", nil
	}

	var globals []omniroute.MemoryItem
	var channelMems []omniroute.MemoryItem
	var userMems []omniroute.MemoryItem
	var otherMems []omniroute.MemoryItem

	chanSession := formatSessionID("channel", channelID)
	userSession := formatSessionID("user", userID)

	for _, it := range allMems {
		switch {
		case it.SessionID == "global:system" || it.SessionID == "global" || getScope(it) == "global":
			globals = append(globals, it)
		case channelID != "" && (it.SessionID == chanSession || (getScope(it) == "channel" && getScopeID(it) == channelID)):
			channelMems = append(channelMems, it)
		case userID != "" && (it.SessionID == userSession || (getScope(it) == "user" && getScopeID(it) == userID)):
			userMems = append(userMems, it)
		default:
			if it.SessionID == "" {
				otherMems = append(otherMems, it)
			}
		}
	}

	var sb strings.Builder

	if len(globals) > 0 {
		sb.WriteString("### OmniRoute Global Facts & SOP:\n")
		for _, item := range globals {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	if len(otherMems) > 0 {
		sb.WriteString("### OmniRoute Shared Knowledge:\n")
		for _, item := range otherMems {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	if len(channelMems) > 0 {
		sb.WriteString("### Channel Context:\n")
		for _, item := range channelMems {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	if len(userMems) > 0 {
		sb.WriteString("### User Profile & Preferences:\n")
		for _, item := range userMems {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// ClearUserMemory deletes all memories associated with a user from OmniRoute
func (m *Manager) ClearUserMemory(userID string) error {
	return m.clearBySession(formatSessionID("user", userID))
}

// ClearChannelMemory deletes all memories associated with a channel from OmniRoute
func (m *Manager) ClearChannelMemory(channelID string) error {
	return m.clearBySession(formatSessionID("channel", channelID))
}

func (m *Manager) clearBySession(sessionID string) error {
	client := m.getClient()
	if client == nil {
		return fmt.Errorf("omniroute client belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	items, err := client.ListMemories(ctx, omniroute.MemoryFilter{
		SessionID: sessionID,
		Limit:     200,
	})
	if err != nil {
		return err
	}

	for _, it := range items {
		_ = client.DeleteMemory(ctx, it.ID)
	}
	return nil
}

func formatSessionID(scope, scopeID string) string {
	if scope == "" && scopeID == "" {
		return ""
	}
	if scopeID == "" {
		return scope
	}
	return fmt.Sprintf("%s:%s", scope, scopeID)
}

func getScope(item omniroute.MemoryItem) string {
	if item.Metadata != nil {
		if s, ok := item.Metadata["scope"].(string); ok && s != "" {
			return s
		}
	}
	if strings.Contains(item.SessionID, ":") {
		return strings.SplitN(item.SessionID, ":", 2)[0]
	}
	return ""
}

func getScopeID(item omniroute.MemoryItem) string {
	if item.Metadata != nil {
		if sid, ok := item.Metadata["scope_id"].(string); ok && sid != "" {
			return sid
		}
	}
	if strings.Contains(item.SessionID, ":") {
		return strings.SplitN(item.SessionID, ":", 2)[1]
	}
	return item.SessionID
}

func matchesScope(item omniroute.MemoryItem, scope, scopeID string) bool {
	if scope == "" && scopeID == "" {
		return true
	}
	targetSession := formatSessionID(scope, scopeID)
	if item.SessionID == targetSession {
		return true
	}
	itemScope := getScope(item)
	itemScopeID := getScopeID(item)
	if scope != "" && itemScope != "" && itemScope != scope {
		return false
	}
	if scopeID != "" && itemScopeID != "" && itemScopeID != scopeID {
		return false
	}
	return true
}

func memoryItemToRecord(item omniroute.MemoryItem) storage.MemoryRecord {
	scope := getScope(item)
	if scope == "" {
		scope = "global"
	}
	scopeID := getScopeID(item)
	if scopeID == "" {
		scopeID = "system"
	}
	category := item.Type
	if item.Metadata != nil {
		if cat, ok := item.Metadata["category"].(string); ok && cat != "" {
			category = cat
		}
	}
	if category == "" {
		category = "fact"
	}

	var createdAt, updatedAt time.Time
	if item.CreatedAt != "" {
		createdAt, _ = time.Parse(time.RFC3339, item.CreatedAt)
	}
	if item.UpdatedAt != "" {
		updatedAt, _ = time.Parse(time.RFC3339, item.UpdatedAt)
	}

	return storage.MemoryRecord{
		ID:        item.ID,
		Scope:     scope,
		ScopeID:   scopeID,
		KeyTag:    item.Key,
		Content:   item.Content,
		Category:  category,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
}
