package provider

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/storage"
)

// ModelCapability defines the operational capabilities of an AI model
type ModelCapability struct {
	ModelID           string `json:"model_id"`
	RawID             string `json:"raw_id"`
	DisplayName       string `json:"display_name"`
	ProviderFamily    string `json:"provider_family"`
	Modality          string `json:"modality"`
	SupportsVision    bool   `json:"supports_vision"`
	SupportsAudioIn   bool   `json:"supports_audio_in"`
	SupportsAudioOut  bool   `json:"supports_audio_out"`
	SupportsTools     bool   `json:"supports_tools"`
	SupportsReasoning bool   `json:"supports_reasoning"`
	ContextLength     int    `json:"context_length"`
	IsCustom          bool   `json:"is_custom"`
}

// ModelCatalog manages model capability resolution, caching, and background sync
type ModelCatalog struct {
	mu    sync.RWMutex
	items map[string]ModelCapability // Normalized model_id -> capability
	db    *storage.DB
}

var (
	globalCatalog     *ModelCatalog
	catalogOnce       sync.Once
	builtInSeedsOnce  sync.Once
	builtInSeedsCache []ModelCapability
)

// GetCatalog returns the singleton ModelCatalog instance
func GetCatalog() *ModelCatalog {
	catalogOnce.Do(func() {
		globalCatalog = &ModelCatalog{
			items: make(map[string]ModelCapability),
		}
		globalCatalog.loadBuiltInSeeds()
	})
	return globalCatalog
}

// InitCatalog initializes catalog with database storage and starts background sync
func InitCatalog(db *storage.DB) *ModelCatalog {
	cat := GetCatalog()
	cat.mu.Lock()
	cat.db = db
	cat.mu.Unlock()

	// 1. Load from DB if existing
	if db != nil {
		if dbRecords, err := db.GetModelCatalog(); err == nil && len(dbRecords) > 0 {
			cat.mu.Lock()
			for _, rec := range dbRecords {
				cat.items[NormalizeModelID(rec.ModelID)] = ModelCapability{
					ModelID:           rec.ModelID,
					RawID:             rec.RawID,
					DisplayName:       rec.DisplayName,
					ProviderFamily:    rec.ProviderFamily,
					Modality:          rec.Modality,
					SupportsVision:    rec.SupportsVision,
					SupportsAudioIn:   rec.SupportsAudioIn,
					SupportsAudioOut:  rec.SupportsAudioOut,
					SupportsTools:     rec.SupportsTools,
					SupportsReasoning: rec.SupportsReasoning,
					ContextLength:     rec.ContextLength,
					IsCustom:          rec.IsCustom,
				}
			}
			cat.mu.Unlock()
			log.Printf("📦 [ModelCatalog] Memuat %d model dari database SQLite", len(dbRecords))
		}
	}

	// 2. Start background sync from OpenRouter public API (non-blocking)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := cat.SyncOpenRouter(ctx); err != nil {
			log.Printf("ℹ️ [ModelCatalog] OpenRouter sync tidak tersedia (offline/timeout): %v", err)
		}
	}()

	return cat
}

// NormalizeModelID cleans and normalizes raw model identifiers
func NormalizeModelID(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return ""
	}

	// Strip common provider/route prefixes (e.g. "wz/...", "dahl/...", "openai/...")
	if slashIdx := strings.Index(s, "/"); slashIdx != -1 {
		prefix := s[:slashIdx]
		// If prefix looks like a known provider or organization
		switch prefix {
		case "wz", "dahl", "9router", "openai", "google", "anthropic", "deepseek", "groq", "meta-llama", "qwen", "mistralai", "cohere", "ollama":
			s = s[slashIdx+1:]
		}
	}

	// Handle second level prefix like "deepseek-ai/DeepSeek-V4-Flash" -> "DeepSeek-V4-Flash"
	if slashIdx := strings.Index(s, "/"); slashIdx != -1 {
		s = s[slashIdx+1:]
	}

	// Remove common tags (:free, :batch, :latest, :online)
	if colonIdx := strings.Index(s, ":"); colonIdx != -1 {
		s = s[:colonIdx]
	}

	return s
}

// Resolve looks up the capability of a model by ID, with heuristic fallback
func (c *ModelCatalog) Resolve(modelName string) ModelCapability {
	if c == nil {
		return InferCapability(modelName)
	}

	cleanID := NormalizeModelID(modelName)
	if cleanID == "" {
		return InferCapability(modelName)
	}

	c.mu.RLock()
	cap, found := c.items[cleanID]
	c.mu.RUnlock()

	if found {
		return cap
	}

	// Try fuzzy / substring match against registered items
	c.mu.RLock()
	for k, v := range c.items {
		if strings.EqualFold(k, cleanID) || strings.HasPrefix(cleanID, k) || strings.HasPrefix(k, cleanID) {
			c.mu.RUnlock()
			return v
		}
	}
	c.mu.RUnlock()

	// Fallback to heuristic rules
	return InferCapability(modelName)
}

// FilterVisionModels filters a slice of model names to only those supporting Vision
func (c *ModelCatalog) FilterVisionModels(models []string) []string {
	var out []string
	for _, m := range models {
		cap := c.Resolve(m)
		if cap.SupportsVision {
			out = append(out, m)
		}
	}
	return out
}

// FilterAudioModels filters a slice of model names to only those supporting Audio/TTS Output
func (c *ModelCatalog) FilterAudioModels(models []string) []string {
	var out []string
	for _, m := range models {
		cap := c.Resolve(m)
		if cap.SupportsAudioOut {
			out = append(out, m)
		}
	}
	return out
}

// InferCapability provides accurate heuristic capabilities based on model name patterns
func InferCapability(modelName string) ModelCapability {
	clean := NormalizeModelID(modelName)
	lower := strings.ToLower(clean)

	cap := ModelCapability{
		ModelID:        modelName,
		RawID:          modelName,
		DisplayName:    modelName,
		SupportsTools:  true,
		ContextLength:  4096,
		Modality:       "text->text",
		SupportsVision: false,
	}

	// 1. Vision Detection Heuristics
	// Models that explicitly support vision
	isVision := false
	if strings.Contains(lower, "vision") ||
		strings.Contains(lower, "-vl") ||
		strings.Contains(lower, "vl-") ||
		strings.Contains(lower, "omni") ||
		strings.Contains(lower, "4o") ||
		strings.Contains(lower, "pixtral") ||
		strings.Contains(lower, "llava") ||
		strings.Contains(lower, "gemini") ||
		strings.Contains(lower, "claude-3") ||
		strings.Contains(lower, "llama-3.2-11b") ||
		strings.Contains(lower, "llama-3.2-90b") {
		isVision = true
	}

	// Specific text-only overrides (even if matching generic patterns)
	if strings.Contains(lower, "deepseek") && !strings.Contains(lower, "vl") && !strings.Contains(lower, "vision") {
		isVision = false
	}
	if strings.Contains(lower, "coder") && !strings.Contains(lower, "vl") {
		isVision = false
	}
	if strings.Contains(lower, "mistral-nemo") || strings.Contains(lower, "mistral-large") {
		isVision = false
	}

	cap.SupportsVision = isVision
	if isVision {
		cap.Modality = "text+image->text"
	}

	// 2. Audio Input / Output Heuristics
	if strings.Contains(lower, "audio") || strings.Contains(lower, "tts") || strings.Contains(lower, "speech") || strings.Contains(lower, "realtime") {
		cap.SupportsAudioIn = true
		cap.SupportsAudioOut = true
		cap.Modality = "multimodal->multimodal"
	} else if strings.Contains(lower, "gemini-2.0") || strings.Contains(lower, "gpt-4o-audio") {
		cap.SupportsAudioIn = true
		cap.SupportsAudioOut = true
	}

	// 3. Reasoning / Thinking Heuristics
	if strings.Contains(lower, "reasoner") ||
		strings.Contains(lower, "-r1") ||
		strings.Contains(lower, "r1-") ||
		strings.Contains(lower, "deepseek-r1") ||
		strings.Contains(lower, "o1") ||
		strings.Contains(lower, "o3") ||
		strings.Contains(lower, "thinking") {
		cap.SupportsReasoning = true
	}

	// 4. Provider Family
	switch {
	case strings.Contains(lower, "deepseek"):
		cap.ProviderFamily = "deepseek"
	case strings.Contains(lower, "gpt") || strings.Contains(lower, "o1") || strings.Contains(lower, "o3"):
		cap.ProviderFamily = "openai"
	case strings.Contains(lower, "gemini"):
		cap.ProviderFamily = "google"
	case strings.Contains(lower, "claude"):
		cap.ProviderFamily = "anthropic"
	case strings.Contains(lower, "qwen"):
		cap.ProviderFamily = "qwen"
	case strings.Contains(lower, "llama"):
		cap.ProviderFamily = "meta"
	case strings.Contains(lower, "mistral") || strings.Contains(lower, "pixtral"):
		cap.ProviderFamily = "mistral"
	default:
		cap.ProviderFamily = "custom"
	}

	return cap
}

// SyncOpenRouter fetches live model capabilities from the public OpenRouter API
func (c *ModelCatalog) SyncOpenRouter(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var orResp struct {
		Data []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ContextLen   int    `json:"context_length"`
			Architecture struct {
				Modality        string   `json:"modality"`
				InputModalities []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &orResp); err != nil {
		return err
	}

	var toUpsert []storage.ModelCatalogRecord
	c.mu.Lock()
	for _, m := range orResp.Data {
		cleanID := NormalizeModelID(m.ID)
		if cleanID == "" {
			continue
		}

		supportsVis := false
		supportsAudIn := false
		supportsAudOut := false

		for _, inMod := range m.Architecture.InputModalities {
			switch strings.ToLower(inMod) {
			case "image":
				supportsVis = true
			case "audio":
				supportsAudIn = true
			}
		}

		for _, outMod := range m.Architecture.OutputModalities {
			if strings.ToLower(outMod) == "audio" {
				supportsAudOut = true
			}
		}

		// Check modality string as fallback
		if !supportsVis && strings.Contains(strings.ToLower(m.Architecture.Modality), "image") {
			supportsVis = true
		}
		if !supportsAudIn && strings.Contains(strings.ToLower(m.Architecture.Modality), "audio") {
			supportsAudIn = true
		}

		cap := ModelCapability{
			ModelID:           cleanID,
			RawID:             m.ID,
			DisplayName:       m.Name,
			Modality:          m.Architecture.Modality,
			SupportsVision:    supportsVis,
			SupportsAudioIn:   supportsAudIn,
			SupportsAudioOut:  supportsAudOut,
			SupportsTools:     true,
			SupportsReasoning: strings.Contains(strings.ToLower(m.ID), "r1") || strings.Contains(strings.ToLower(m.ID), "reasoner") || strings.Contains(strings.ToLower(m.ID), "o1") || strings.Contains(strings.ToLower(m.ID), "o3"),
			ContextLength:     m.ContextLen,
		}

		c.items[cleanID] = cap
		toUpsert = append(toUpsert, storage.ModelCatalogRecord{
			ModelID:           cleanID,
			RawID:             m.ID,
			DisplayName:       m.Name,
			Modality:          m.Architecture.Modality,
			SupportsVision:    supportsVis,
			SupportsAudioIn:   supportsAudIn,
			SupportsAudioOut:  supportsAudOut,
			SupportsTools:     true,
			SupportsReasoning: cap.SupportsReasoning,
			ContextLength:     m.ContextLen,
		})
	}
	c.mu.Unlock()

	// Persist to database if db connected
	if c.db != nil && len(toUpsert) > 0 {
		_ = c.db.UpsertModelCatalog(toUpsert)
		log.Printf("✅ [ModelCatalog] Berhasil sinkronisasi & memperbarui %d model dari OpenRouter API", len(toUpsert))
	}

	return nil
}

// loadBuiltInSeeds seeds the catalog with well-known models for instant offline lookup
func (c *ModelCatalog) loadBuiltInSeeds() {
	builtInSeedsOnce.Do(func() {
		builtInSeedsCache = []ModelCapability{
			// DeepSeek Models (STRICTLY Text-Only)
			{ModelID: "deepseek-chat", DisplayName: "DeepSeek V3 Chat", ProviderFamily: "deepseek", SupportsVision: false, SupportsTools: true},
			{ModelID: "deepseek-v3", DisplayName: "DeepSeek V3", ProviderFamily: "deepseek", SupportsVision: false, SupportsTools: true},
			{ModelID: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", ProviderFamily: "deepseek", SupportsVision: false, SupportsTools: true},
			{ModelID: "deepseek-v4-flash-0731", DisplayName: "DeepSeek V4 Flash 0731", ProviderFamily: "deepseek", SupportsVision: false, SupportsTools: true},
			{ModelID: "deepseek-coder", DisplayName: "DeepSeek Coder", ProviderFamily: "deepseek", SupportsVision: false, SupportsTools: true},
			{ModelID: "deepseek-reasoner", DisplayName: "DeepSeek R1 Reasoner", ProviderFamily: "deepseek", SupportsVision: false, SupportsReasoning: true, SupportsTools: false},
			{ModelID: "deepseek-r1", DisplayName: "DeepSeek R1", ProviderFamily: "deepseek", SupportsVision: false, SupportsReasoning: true, SupportsTools: false},

			// OpenAI Models (Multimodal / Vision)
			{ModelID: "gpt-4o", DisplayName: "GPT-4o (Omni)", ProviderFamily: "openai", SupportsVision: true, SupportsAudioIn: true, SupportsAudioOut: true, SupportsTools: true},
			{ModelID: "gpt-4o-mini", DisplayName: "GPT-4o Mini", ProviderFamily: "openai", SupportsVision: true, SupportsTools: true},
			{ModelID: "gpt-4o-audio-preview", DisplayName: "GPT-4o Audio", ProviderFamily: "openai", SupportsVision: true, SupportsAudioIn: true, SupportsAudioOut: true, SupportsTools: true},
			{ModelID: "chatgpt-4o-latest", DisplayName: "ChatGPT-4o Latest", ProviderFamily: "openai", SupportsVision: true, SupportsTools: true},
			{ModelID: "gpt-4-turbo", DisplayName: "GPT-4 Turbo Vision", ProviderFamily: "openai", SupportsVision: true, SupportsTools: true},
			{ModelID: "gpt-3.5-turbo", DisplayName: "GPT-3.5 Turbo", ProviderFamily: "openai", SupportsVision: false, SupportsTools: true},
			{ModelID: "o1", DisplayName: "OpenAI o1 Reasoning", ProviderFamily: "openai", SupportsVision: true, SupportsReasoning: true, SupportsTools: true},
			{ModelID: "o3-mini", DisplayName: "OpenAI o3-mini Reasoning", ProviderFamily: "openai", SupportsVision: false, SupportsReasoning: true, SupportsTools: true},

			// Google Gemini Models (Multimodal / Vision + Audio)
			{ModelID: "gemini-2.0-flash", DisplayName: "Gemini 2.0 Flash", ProviderFamily: "google", SupportsVision: true, SupportsAudioIn: true, SupportsAudioOut: true, SupportsTools: true},
			{ModelID: "gemini-2.0-flash-exp", DisplayName: "Gemini 2.0 Flash Exp", ProviderFamily: "google", SupportsVision: true, SupportsAudioIn: true, SupportsAudioOut: true, SupportsTools: true},
			{ModelID: "gemini-2.0-flash-lite", DisplayName: "Gemini 2.0 Flash Lite", ProviderFamily: "google", SupportsVision: true, SupportsTools: true},
			{ModelID: "gemini-2.0-pro-exp", DisplayName: "Gemini 2.0 Pro Exp", ProviderFamily: "google", SupportsVision: true, SupportsTools: true},
			{ModelID: "gemini-1.5-pro", DisplayName: "Gemini 1.5 Pro", ProviderFamily: "google", SupportsVision: true, SupportsAudioIn: true, SupportsTools: true},
			{ModelID: "gemini-1.5-flash", DisplayName: "Gemini 1.5 Flash", ProviderFamily: "google", SupportsVision: true, SupportsAudioIn: true, SupportsTools: true},

			// Anthropic Claude Models (Vision)
			{ModelID: "claude-3-5-sonnet", DisplayName: "Claude 3.5 Sonnet", ProviderFamily: "anthropic", SupportsVision: true, SupportsTools: true},
			{ModelID: "claude-3-5-sonnet-20241022", DisplayName: "Claude 3.5 Sonnet Latest", ProviderFamily: "anthropic", SupportsVision: true, SupportsTools: true},
			{ModelID: "claude-3-5-haiku", DisplayName: "Claude 3.5 Haiku", ProviderFamily: "anthropic", SupportsVision: true, SupportsTools: true},
			{ModelID: "claude-3-opus", DisplayName: "Claude 3 Opus", ProviderFamily: "anthropic", SupportsVision: true, SupportsTools: true},
			{ModelID: "claude-3-7-sonnet", DisplayName: "Claude 3.7 Sonnet Hybrid", ProviderFamily: "anthropic", SupportsVision: true, SupportsReasoning: true, SupportsTools: true},

			// Qwen Models
			{ModelID: "qwen-vl-max", DisplayName: "Qwen VL Max", ProviderFamily: "qwen", SupportsVision: true, SupportsTools: true},
			{ModelID: "qwen2.5-vl-72b", DisplayName: "Qwen 2.5 VL 72B", ProviderFamily: "qwen", SupportsVision: true, SupportsTools: true},
			{ModelID: "qwen2.5-vl-7b", DisplayName: "Qwen 2.5 VL 7B", ProviderFamily: "qwen", SupportsVision: true, SupportsTools: true},
			{ModelID: "qwen2.5-coder-32b", DisplayName: "Qwen 2.5 Coder 32B", ProviderFamily: "qwen", SupportsVision: false, SupportsTools: true},
			{ModelID: "qwen2.5-72b", DisplayName: "Qwen 2.5 72B", ProviderFamily: "qwen", SupportsVision: false, SupportsTools: true},

			// Meta LLaMA Models
			{ModelID: "llama-3.2-11b-vision", DisplayName: "LLaMA 3.2 11B Vision", ProviderFamily: "meta", SupportsVision: true, SupportsTools: true},
			{ModelID: "llama-3.2-90b-vision", DisplayName: "LLaMA 3.2 90B Vision", ProviderFamily: "meta", SupportsVision: true, SupportsTools: true},
			{ModelID: "llama-3.3-70b", DisplayName: "LLaMA 3.3 70B Instruct", ProviderFamily: "meta", SupportsVision: false, SupportsTools: true},
			{ModelID: "llama-3.1-8b", DisplayName: "LLaMA 3.1 8B Instruct", ProviderFamily: "meta", SupportsVision: false, SupportsTools: true},

			// Mistral Models
			{ModelID: "pixtral-12b", DisplayName: "Pixtral 12B Vision", ProviderFamily: "mistral", SupportsVision: true, SupportsTools: true},
			{ModelID: "pixtral-large", DisplayName: "Pixtral Large Vision", ProviderFamily: "mistral", SupportsVision: true, SupportsTools: true},
			{ModelID: "mistral-large", DisplayName: "Mistral Large", ProviderFamily: "mistral", SupportsVision: false, SupportsTools: true},
			{ModelID: "mistral-nemo", DisplayName: "Mistral NeMo", ProviderFamily: "mistral", SupportsVision: false, SupportsTools: true},
		}
	})

	for _, seed := range builtInSeedsCache {
		c.items[NormalizeModelID(seed.ModelID)] = seed
	}
}
