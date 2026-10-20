package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestDescribeToolCall(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "browser",
			args: map[string]interface{}{
				"action": "open",
				"url":    "https://a1.tsel.my.id/dashboard",
			},
			expected: "Membuka web https://a1.tsel.my.id/dashboard",
		},
		{
			name: "browser",
			args: map[string]interface{}{
				"action": "click",
				"index":  2,
			},
			expected: "Klik elemen web (index 2)",
		},
		{
			name: "http_request",
			args: map[string]interface{}{
				"method": "POST",
				"url":    "https://api.example.com/data",
			},
			expected: "Request POST ke https://api.example.com/data",
		},
		{
			name: "g3a_query_analytics",
			args: map[string]interface{}{
				"dataset": "visit",
			},
			expected: "Query analitik dataset visit",
		},
		{
			name: "bash_exec",
			args: map[string]interface{}{
				"command": "ls -la",
			},
			expected: "Eksekusi perintah: ls -la",
		},
		{
			name: "delegate_task",
			args: map[string]interface{}{
				"role":        "analyst",
				"instruction": "Analisa data visit",
			},
			expected: "Delegasi tugas ke sub-agen @analyst",
		},
		{
			name: "delegate_task",
			args: map[string]interface{}{
				"tasks": `[{"role":"analyst"}]`,
			},
			expected: "Delegasi tugas paralel ke sub-agen",
		},
	}

	for _, tt := range tests {
		got := DescribeToolCall(tt.name, tt.args)
		if got != tt.expected {
			t.Errorf("DescribeToolCall(%q, %q) = %q; want %q", tt.name, tt.args, got, tt.expected)
		}
	}
}

func TestProgressTracker_Render(t *testing.T) {
	var lastUpdate string
	tracker := NewProgressTracker(func(s string) {
		lastUpdate = s
	})

	tracker.SetPlan("1. Buka halaman web\n2. Cari visit performance")
	if !strings.Contains(lastUpdate, "Rencana Pengerjaan") {
		t.Fatalf("expected plan in update, got: %s", lastUpdate)
	}

	tracker.StartStep("Membuka web https://a1.tsel.my.id")
	if !strings.Contains(lastUpdate, "⏳ [1/1] <i>Membuka web https://a1.tsel.my.id...</i>") {
		t.Fatalf("expected running step in update, got: %s", lastUpdate)
	}

	tracker.CompleteStep("Membuka web https://a1.tsel.my.id", nil)
	if !strings.Contains(lastUpdate, "✅ [1/1] Membuka web https://a1.tsel.my.id") {
		t.Fatalf("expected completed step in update, got: %s", lastUpdate)
	}

	tracker.StartStep("Mengekstrak data performa")
	tracker.CompleteStep("Mengekstrak data performa", errors.New("timeout"))
	if !strings.Contains(lastUpdate, "⚠️ [2/2] Mengekstrak data performa <i>(gagal: timeout)</i>") {
		t.Fatalf("expected failed step in update, got: %s", lastUpdate)
	}

	tracker.SetCurrent("Sedang menyusun laporan akhir")
	if !strings.Contains(lastUpdate, "⏳ [3/3] <i>Sedang menyusun laporan akhir...</i>") {
		t.Fatalf("expected final status in update, got: %s", lastUpdate)
	}
}
