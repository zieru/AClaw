package webadmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
)

var (
	ErrNotAdmin        = errors.New("ID Telegram tidak terdaftar sebagai Administrator")
	ErrInvalidOTP      = errors.New("Kode OTP salah atau telah kedaluwarsa")
	ErrTooManyAttempts = errors.New("Terlalu banyak percobaan yang salah. Silakan minta OTP baru")
	ErrRateLimited     = errors.New("Silakan tunggu sebelum meminta OTP baru")
)

type contextKey string

const sessionContextKey contextKey = "webadmin_session"

// TelegramSender defines a function that sends a message to a Telegram user
type TelegramSender func(userID int64, text string) error

type otpRecord struct {
	code      string
	expiresAt time.Time
	createdAt time.Time
	attempts  int
}

type Session struct {
	Token      string    `json:"token"`
	TelegramID int64     `json:"telegram_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type AuthManager struct {
	cfg      *config.AppConfig
	sender   TelegramSender
	db       *storage.DB
	mu       sync.RWMutex
	otps     map[int64]*otpRecord
	sessions map[string]*Session
}

func NewAuthManager(cfg *config.AppConfig, sender TelegramSender, db *storage.DB) *AuthManager {
	a := &AuthManager{
		cfg:      cfg,
		sender:   sender,
		db:       db,
		otps:     make(map[int64]*otpRecord),
		sessions: make(map[string]*Session),
	}
	a.loadSessions()
	return a
}

// loadSessions memuat sesi yang tersimpan di DB agar login tetap valid
// setelah aplikasi/webadmin direstart (durasi sesi sesuai TTL).
func (a *AuthManager) loadSessions() {
	if a.db == nil {
		return
	}
	raw, err := a.db.GetSetting("webadmin_sessions", "")
	if err != nil || raw == "" {
		return
	}
	var stored map[string]*Session
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		log.Printf("⚠️ [WebAdmin] Gagal memuat sesi tersimpan: %v", err)
		return
	}
	now := time.Now()
	for token, sess := range stored {
		if sess != nil && now.Before(sess.ExpiresAt) {
			a.sessions[token] = sess
		}
	}
}

// persistSessions menyimpan daftar sesi aktif ke DB (dipanggil dengan lock aktif).
func (a *AuthManager) persistSessionsLocked() {
	if a.db == nil {
		return
	}
	data, err := json.Marshal(a.sessions)
	if err != nil {
		return
	}
	if err := a.db.SetSetting("webadmin_sessions", string(data)); err != nil {
		log.Printf("⚠️ [WebAdmin] Gagal menyimpan sesi: %v", err)
	}
}

// RequestOTP generates a 6-digit OTP and sends it via Telegram to the admin
func (a *AuthManager) RequestOTP(telegramID int64) error {
	if !a.cfg.IsAdminUser(telegramID) {
		return ErrNotAdmin
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Rate limit: prevent requesting faster than once every 15 seconds
	if rec, exists := a.otps[telegramID]; exists {
		if time.Since(rec.createdAt) < 15*time.Second && time.Now().Before(rec.expiresAt) {
			return ErrRateLimited
		}
	}

	// Generate secure 6-digit OTP
	num, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return fmt.Errorf("gagal generate OTP: %w", err)
	}
	code := fmt.Sprintf("%06d", num.Int64()+100000)

	ttlMinutes := a.cfg.WebAdmin.OTPTTLMinutes
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	expiresAt := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)

	a.otps[telegramID] = &otpRecord{
		code:      code,
		expiresAt: expiresAt,
		createdAt: time.Now(),
		attempts:  0,
	}

	if a.sender != nil {
		msg := fmt.Sprintf("🔐 <b>[GoAssistant Web Admin]</b>\n\n"+
			"Kode OTP Login Anda: <code>%s</code>\n"+
			"⏱️ Berlaku selama %d menit.\n"+
			"⚠️ <i>Jangan berikan kode ini kepada siapapun!</i>", code, ttlMinutes)
		if err := a.sender(telegramID, msg); err != nil {
			return fmt.Errorf("gagal mengirimkan OTP ke Telegram: %w", err)
		}
	}

	return nil
}

// VerifyOTP validates the provided OTP code and issues a new session token
func (a *AuthManager) VerifyOTP(telegramID int64, code string) (*Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	rec, exists := a.otps[telegramID]
	if !exists || time.Now().After(rec.expiresAt) {
		delete(a.otps, telegramID)
		return nil, ErrInvalidOTP
	}

	if rec.attempts >= 3 {
		delete(a.otps, telegramID)
		return nil, ErrTooManyAttempts
	}

	if strings.TrimSpace(rec.code) != strings.TrimSpace(code) {
		rec.attempts++
		return nil, ErrInvalidOTP
	}

	// OTP is valid; consume it
	delete(a.otps, telegramID)

	// Generate 32-byte secure session token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("gagal membuat token sesi: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	sessionTTL := a.cfg.WebAdmin.SessionTTLMinutes
	if sessionTTL <= 0 {
		sessionTTL = 1440 // 24 hours
	}

	sess := &Session{
		Token:      token,
		TelegramID: telegramID,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(time.Duration(sessionTTL) * time.Minute),
	}
	a.sessions[token] = sess
	a.persistSessionsLocked()

	return sess, nil
}

// ValidateSession checks if the given token is valid and active
func (a *AuthManager) ValidateSession(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	a.mu.RLock()
	sess, exists := a.sessions[token]
	a.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if time.Now().After(sess.ExpiresAt) {
		a.mu.Lock()
		delete(a.sessions, token)
		a.persistSessionsLocked()
		a.mu.Unlock()
		return nil, false
	}

	return sess, true
}

// RevokeSession logs out a session
func (a *AuthManager) RevokeSession(token string) {
	if token == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
	a.persistSessionsLocked()
}

// RequireAuth middleware protects handler endpoints
func (a *AuthManager) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""

		// 1. Check Authorization header: Bearer <token>
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// 2. Check Cookie
		if token == "" {
			if cookie, err := r.Cookie("goassist_admin_session"); err == nil {
				token = cookie.Value
			}
		}

		sess, ok := a.ValidateSession(token)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Sesi tidak valid atau telah kedaluwarsa. Silakan login kembali."}`))
			return
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, sess)
		next(w, r.WithContext(ctx))
	}
}

// GetSessionFromContext retrieves the Session object from request context
func GetSessionFromContext(ctx context.Context) *Session {
	if sess, ok := ctx.Value(sessionContextKey).(*Session); ok {
		return sess
	}
	return nil
}
