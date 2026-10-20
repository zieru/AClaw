package updater

import (
	"testing"
)

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"1.2.0", "v1.2.1", true},
		{"v1.2.0", "v1.2.0", false},
		{"1.2.0", "1.2.0", false},
		{"1.2.0", "v1.3.0", true},
		{"v1.3.0", "v1.2.0", true}, // differs
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
	}

	for _, tt := range tests {
		got := isNewerVersion(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("isNewerVersion(%q, %q) = %v; want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestFindMatchingAsset(t *testing.T) {
	assets := []Asset{
		{Name: "goassistant-linux-amd64", Size: 12345, BrowserDownloadURL: "http://example.com/linux-amd64"},
		{Name: "goassistant-linux-arm64", Size: 12345, BrowserDownloadURL: "http://example.com/linux-arm64"},
		{Name: "goassistant-windows-amd64.exe", Size: 12345, BrowserDownloadURL: "http://example.com/windows-amd64.exe"},
	}

	// Linux AMD64
	match := findMatchingAsset(assets, "linux", "amd64")
	if match == nil || match.Name != "goassistant-linux-amd64" {
		t.Errorf("expected linux-amd64 match, got %+v", match)
	}

	// Non-matching list
	assetsNonLinux := []Asset{
		{Name: "goassistant-windows-amd64.exe", Size: 12345, BrowserDownloadURL: "http://example.com/windows-amd64.exe"},
	}
	match = findMatchingAsset(assetsNonLinux, "linux", "amd64")
	if match != nil {
		t.Errorf("expected nil for windows-only list, got %+v", match)
	}
}
