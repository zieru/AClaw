package tokensaver

import (
	"strings"
	"testing"

	"goassistant/internal/provider"
)

func TestEvalHarnessSavings(t *testing.T) {
	// Sample corpus containing mixed prompts: diffs, logs, json array, and conversational prose
	sampleDiff := `diff --git a/pkg/service.go b/pkg/service.go
index a1b2c3d..e4f5g6h 100644
--- a/pkg/service.go
+++ b/pkg/service.go
@@ -10,12 +10,14 @@ func HandleRequest(req *Request) error {
 	if req == nil {
 		return errors.New("nil request")
 	}
+	log.Printf("processing request: %v", req.ID)
 	return nil
 }
`

	sampleJSONArray := `[
		{"id": "usr_001", "name": "Alexander", "status": "active", "tier": "premium"},
		{"id": "usr_002", "name": "Beatrix", "status": "pending", "tier": "standard"},
		{"id": "usr_003", "name": "Christian", "status": "active", "tier": "enterprise"},
		{"id": "usr_004", "name": "Diana", "status": "inactive", "tier": "free"}
	]`

	sampleProse := `As an AI language model, I would be happy to help you understand the architecture.
Please note that in order to ensure maximum reliability and performance, we recommend using a round-robin proxy pool.
Feel free to ask if you need anything else!`

	corpus := []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "Here is the error diff and user payload:   \n\n\n\nSee https://example.com/asset.png?utm_source=slack&utm_medium=chat"},
		{Role: provider.RoleTool, Content: sampleDiff},
		{Role: provider.RoleTool, Content: sampleJSONArray},
		{Role: provider.RoleAssistant, Content: sampleProse},
		{Role: provider.RoleUser, Content: "How to fix the service request handler error?"},
	}

	origTokens := CalculateTotalTokens(corpus)
	t.Logf("=== EVAL HARNESS BASELINE: %d tokens ===", origTokens)

	presetsToEval := []string{PresetLite, PresetStandard, PresetAggressive, PresetRTK, PresetStacked, PresetUltra}

	for _, preset := range presetsToEval {
		cfg := GetPresetConfig(preset)
		compressed, report := CompressMessagesPipeline(corpus, cfg)

		t.Logf("[%s] Final Tokens: %d | Saved: %d | Savings: %.1f%% | Active Engines: %s",
			strings.ToUpper(preset),
			report.FinalTokens,
			report.TokensSaved,
			report.SavingsPercent,
			strings.Join(report.ActiveEngines, ", "),
		)

		if report.SavingsPercent <= 0 && preset != PresetOff {
			t.Errorf("expected preset %s to achieve positive savings, got %.1f%%", preset, report.SavingsPercent)
		}

		// Ensure code blocks and key identifiers are not completely lost
		joined := ""
		for _, m := range compressed {
			joined += m.Content + "\n"
		}
		if !strings.Contains(joined, "HandleRequest") && !strings.Contains(joined, "service.go") {
			t.Errorf("preset %s lost critical code symbol HandleRequest", preset)
		}
	}
}

func BenchmarkPipeline(b *testing.B) {
	msgs := []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "Hello assistant, please help me optimize this query."},
		{Role: provider.RoleAssistant, Content: "Sure! As an AI language model, I would be glad to assist you in order to tune your SQL."},
	}
	cfg := GetPresetConfig(PresetStacked)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompressMessagesPipeline(msgs, cfg)
	}
}
