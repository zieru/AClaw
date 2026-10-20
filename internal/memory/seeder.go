package memory

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/storage"
)

// SeedResult holds statistics about CSV seeding
type SeedResult struct {
	TotalProcessed int
	Inserted       int
	Errors         []string
}

// SeedFromCSV imports memories from an OmniRoute CSV export file into the local SQLite database
func SeedFromCSV(ctx context.Context, db *storage.DB, csvPath string, defaultAdminID string, embedder Embedder) (*SeedResult, error) {
	if db == nil {
		return nil, fmt.Errorf("database instance is nil")
	}
	if defaultAdminID == "" {
		defaultAdminID = "399999658"
	}

	cleanPath := strings.Trim(csvPath, "\"")
	file, err := os.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open csv file %s: %w", cleanPath, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read header
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read csv header: %w", err)
	}

	colMap := make(map[string]int)
	for i, h := range header {
		colMap[strings.ToLower(strings.TrimSpace(h))] = i
	}

	getCol := func(row []string, name string) string {
		if idx, ok := colMap[name]; ok && idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
		return ""
	}

	result := &SeedResult{}

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("read row error: %v", err))
			continue
		}

		result.TotalProcessed++

		id := getCol(row, "id")
		memType := strings.ToLower(getCol(row, "type"))
		key := getCol(row, "key")
		content := getCol(row, "content")
		metaRaw := getCol(row, "metadata")
		createdAtStr := getCol(row, "created_at")
		updatedAtStr := getCol(row, "updated_at")
		accessCountStr := getCol(row, "access_count")

		if key == "" || content == "" {
			continue
		}

		if memType == "" {
			memType = "factual"
		}

		var metadata map[string]interface{}
		if metaRaw != "" {
			_ = json.Unmarshal([]byte(metaRaw), &metadata)
		}
		if metadata == nil {
			metadata = make(map[string]interface{})
		}

		// Resolve scope and scope_id
		scope := "user"
		scopeID := defaultAdminID

		if s, ok := metadata["scope"].(string); ok && s != "" {
			scope = s
		}
		if sid, ok := metadata["scope_id"].(string); ok && sid != "" {
			scopeID = sid
		}

		category := "fact"
		if cat, ok := metadata["category"].(string); ok && cat != "" {
			category = cat
		}

		accessCount := 0
		if accessCountStr != "" {
			accessCount, _ = strconv.Atoi(accessCountStr)
		}

		var createdAt, updatedAt time.Time
		if createdAtStr != "" {
			createdAt, _ = time.Parse(time.RFC3339, createdAtStr)
		}
		if updatedAtStr != "" {
			updatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
		}
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		if updatedAt.IsZero() {
			updatedAt = time.Now()
		}

		// Optional embedding generation
		var emb []float32
		if embedder != nil && embedder.IsEnabled() {
			textToEmbed := fmt.Sprintf("%s: %s", key, content)
			if vec, embErr := embedder.Embed(ctx, textToEmbed); embErr == nil {
				emb = vec
			}
		}

		record := &storage.MemoryItemRecord{
			ID:          id,
			Type:        memType,
			Scope:       scope,
			ScopeID:     scopeID,
			Key:         key,
			Content:     content,
			Category:    category,
			Embedding:   emb,
			Metadata:    metadata,
			AccessCount: accessCount,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
		}

		if err := db.UpsertMemory(record); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to upsert %s: %v", key, err))
		} else {
			result.Inserted++
		}
	}

	log.Printf("📥 [Memory Seeder] Berhasil mengimpor %d/%d catatan memori dari CSV (%s) ke SQLite lokal untuk admin %s",
		result.Inserted, result.TotalProcessed, cleanPath, defaultAdminID)

	return result, nil
}
