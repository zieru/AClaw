package provider

import (
	"testing"
)

func TestModelCatalogLookup(t *testing.T) {
	cat := GetCatalog()

	// 1. DeepSeek models should be STRICTLY text-only (No vision)
	dsTests := []string{
		"deepseek-chat",
		"deepseek-v3",
		"deepseek-v4-flash",
		"deepseek-v4-flash-0731",
		"wz/deepseek-ai/DeepSeek-V4-Flash",
		"dahl/DeepSeek-V4-Flash-0731",
	}

	for _, m := range dsTests {
		cap := cat.Resolve(m)
		if cap.SupportsVision {
			t.Errorf("Expected model %s to NOT support vision, but got SupportsVision=true", m)
		}
	}

	// 2. Multimodal models should support vision
	visTests := []string{
		"gpt-4o",
		"openai/gpt-4o",
		"gemini-2.0-flash",
		"claude-3-5-sonnet",
		"qwen-vl-max",
		"qwen2.5-vl-72b",
		"llama-3.2-11b-vision",
		"pixtral-12b",
	}

	for _, m := range visTests {
		cap := cat.Resolve(m)
		if !cap.SupportsVision {
			t.Errorf("Expected model %s to support vision, but got SupportsVision=false", m)
		}
	}

	// 3. Audio / TTS models
	audTests := []string{
		"gpt-4o-audio-preview",
		"gemini-2.0-flash",
	}

	for _, m := range audTests {
		cap := cat.Resolve(m)
		if !cap.SupportsAudioOut {
			t.Errorf("Expected model %s to support audio out, but got SupportsAudioOut=false", m)
		}
	}
}

func TestModelCatalogFilters(t *testing.T) {
	cat := GetCatalog()

	candidates := []string{
		"deepseek-v4-flash",
		"gpt-4o",
		"qwen2.5-coder-32b",
		"gemini-2.0-flash",
		"llama-3.1-8b",
		"qwen-vl-max",
	}

	// Filter for vision
	visOnly := cat.FilterVisionModels(candidates)
	expectedVis := map[string]bool{
		"gpt-4o":           true,
		"gemini-2.0-flash": true,
		"qwen-vl-max":      true,
	}

	if len(visOnly) != len(expectedVis) {
		t.Fatalf("Expected %d vision models, got %d: %v", len(expectedVis), len(visOnly), visOnly)
	}

	for _, m := range visOnly {
		if !expectedVis[m] {
			t.Errorf("Unexpected vision model in filtered list: %s", m)
		}
	}
}

func TestInferCapabilityHeuristics(t *testing.T) {
	// Unknown models from custom vendors
	cap1 := InferCapability("my-custom-provider/custom-vl-model-3b")
	if !cap1.SupportsVision {
		t.Errorf("Expected custom-vl-model-3b to support vision via -vl pattern")
	}

	cap2 := InferCapability("private-inference/qwen-coder-32b-instruct")
	if cap2.SupportsVision {
		t.Errorf("Expected qwen-coder to be text-only")
	}

	cap3 := InferCapability("tts-custom-voice-engine")
	if !cap3.SupportsAudioOut {
		t.Errorf("Expected tts-custom-voice-engine to support audio out")
	}
}
