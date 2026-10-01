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
	UpsertMemoryRecord(rec *storage.MemoryItemRecord) error
	ListMemories(scope, scopeID string) ([]storage.MemoryRecord, error)
	SearchMemories(scope, scopeID, query string) ([]storage.MemoryRecord, error)
	SearchMemoriesAdvanced(scope, scopeID, query string, strategy string, limit int) ([]storage.MemoryItemRecord, error)
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
	return "Tool mandiri untuk menyimpan, mencari, melihat, dan menghapus catatan jangka panjang (faktual, prosedural, episodik, semantik) tentang preferensi pengguna, fakta profil, to-do list, catatan proyek, atau SOP teknis ke SQLite FTS5 Memory."
}

func (m *UserMemoryTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"action": {
				Type:        "string",
				Description: "Aksi memori: 'save' (simpan fakta/catatan baru atau update), 'list' (tampilkan seluruh catatan), 'search' (cari catatan dengan kata kunci/makna), 'delete' (hapus catatan berdasarkan key atau id), 'clear' (hapus semua catatan). Default: list.",
				Enum:        []string{"save", "list", "search", "delete", "clear"},
			},
			"key": {
				Type:        "string",
				Description: "Kunci/tag ringkas catatan (contoh: 'nama_panggilan', 'kai_bypass_ssl', 'rekening_bca', 'bahasa_coding'). Wajib untuk aksi 'save' dan 'delete' jika ID tidak diberikan.",
			},
			"content": {
				Type:        "string",
				Description: "Isi fakta atau detail informasi yang ingin disimpan. Wajib untuk aksi 'save'.",
			},
			"type": {
				Type:        "string",
				Description: "Tipe memori: 'factual' (profil/fakta/preferensi), 'episodic' (riwayat/progres tugas/keputusan), 'procedural' (SOP/langkah teknis), 'semantic' (konsep/struktur). Default: 'factual'.",
				Enum:        []string{"factual", "episodic", "procedural", "semantic"},
			},
			"category": {
				Type:        "string",
				Description: "Kategori catatan: 'preference', 'profile', 'fact', 'todo', 'work', 'decision', 'sop'. Default: 'fact'.",
			},
			"query": {
				Type:        "string",
				Description: "Kata kunci atau pertanyaan pencarian untuk aksi 'search'.",
			},
			"strategy": {
				Type:        "string",
				Description: "Strategi pencarian: 'hybrid' (gabungan FTS5 BM25 + Vektor), 'semantic' (vektor kemiripan makna), atau 'exact' (pencocokan teks). Default: 'hybrid'.",
				Enum:        []string{"hybrid", "semantic", "exact"},
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

	callerUserID, _ := ctx.Value("user_id").(string)
	callerChannelID, _ := ctx.Value("channel_id").(string)

	// Resolve scopeID from context with strict isolation boundaries
	var scopeID string
	switch scope {
	case "global":
		// Hanya admin yang boleh menyimpan atau menghapus memori global
		if action == "save" || action == "delete" || action == "clear" {
			if callerUserID != "" && callerUserID != "399999658" {
				return "", fmt.Errorf("akses ditolak: hanya administrator yang diizinkan mengubah memori lingkup 'global'")
			}
		}
		scopeID = "system"
	case "channel":
		if callerChannelID != "" {
			scopeID = callerChannelID
		} else if chID, ok := args["channel_id"].(string); ok && chID != "" {
			scopeID = chID
		}
	default: // "user"
		// Wajib terikat ke user yang sedang aktif di context (mencegah spoofing ID user lain)
		if callerUserID != "" {
			scopeID = callerUserID
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

	memType := "factual"
	if mt, ok := args["type"].(string); ok && strings.TrimSpace(mt) != "" {
		memType = strings.ToLower(strings.TrimSpace(mt))
	}

	strategy := "hybrid"
	if st, ok := args["strategy"].(string); ok && strings.TrimSpace(st) != "" {
		strategy = strings.ToLower(strings.TrimSpace(st))
	}

	switch action {
	case "save":
		if key == "" {
			return "", fmt.Errorf("parameter 'key' wajib diisi untuk menyimpan memori (contoh: 'makanan_favorit')")
		}
		if content == "" {
			return "", fmt.Errorf("parameter 'content' wajib diisi untuk menyimpan memori")
		}

		err := m.manager.UpsertMemoryRecord(&storage.MemoryItemRecord{
			Type:     memType,
			Scope:    scope,
			ScopeID:  scopeID,
			Key:      key,
			Content:  content,
			Category: category,
		})
		if err != nil {
			return "", fmt.Errorf("gagal menyimpan memori: %w", err)
		}
		return fmt.Sprintf("✅ <b>Memori Berhasil Disimpan!</b>\n• Kunci: <code>%s</code>\n• Tipe: <code>%s</code>\n• Kategori: <code>%s</code>\n• Lingkup: <code>%s</code>\n• Isi: <i>%s</i>", key, memType, category, scope, content), nil

	case "search":
		if query == "" {
			return "", fmt.Errorf("parameter 'query' wajib diisi untuk mencari memori")
		}
		items, err := m.manager.SearchMemoriesAdvanced(scope, scopeID, query, strategy, 10)
		if err != nil {
			return "", fmt.Errorf("gagal mencari memori: %w", err)
		}
		if len(items) == 0 {
			return fmt.Sprintf("🔍 Tidak ditemukan catatan dengan kata kunci: <i>\"%s\"</i> (lingkup: %s, strategi: %s).", query, scope, strategy), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 <b>Hasil Pencarian Memori (%s - %s - %s):</b>\n\n", query, scope, strategy))
		for _, it := range items {
			scoreInfo := ""
			if it.Score > 0 {
				scoreInfo = fmt.Sprintf(" (skor: %.2f)", it.Score)
			}
			sb.WriteString(fmt.Sprintf("• <b>[%s|%s]</b> <code>%s</code>%s\n  <i>%s</i>\n", it.Type, it.Category, it.Key, scoreInfo, it.Content))
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
