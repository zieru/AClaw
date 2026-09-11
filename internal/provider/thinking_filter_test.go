package provider

import (
	"strings"
	"testing"
)

func TestExtractThinkingTags(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContent  string
		wantThinking string
	}{
		{
			name:         "clean text without think",
			input:        "Halo, apa kabar?",
			wantContent:  "Halo, apa kabar?",
			wantThinking: "",
		},
		{
			name: "closed think tag",
			input: `<think>
Saya harus menganalisis data banjir.
Kondisinya sudah surut.
</think>
Iya Sir, baru kemarin banjir besar. Air sudah surut.`,
			wantContent:  "Iya Sir, baru kemarin banjir besar. Air sudah surut.",
			wantThinking: "Saya harus menganalisis data banjir.\nKondisinya sudah surut.",
		},
		{
			name: "thought tag",
			input: `<thought>
Step 1: Check sources
</thought>
Ini adalah jawaban akhir.`,
			wantContent:  "Ini adalah jawaban akhir.",
			wantThinking: "Step 1: Check sources",
		},
		{
			name: "multiple think tags",
			input: `<think>First step</think>
Part 1
<think>Second step</think>
Part 2`,
			wantContent:  "Part 1\n\nPart 2",
			wantThinking: "First step\n\nSecond step",
		},
		{
			name: "unclosed think tag",
			input: `<think>
Sedang memikirkan sesuatu yang panjang...`,
			wantContent:  "",
			wantThinking: "Sedang memikirkan sesuatu yang panjang...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, thinking := ExtractThinkingTags(tt.input)
			if strings.TrimSpace(content) != strings.TrimSpace(tt.wantContent) {
				t.Errorf("content mismatch: got %q, want %q", content, tt.wantContent)
			}
			if strings.TrimSpace(thinking) != strings.TrimSpace(tt.wantThinking) {
				t.Errorf("thinking mismatch: got %q, want %q", thinking, tt.wantThinking)
			}
		})
	}
}

func TestStreamingThinkingFilter(t *testing.T) {
	var receivedThinking strings.Builder
	var receivedContent strings.Builder

	filter := NewStreamingThinkingFilter(func(chunk StreamChunk) {
		if chunk.Thinking != "" {
			receivedThinking.WriteString(chunk.Thinking)
		}
		if chunk.Content != "" {
			receivedContent.WriteString(chunk.Content)
		}
	})

	// Feed chunks where tags are split across chunk boundaries
	chunks := []string{
		"<th", "ink>",
		"Menganalisis ", "kondisi banjir...",
		"\nBanjir telah ", "surut.",
		"</th", "ink>",
		"Iya Sir, ", "kondisi banjir ", "saat ini sudah aman.",
	}

	for _, c := range chunks {
		filter.Feed(c, "")
	}
	filter.Flush()

	gotThinking := filter.Thinking()
	gotContent := filter.Content()

	wantThinking := "Menganalisis kondisi banjir...\nBanjir telah surut."
	wantContent := "Iya Sir, kondisi banjir saat ini sudah aman."

	if gotThinking != wantThinking {
		t.Errorf("filter.Thinking mismatch:\ngot  %q\nwant %q", gotThinking, wantThinking)
	}
	if gotContent != wantContent {
		t.Errorf("filter.Content mismatch:\ngot  %q\nwant %q", gotContent, wantContent)
	}

	if receivedThinking.String() != wantThinking {
		t.Errorf("streamed thinking mismatch:\ngot  %q\nwant %q", receivedThinking.String(), wantThinking)
	}
	if receivedContent.String() != wantContent {
		t.Errorf("streamed content mismatch:\ngot  %q\nwant %q", receivedContent.String(), wantContent)
	}
}
