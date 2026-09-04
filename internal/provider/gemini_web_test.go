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

func TestSanitizeGarbledGeminiWebText(t *testing.T) {
	// Exact case reported by user: broken draft 1 ending at "| **Rentang Suhu Oper" fused with revised draft 2
	garbled := `| Parameter | LTO (Lithium Titanate) | LFP (LiFePO4) |
| :--- | :--- | :--- |
| **Tegangan Nominal** | ~2.3V – 2.4V per sel | ~3.2V per sel |
| **Siklus Hidup (Cycle Life)** | 10.000 – 25.000+ siklus | 2.500 – 5.000 siklus |
| **Kecepatan Cas/Discharge (C-Rate)** | Sangat tinggi (bisa 5C – 10C+, penuh < 15 menit) | Moderat (umumnya 0.5C – 1C, fast charge 2C) |
| **Kepadatan Energi (Energy Density)** | Rendah (~60–110 Wh/kg) | Sedang (~130–170 Wh/kg) |
| **Rentang Suhu Oper**Lithium Titanate (LTO)** unggul mutlak dalam kecepatan isi daya, daya tahan siklus, dan toleransi suhu ekstrem, sedangkan **Lithium Iron Phosphate (LiFePO4 / LFP)** jauh lebih ekonomis dan memiliki densitas energi yang lebih padat untuk penggunaan umum.

| Parameter | Lithium Titanate (LTO) | Lithium Iron Phosphate (LiFePO4) |
| :--- | :--- | :--- |
| **Tegangan Nominal Sel** | 2,3V – 2,4V | 3,2V |
| **Siklus Hidup (Cycle Life)** | 10.000 – 25.000+ siklus | 3.000 – 6.000 siklus |
| **Kecepatan Cas (C-Rate)** | Ekstrem (bisa 6C – 10C+, penuh < 10 menit) | Standar–Tinggi (umumnya 0,5C – 1C, cepat di 2C) |
| **Kepadatan Energi** | Rendah (~60–110 Wh/kg) | Sedang (~120–180 Wh/kg) |
| **Toleransi Suhu Dingin** | Sangat prima (bisa cas hingga -30°C) | Kurang baik (sulit/rusak jika dicas di bawah 0°C) |
| **Keamanan Termal** | Sangat aman (paling tahan ledakan/tusukan) | Sangat aman (stabil dibanding NMC/LCO) |
| **Biaya per Wh** | Mahal (2–3x lipat dibanding LFP) | Murah dan sangat ekonomis |

**Perbedaan Praktis:**
* **LiFePO4:** Pilihan terbaik untuk aplikasi hemat biaya dengan kapasitas besar.
* **LTO:** Pilihan khusus jika sistem butuh pengisian super cepat.`

	cleaned := sanitizeGarbledGeminiWebText(garbled)

	if strings.Contains(cleaned, "Rentang Suhu Oper") {
		t.Errorf("expected broken draft 1 to be removed, but found 'Rentang Suhu Oper'")
	}
	if !strings.HasPrefix(cleaned, "**Lithium Titanate (LTO)** unggul mutlak") {
		t.Errorf("expected cleaned text to start with the second draft, got: %s", cleaned[:50])
	}
	if !strings.Contains(cleaned, "2,3V – 2,4V") {
		t.Errorf("expected clean table in draft 2 to be preserved")
	}

	// Normal text with a single table should remain untouched
	normal := `Berikut perbandingan:

| Item | Nilai |
| :--- | :--- |
| A | 10 |
| B | 20 |

Selesai.`
	if sanitizeGarbledGeminiWebText(normal) != normal {
		t.Errorf("expected normal text with single table to remain unchanged")
	}
}

func TestIsGeminiWebRefusal(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"Saya hanya AI berbasis teks dan tidak bisa membantu Anda untuk itu.", true},
		{"Maaf, saya hanya model bahasa dan tidak dapat membantu Anda untuk itu.", true},
		{"I am a large language model, trained by Google.", true},
		{"Lithium titanate adalah bahan anoda baterai.", false},
		{"Tentu, ini penjelasan mengenai Lithium Titanate.", false},
	}

	for _, c := range cases {
		actual := isGeminiWebRefusal(c.input)
		if actual != c.expected {
			t.Errorf("for input %q, expected refusal=%v, got=%v", c.input, c.expected, actual)
		}
	}
}

