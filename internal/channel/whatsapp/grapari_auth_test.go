package whatsapp

import (
	"testing"

	"goassistant/internal/storage"
)

func TestParseRole(t *testing.T) {
	cases := []struct {
		input    string
		expected string
		valid    bool
	}{
		{"1", "TL", true},
		{"2", "FOS", true},
		{"3", "CS", true},
		{"4", "Admin CC", true},
		{"5", "Admin Region", true},
		{"6", "Admin Area", true},
		{"7", "Lainnya", true},
		{"cs", "CS", true},
		{"admin cc", "Admin CC", true},
		{"TL", "TL", true},
		{"invalid", "", false},
		{"8", "", false},
	}

	for _, c := range cases {
		got, ok := ParseRole(c.input)
		if ok != c.valid || got != c.expected {
			t.Errorf("ParseRole(%q) = (%q, %v); want (%q, %v)", c.input, got, ok, c.expected, c.valid)
		}
	}
}

func TestGrapariAuthWizardFlow(t *testing.T) {
	wiz := GetGrapariAuthWizard()
	userJID := "628999999999@s.whatsapp.net"
	phone := "628999999999"
	name := "Dian"

	// 1. Initial message (any text)
	handled, reply, completed := wiz.HandleIncomingMessage(userJID, phone, name, "Halo", nil)
	if !handled || completed != nil {
		t.Fatalf("expected handled true, completed nil, got handled=%v, completed=%v", handled, completed)
	}
	if reply == "" {
		t.Fatalf("expected welcome prompt, got empty string")
	}

	// 2. Input NIK
	handled, reply, completed = wiz.HandleIncomingMessage(userJID, phone, name, "98765432", nil)
	if !handled || completed != nil {
		t.Fatalf("expected handled true, completed nil after NIK")
	}
	if reply == "" {
		t.Fatalf("expected role prompt, got empty string")
	}

	// 3. Input Invalid Role
	handled, reply, completed = wiz.HandleIncomingMessage(userJID, phone, name, "10", nil)
	if !handled || completed != nil {
		t.Fatalf("expected handled true with invalid role warning")
	}

	// 4. Input Valid Role
	handled, reply, completed = wiz.HandleIncomingMessage(userJID, phone, name, "3", nil)
	if !handled || completed == nil {
		t.Fatalf("expected completed user not nil")
	}
	if completed.NIKSIAD != "98765432" || completed.Role != "CS" || completed.Status != storage.GrapariStatusPending {
		t.Fatalf("completed user mismatch: %+v", completed)
	}

	// 5. Subsequent message when status is pending
	handled, reply, completed = wiz.HandleIncomingMessage(userJID, phone, name, "Ada info?", completed)
	if !handled || completed != nil {
		t.Fatalf("expected handled true with pending notice")
	}
}
