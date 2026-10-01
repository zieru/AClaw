package memory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

// Manager coordinates storage, embedding, hybrid retrieval, and auto-extraction for memories.
// It is 100% standalone and operates on local SQLite with FTS5.
type Manager struct {
	db        *storage.DB
	embedder  Embedder
	extractor *AutoExtractor
	cfg       config.MemoryConfig
}

// NewManager creates a standalone memory manager backed by local SQLite and optional embedder
func NewManager(db *storage.DB, cfg config.MemoryConfig, embedder Embedder, pm *provider.Manager) *Manager {
	var ext *AutoExtractor
	if db != nil && pm != nil {
		ext = NewAutoExtractor(db, embedder, pm)
	}

	if cfg.RetrievalStrategy == "" {
		cfg.RetrievalStrategy = "hybrid"
	}
	if cfg.MaxContextItems <= 0 {
		cfg.MaxContextItems = 10
	}
	if cfg.SimilarityThreshold <= 0 {
		cfg.SimilarityThreshold = 0.60
	}

	return &Manager{
		db:        db,
		embedder:  embedder,
		extractor: ext,
		cfg:       cfg,
	}
}

// IsAutoExtractEnabled returns true if automatic conversation extraction is enabled
func (m *Manager) IsAutoExtractEnabled() bool {
	return m != nil && m.cfg.Enabled && m.cfg.AutoExtract && m.extractor != nil
}

// AutoExtractAsync launches asynchronous memory extraction in background
func (m *Manager) AutoExtractAsync(channelID, userID, userPrompt, assistantResponse, activeModel, activeProvider string) {
	if !m.IsAutoExtractEnabled() {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("⚠️ [Memory Auto-Extract] Panic recovered: %v", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		if err := m.extractor.ExtractFromConversation(ctx, channelID, userID, userPrompt, assistantResponse, activeModel, activeProvider); err != nil {
			log.Printf("⚠️ [Memory Auto-Extract] Error: %v", err)
		}
	}()
}

// SaveFact saves a learned fact or profile item
func (m *Manager) SaveFact(scope, scopeID, key, content, category string) error {
	return m.UpsertFact(scope, scopeID, key, content, category)
}

// UpsertFact saves or updates a memory item with type 'factual'
func (m *Manager) UpsertFact(scope, scopeID, key, content, category string) error {
	return m.UpsertMemoryRecord(&storage.MemoryItemRecord{
		Type:     "factual",
		Scope:    scope,
		ScopeID:  scopeID,
		Key:      key,
		Content:  content,
		Category: category,
	})
}

// UpsertMemoryRecord saves or updates a full memory record (and calculates embedding if enabled)
func (m *Manager) UpsertMemoryRecord(rec *storage.MemoryItemRecord) error {
	if m.db == nil {
		return fmt.Errorf("database memory belum terhubung")
	}

	if rec.Type == "" {
		rec.Type = "factual"
	}
	if rec.Scope == "" {
		rec.Scope = "user"
	}
	if rec.Category == "" {
		rec.Category = "fact"
	}

	// Generate embedding if embedder is active and no embedding was provided
	if len(rec.Embedding) == 0 && m.embedder != nil && m.embedder.IsEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		textToEmbed := fmt.Sprintf("%s: %s", rec.Key, rec.Content)
		if vec, err := m.embedder.Embed(ctx, textToEmbed); err == nil && len(vec) > 0 {
			rec.Embedding = vec
		}
	}

	return m.db.UpsertMemory(rec)
}

// ListMemories returns all memory items for a specific scope and scope ID as legacy records
func (m *Manager) ListMemories(scope, scopeID string) ([]storage.MemoryRecord, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database memory belum terhubung")
	}
	return m.db.ListMemoryItems(scope, scopeID)
}

// ListMemoryItems returns full memory item records
func (m *Manager) ListMemoryItems(scope, scopeID, memType string, limit int) ([]storage.MemoryItemRecord, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database memory belum terhubung")
	}
	return m.db.ListMemoriesByScope(scope, scopeID, memType, limit)
}

// SearchMemories searches memories by query using the default strategy
func (m *Manager) SearchMemories(scope, scopeID, query string) ([]storage.MemoryRecord, error) {
	items, err := m.SearchMemoriesAdvanced(scope, scopeID, query, m.cfg.RetrievalStrategy, 50)
	if err != nil {
		return nil, err
	}
	var records []storage.MemoryRecord
	for _, it := range items {
		records = append(records, storage.MemoryRecord{
			ID:        it.ID,
			Scope:     it.Scope,
			ScopeID:   it.ScopeID,
			KeyTag:    it.Key,
			Content:   it.Content,
			Category:  it.Category,
			CreatedAt: it.CreatedAt,
			UpdatedAt: it.UpdatedAt,
		})
	}
	return records, nil
}

// SearchMemoriesAdvanced performs search using exact, semantic, or hybrid strategy
func (m *Manager) SearchMemoriesAdvanced(scope, scopeID, query string, strategy string, limit int) ([]storage.MemoryItemRecord, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database memory belum terhubung")
	}
	if limit <= 0 {
		limit = 20
	}
	if strategy == "" {
		strategy = m.cfg.RetrievalStrategy
	}
	strategy = strings.ToLower(strategy)

	switch strategy {
	case "exact":
		return m.searchExact(scope, scopeID, query, limit)
	case "semantic":
		return m.searchSemantic(scope, scopeID, query, limit)
	default: // "hybrid"
		return m.searchHybrid(scope, scopeID, query, limit)
	}
}

func (m *Manager) searchExact(scope, scopeID, query string, limit int) ([]storage.MemoryItemRecord, error) {
	if strings.TrimSpace(query) == "" {
		return m.db.ListMemoriesByScope(scope, scopeID, "", limit)
	}
	return m.db.SearchMemoriesFTS5(scope, scopeID, query, limit)
}

func (m *Manager) searchSemantic(scope, scopeID, query string, limit int) ([]storage.MemoryItemRecord, error) {
	if m.embedder == nil || !m.embedder.IsEnabled() || strings.TrimSpace(query) == "" {
		// Fallback to FTS5 if embedding disabled
		return m.db.SearchMemoriesFTS5(scope, scopeID, query, limit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queryVec, err := m.embedder.Embed(ctx, query)
	if err != nil || len(queryVec) == 0 {
		return m.db.SearchMemoriesFTS5(scope, scopeID, query, limit)
	}

	allMemories, err := m.db.ListMemoriesByScope(scope, scopeID, "", 200)
	if err != nil {
		return nil, err
	}

	var scored []storage.MemoryItemRecord
	threshold := float32(m.cfg.SimilarityThreshold)

	for _, mem := range allMemories {
		if len(mem.Embedding) > 0 {
			sim := CosineSimilarity(queryVec, mem.Embedding)
			if sim >= threshold {
				mem.Score = sim
				scored = append(scored, mem)
			}
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

func (m *Manager) searchHybrid(scope, scopeID, query string, limit int) ([]storage.MemoryItemRecord, error) {
	cleanQ := strings.TrimSpace(query)
	if cleanQ == "" {
		return m.db.ListMemoriesByScope(scope, scopeID, "", limit)
	}

	// 1. FTS5 Search
	ftsResults, _ := m.db.SearchMemoriesFTS5(scope, scopeID, cleanQ, limit*2)
	ftsScoreMap := make(map[string]float32)
	for _, it := range ftsResults {
		ftsScoreMap[it.ID] = it.Score
	}

	// 2. Vector Semantic Search
	semanticScoreMap := make(map[string]float32)
	if m.embedder != nil && m.embedder.IsEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		if queryVec, err := m.embedder.Embed(ctx, cleanQ); err == nil && len(queryVec) > 0 {
			allMemories, _ := m.db.ListMemoriesByScope(scope, scopeID, "", 150)
			for _, mem := range allMemories {
				if len(mem.Embedding) > 0 {
					semanticScoreMap[mem.ID] = CosineSimilarity(queryVec, mem.Embedding)
				}
			}
		}
		cancel()
	}

	// 3. Combine scores
	memoryMap := make(map[string]storage.MemoryItemRecord)
	for _, it := range ftsResults {
		memoryMap[it.ID] = it
	}

	// Load candidates if semantic found items not in FTS
	if len(semanticScoreMap) > 0 {
		candidates, _ := m.db.ListMemoriesByScope(scope, scopeID, "", 150)
		for _, it := range candidates {
			if _, exists := memoryMap[it.ID]; !exists {
				memoryMap[it.ID] = it
			}
		}
	}

	var combined []storage.MemoryItemRecord
	hasEmbeddings := len(semanticScoreMap) > 0

	for id, mem := range memoryMap {
		ftsScore := ftsScoreMap[id]
		semScore := semanticScoreMap[id]

		var finalScore float32
		if hasEmbeddings {
			// Hybrid: 50% FTS5 + 50% Semantic Vector
			finalScore = 0.5*ftsScore + 0.5*semScore
			// Bonus for exact key match
			if strings.EqualFold(mem.Key, cleanQ) {
				finalScore += 0.2
			}
		} else {
			finalScore = ftsScore
			if strings.EqualFold(mem.Key, cleanQ) {
				finalScore += 0.3
			}
		}

		if finalScore > 0.05 {
			mem.Score = finalScore
			combined = append(combined, mem)
		}
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	if len(combined) > limit {
		combined = combined[:limit]
	}
	return combined, nil
}

// DeleteMemoryItem deletes a memory item by ID
func (m *Manager) DeleteMemoryItem(id string) error {
	if m.db == nil {
		return fmt.Errorf("database memory belum terhubung")
	}
	return m.db.DeleteMemory(id)
}

// DeleteMemoryByKey deletes a memory item by key within a scope
func (m *Manager) DeleteMemoryByKey(scope, scopeID, key string) error {
	if m.db == nil {
		return fmt.Errorf("database memory belum terhubung")
	}
	return m.db.DeleteMemoryByKey(scope, scopeID, key)
}

// ClearUserMemory deletes all memories associated with a user
func (m *Manager) ClearUserMemory(userID string) error {
	if m.db == nil {
		return fmt.Errorf("database memory belum terhubung")
	}
	return m.db.ClearMemoriesByScope("user", userID)
}

// ClearChannelMemory deletes all memories associated with a channel
func (m *Manager) ClearChannelMemory(channelID string) error {
	if m.db == nil {
		return fmt.Errorf("database memory belum terhubung")
	}
	return m.db.ClearMemoriesByScope("channel", channelID)
}

// GetContextMemory retrieves formatted memory context structured by category for system prompt injection.
// It searches relevant memories across global, channel, and user scopes based on query.
func (m *Manager) GetContextMemory(channelID, userID, query string) (string, error) {
	if m.db == nil || !m.cfg.Enabled {
		return "", nil
	}

	scopes := []struct{ Scope, ScopeID string }{
		{Scope: "global", ScopeID: "system"},
	}
	if channelID != "" {
		scopes = append(scopes, struct{ Scope, ScopeID string }{Scope: "channel", ScopeID: channelID})
	}
	if userID != "" {
		scopes = append(scopes, struct{ Scope, ScopeID string }{Scope: "user", ScopeID: userID})
	}

	candidates, err := m.db.ListCandidateMemoriesForScopes(scopes)
	if err != nil || len(candidates) == 0 {
		return "", nil
	}

	// If query is provided, rank candidates by relevance using hybrid/FTS5
	rankedMemories := candidates
	cleanQuery := strings.TrimSpace(query)

	if cleanQuery != "" {
		// Calculate relevance scores
		var queryVec []float32
		if m.embedder != nil && m.embedder.IsEnabled() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			queryVec, _ = m.embedder.Embed(ctx, cleanQuery)
			cancel()
		}

		ftsMap := make(map[string]bool)
		for _, sc := range scopes {
			if ftsResults, err := m.db.SearchMemoriesFTS5(sc.Scope, sc.ScopeID, cleanQuery, 15); err == nil {
				for _, r := range ftsResults {
					ftsMap[r.ID] = true
				}
			}
		}

		for i := range rankedMemories {
			mem := &rankedMemories[i]
			var score float32

			if ftsMap[mem.ID] {
				score += 0.5
			}
			if len(queryVec) > 0 && len(mem.Embedding) > 0 {
				sim := CosineSimilarity(queryVec, mem.Embedding)
				score += sim * 0.5
			}
			// Exact key match bonus
			if strings.Contains(strings.ToLower(cleanQuery), strings.ToLower(mem.Key)) {
				score += 0.4
			}
			// Recent memory recency bonus (within 7 days)
			if time.Since(mem.UpdatedAt) < 7*24*time.Hour {
				score += 0.1
			}
			mem.Score = score
		}

		sort.Slice(rankedMemories, func(i, j int) bool {
			// Prioritize items with higher score, then by updated_at
			if rankedMemories[i].Score != rankedMemories[j].Score {
				return rankedMemories[i].Score > rankedMemories[j].Score
			}
			return rankedMemories[i].UpdatedAt.After(rankedMemories[j].UpdatedAt)
		})
	}

	// Limit context items
	maxItems := m.cfg.MaxContextItems
	if maxItems <= 0 {
		maxItems = 10
	}
	if len(rankedMemories) > maxItems {
		rankedMemories = rankedMemories[:maxItems]
	}

	// Group into 4 categories: factual, procedural, episodic, semantic
	var factuals []storage.MemoryItemRecord
	var procedurals []storage.MemoryItemRecord
	var episodics []storage.MemoryItemRecord
	var semantics []storage.MemoryItemRecord

	for _, item := range rankedMemories {
		// Increment access count asynchronously
		go func(id string) {
			_ = m.db.IncrementMemoryAccess(id)
		}(item.ID)

		switch strings.ToLower(item.Type) {
		case "procedural":
			procedurals = append(procedurals, item)
		case "episodic":
			episodics = append(episodics, item)
		case "semantic":
			semantics = append(semantics, item)
		default: // "factual"
			factuals = append(factuals, item)
		}
	}

	var sb strings.Builder

	if len(factuals) > 0 {
		sb.WriteString("### Faktual & Preferensi (User Profile & Facts):\n")
		for _, item := range factuals {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	if len(procedurals) > 0 {
		sb.WriteString("### SOP & Instruksi Prosedural (Procedural Knowledge):\n")
		for _, item := range procedurals {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	if len(episodics) > 0 {
		sb.WriteString("### Riwayat & Progres Tugas (Episodic Context):\n")
		for _, item := range episodics {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	if len(semantics) > 0 {
		sb.WriteString("### Pemahaman Konsep & Sistem (Semantic Knowledge):\n")
		for _, item := range semantics {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Key, item.Content))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()), nil
}
