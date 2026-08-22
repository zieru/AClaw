package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"goassistant/internal/agent"
	"goassistant/internal/storage"

	_ "modernc.org/sqlite"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Manager manages multiple WhatsApp Native channel adapters using a shared sqlstore container
type Manager struct {
	container    *sqlstore.Container
	adapters     map[string]*NativeAdapter // channelID -> Adapter
	orchestrator *agent.Orchestrator
	db           *storage.DB
	mu           sync.RWMutex
}

var (
	globalManager *Manager
	managerMu     sync.Mutex
)

// InitManager initializes or returns the singleton WhatsApp Manager
func InitManager(dataDir string, orch *agent.Orchestrator, db *storage.DB) (*Manager, error) {
	managerMu.Lock()
	defer managerMu.Unlock()

	if globalManager != nil {
		return globalManager, nil
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("gagal membuat direktori data whatsapp: %w", err)
	}

	dbPath := filepath.Join(dataDir, "whatsapp.db")
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", dbPath)

	logger := waLog.Stdout("WAStore", "WARN", true)
	container, err := sqlstore.New(context.Background(), "sqlite", dsn, logger)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka database sqlstore whatsmeow: %w", err)
	}

	globalManager = &Manager{
		container:    container,
		adapters:     make(map[string]*NativeAdapter),
		orchestrator: orch,
		db:           db,
	}

	return globalManager, nil
}

// GetManager returns the global WhatsApp Manager instance
func GetManager() *Manager {
	managerMu.Lock()
	defer managerMu.Unlock()
	return globalManager
}

// GetAdapter retrieves an active adapter by channel ID
func (m *Manager) GetAdapter(channelID string) *NativeAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adapters[channelID]
}

// ListAdapters returns all active WhatsApp adapters
func (m *Manager) ListAdapters() []*NativeAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*NativeAdapter, 0, len(m.adapters))
	for _, a := range m.adapters {
		list = append(list, a)
	}
	return list
}

// CreateOrGetAdapter initializes or returns a WhatsApp adapter for a given channel record
func (m *Manager) CreateOrGetAdapter(ch *storage.ChannelRecord) (*NativeAdapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if adapter, exists := m.adapters[ch.ID]; exists {
		return adapter, nil
	}

	var settings WhatsAppSettings
	if ch.SettingsJSON != "" && ch.SettingsJSON != "{}" {
		_ = json.Unmarshal([]byte(ch.SettingsJSON), &settings)
	} else {
		settings = DefaultWhatsAppSettings()
	}

	var device *store.Device
	if ch.Identifier != "" {
		if jid, err := waTypes.ParseJID(ch.Identifier); err == nil {
			device, err = m.container.GetDevice(context.Background(), jid)
			if err != nil {
				log.Printf("⚠️ [Channel-WA] Gagal memuat device %s, membuat instance baru: %v", ch.Identifier, err)
			}
		}
	}

	if device == nil {
		device = m.container.NewDevice()
	}

	adapter := NewNativeAdapter(ch.ID, ch.Name, device, settings, m.orchestrator, m.db)
	m.adapters[ch.ID] = adapter

	return adapter, nil
}

// DeleteAdapter disconnects and removes a channel adapter
func (m *Manager) DeleteAdapter(channelID string) error {
	m.mu.Lock()
	adapter, exists := m.adapters[channelID]
	if exists {
		delete(m.adapters, channelID)
	}
	m.mu.Unlock()

	if exists && adapter != nil {
		_ = adapter.Stop()
		if adapter.device != nil {
			_ = adapter.device.Delete(context.Background())
		}
	}
	return nil
}

// Close gracefully stops all WhatsApp adapters and closes store
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, a := range m.adapters {
		_ = a.Stop()
		delete(m.adapters, id)
	}
	if m.container != nil {
		_ = m.container.Close()
	}
}
