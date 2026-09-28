package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/tgformat"
)

func TestFormatDuration(t *testing.T) {
	d1 := 45 * time.Second
	if s := formatDuration(d1); s != "45s" {
		t.Errorf("expected '45s', got '%s'", s)
	}

	d2 := 2*time.Minute + 15*time.Second
	if s := formatDuration(d2); s != "2m 15s" {
		t.Errorf("expected '2m 15s', got '%s'", s)
	}

	d3 := 3*time.Hour + 20*time.Minute + 10*time.Second
	if s := formatDuration(d3); s != "3j 20m 10s" {
		t.Errorf("expected '3j 20m 10s', got '%s'", s)
	}

	d4 := 26*time.Hour + 10*time.Minute + 5*time.Second
	if s := formatDuration(d4); s != "1h 2j 10m 5s" {
		t.Errorf("expected '1h 2j 10m 5s', got '%s'", s)
	}
}

func TestAdminFriendlyErrorHTMLFormatting(t *testing.T) {
	errTimeout := context.DeadlineExceeded
	friendlyRaw := agent.FormatUserFriendlyError(errTimeout)
	friendlyHTML := tgformat.MarkdownToTelegramHTML(friendlyRaw)

	if strings.Contains(friendlyHTML, "**") {
		t.Errorf("expected friendly error markdown to be converted to HTML, found raw asterisks: %s", friendlyHTML)
	}
	if !strings.Contains(friendlyHTML, "<b>Waktu Tunggu Habis (Timeout)</b>") {
		t.Errorf("expected <b>Waktu Tunggu Habis (Timeout)</b> in Telegram HTML, got: %s", friendlyHTML)
	}
}

func TestAdminBot_InteractiveOptionsHandling(t *testing.T) {
	a := &AdminBot{}
	text := "Berikut adalah opsi lanjutan:\n[OPSI: Cara kerja OmniRoute | Provider AI didukung | Lainnya]\n\n—\n⚡ 29.4s"
	cleanText, options := tgformat.ExtractInteractiveOptions(text)

	if strings.Contains(cleanText, "[OPSI:") {
		t.Errorf("expected [OPSI:] tag to be stripped, got: %s", cleanText)
	}
	if len(options) != 3 {
		t.Fatalf("expected 3 options, got %d", len(options))
	}
	// Simulate storing options as sendOrEditSplitMessage does
	for i, opt := range options {
		optKey := "test_" + string(rune('0'+i))
		a.pendingOptions.Store(optKey, opt)
	}

	val, ok := a.pendingOptions.Load("test_0")
	if !ok || val.(string) != "Cara kerja OmniRoute" {
		t.Errorf("expected option 'Cara kerja OmniRoute', got %v", val)
	}
}

