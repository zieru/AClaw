package goassisthttp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

// AuthState holds session info for pending OAuth/Login requests
type AuthState struct {
	State      string
	UserID     int64
	ProviderID string
	CreatedAt  time.Time
}

// OAuthManager manages in-memory OAuth/Login states
type OAuthManager struct {
	mu           sync.RWMutex
	states       map[string]AuthState
	db           *storage.DB
	provMgr      *provider.Manager
	onSuccess    func(userID int64, provID string, authData string) error
	externalHost string
	port         int
}

var (
	globalOAuthMgr *OAuthManager
	oauthOnce      sync.Once
)

// InitOAuthManager initializes the singleton OAuth manager
func InitOAuthManager(db *storage.DB, pm *provider.Manager, port int, externalHost string, onSuccess func(userID int64, provID string, authData string) error) *OAuthManager {
	oauthOnce.Do(func() {
		globalOAuthMgr = &OAuthManager{
			states:       make(map[string]AuthState),
			db:           db,
			provMgr:      pm,
			onSuccess:    onSuccess,
			externalHost: externalHost,
			port:         port,
		}
		// Periodically cleanup expired states (>15m)
		go globalOAuthMgr.cleanupLoop()
	})
	return globalOAuthMgr
}

// GetOAuthManager returns the global OAuthManager
func GetOAuthManager() *OAuthManager {
	return globalOAuthMgr
}

func (m *OAuthManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for k, v := range m.states {
			if now.Sub(v.CreatedAt) > 15*time.Minute {
				delete(m.states, k)
			}
		}
		m.mu.Unlock()
	}
}

// CreateState creates a new state token associated with a Telegram UserID
func (m *OAuthManager) CreateState(userID int64, provID string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	m.mu.Lock()
	m.states[state] = AuthState{
		State:      state,
		UserID:     userID,
		ProviderID: provID,
		CreatedAt:  time.Now(),
	}
	m.mu.Unlock()

	return state
}

// GetLoginURL builds the local/external URL for Google login
func (m *OAuthManager) GetLoginURL(state string) string {
	host := m.externalHost
	if host == "" {
		host = "localhost"
	}
	if m.port != 80 && m.port != 443 && !strings.Contains(host, ":") {
		return fmt.Sprintf("http://%s:%d/auth/gemini/login?state=%s", host, m.port, state)
	}
	return fmt.Sprintf("http://%s/auth/gemini/login?state=%s", host, state)
}

// ValidateAndPopState validates a state and removes it
func (m *OAuthManager) ValidateAndPopState(state string) (AuthState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	auth, exists := m.states[state]
	if !exists {
		return AuthState{}, false
	}
	if time.Since(auth.CreatedAt) > 15*time.Minute {
		delete(m.states, state)
		return AuthState{}, false
	}
	delete(m.states, state)
	return auth, true
}

// RegisterOAuthRoutes registers authentication handlers into http.ServeMux
func (m *OAuthManager) RegisterOAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/auth/gemini/login", m.handleGeminiLogin)
	mux.HandleFunc("/auth/gemini/callback", m.handleGeminiCallback)
}

func (m *OAuthManager) handleGeminiLogin(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, "Parameter 'state' diperlukan.", http.StatusBadRequest)
		return
	}

	m.mu.RLock()
	_, valid := m.states[state]
	m.mu.RUnlock()

	if !valid {
		renderHTMLPage(w, http.StatusUnauthorized, "Sesi Kadaluarsa", `
			<div class="error-badge">⚠️ Sesi Tidak Valid</div>
			<h2>Sesi Login Telah Berakhir</h2>
			<p>Tautan login ini sudah kadaluarsa atau tidak valid. Silakan kembali ke Telegram Bot dan klik tombol login ulang.</p>
		`)
		return
	}

	htmlContent := fmt.Sprintf(`
		<div class="card">
			<div class="icon">🌐</div>
			<h2>Hubungkan Google Gemini Web</h2>
			<p>Pilih metode untuk menghubungkan akun Google Anda ke <strong>GoAssistant</strong>:</p>

			<div class="action-box">
				<h3>Opsi 1: Buka Gemini di Browser</h3>
				<p>Buka tautan di bawah pada tab baru, pastikan Anda telah login akun Google di <a href="https://gemini.google.com/app" target="_blank">gemini.google.com</a>.</p>
				<a href="https://gemini.google.com/app" target="_blank" class="btn btn-secondary">🔗 Buka gemini.google.com</a>
			</div>

			<div class="action-box" style="margin-top: 20px;">
				<h3>Opsi 2: Verifikasi & Kirim Sesi Otomatis</h3>
				<form method="POST" action="/auth/gemini/callback">
					<input type="hidden" name="state" value="%s" />
					<label for="auth_input" style="font-weight:600; display:block; margin-bottom:8px;">
						Tempel URL Redirect atau Cookie <code>__Secure-1PSID</code>:
					</label>
					<textarea id="auth_input" name="auth_input" rows="3" placeholder="Contoh: https://gemini.google.com/app atau __Secure-1PSID=g.a000..." required></textarea>
					<button type="submit" class="btn btn-primary" style="margin-top:12px; width:100%%;">🚀 Hubungkan Akun Sekarang</button>
				</form>
			</div>
		</div>
	`, html.EscapeString(state))

	renderHTMLPage(w, http.StatusOK, "Login Gemini Web - GoAssistant", htmlContent)
}

func (m *OAuthManager) handleGeminiCallback(w http.ResponseWriter, r *http.Request) {
	var state string
	var authInput string

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		state = r.FormValue("state")
		authInput = r.FormValue("auth_input")
	} else {
		state = r.URL.Query().Get("state")
		authInput = r.URL.Query().Get("code")
		if authInput == "" {
			authInput = r.URL.Query().Get("token")
		}
		if authInput == "" {
			authInput = r.URL.String()
		}
	}

	if state == "" {
		renderHTMLPage(w, http.StatusBadRequest, "Error", `
			<div class="error-badge">❌ Error</div>
			<h2>State Token Kosong</h2>
			<p>Parameter state tidak ditemukan pada request autentikasi.</p>
		`)
		return
	}

	authState, ok := m.ValidateAndPopState(state)
	if !ok {
		renderHTMLPage(w, http.StatusUnauthorized, "Sesi Kadaluarsa", `
			<div class="error-badge">⚠️ Sesi Tidak Valid</div>
			<h2>Sesi Telah Berakhir</h2>
			<p>Sesi login telah selesai atau kadaluarsa. Silakan lakukan setup ulang dari Telegram Bot jika belum terhubung.</p>
		`)
		return
	}

	// Parse Google cookies / credentials
	parsedCookies, err := provider.ParseGoogleAuthInput(authInput)
	if err != nil {
		renderHTMLPage(w, http.StatusBadRequest, "Autentikasi Gagal", fmt.Sprintf(`
			<div class="error-badge">⚠️ Format Tidak Dikenali</div>
			<h2>Gagal Mengekstrak Kredensial</h2>
			<p>Pesan error: <code>%s</code></p>
			<p>Pastikan Anda memasukkan cookie <code>__Secure-1PSID</code> atau URL yang benar.</p>
			<a href="/auth/gemini/login?state=%s" class="btn btn-secondary">🔄 Coba Lagi</a>
		`, html.EscapeString(err.Error()), html.EscapeString(state)))
		return
	}

	var cookieParts []string
	for k, v := range parsedCookies {
		cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", k, v))
	}
	rawCookieStr := strings.Join(cookieParts, "; ")

	provID := authState.ProviderID
	if provID == "" {
		provID = "gemini_web"
	}

	// Save or update provider in DB
	if m.db != nil {
		existing, _ := m.db.GetProvider(provID)
		if existing == nil {
			existing = &storage.ProviderRecord{
				ID:           provID,
				Name:         "Gemini Web (Google Auth)",
				Type:         "gemini_web",
				BaseURL:      "https://gemini.google.com",
				APIKey:       rawCookieStr,
				APIKeys:      []string{rawCookieStr},
				DefaultModel: "gemini-web-pro",
				Models:       []string{"gemini-web-pro", "gemini-web-flash", "gemini-web-ultra"},
				Strategy:     "failsafe",
				KeyStrategy:  "round-robin",
				IsActive:     true,
				Priority:     1,
			}
		} else {
			existing.APIKey = rawCookieStr
			existing.APIKeys = []string{rawCookieStr}
			existing.IsActive = true
		}
		_ = m.db.SaveProvider(existing)

		// Sync with ProviderManager
		if m.provMgr != nil {
			inst := provider.NewGeminiWebProvider(existing.Name, rawCookieStr, existing.DefaultModel, existing.Models)
			m.provMgr.Register(inst, existing.Priority)
		}
	}

	// Trigger callback notification (e.g. notify Telegram Bot)
	if m.onSuccess != nil {
		go func() {
			if err := m.onSuccess(authState.UserID, provID, rawCookieStr); err != nil {
				log.Printf("⚠️ OAuth onSuccess error: %v", err)
			}
		}()
	}

	renderHTMLPage(w, http.StatusOK, "Login Berhasil - GoAssistant", `
		<div class="card success">
			<div class="icon-success">🎉</div>
			<h2>Autentikasi Berhasil!</h2>
			<p>Akun Google Anda berhasil terhubung ke <strong>GoAssistant (Gemini Web)</strong>.</p>
			<div class="info-box">
				<p>✅ Sesi aktif telah tersimpan di sistem.</p>
				<p>📱 Anda dapat menutup tab ini dan kembali ke <strong>Telegram Bot</strong>.</p>
			</div>
		</div>
	`)
}

func renderHTMLPage(w http.ResponseWriter, statusCode int, title, bodyHTML string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="id">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>%s</title>
	<style>
		:root {
			--bg-gradient: linear-gradient(135deg, #0f172a 0%%, #1e293b 100%%);
			--card-bg: rgba(30, 41, 59, 0.85);
			--border: rgba(255, 255, 255, 0.1);
			--primary: #38bdf8;
			--primary-hover: #0284c7;
			--success: #22c55e;
			--warning: #f59e0b;
			--text-main: #f8fafc;
			--text-muted: #94a3b8;
		}
		* { box-sizing: border-box; margin: 0; padding: 0; }
		body {
			font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
			background: var(--bg-gradient);
			color: var(--text-main);
			min-height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
			padding: 20px;
		}
		.container {
			width: 100%%;
			max-width: 520px;
			background: var(--card-bg);
			border: 1px solid var(--border);
			backdrop-filter: blur(12px);
			border-radius: 16px;
			padding: 32px 28px;
			box-shadow: 0 20px 40px rgba(0, 0, 0, 0.4);
			text-align: center;
		}
		h2 { font-size: 1.5rem; margin-bottom: 12px; font-weight: 700; color: #fff; }
		p { color: var(--text-muted); font-size: 0.95rem; line-height: 1.5; margin-bottom: 16px; }
		.icon { font-size: 3rem; margin-bottom: 12px; }
		.icon-success { font-size: 3.5rem; margin-bottom: 12px; }
		.action-box {
			background: rgba(15, 23, 42, 0.6);
			border: 1px solid rgba(255, 255, 255, 0.08);
			border-radius: 12px;
			padding: 18px;
			margin-top: 16px;
			text-align: left;
		}
		.action-box h3 { font-size: 1rem; margin-bottom: 6px; color: var(--primary); }
		textarea {
			width: 100%%;
			padding: 12px;
			background: rgba(0,0,0,0.3);
			border: 1px solid var(--border);
			border-radius: 8px;
			color: #fff;
			font-family: monospace;
			font-size: 0.85rem;
			resize: vertical;
		}
		textarea:focus { outline: none; border-color: var(--primary); }
		.btn {
			display: inline-block;
			text-align: center;
			text-decoration: none;
			font-weight: 600;
			padding: 12px 20px;
			border-radius: 8px;
			border: none;
			cursor: pointer;
			transition: all 0.2s ease;
		}
		.btn-primary { background: var(--primary); color: #0f172a; }
		.btn-primary:hover { background: var(--primary-hover); }
		.btn-secondary { background: rgba(255, 255, 255, 0.1); color: #fff; width: 100%%; }
		.btn-secondary:hover { background: rgba(255, 255, 255, 0.18); }
		.error-badge { color: #ef4444; font-weight: bold; margin-bottom: 8px; }
		.info-box {
			background: rgba(34, 197, 94, 0.1);
			border: 1px solid rgba(34, 197, 94, 0.3);
			border-radius: 10px;
			padding: 14px;
			margin-top: 20px;
			text-align: left;
		}
		.info-box p { margin-bottom: 6px; color: #bbf7d0; font-size: 0.9rem; }
		.info-box p:last-child { margin-bottom: 0; }
		code { background: rgba(0,0,0,0.4); padding: 2px 6px; border-radius: 4px; color: #38bdf8; font-size: 0.85em; }
		a { color: var(--primary); text-decoration: none; }
		a:hover { text-decoration: underline; }
	</style>
</head>
<body>
	<div class="container">
		%s
	</div>
</body>
</html>`, html.EscapeString(title), bodyHTML)

	_, _ = fmt.Fprint(w, page)
}
