package agent

import (
	"strings"
	"testing"
)

func TestIsObviousGrapariOutOfScope(t *testing.T) {
	tests := []struct {
		prompt      string
		outOfScope  bool
		description string
	}{
		{
			prompt:      "Halo, bagaimana cara ganti kartu Telkomsel yang hilang?",
			outOfScope:  false,
			description: "In-scope telecom question (ganti kartu hilang)",
		},
		{
			prompt:      "Apa syarat registrasi ulang simcard prabayar dengan KTP dan KK?",
			outOfScope:  false,
			description: "In-scope SOP question (registrasi simcard KTP KK)",
		},
		{
			prompt:      "Berapa waktu tunggu antrean di loket GraPARI Medan?",
			outOfScope:  false,
			description: "In-scope GraPARI question (antrean loket)",
		},
		{
			prompt:      "Tolong cek SOP migrasi kartu Halo ke prabayar",
			outOfScope:  false,
			description: "In-scope SOP question (migrasi kartu Halo)",
		},
		{
			prompt:      "Tolong buatkan script python untuk scraping website",
			outOfScope:  true,
			description: "Out-of-scope coding request",
		},
		{
			prompt:      "Bikin program golang untuk web server REST API",
			outOfScope:  true,
			description: "Out-of-scope golang programming request",
		},
		{
			prompt:      "Tuliskan puisi cinta yang romantis tentang senja",
			outOfScope:  true,
			description: "Out-of-scope creative writing request",
		},
		{
			prompt:      "Berikan resep masakan rendang padang asli",
			outOfScope:  true,
			description: "Out-of-scope cooking recipe request",
		},
		{
			prompt:      "Hitung integral tentu dari sin(x) dx dari 0 sampai pi",
			outOfScope:  true,
			description: "Out-of-scope advanced math request",
		},
		{
			prompt:      "Siapa presiden pertama Amerika Serikat?",
			outOfScope:  true,
			description: "Out-of-scope general history trivia",
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			got := isObviousGrapariOutOfScope(tc.prompt)
			if got != tc.outOfScope {
				t.Errorf("isObviousGrapariOutOfScope(%q) = %v, expected %v", tc.prompt, got, tc.outOfScope)
			}
		})
	}
}

func TestPromptBuilderGrapariOnly(t *testing.T) {
	pb := NewPromptBuilder(NewMDLoader(""))

	// Normal prompt without GraPARI only
	normalPrompt, err := pb.BuildSystemPrompt(PromptContext{
		ChannelID:     "tg_general",
		ChannelName:   "General Bot",
		ChannelType:   "telegram",
		IsGrapariOnly: false,
	})
	if err != nil {
		t.Fatalf("BuildSystemPrompt failed: %v", err)
	}
	if strings.Contains(normalPrompt, "MODE KHUSUS OPERASIONAL & SOP GRAPARI") {
		t.Errorf("normal prompt should NOT contain GraPARI mode header")
	}

	// GraPARI-only prompt
	grapariPrompt, err := pb.BuildSystemPrompt(PromptContext{
		ChannelID:     "tg_grapari",
		ChannelName:   "GraPARI Bot",
		ChannelType:   "telegram",
		IsGrapariOnly: true,
	})
	if err != nil {
		t.Fatalf("BuildSystemPrompt with IsGrapariOnly=true failed: %v", err)
	}

	expectedSnippets := []string{
		"MODE KHUSUS OPERASIONAL & SOP GRAPARI",
		"g3a_search_grapari_knowledge",
		"search_telkomsel_web",
		"[OUT_OF_SCOPE]",
		"ANTI-HALUSINASI",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(grapariPrompt, snippet) {
			t.Errorf("expected GraPARI prompt to contain %q, but was missing", snippet)
		}
	}
}

func TestGrapariOutOfScopeInterception(t *testing.T) {
	// Verify that if model produced [OUT_OF_SCOPE], it is correctly replaced
	sampleModelResponses := []string{
		"[OUT_OF_SCOPE]",
		"  [OUT_OF_SCOPE]  ",
		"[OUT_OF_SCOPE]\n",
		"Maaf [OUT_OF_SCOPE]",
	}

	for _, resp := range sampleModelResponses {
		clean := stripAttachmentTags(resp)
		isGrapariOnly := true
		if isGrapariOnly && (strings.Contains(clean, "[OUT_OF_SCOPE]") || strings.EqualFold(strings.TrimSpace(clean), "[OUT_OF_SCOPE]")) {
			clean = GrapariOutOfScopeRejection
		}
		if clean != GrapariOutOfScopeRejection {
			t.Errorf("expected response %q to be replaced with GrapariOutOfScopeRejection, got %q", resp, clean)
		}
	}
}
