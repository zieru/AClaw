package sysenv

import (
	"strings"
	"testing"
)

func TestGetHostInfo(t *testing.T) {
	ResetCache()
	info := GetHostInfo()

	if info.OS == "" {
		t.Errorf("expected OS to be detected, got empty string")
	}

	prompt := info.FormatPrompt()
	if !strings.Contains(prompt, "Host System Environment") {
		t.Errorf("expected prompt to contain 'Host System Environment', got: %s", prompt)
	}
	if !strings.Contains(prompt, info.OS) {
		t.Errorf("expected prompt to contain OS %q, got: %s", info.OS, prompt)
	}
}

func TestFormatPromptPrivilegeVariations(t *testing.T) {
	// Test doas
	doasInfo := HostInfo{
		OS:             "Alpine Linux v3.24",
		Arch:           "amd64",
		PrivilegeTool:  "doas",
		PackageManager: "apk (misal: 'apk add <pkg>')",
		InitSystem:     "OpenRC (gunakan 'rc-service <nama_service> restart')",
	}
	promptDoas := doasInfo.FormatPrompt()
	if !strings.Contains(promptDoas, "WAJIB gunakan 'doas'") {
		t.Errorf("expected doas instruction, got: %s", promptDoas)
	}
	if !strings.Contains(promptDoas, "DILARANG menggunakan 'sudo'") {
		t.Errorf("expected instruction forbidding sudo on doas-only host, got: %s", promptDoas)
	}

	// Test root
	rootInfo := HostInfo{
		OS:     "Alpine Linux v3.24",
		IsRoot: true,
	}
	promptRoot := rootInfo.FormatPrompt()
	if !strings.Contains(promptRoot, "root (UID 0)") {
		t.Errorf("expected root instruction, got: %s", promptRoot)
	}
}
