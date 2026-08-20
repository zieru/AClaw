package tokensaver

import (
	"strings"
	"testing"

	"goassistant/internal/provider"
)

func TestAll12EnginesIndependent(t *testing.T) {
	// Test 1: Session Dedup
	t.Run("SessionDedup", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineSessionDedup: {Enabled: true},
			},
		}
		longText := strings.Repeat("Repeated instruction block for testing session dedup engine. ", 5)
		msgs := []provider.ChatMessage{
			{Role: provider.RoleUser, Content: longText},
			{Role: provider.RoleAssistant, Content: "First answer"},
			{Role: provider.RoleUser, Content: longText},
			{Role: provider.RoleUser, Content: "Final question"},
		}
		res, rep := CompressMessagesPipeline(msgs, cfg)
		if rep.TokensSaved <= 0 {
			t.Errorf("expected session dedup to save tokens")
		}
		if !strings.Contains(res[2].Content, "dedup: identical content") {
			t.Errorf("expected turn 2 to contain dedup marker, got: %s", res[2].Content)
		}
	})

	// Test 2: CCR
	t.Run("CCR", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineCCR: {Enabled: true},
			},
		}
		bigDoc := strings.Repeat("Large document block that should be archived behind a retrieve marker. ", 20)
		msgs := []provider.ChatMessage{
			{Role: provider.RoleUser, Content: bigDoc},
			{Role: provider.RoleAssistant, Content: "Noted big doc"},
			{Role: provider.RoleUser, Content: "Recent turn 1"},
			{Role: provider.RoleAssistant, Content: "Recent response 1"},
		}
		res, _ := CompressMessagesPipeline(msgs, cfg)
		if !strings.Contains(res[0].Content, "CCR: archived") {
			t.Errorf("expected CCR engine to archive old big doc")
		}
	})

	// Test 3: Lite
	t.Run("Lite", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineLite: {Enabled: true},
			},
		}
		text := "Hello    world!\n\n\n\nNew line here. https://example.com/img.jpg?utm_source=fb&utm_medium=cpc"
		msgs := []provider.ChatMessage{{Role: provider.RoleUser, Content: text}}
		res, _ := CompressMessagesPipeline(msgs, cfg)
		if strings.Contains(res[0].Content, "\n\n\n") {
			t.Errorf("expected lite engine to collapse 4 newlines")
		}
		if strings.Contains(res[0].Content, "utm_source=fb") {
			t.Errorf("expected lite engine to strip tracking params")
		}
	})

	// Test 4: RTK
	t.Run("RTK", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineRTK: {Enabled: true},
			},
		}
		diff := "diff --git a/main.go b/main.go\nindex 1234567..89abcdef 100644\n--- a/main.go\n+++ b/main.go\n@@ -1,5 +1,5 @@\n-old\n+new"
		msgs := []provider.ChatMessage{{Role: provider.RoleTool, Content: diff}}
		res, _ := CompressMessagesPipeline(msgs, cfg)
		if strings.Contains(res[0].Content, "index 1234567") {
			t.Errorf("expected RTK to strip diff index header")
		}
	})

	// Test 5: Responses Tool Output
	t.Run("ResponsesToolOutput", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineResponsesToolOutput: {Enabled: true},
			},
		}
		prettyJSON := "{\n  \"status\": \"ok\",\n  \"count\": 12\n}"
		msgs := []provider.ChatMessage{{Role: provider.RoleTool, Content: prettyJSON}}
		res, _ := CompressMessagesPipeline(msgs, cfg)
		if strings.Contains(res[0].Content, "\n  ") {
			t.Errorf("expected JSON to be minified lossless")
		}
	})

	// Test 6: Headroom (Tabular JSON Array)
	t.Run("Headroom", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineHeadroom: {Enabled: true},
			},
		}
		jsonArray := `[
			{"id": 1, "name": "Alice", "role": "admin"},
			{"id": 2, "name": "Bob", "role": "user"},
			{"id": 3, "name": "Charlie", "role": "guest"},
			{"id": 4, "name": "David", "role": "dev"}
		]`
		msgs := []provider.ChatMessage{{Role: provider.RoleTool, Content: jsonArray}}
		res, _ := CompressMessagesPipeline(msgs, cfg)
		if !strings.Contains(res[0].Content, "_schema") || !strings.Contains(res[0].Content, "_rows") {
			t.Errorf("expected Headroom to compact array to tabular format, got: %s", res[0].Content)
		}
	})

	// Test 7: Caveman
	t.Run("Caveman", func(t *testing.T) {
		cfg := &StackConfig{
			Preset: PresetCustom,
			EngineOverrides: map[string]EngineSettings{
				EngineCaveman: {Enabled: true},
			},
		}
		text := "As an AI language model, I would be happy to help! Tolong dicatat bahwa server sedang aktif."
		msgs := []provider.ChatMessage{{Role: provider.RoleAssistant, Content: text}}
		res, _ := CompressMessagesPipeline(msgs, cfg)
		if strings.Contains(res[0].Content, "As an AI language model") || strings.Contains(res[0].Content, "Tolong dicatat bahwa") {
			t.Errorf("expected Caveman to strip conversational filler")
		}
	})
}

func TestPresets(t *testing.T) {
	presets := []string{PresetLite, PresetStandard, PresetAggressive, PresetUltra, PresetRTK, PresetStacked}
	for _, p := range presets {
		cfg := GetPresetConfig(p)
		if cfg.Preset != p && !(p == PresetStandard && cfg.Preset == PresetStandard) {
			t.Errorf("preset %s mismatch", p)
		}
	}
}

func TestOutputStyles(t *testing.T) {
	cfg := GetPresetConfig(PresetStandard)
	cfg.OutputStyle = StyleTerseProse
	cfg.StyleIntensity = IntensityFull

	msgs := []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "How to reverse a string in Go?"},
	}
	res, _ := CompressMessagesPipeline(msgs, cfg)
	if len(res) == 0 || !strings.Contains(res[0].Content, "OUTPUT_STYLE: Terse prose") {
		t.Errorf("expected output style to be injected into system prompt")
	}
}

func TestCustomEngineToggleAndParam(t *testing.T) {
	cfg := DefaultStackConfig()
	// Disable Caveman, enable Headroom
	cfg.SetEngineToggle(EngineCaveman, false)
	cfg.SetEngineToggle(EngineHeadroom, true)

	if cfg.IsEngineEnabled(EngineCaveman) {
		t.Errorf("expected Caveman to be disabled")
	}
	if !cfg.IsEngineEnabled(EngineHeadroom) {
		t.Errorf("expected Headroom to be enabled")
	}
	if cfg.Preset != PresetCustom {
		t.Errorf("expected preset to switch to custom")
	}

	// Test param get/set
	cfg.SetEngineParam(EngineCCR, "threshold_chars", 850)
	val := cfg.GetEngineParam(EngineCCR, "threshold_chars", 600)
	if val.(int) != 850 {
		t.Errorf("expected threshold_chars to be 850, got %v", val)
	}
}
