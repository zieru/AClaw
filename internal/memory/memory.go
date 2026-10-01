package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

// Manager coordinates storage, embedding, hybrid retrieval, and auto-extraction for memories.
// It is 100% standalone and operates on local SQLite with FTS5.
type Manager struct {
	mu        sync.RWMutex
	db        *storage.DB
	embedder  Embedder
	extractor *AutoExtractor
	cfg       config.MemoryConfig
}

// NewManager creates a standalone memory manager backed by local SQLite and optional embedder
func NewManager(db *storage.DB, cfg config.MemoryConfig, embedder Embedder, pm *provider.Manager) *Manager {
	// Restore persisted runtime configuration from database if available
	if db != nil {
		if savedJSON, err := db.GetSetting("memory_config", ""); err == nil && strings.TrimSpace(savedJSON) != "" {
			var savedCfg config.MemoryConfig
			if err := json.Unmarshal([]byte(savedJSON), &savedCfg); err == nil {
				cfg = savedCfg
			}
		}
	}

	if cfg.Strategy == "" {
		cfg.Strategy = cfg.GetStrategy()
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = cfg.GetMaxTokens()
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = cfg.GetRetentionDays()
	}
	if cfg.PromotionThreshold <= 0 {
		cfg.PromotionThreshold = cfg.GetPromotionThreshold()
	}
	if cfg.MaxContextItems <= 0 {
		cfg.MaxContextItems = 20
	}
	if cfg.SimilarityThreshold <= 0 {
		cfg.SimilarityThreshold = 0.60
	}

	// Re-evaluate embedder if configuration specifies embedding
	embCfg := cfg.GetEmbeddingConfig()
	if embedder == nil || embCfg.Enabled {
		embedder = NewEmbedder(embCfg)
	}

	var ext *AutoExtractor
	if db != nil && pm != nil {
		ext = NewAutoExtractor(db, embedder, pm)
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

// SearchMemoriesAdvanced performs search using recent, semantic, exact, or hybrid strategy
func (m *Manager) SearchMemoriesAdvanced(scope, scopeID, query string, strategy string, limit int) ([]storage.MemoryItemRecord, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database memory belum terhubung")
	}
	if limit <= 0 {
		limit = 20
	}
	if strategy == "" {
		strategy = m.GetStrategy()
	}
	strategy = strings.ToLower(strategy)

	switch strategy {
	case "recent":
		return m.searchRecent(scope, scopeID, query, limit)
	case "exact":
		return m.searchExact(scope, scopeID, query, limit)
	case "semantic":
		return m.searchSemantic(scope, scopeID, query, limit)
	default: // "hybrid"
		return m.searchHybrid(scope, scopeID, query, limit)
	}
}

func (m *Manager) searchRecent(scope, scopeID, query string, limit int) ([]storage.MemoryItemRecord, error) {
	if strings.TrimSpace(query) == "" {
		return m.db.ListMemoriesByScope(scope, scopeID, "", limit)
	}
	// For recent strategy with search term, retrieve matching memories and sort by UpdatedAt DESC
	ftsResults, err := m.db.SearchMemoriesFTS5(scope, scopeID, query, limit)
	if err == nil && len(ftsResults) > 0 {
		sort.Slice(ftsResults, func(i, j int) bool {
			return ftsResults[i].UpdatedAt.After(ftsResults[j].UpdatedAt)
		})
		return ftsResults, nil
	}
	return m.db.ListMemoriesByScope(scope, scopeID, "", limit)
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
// It searches relevant memories across global, channel, and user scopes based on query, respecting
// memoryStrategy, memoryRetentionDays, and budget-packing up to memoryMaxTokens.
func (m *Manager) GetContextMemory(channelID, userID, query string) (string, error) {
	if m.db == nil {
		return "", nil
	}
	m.mu.RLock()
	enabled := m.cfg.Enabled
	strategy := m.cfg.GetStrategy()
	maxTokens := m.cfg.GetMaxTokens()
	maxItems := m.cfg.MaxContextItems
	retentionDays := m.cfg.GetRetentionDays()
	promotionThreshold := m.cfg.GetPromotionThreshold()
	m.mu.RUnlock()

	if !enabled {
		return "", nil
	}
	if maxItems <= 0 {
		maxItems = 20
	}
	if maxTokens <= 0 {
		maxTokens = 2000
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

	candidates, err := m.db.ListCandidateMemoriesForScopes(scopes, retentionDays)
	if err != nil || len(candidates) == 0 {
		return "", nil
	}

	cleanQuery := strings.TrimSpace(query)
	rankedMemories := candidates

	// Apply Strategy
	switch strings.ToLower(strategy) {
	case "recent":
		// Recency only: order by UpdatedAt DESC
		sort.Slice(rankedMemories, func(i, j int) bool {
			return rankedMemories[i].UpdatedAt.After(rankedMemories[j].UpdatedAt)
		})

	case "semantic":
		var queryVec []float32
		if m.embedder != nil && m.embedder.IsEnabled() && cleanQuery != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			queryVec, _ = m.embedder.Embed(ctx, cleanQuery)
			cancel()
		}
		if len(queryVec) > 0 {
			for i := range rankedMemories {
				mem := &rankedMemories[i]
				if len(mem.Embedding) > 0 {
					mem.Score = CosineSimilarity(queryVec, mem.Embedding)
				}
			}
			sort.Slice(rankedMemories, func(i, j int) bool {
				if rankedMemories[i].Score != rankedMemories[j].Score {
					return rankedMemories[i].Score > rankedMemories[j].Score
				}
				return rankedMemories[i].UpdatedAt.After(rankedMemories[j].UpdatedAt)
			})
		} else {
			sort.Slice(rankedMemories, func(i, j int) bool {
				return rankedMemories[i].UpdatedAt.After(rankedMemories[j].UpdatedAt)
			})
		}

	default: // "hybrid"
		if cleanQuery != "" {
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
				if strings.Contains(strings.ToLower(cleanQuery), strings.ToLower(mem.Key)) {
					score += 0.4
				}
				if time.Since(mem.UpdatedAt) < 7*24*time.Hour {
					score += 0.1
				}
				mem.Score = score
			}

			sort.Slice(rankedMemories, func(i, j int) bool {
				if rankedMemories[i].Score != rankedMemories[j].Score {
					return rankedMemories[i].Score > rankedMemories[j].Score
				}
				return rankedMemories[i].UpdatedAt.After(rankedMemories[j].UpdatedAt)
			})
		}
	}

	// Token budgeting & packing
	var factuals []storage.MemoryItemRecord
	var procedurals []storage.MemoryItemRecord
	var episodics []storage.MemoryItemRecord
	var semantics []storage.MemoryItemRecord

	usedTokens := 0
	count := 0

	for _, item := range rankedMemories {
		if count >= maxItems {
			break
		}
		line := fmt.Sprintf("- [%s] %s\n", item.Key, item.Content)
		lineTok := estimateTokens(line)
		if usedTokens+lineTok > maxTokens && count > 0 {
			// Budget reached
			break
		}
		usedTokens += lineTok
		count++

		// Increment access count asynchronously with promotion threshold
		go func(id string) {
			_ = m.db.IncrementMemoryAccess(id, promotionThreshold)
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

// estimateTokens approximates token count for text (~3 chars per token heuristic)
func estimateTokens(text string) int {
	runes := len([]rune(text))
	if runes == 0 {
		return 0
	}
	tok := runes / 3
	if tok == 0 {
		return 1
	}
	return tok
}

// CompactionReport summarizes the results of a compaction run
type CompactionReport struct {
	Scope         string `json:"scope,omitempty"`
	ScopeID       string `json:"scope_id,omitempty"`
	PrunedExpired int64  `json:"pruned_expired"`
	OptimizedFTS  bool   `json:"optimized_fts"`
	TotalActive   int    `json:"total_active"`
}

// Compact triggers memory compaction: prunes expired records and optimizes FTS5 index
func (m *Manager) Compact(ctx context.Context, scope, scopeID string) (*CompactionReport, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database memory belum terhubung")
	}
	m.mu.RLock()
	retentionDays := m.cfg.GetRetentionDays()
	m.mu.RUnlock()

	pruned, err := m.db.PruneExpiredMemories(retentionDays)
	if err != nil {
		return nil, fmt.Errorf("prune expired memories: %w", err)
	}

	_ = m.db.OptimizeMemoryStorage()

	total := 0
	if scope != "" && scopeID != "" {
		total, _ = m.db.CountMemories(scope, scopeID)
	}

	return &CompactionReport{
		Scope:         scope,
		ScopeID:       scopeID,
		PrunedExpired: pruned,
		OptimizedFTS:  true,
		TotalActive:   total,
	}, nil
}

// StartAutoCompactor runs periodic compaction in background
func (m *Manager) StartAutoCompactor(ctx context.Context) {
	if m == nil || m.db == nil {
		return
	}
	m.mu.RLock()
	intervalHours := m.cfg.CompactionIntervalHours
	if intervalHours <= 0 {
		intervalHours = 24
	}
	m.mu.RUnlock()

	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.mu.RLock()
				enabled := m.cfg.AutoCompaction
				m.mu.RUnlock()
				if enabled {
					_, _ = m.Compact(context.Background(), "", "")
				}
			}
		}
	}()
}

// Runtime Configuration Getters and Setters

// persistConfig serializes and writes current memory config to SQLite system_settings
func (m *Manager) persistConfig() {
	if m == nil || m.db == nil {
		return
	}
	m.mu.RLock()
	cfgCopy := m.cfg
	m.mu.RUnlock()

	data, err := json.Marshal(cfgCopy)
	if err != nil {
		log.Printf("⚠️ [Memory Manager] Failed to marshal memory_config: %v", err)
		return
	}
	if err := m.db.SetSetting("memory_config", string(data)); err != nil {
		log.Printf("⚠️ [Memory Manager] Failed to persist memory_config: %v", err)
	}
}

func (m *Manager) GetConfig() config.MemoryConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Enabled
}

func (m *Manager) SetEnabled(enabled bool) {
	m.mu.Lock()
	m.cfg.Enabled = enabled
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) SetAutoExtract(enabled bool) {
	m.mu.Lock()
	m.cfg.AutoExtract = enabled
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetStrategy() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.GetStrategy()
}

func (m *Manager) SetStrategy(strategy string) {
	m.mu.Lock()
	m.cfg.Strategy = strings.ToLower(strategy)
	m.cfg.RetrievalStrategy = m.cfg.Strategy
	m.cfg.MemoryStrategy = m.cfg.Strategy
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetMaxTokens() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.GetMaxTokens()
}

func (m *Manager) SetMaxTokens(tokens int) {
	m.mu.Lock()
	if tokens > 0 {
		m.cfg.MaxTokens = tokens
		m.cfg.MemoryMaxTokens = tokens
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetMaxContextItems() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.MaxContextItems <= 0 {
		return 20
	}
	return m.cfg.MaxContextItems
}

func (m *Manager) SetMaxContextItems(items int) {
	m.mu.Lock()
	if items > 0 {
		m.cfg.MaxContextItems = items
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetRetentionDays() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.GetRetentionDays()
}

func (m *Manager) SetRetentionDays(days int) {
	m.mu.Lock()
	if days > 0 {
		m.cfg.RetentionDays = days
		m.cfg.MemoryRetentionDays = days
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetPromotionThreshold() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.GetPromotionThreshold()
}

func (m *Manager) SetPromotionThreshold(threshold int) {
	m.mu.Lock()
	if threshold > 0 {
		m.cfg.PromotionThreshold = threshold
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) SetAutoCompaction(enabled bool) {
	m.mu.Lock()
	m.cfg.AutoCompaction = enabled
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) SetCompactionIntervalHours(hours int) {
	m.mu.Lock()
	if hours > 0 {
		m.cfg.CompactionIntervalHours = hours
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) SetCompactionThreshold(threshold int) {
	m.mu.Lock()
	if threshold > 0 {
		m.cfg.CompactionThreshold = threshold
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetSimilarityThreshold() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.SimilarityThreshold <= 0 {
		return 0.60
	}
	return m.cfg.SimilarityThreshold
}

func (m *Manager) SetSimilarityThreshold(thresh float64) {
	m.mu.Lock()
	if thresh > 0 && thresh <= 1.0 {
		m.cfg.SimilarityThreshold = thresh
	}
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) GetSeedCSVPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.SeedCSVPath
}

func (m *Manager) SetSeedCSVPath(path string) {
	m.mu.Lock()
	m.cfg.SeedCSVPath = strings.TrimSpace(path)
	m.mu.Unlock()
	m.persistConfig()
}

func (m *Manager) CountMemories(scope, scopeID string) (int, error) {
	if m.db == nil {
		return 0, fmt.Errorf("database memory belum terhubung")
	}
	return m.db.CountMemories(scope, scopeID)
}

func (m *Manager) GetEmbedder() Embedder {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.embedder
}

// SetEmbeddingModel updates embedding model name and activates embedding
func (m *Manager) SetEmbeddingModel(model string) {
	m.mu.Lock()
	cleanModel := strings.TrimSpace(model)
	m.cfg.Embedding.Model = cleanModel
	m.cfg.EmbeddingSource.Model = cleanModel
	if cleanModel != "" {
		m.cfg.Embedding.Enabled = true
		m.cfg.EmbeddingSource.Enabled = true
	}
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// SetEmbeddingProvider sets embedding provider (openai, gemini, ollama, custom)
func (m *Manager) SetEmbeddingProvider(prov string) {
	m.mu.Lock()
	cleanProv := strings.TrimSpace(strings.ToLower(prov))
	m.cfg.Embedding.Provider = cleanProv
	m.cfg.EmbeddingSource.Provider = cleanProv
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// SetEmbeddingBaseURL sets custom endpoint URL for embeddings (e.g. Ollama or reverse proxy)
func (m *Manager) SetEmbeddingBaseURL(baseURL string) {
	m.mu.Lock()
	cleanURL := strings.TrimSpace(baseURL)
	m.cfg.Embedding.BaseURL = cleanURL
	m.cfg.EmbeddingSource.BaseURL = cleanURL
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// SetEmbeddingAPIKey sets custom API key for embedding
func (m *Manager) SetEmbeddingAPIKey(apiKey string) {
	m.mu.Lock()
	cleanKey := strings.TrimSpace(apiKey)
	m.cfg.Embedding.APIKey = cleanKey
	m.cfg.EmbeddingSource.APIKey = cleanKey
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// SetEmbeddingDimensions sets vector dimensions for embedding (e.g. 768, 1536)
func (m *Manager) SetEmbeddingDimensions(dims int) {
	m.mu.Lock()
	if dims > 0 {
		m.cfg.Embedding.Dimensions = dims
		m.cfg.EmbeddingSource.Dimensions = dims
	}
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// SetEmbeddingEnabled toggles vector embedding on or off
func (m *Manager) SetEmbeddingEnabled(enabled bool) {
	m.mu.Lock()
	m.cfg.Embedding.Enabled = enabled
	m.cfg.EmbeddingSource.Enabled = enabled
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// ResetToDefaults resets all memory configuration values back to default settings
func (m *Manager) ResetToDefaults() {
	m.mu.Lock()
	m.cfg.Enabled = true
	m.cfg.AutoExtract = true
	m.cfg.Strategy = "hybrid"
	m.cfg.RetrievalStrategy = "hybrid"
	m.cfg.MemoryStrategy = "hybrid"
	m.cfg.MaxTokens = 2000
	m.cfg.MemoryMaxTokens = 2000
	m.cfg.MaxContextItems = 20
	m.cfg.RetentionDays = 30
	m.cfg.MemoryRetentionDays = 30
	m.cfg.PromotionThreshold = 3
	m.cfg.AutoCompaction = true
	m.cfg.CompactionIntervalHours = 24
	m.cfg.CompactionThreshold = 100
	m.cfg.SimilarityThreshold = 0.60
	m.cfg.Embedding.Enabled = false
	m.cfg.Embedding.Provider = "openai"
	m.cfg.Embedding.Model = "text-embedding-3-small"
	m.cfg.Embedding.BaseURL = ""
	m.cfg.Embedding.APIKey = ""
	m.cfg.Embedding.Dimensions = 1536
	m.cfg.EmbeddingSource = m.cfg.Embedding
	m.embedder = NewEmbedder(m.cfg.Embedding)
	if m.extractor != nil {
		m.extractor.SetEmbedder(m.embedder)
	}
	m.mu.Unlock()
	m.persistConfig()
}

// ImportSeedCSV imports memories from an export CSV file for a scope/user
func (m *Manager) ImportSeedCSV(ctx context.Context, csvPath, userID string) (*SeedResult, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database memory belum terhubung")
	}
	if strings.TrimSpace(csvPath) == "" {
		m.mu.RLock()
		csvPath = m.cfg.SeedCSVPath
		m.mu.RUnlock()
	}
	return SeedFromCSV(ctx, m.db, csvPath, userID, m.embedder)
}
