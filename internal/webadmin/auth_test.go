package webadmin

import (
	"testing"

	"goassistant/internal/config"
)

func TestAuthManager_OTPWorkflow(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.AdminTelegram.AllowedUserIDs = []int64{123456789}
	cfg.WebAdmin.OTPTTLMinutes = 5
	cfg.WebAdmin.SessionTTLMinutes = 60

	var lastSentID int64
	var lastSentMsg string
	sender := func(userID int64, text string) error {
		lastSentID = userID
		lastSentMsg = text
		return nil
	}

	auth := NewAuthManager(cfg, sender)

	// 1. Unauthorized user should be rejected
	err := auth.RequestOTP(999999999)
	if err != ErrNotAdmin {
		t.Fatalf("expected ErrNotAdmin, got %v", err)
	}

	// 2. Authorized user requests OTP
	err = auth.RequestOTP(123456789)
	if err != nil {
		t.Fatalf("unexpected error requesting OTP: %v", err)
	}
	if lastSentID != 123456789 || lastSentMsg == "" {
		t.Fatalf("expected sent ID 123456789 and non-empty message, got %d, '%s'", lastSentID, lastSentMsg)
	}

	// Extract code from stored record
	auth.mu.RLock()
	rec, ok := auth.otps[123456789]
	code := ""
	if ok {
		code = rec.code
	}
	auth.mu.RUnlock()

	if code == "" || len(code) != 6 {
		t.Fatalf("expected 6-digit OTP code, got '%s'", code)
	}

	// 3. Wrong OTP attempt
	_, err = auth.VerifyOTP(123456789, "000000")
	if err != ErrInvalidOTP {
		t.Fatalf("expected ErrInvalidOTP, got %v", err)
	}

	// 4. Correct OTP attempt
	sess, err := auth.VerifyOTP(123456789, code)
	if err != nil {
		t.Fatalf("unexpected error verifying OTP: %v", err)
	}
	if sess == nil || sess.Token == "" {
		t.Fatalf("expected valid session, got nil")
	}

	// 5. Session validation
	validatedSess, ok := auth.ValidateSession(sess.Token)
	if !ok || validatedSess.TelegramID != 123456789 {
		t.Fatalf("failed to validate session: ok=%v", ok)
	}

	// 6. Revoke session
	auth.RevokeSession(sess.Token)
	_, ok = auth.ValidateSession(sess.Token)
	if ok {
		t.Fatalf("expected session to be revoked")
	}
}

func TestAuthManager_MaxAttempts(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.AdminTelegram.AllowedUserIDs = []int64{123456789}
	cfg.WebAdmin.OTPTTLMinutes = 5

	auth := NewAuthManager(cfg, nil)
	_ = auth.RequestOTP(123456789)

	// Fail 3 times
	_, _ = auth.VerifyOTP(123456789, "111111")
	_, _ = auth.VerifyOTP(123456789, "222222")
	_, _ = auth.VerifyOTP(123456789, "333333")

	// 4th attempt should be blocked
	_, err := auth.VerifyOTP(123456789, "444444")
	if err != ErrTooManyAttempts {
		t.Fatalf("expected ErrTooManyAttempts, got %v", err)
	}
}
