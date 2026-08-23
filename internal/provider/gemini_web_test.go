package provider

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseGoogleAuthInput(t *testing.T) {
	// Test Case 1: Standard Cookie string
	cookieStr := "__Secure-1PSID=g.a000abc123; __Secure-1PSIDTS=sidts_val; SID=sid_val"
	cookies, err := ParseGoogleAuthInput(cookieStr)
	if err != nil {
		t.Fatalf("unexpected error parsing cookie string: %v", err)
	}
	if cookies["__Secure-1PSID"] != "g.a000abc123" {
		t.Errorf("expected __Secure-1PSID=g.a000abc123, got %s", cookies["__Secure-1PSID"])
	}
	if cookies["__Secure-1PSIDTS"] != "sidts_val" {
		t.Errorf("expected __Secure-1PSIDTS=sidts_val, got %s", cookies["__Secure-1PSIDTS"])
	}

	// Test Case 2: URL with query parameters
	urlStr := "https://gemini.google.com/app?code=auth_code_xyz&__Secure-1PSID=cookie_from_url"
	cookies2, err := ParseGoogleAuthInput(urlStr)
	if err != nil {
		t.Fatalf("unexpected error parsing URL: %v", err)
	}
	if cookies2["code"] != "auth_code_xyz" {
		t.Errorf("expected code=auth_code_xyz, got %s", cookies2["code"])
	}
	if cookies2["__Secure-1PSID"] != "cookie_from_url" {
		t.Errorf("expected __Secure-1PSID=cookie_from_url, got %s", cookies2["__Secure-1PSID"])
	}

	// Test Case 3: JSON cookie list (e.g. from Cookie-Editor extension)
	jsonCookies := `[{"name":"__Secure-1PSID","value":"json_psid_val"},{"name":"__Secure-1PSIDTS","value":"json_psidts_val"}]`
	cookies3, err := ParseGoogleAuthInput(jsonCookies)
	if err != nil {
		t.Fatalf("unexpected error parsing JSON cookies: %v", err)
	}
	if cookies3["__Secure-1PSID"] != "json_psid_val" {
		t.Errorf("expected __Secure-1PSID=json_psid_val, got %s", cookies3["__Secure-1PSID"])
	}

	// Test Case 4: Single raw PSID token
	singleToken := "g.a000testpsid12345678901234567890"
	cookies4, err := ParseGoogleAuthInput(singleToken)
	if err != nil {
		t.Fatalf("unexpected error parsing single token: %v", err)
	}
	if cookies4["__Secure-1PSID"] != singleToken {
		t.Errorf("expected __Secure-1PSID=%s, got %s", singleToken, cookies4["__Secure-1PSID"])
	}
}

func TestGeminiWebProviderInterface(t *testing.T) {
	p := NewGeminiWebProvider("gemini_web", "__Secure-1PSID=test_psid", "gemini-web-pro", nil)
	if p.Name() != "gemini_web" {
		t.Errorf("expected name gemini_web, got %s", p.Name())
	}
	if p.Type() != "gemini_web" {
		t.Errorf("expected type gemini_web, got %s", p.Type())
	}
	if !p.HasValidCookies() {
		t.Errorf("expected HasValidCookies to be true")
	}
	if !IsFreeProvider(p.Type()) {
		t.Errorf("expected IsFreeProvider(gemini_web) to be true")
	}
}

func TestGeminiWebCookieAutoUpdate(t *testing.T) {
	p := NewGeminiWebProvider("gemini_web", "__Secure-1PSID=initial_psid", "gemini-web-pro", nil)

	var callbackInvoked bool
	var capturedName, capturedCookies string
	var capturedMap map[string]string

	p.SetOnCookieUpdate(func(provName, newCookies string, cookieMap map[string]string) {
		callbackInvoked = true
		capturedName = provName
		capturedCookies = newCookies
		capturedMap = cookieMap
	})

	// Simulate HTTP response with Set-Cookie headers
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Add("Set-Cookie", "__Secure-1PSIDTS=rotated_token_123; Path=/; Domain=.google.com; Secure; HttpOnly")
	resp.Header.Add("Set-Cookie", "__Secure-1PSIDCC=cc_token_456; Path=/; Domain=.google.com")

	p.updateCookiesFromResponse(resp)

	// Allow goroutine callback to execute
	time.Sleep(50 * time.Millisecond)

	if !callbackInvoked {
		t.Fatalf("expected SetOnCookieUpdate callback to be invoked")
	}
	if capturedName != "gemini_web" {
		t.Errorf("expected provName gemini_web, got %s", capturedName)
	}
	if capturedMap["__Secure-1PSIDTS"] != "rotated_token_123" {
		t.Errorf("expected __Secure-1PSIDTS=rotated_token_123, got %s", capturedMap["__Secure-1PSIDTS"])
	}
	if capturedMap["__Secure-1PSIDCC"] != "cc_token_456" {
		t.Errorf("expected __Secure-1PSIDCC=cc_token_456, got %s", capturedMap["__Secure-1PSIDCC"])
	}
	if capturedMap["__Secure-1PSID"] != "initial_psid" {
		t.Errorf("expected __Secure-1PSID=initial_psid to be preserved, got %s", capturedMap["__Secure-1PSID"])
	}
	if !strings.Contains(capturedCookies, "__Secure-1PSIDTS=rotated_token_123") {
		t.Errorf("expected cookies string to contain rotated token: %s", capturedCookies)
	}
}

