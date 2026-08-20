package tokensaver

import (
	"encoding/json"
	"strings"
)

// Engine identifiers
const (
	EngineSessionDedup        = "session_dedup"
	EngineCCR                 = "ccr"
	EngineLite                = "lite"
	EngineRTK                 = "rtk"
	EngineResponsesToolOutput = "responses_tool_output"
	EngineHeadroom            = "headroom"
	EngineRelevance           = "relevance"
	EngineCaveman             = "caveman"
	EngineAggressive          = "aggressive"
	EngineLLMLingua2          = "llmlingua2"
	EngineUltra               = "ultra"
	EngineOmniGlyph           = "omniglyph"
)

// List of all 12 engines in canonical execution order
var CanonicalEngines = []string{
	EngineSessionDedup,
	EngineCCR,
	EngineLite,
	EngineRTK,
	EngineResponsesToolOutput,
	EngineHeadroom,
	EngineRelevance,
	EngineCaveman,
	EngineAggressive,
	EngineLLMLingua2,
	EngineUltra,
	EngineOmniGlyph,
}

// Preset types
const (
	PresetLite       = "lite"       // ~15% - Always-on safe default
	PresetStandard   = "standard"   // ~30% - Caveman / daily coding
	PresetAggressive = "aggressive" // ~50% - Long tool sessions
	PresetUltra      = "ultra"      // ~75% - Max savings
	PresetRTK        = "rtk"        // 60-90% - Shell/tool/git output focus
	PresetStacked    = "stacked"    // 78-95% - RTK -> Caveman -> Headroom -> Dedup
	PresetCustom     = "custom"     // Fully custom per-engine toggles
	PresetOff        = "off"        // Disabled
)

// Output Styles (output-axis steering)
const (
	StyleNone             = "none"
	StyleTerseProse       = "terse_prose"       // Drop filler / articles / hedging
	StyleLessCode         = "less_code"         // Lazy senior dev: smallest working diff
	StyleIndonesianRingkas = "id_ringkas"        // Bahasa Indonesia super padat to-the-point
	StyleTroglodita       = "troglodita"        // PT-BR / concise shorthand
	StyleTerseCJK         = "terse_cjk"         // Classical-Chinese ultra-terse
)

// Intensity levels for output styles
const (
	IntensityLite  = "lite"
	IntensityFull  = "full"
	IntensityUltra = "ultra"
)

// EngineSettings stores individual settings for a single engine
type EngineSettings struct {
	Enabled    bool                   `json:"enabled"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

// StackConfig represents the comprehensive configuration for the 12-engine stack
type StackConfig struct {
	Preset          string                    `json:"preset"`                     // lite, standard, aggressive, ultra, rtk, stacked, custom, off
	OutputStyle     string                    `json:"output_style"`              // none, terse_prose, less_code, id_ringkas, troglodita, terse_cjk
	StyleIntensity  string                    `json:"style_intensity"`           // lite, full, ultra
	AdaptiveDial    bool                      `json:"adaptive_dial"`             // auto-escalate engines when approaching context limit
	ContextBudget   int                       `json:"context_budget"`            // target max tokens (0 = use model default)
	EngineOverrides map[string]EngineSettings `json:"engine_overrides,omitempty"`// per-engine toggle and parameters
}

// DefaultStackConfig returns the default configuration (Standard / RTK + Lite)
func DefaultStackConfig() *StackConfig {
	return GetPresetConfig(PresetStandard)
}

// GetPresetConfig builds a StackConfig for a given preset
func GetPresetConfig(preset string) *StackConfig {
	cfg := &StackConfig{
		Preset:          preset,
		OutputStyle:     StyleNone,
		StyleIntensity:  IntensityFull,
		AdaptiveDial:    true,
		ContextBudget:   0,
		EngineOverrides: make(map[string]EngineSettings),
	}

	// Initialize all engines disabled by default
	for _, id := range CanonicalEngines {
		cfg.EngineOverrides[id] = EngineSettings{
			Enabled:    false,
			Parameters: make(map[string]interface{}),
		}
	}

	preset = strings.ToLower(strings.TrimSpace(preset))
	switch preset {
	case PresetOff:
		// All disabled

	case PresetLite:
		// 1. Session-Dedup, 3. Lite
		cfg.setEngine(EngineSessionDedup, true)
		cfg.setEngine(EngineLite, true)

	case PresetStandard, "caveman", "auto":
		// 1. Session-Dedup, 3. Lite, 4. RTK, 8. Caveman
		cfg.Preset = PresetStandard
		cfg.setEngine(EngineSessionDedup, true)
		cfg.setEngine(EngineLite, true)
		cfg.setEngine(EngineRTK, true)
		cfg.setEngine(EngineCaveman, true)

	case PresetRTK:
		// 3. Lite, 4. RTK, 5. Responses Tool Output, 6. Headroom
		cfg.setEngine(EngineLite, true)
		cfg.setEngine(EngineRTK, true)
		cfg.setEngine(EngineResponsesToolOutput, true)
		cfg.setEngine(EngineHeadroom, true)

	case PresetStacked:
		// 1. Session-Dedup, 3. Lite, 4. RTK, 5. Responses, 6. Headroom, 8. Caveman
		cfg.setEngine(EngineSessionDedup, true)
		cfg.setEngine(EngineLite, true)
		cfg.setEngine(EngineRTK, true)
		cfg.setEngine(EngineResponsesToolOutput, true)
		cfg.setEngine(EngineHeadroom, true)
		cfg.setEngine(EngineCaveman, true)

	case PresetAggressive:
		// 1. Session-Dedup, 2. CCR, 3. Lite, 4. RTK, 5. Responses, 6. Headroom, 8. Caveman, 9. Aggressive
		cfg.setEngine(EngineSessionDedup, true)
		cfg.setEngine(EngineCCR, true)
		cfg.setEngine(EngineLite, true)
		cfg.setEngine(EngineRTK, true)
		cfg.setEngine(EngineResponsesToolOutput, true)
		cfg.setEngine(EngineHeadroom, true)
		cfg.setEngine(EngineCaveman, true)
		cfg.setEngine(EngineAggressive, true)

	case PresetUltra:
		// Enable all 12 engines
		for _, id := range CanonicalEngines {
			cfg.setEngine(id, true)
		}

	default:
		// Standard
		cfg.Preset = PresetStandard
		cfg.setEngine(EngineSessionDedup, true)
		cfg.setEngine(EngineLite, true)
		cfg.setEngine(EngineRTK, true)
		cfg.setEngine(EngineCaveman, true)
	}

	return cfg
}

func (c *StackConfig) setEngine(id string, enabled bool) {
	s := c.EngineOverrides[id]
	s.Enabled = enabled
	c.EngineOverrides[id] = s
}

// IsEngineEnabled checks if a specific engine is active under current configuration
func (c *StackConfig) IsEngineEnabled(id string) bool {
	if c.Preset == PresetOff {
		return false
	}
	if s, ok := c.EngineOverrides[id]; ok {
		return s.Enabled
	}
	return false
}

// SetEngineToggle sets the status of a specific engine and marks preset as custom if needed
func (c *StackConfig) SetEngineToggle(id string, enabled bool) {
	if c.EngineOverrides == nil {
		c.EngineOverrides = make(map[string]EngineSettings)
	}
	s := c.EngineOverrides[id]
	s.Enabled = enabled
	c.EngineOverrides[id] = s
	c.Preset = PresetCustom
}

// GetEngineParam returns a parameter value or fallback default
func (c *StackConfig) GetEngineParam(id string, key string, fallback interface{}) interface{} {
	if s, ok := c.EngineOverrides[id]; ok && s.Parameters != nil {
		if val, exists := s.Parameters[key]; exists {
			return val
		}
	}
	return fallback
}

// SetEngineParam sets a specific parameter for an engine
func (c *StackConfig) SetEngineParam(id string, key string, val interface{}) {
	if c.EngineOverrides == nil {
		c.EngineOverrides = make(map[string]EngineSettings)
	}
	s := c.EngineOverrides[id]
	if s.Parameters == nil {
		s.Parameters = make(map[string]interface{})
	}
	s.Parameters[key] = val
	c.EngineOverrides[id] = s
}

// SerializeToJSON converts stack config to JSON string
func (c *StackConfig) SerializeToJSON() string {
	b, err := json.Marshal(c)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ParseStackConfig decodes a JSON or string into StackConfig
func ParseStackConfig(raw string) *StackConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultStackConfig()
	}

	// If it's a simple preset string (e.g. "auto", "caveman", "lite", "ultra")
	if !strings.HasPrefix(raw, "{") {
		return GetPresetConfig(raw)
	}

	cfg := &StackConfig{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return DefaultStackConfig()
	}

	if cfg.EngineOverrides == nil {
		cfg.EngineOverrides = make(map[string]EngineSettings)
	}
	return cfg
}
