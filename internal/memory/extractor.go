package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

// ExtractedMemoryItem represents a memory item returned by LLM auto-extraction
type ExtractedMemoryItem struct {
	Key      string `json:"key"`
	Content  string `json:"content"`
	Type     string `json:"type"`     // factual, episodic, procedural, semantic
	Category string `json:"category"` // preference, profile, fact, work, decision, sop
	Scope    string `json:"scope"`    // user, channel, global
}

const autoExtractSystemPrompt = `Kamu adalah Memory Extraction Engine untuk asisten AI.
Tugasmu adalah menganalisis interaksi percakapan dan mengekstrak informasi penting, fakta baru, preferensi pengguna, keputusan teknis, progres tugas, atau SOP yang layak diingat secara permanen untuk percakapan masa depan.

Kategori Tipe Memori:
1. 'factual': Preferensi personal pengguna, profil, data login/akses, konfigurasi server/alat, informasi lingkungan.
2. 'episodic': Kejadian/tugas spesifik yang baru diselesaikan, progres yang dicapai, keputusan tindakan yang diambil (decision).
3. 'procedural': Panduan langkah kerja, SOP, aturan teknis wajib (contoh: "WAJIB gunakan engine camoufox untuk KAI").
4. 'semantic': Konsep, definisi, pemahaman struktur halaman web/antarmuka (contoh: "form booking ada di shadow DOM").

Aturan Ekstraksi:
- HANYA ekstrak informasi bernilai tinggi dan berjangka panjang.
- JANGAN ekstrak salam, obrolan santai, konfirmasi sepele, atau instruksi sekali pakai.
- Format 'key': snake_case yang deskriptif dan unik (contoh: 'preferensi_bahasa', 'kai_bypass_ssl', 'btc_monitor_config').
- Format 'scope': 'user' (spesifik pengguna ini), 'channel' (grup/saluran kerja), atau 'global' (SOP umum untuk semua).
- Jika ada pembaruan pada fakta lama, gunakan key yang sama agar ter-update.
- Respon WAJIB berupa JSON array valid tanpa teks pengantar, markdown codeblock opsional.

Format Output:
[
  {
    "key": "nama_kunci",
    "content": "Isi memori ringkas, jelas, dan faktual",
    "type": "factual|episodic|procedural|semantic",
    "category": "preference|profile|fact|work|decision|sop",
    "scope": "user|channel|global"
  }
]
Jika TIDAK ADA informasi penting yang layak diingat dari percakapan ini, kembalikan array kosong: []`

// AutoExtractor handles asynchronous conversation analysis and memory extraction
type AutoExtractor struct {
	db          *storage.DB
	embedder    Embedder
	provManager *provider.Manager
}

// NewAutoExtractor creates a new AutoExtractor
func NewAutoExtractor(db *storage.DB, embedder Embedder, pm *provider.Manager) *AutoExtractor {
	return &AutoExtractor{
		db:          db,
		embedder:    embedder,
		provManager: pm,
	}
}

// ExtractFromConversation analyzes a turn and saves extracted memories into local SQLite
func (e *AutoExtractor) ExtractFromConversation(ctx context.Context, channelID, userID, userPrompt, assistantResponse string, activeModel, activeProvider string) error {
	if e == nil || e.db == nil || e.provManager == nil {
		return nil
	}
	if strings.TrimSpace(userPrompt) == "" || strings.TrimSpace(assistantResponse) == "" {
		return nil
	}

	// Filter out trivial conversations (e.g. greetings or single word commands)
	cleanPrompt := strings.TrimSpace(userPrompt)
	if len(cleanPrompt) < 5 || strings.HasPrefix(cleanPrompt, "/") {
		// Skip slash commands
		return nil
	}

	dialogText := fmt.Sprintf("User: %s\nAssistant: %s", userPrompt, assistantResponse)
	// Truncate long dialog to prevent excessive token usage in extractor
	if len(dialogText) > 4000 {
		dialogText = dialogText[:4000] + "... (truncated)"
	}

	extractReq := provider.ChatRequest{
		Model:       activeModel,
		Temperature: 0.1,
		MaxTokens:   800,
		Messages: []provider.ChatMessage{
			{
				Role:    provider.RoleSystem,
				Content: autoExtractSystemPrompt,
			},
			{
				Role:    provider.RoleUser,
				Content: fmt.Sprintf("Berikut dialog yang perlu dianalisis (User ID: %s, Channel: %s):\n\n%s", userID, channelID, dialogText),
			},
		},
	}

	callCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	resp, err := e.provManager.GenerateWithFallback(callCtx, activeProvider, extractReq)
	if err != nil {
		return fmt.Errorf("auto-extraction LLM call failed: %w", err)
	}

	rawContent := strings.TrimSpace(resp.Content)
	if rawContent == "" || rawContent == "[]" {
		return nil
	}

	// Strip potential markdown code block ```json ... ```
	cleanedJSON := rawContent
	if strings.Contains(cleanedJSON, "```") {
		lines := strings.Split(cleanedJSON, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inBlock = !inBlock
				continue
			}
			jsonLines = append(jsonLines, line)
		}
		cleanedJSON = strings.Join(jsonLines, "\n")
	}

	cleanedJSON = strings.TrimSpace(cleanedJSON)
	// Find JSON array bounds [ ... ]
	startIdx := strings.Index(cleanedJSON, "[")
	endIdx := strings.LastIndex(cleanedJSON, "]")
	if startIdx == -1 || endIdx == -1 || endIdx < startIdx {
		return nil
	}
	cleanedJSON = cleanedJSON[startIdx : endIdx+1]

	var items []ExtractedMemoryItem
	if err := json.Unmarshal([]byte(cleanedJSON), &items); err != nil {
		return fmt.Errorf("parse extracted memories json: %w", err)
	}

	if len(items) == 0 {
		return nil
	}

	savedCount := 0
	for _, it := range items {
		key := strings.TrimSpace(it.Key)
		content := strings.TrimSpace(it.Content)
		if key == "" || content == "" {
			continue
		}

		scope := strings.ToLower(strings.TrimSpace(it.Scope))
		if scope != "global" && scope != "channel" {
			scope = "user"
		}

		// Security: Non-admin users cannot write to 'global' scope
		if scope == "global" && userID != "399999658" {
			scope = "user"
		}

		scopeID := userID
		switch scope {
		case "global":
			scopeID = "system"
		case "channel":
			scopeID = channelID
		default:
			if scopeID == "" {
				scopeID = "default"
			}
		}

		memType := strings.ToLower(strings.TrimSpace(it.Type))
		if memType != "episodic" && memType != "procedural" && memType != "semantic" {
			memType = "factual"
		}

		category := strings.ToLower(strings.TrimSpace(it.Category))
		if category == "" {
			category = "fact"
		}

		var emb []float32
		if e.embedder != nil && e.embedder.IsEnabled() {
			textToEmbed := fmt.Sprintf("%s: %s", key, content)
			if vec, embErr := e.embedder.Embed(callCtx, textToEmbed); embErr == nil {
				emb = vec
			}
		}

		rec := &storage.MemoryItemRecord{
			Type:      memType,
			Scope:     scope,
			ScopeID:   scopeID,
			Key:       key,
			Content:   content,
			Category:  category,
			Embedding: emb,
			Metadata: map[string]interface{}{
				"source":       "auto_extract",
				"extracted_at": time.Now().Format(time.RFC3339),
			},
		}

		if err := e.db.UpsertMemory(rec); err == nil {
			savedCount++
		}
	}

	if savedCount > 0 {
		log.Printf("🧠 [Memory Auto-Extractor] Berhasil mengekstrak dan menyimpan %d memori baru (User: %s)", savedCount, userID)
	}
	return nil
}
