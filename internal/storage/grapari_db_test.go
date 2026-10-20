package storage

import (
	"testing"
)

func TestCleanChannelFileName(t *testing.T) {
	tests := []struct {
		id       string
		name     string
		expected string
	}{
		{"awesomeleap", "Awesome Leap", "grapari_awesomeleap_Awesome_Leap.db"},
		{"tg_grapari", "GraPARI CS 24/7!", "grapari_tg_grapari_GraPARI_CS_24_7.db"},
		{"test", "", "grapari_test.db"},
	}

	for _, tc := range tests {
		got := CleanChannelFileName(tc.id, tc.name)
		if got != tc.expected {
			t.Errorf("CleanChannelFileName(%q, %q) = %q; want %q", tc.id, tc.name, got, tc.expected)
		}
	}
}

func TestGrapariDBOperations(t *testing.T) {
	tempDir := t.TempDir()
	channelID := "test_ch_1"
	channelName := "Test GraPARI"

	gdb, err := OpenGrapariDB(tempDir, channelID, channelName)
	if err != nil {
		t.Fatalf("OpenGrapariDB failed: %v", err)
	}
	defer gdb.Close()

	// 1. Initial GetUser should return nil
	u, err := gdb.GetUser("628123456789@s.whatsapp.net")
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if u != nil {
		t.Fatalf("expected nil user, got %+v", u)
	}

	// 2. UpsertUser
	newUser := &GrapariUser{
		UserJID:     "628123456789@s.whatsapp.net",
		PhoneNumber: "628123456789",
		Name:        "Budi Santoso",
		NIKSIAD:     "12345678",
		Role:        "CS",
		Status:      GrapariStatusPending,
	}
	if err := gdb.UpsertUser(newUser); err != nil {
		t.Fatalf("UpsertUser failed: %v", err)
	}

	// Verify loaded
	loaded, err := gdb.GetUser("628123456789@s.whatsapp.net")
	if err != nil {
		t.Fatalf("GetUser after upsert failed: %v", err)
	}
	if loaded == nil || loaded.NIKSIAD != "12345678" || loaded.Status != GrapariStatusPending {
		t.Fatalf("loaded user mismatch: %+v", loaded)
	}

	// 3. SetUserStatus Authorized
	if err := gdb.SetUserStatus("628123456789@s.whatsapp.net", GrapariStatusAuthorized, "@supervisor"); err != nil {
		t.Fatalf("SetUserStatus failed: %v", err)
	}

	updated, _ := gdb.GetUser("628123456789@s.whatsapp.net")
	if updated.Status != GrapariStatusAuthorized || updated.ApprovedBy != "@supervisor" || updated.ApprovedAt == nil {
		t.Fatalf("updated status mismatch: %+v", updated)
	}

	// 4. Set & Get Approval Group
	group, err := gdb.GetApprovalGroup()
	if err != nil {
		t.Fatalf("GetApprovalGroup failed: %v", err)
	}
	if group != 0 {
		t.Fatalf("expected initial group 0, got %d", group)
	}

	if err := gdb.SetApprovalGroup(-1001987654321); err != nil {
		t.Fatalf("SetApprovalGroup failed: %v", err)
	}

	group, err = gdb.GetApprovalGroup()
	if err != nil {
		t.Fatalf("GetApprovalGroup after set failed: %v", err)
	}
	if group != -1001987654321 {
		t.Fatalf("expected group -1001987654321, got %d", group)
	}

	// 5. List Users
	list, err := gdb.ListUsers("")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListUsers failed: len=%d, err=%v", len(list), err)
	}
}

func TestMultiDevicePersistence(t *testing.T) {
	tempDir := t.TempDir()
	channelID := "test_ch_md"
	channelName := "Test MD"

	gdb, err := OpenGrapariDB(tempDir, channelID, channelName)
	if err != nil {
		t.Fatalf("OpenGrapariDB failed: %v", err)
	}
	defer gdb.Close()

	// 1. User registers initially from device :72
	u1 := &GrapariUser{
		UserJID:     "6282298380628:72@s.whatsapp.net",
		PhoneNumber: "6282298380628",
		Name:        "A nazirul",
		NIKSIAD:     "22243067",
		Role:        "Admin Area",
		Status:      GrapariStatusPending,
	}
	if err := gdb.UpsertUser(u1); err != nil {
		t.Fatalf("UpsertUser u1 failed: %v", err)
	}

	// 2. Authorize user
	if err := gdb.SetUserStatus("6282298380628@s.whatsapp.net", GrapariStatusAuthorized, "@supervisor"); err != nil {
		t.Fatalf("SetUserStatus failed: %v", err)
	}

	// 3. User later connects from new device :73
	found73, err := gdb.GetUser("6282298380628:73@s.whatsapp.net")
	if err != nil {
		t.Fatalf("GetUser :73 failed: %v", err)
	}
	if found73 == nil || found73.Status != GrapariStatusAuthorized {
		t.Fatalf("expected user from :73 to be found and AUTHORIZED, got: %+v", found73)
	}

	// 4. Lookup by phone number
	foundPhone, err := gdb.GetUser("6282298380628")
	if err != nil || foundPhone == nil || foundPhone.Status != GrapariStatusAuthorized {
		t.Fatalf("expected lookup by phone to succeed, got: %+v, err=%v", foundPhone, err)
	}

	// 5. Lookup by Indonesian 08 local phone
	found08, err := gdb.GetUser("082298380628")
	if err != nil || found08 == nil || found08.Status != GrapariStatusAuthorized {
		t.Fatalf("expected lookup by 08 phone to succeed, got: %+v, err=%v", found08, err)
	}

	// 6. User registers again or updates - should update existing, not duplicate
	u2 := &GrapariUser{
		UserJID:     "6282298380628@s.whatsapp.net",
		PhoneNumber: "6282298380628",
		Name:        "A nazirul Updated",
		NIKSIAD:     "22243067",
		Role:        "Admin Area",
		Status:      GrapariStatusAuthorized,
	}
	if err := gdb.UpsertUser(u2); err != nil {
		t.Fatalf("UpsertUser u2 failed: %v", err)
	}

	all, _ := gdb.ListUsers("")
	if len(all) != 1 {
		t.Fatalf("expected 1 user without duplication, got %d", len(all))
	}
	if all[0].Name != "A nazirul Updated" {
		t.Fatalf("expected updated name, got %s", all[0].Name)
	}
}
