package tools

import (
	"context"
	"fmt"
	"strings"

	"goassistant/internal/storage"
)

// MemoryManager defines the interface for interacting with persistent facts and memories
type MemoryManager interface {
	UpsertFact(scope, scopeID, key, content, category string) error
	ListMemories(scope, scopeID string) ([]storage.MemoryRecord, error)
	SearchMemories(scope, scopeID, query string) ([]storage.MemoryRecord, error)
	DeleteMemoryItem(id string) error
	DeleteMemoryByKey(scope, scopeID, key string) error
	ClearUserMemory(userID string) error
	ClearChannelMemory(channelID string) error
}

// UserMemoryTool provides capability for the AI agent to persist, search, and manage long-term user memories and facts.
type UserMemoryTool struct {
	manager MemoryManager
}

// NewUserMemoryTool creates a new UserMemoryTool instance
func NewUserMemoryTool(mgr MemoryManager) *UserMemoryTool {
	return &UserMemoryTool{
		manager: mgr,
	}
}

func (m *UserMemoryTool) Name() string {
	return "user_memory"
}

func (m *UserMemoryTool) Description() string {
	return "Tool untuk menyimpan, mencari, melihat, dan menghapus catatan jangka panjang tentang preferensi pengguna, fakta profil, to-do list, catatan proyek, atau informasi penting lainnya ke database lokal goassistant."
}

func (m *UserMemoryTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"action": {
				Type:        "string",
				Description: "Aksi memori: 'save' (simpan fakta/catatan baru atau update), 'list' (tampilkan seluruh catatan), 'search' (cari catatan dengan kata kunci), 'delete' (hapus catatan berdasarkan key atau id), 'clear' (hapus semua catatan). Default: list.",
				Enum:        []string{"save", "list", "search", "delete", "clear"},
			},
			"key": {
				Type:        "string",
				Description: "Kunci/tag ringkas catatan (contoh: 'nama_panggilan', 'makanan_favorit', 'rekening_bca', 'bahasa_coding', 'proyek_website'). Wajib untuk aksi 'save' dan 'delete' jika ID tidak diberikan.",
			},
			"content": {
				Type:        "string",
				Description: "Isi fakta atau detail informasi yang ingin disimpan. Wajib untuk aksi 'save'.",
			},
			"category": {
				Type:        "string",
				Description: "Kategori catatan: 'preference', 'profile', 'fact', 'todo', 'work', 'note'. Default: 'fact'.",
			},
			"query": {
				Type:        "string",
				Description: "Kata kunci pencarian untuk aksi 'search'.",
			},
			"id": {
				Type:        "string",
				Description: "ID spesifik catatan jika ingin menghapus berdasarkan ID untuk aksi 'delete'.",
			},
			"scope": {
				Type:        "string",
				Description: "Lingkup memori: 'user' (catatan spesifik pengguna saat ini), 'channel' (catatan untuk grup/channel saat ini), atau 'global'. Default: 'user'.",
				Enum:        []string{"user", "channel", "global"},
			},
		},
		Required: []string{"action"},
	}
}

func (m *UserMemoryTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if m.manager == nil {
		return "", fmt.Errorf("memory manager belum terinisialisasi")
	}

	action := "list"
	if act, ok := args["action"].(string); ok && strings.TrimSpace(act) != "" {
		action = strings.ToLower(strings.TrimSpace(act))
	}

	scope := "user"
	if sc, ok := args["scope"].(string); ok && strings.TrimSpace(sc) != "" {
		scope = strings.ToLower(strings.TrimSpace(sc))
	}

	// Resolve scopeID from context
	var scopeID string
	switch scope {
	case "global":
		scopeID = "system"
	case "channel":
		if chID, ok := ctx.Value("channel_id").(string); ok && chID != "" {
			scopeID = chID
		} else if chID, ok := args["channel_id"].(string); ok && chID != "" {
			scopeID = chID
		}
	default: // "user"
		if uID, ok := ctx.Value("user_id").(string); ok && uID != "" {
			scopeID = uID
		} else if uID, ok := args["user_id"].(string); ok && uID != "" {
			scopeID = uID
		}
	}

	if scopeID == "" {
		scopeID = "default"
	}

	key, _ := args["key"].(string)
	key = strings.TrimSpace(key)

	content, _ := args["content"].(string)
	content = strings.TrimSpace(content)

	category := "fact"
	if cat, ok := args["category"].(string); ok && strings.TrimSpace(cat) != "" {
		category = strings.ToLower(strings.TrimSpace(cat))
	}

	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)

	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)

	switch action {
	case "save":
		if key == "" {
			return "", fmt.Errorf("parameter 'key' wajib diisi untuk menyimpan memori (contoh: 'makanan_favorit')")
		}
		if content == "" {
			return "", fmt.Errorf("parameter 'content' wajib diisi untuk menyimpan memori")
		}

		err := m.manager.UpsertFact(scope, scopeID, key, content, category)
		if err != nil {
			return "", fmt.Errorf("gagal menyimpan memori: %w", err)
		}
		return fmt.Sprintf("✅ <b>Memori Berhasil Disimpan!</b>\n• Kunci: <code>%s</code>\n• Kategori: <code>%s</code>\n• Lingkup: <code>%s</code>\n• Isi: <i>%s</i>", key, category, scope, content), nil

	case "search":
		if query == "" {
			return "", fmt.Errorf("parameter 'query' wajib diisi untuk mencari memori")
		}
		items, err := m.manager.SearchMemories(scope, scopeID, query)
		if err != nil {
			return "", fmt.Errorf("gagal mencari memori: %w", err)
		}
		if len(items) == 0 {
			return fmt.Sprintf("🔍 Tidak ditemukan catatan dengan kata kunci: <i>\"%s\"</i> (lingkup: %s).", query, scope), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 <b>Hasil Pencarian Memori (\"%s\" - %s):</b>\n\n", query, scope))
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("• <b>[%s]</b> <code>%s</code> (ID: <code>%s</code>)\n  <i>%s</i>\n", it.Category, it.KeyTag, it.ID, it.Content))
		}
		return sb.String(), nil

	case "delete":
		if id != "" {
			if err := m.manager.DeleteMemoryItem(id); err != nil {
				return "", fmt.Errorf("gagal menghapus memori ID %s: %w", id, err)
			}
			return fmt.Sprintf("🗑️ <b>Memori Berhasil Dihapus!</b> (ID: <code>%s</code>)", id), nil
		}
		if key != "" {
			if err := m.manager.DeleteMemoryByKey(scope, scopeID, key); err != nil {
				return "", fmt.Errorf("gagal menghapus memori dengan kunci '%s': %w", key, err)
			}
			return fmt.Sprintf("🗑️ <b>Memori Berhasil Dihapus!</b> (Kunci: <code>%s</code>, Lingkup: <code>%s</code>)", key, scope), nil
		}
		return "", fmt.Errorf("salah satu dari parameter 'key' atau 'id' wajib diisi untuk aksi delete")

	case "clear":
		var err error
		if scope == "channel" {
			err = m.manager.ClearChannelMemory(scopeID)
		} else {
			err = m.manager.ClearUserMemory(scopeID)
		}
		if err != nil {
			return "", fmt.Errorf("gagal membersihkan memori: %w", err)
		}
		return fmt.Sprintf("🧹 <b>Semua catatan memori pada lingkup %s (%s) telah berhasil dibersihkan.</b>", scope, scopeID), nil

	default: // "list"
		items, err := m.manager.ListMemories(scope, scopeID)
		if err != nil {
			return "", fmt.Errorf("gagal mengambil daftar memori: %w", err)
		}
		if len(items) == 0 {
			return fmt.Sprintf("📝 Belum ada catatan atau preferensi yang tersimpan pada lingkup %s (%s).", scope, scopeID), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📝 <b>Daftar Catatan & Preferensi (%s - %s):</b>\n\n", scope, scopeID))
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("• <b>[%s]</b> <code>%s</code> (ID: <code>%s</code>)\n  <i>%s</i>\n", it.Category, it.KeyTag, it.ID, it.Content))
		}
		return sb.String(), nil
	}
}
