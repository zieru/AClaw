package webadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/storage"
)

type Server struct {
	mu           sync.Mutex
	bindAddress  string
	port         int
	httpServer   *http.Server
	listener     net.Listener
	db           *storage.DB
	cfg          *config.AppConfig
	orchestrator *agent.Orchestrator
	authMgr      *AuthManager
	startTime    time.Time
	isClosed     bool
	activeChats  sync.Map
}

func NewServer(
	cfg *config.AppConfig,
	db *storage.DB,
	orch *agent.Orchestrator,
	sender TelegramSender,
) *Server {
	bindAddress := "0.0.0.0"
	if cfg != nil && cfg.WebAdmin.BindAddress != "" {
		bindAddress = cfg.WebAdmin.BindAddress
	}

	port := 12111
	if cfg != nil && cfg.WebAdmin.Port > 0 {
		port = cfg.WebAdmin.Port
	}

	// Check if port or bind address is configured in DB system_settings
	if db != nil {
		if savedBind, err := db.GetSetting("webadmin_bind_address", ""); err == nil && savedBind != "" {
			bindAddress = savedBind
		}
		if savedPortStr, err := db.GetSetting("webadmin_port", ""); err == nil && savedPortStr != "" {
			if p, err := strconv.Atoi(savedPortStr); err == nil && p >= 1024 && p <= 65535 {
				port = p
			}
		}
	}

	authMgr := NewAuthManager(cfg, sender)

	return &Server{
		bindAddress:  bindAddress,
		port:         port,
		db:           db,
		cfg:          cfg,
		orchestrator: orch,
		authMgr:      authMgr,
		startTime:    time.Now(),
	}
}

// GetPort returns the current listening port
func (s *Server) GetPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// GetBindAddress returns current binding address
func (s *Server) GetBindAddress() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bindAddress == "" {
		return "0.0.0.0"
	}
	return s.bindAddress
}

// GetURL returns the accessible local admin URL
func (s *Server) GetURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	host := s.bindAddress
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d/admin", host, s.port)
}

// AuthManager returns the AuthManager instance
func (s *Server) AuthManager() *AuthManager {
	return s.authMgr
}

// Start boots up the Web Admin HTTP server
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isClosed {
		return fmt.Errorf("server telah dimatikan")
	}

	mux := s.buildRoutes()

	addr := fmt.Sprintf("%s:%d", s.bindAddress, s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gagal bind ke %s:%d: %w", s.bindAddress, s.port, err)
	}
	s.listener = ln

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		log.Printf("🌐 [WebAdmin] Server aktif mendengarkan di http://%s:%d/admin", s.bindAddress, s.port)
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			log.Printf("⚠️ [WebAdmin] Server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.isClosed = true
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// Restart rebinds the server to a new address and port gracefully
func (s *Server) Restart(bindAddress string, newPort int) error {
	if newPort < 1024 || newPort > 65535 {
		return fmt.Errorf("nomor port harus antara 1024 sampai 65535")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(bindAddress) == "" {
		bindAddress = s.bindAddress
	}
	if bindAddress == "" {
		bindAddress = "0.0.0.0"
	}

	// 1. Test listening on new address and port first
	newAddr := fmt.Sprintf("%s:%d", bindAddress, newPort)
	newLn, err := net.Listen("tcp", newAddr)
	if err != nil {
		return fmt.Errorf("alamat %s:%d sedang digunakan atau tidak dapat dibuka: %w", bindAddress, newPort, err)
	}

	// 2. Shut down old server gracefully
	if s.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.httpServer.Shutdown(shutdownCtx)
		cancel()
	}

	// 3. Save new settings to DB
	if s.db != nil {
		_ = s.db.SetSetting("webadmin_bind_address", bindAddress)
		_ = s.db.SetSetting("webadmin_port", strconv.Itoa(newPort))
	}

	s.bindAddress = bindAddress
	s.port = newPort
	s.listener = newLn

	mux := s.buildRoutes()
	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		log.Printf("🌐 [WebAdmin] Server berhasil berpindah ke http://%s:%d/admin", bindAddress, newPort)
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			log.Printf("⚠️ [WebAdmin] Server error: %v", err)
		}
	}()

	return nil
}

// RestartPort restarts server retaining current bindAddress
func (s *Server) RestartPort(newPort int) error {
	return s.Restart("", newPort)
}

func (s *Server) buildRoutes() http.Handler {
	mux := http.NewServeMux()

	// Embedded Static Assets
	uiFs, err := GetUIFileSystem()
	if err == nil {
		fileServer := http.FileServer(uiFs)
		mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
			relPath := strings.TrimPrefix(r.URL.Path, "/admin/")
			if relPath == "" {
				relPath = "index.html"
			}
			f, err := uiFs.Open(relPath)
			if err != nil {
				// Fallback to index.html for client-side Nuxt SPA routing
				indexFile, indexErr := uiFs.Open("index.html")
				if indexErr == nil {
					defer indexFile.Close()
					if stat, err := indexFile.Stat(); err == nil {
						if seeker, ok := indexFile.(io.ReadSeeker); ok {
							http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
							return
						}
					}
				}
			} else {
				_ = f.Close()
			}
			http.StripPrefix("/admin", fileServer).ServeHTTP(w, r)
		})
		mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
				return
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	// Auth Endpoints
	mux.HandleFunc("/api/auth/request-otp", s.handleRequestOTP)
	mux.HandleFunc("/api/auth/verify-otp", s.handleVerifyOTP)
	mux.HandleFunc("/api/auth/me", s.handleAuthMe)
	mux.HandleFunc("/api/auth/logout", s.handleLogout)

	// Protected Endpoints
	mux.HandleFunc("/api/activities", s.authMgr.RequireAuth(s.handleListActivities))
	mux.HandleFunc("/api/activities/detail", s.authMgr.RequireAuth(s.handleGetActivity))
	mux.HandleFunc("/api/system/stats", s.authMgr.RequireAuth(s.handleSystemStats))
	mux.HandleFunc("/api/system/port", s.authMgr.RequireAuth(s.handleUpdateAddress))
	mux.HandleFunc("/api/system/address", s.authMgr.RequireAuth(s.handleUpdateAddress))

	// Topic Endpoints
	mux.HandleFunc("/api/topics", s.authMgr.RequireAuth(s.handleListTopics))
	mux.HandleFunc("/api/topics/switch", s.authMgr.RequireAuth(s.handleSwitchTopic))
	mux.HandleFunc("/api/topics/new", s.authMgr.RequireAuth(s.handleNewTopic))
	mux.HandleFunc("/api/topics/messages", s.authMgr.RequireAuth(s.handleGetTopicMessages))

	// AI Chat Endpoints
	mux.HandleFunc("/api/chat", s.authMgr.RequireAuth(s.handleChat))
	mux.HandleFunc("/api/chat/stop", s.authMgr.RequireAuth(s.handleChatStop))
	mux.HandleFunc("/api/chat/history", s.authMgr.RequireAuth(s.handleChatHistory))
	mux.HandleFunc("/api/chat/clear", s.authMgr.RequireAuth(s.handleClearChat))
	mux.HandleFunc("/api/models", s.authMgr.RequireAuth(s.handleListModels))

	return s.corsMiddleware(mux)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Auth HTTP Handlers ---

type RequestOTPPayload struct {
	TelegramID int64 `json:"telegram_id"`
}

func (s *Server) handleRequestOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RequestOTPPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TelegramID <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Telegram User ID tidak valid"})
		return
	}

	if err := s.authMgr.RequestOTP(req.TelegramID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusBadRequest
		if err == ErrNotAdmin {
			status = http.StatusForbidden
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Kode OTP telah dikirimkan ke Telegram Anda",
	})
}

type VerifyOTPPayload struct {
	TelegramID int64  `json:"telegram_id"`
	OTP        string `json:"otp"`
}

func (s *Server) handleVerifyOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req VerifyOTPPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TelegramID <= 0 || req.OTP == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Payload tidak lengkap"})
		return
	}

	sess, err := s.authMgr.VerifyOTP(req.TelegramID, req.OTP)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Set HTTP-Only Cookie
	ttlSeconds := int(time.Until(sess.ExpiresAt).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     "goassist_admin_session",
		Value:    sess.Token,
		Path:     "/",
		MaxAge:   ttlSeconds,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"token":       sess.Token,
		"telegram_id": sess.TelegramID,
		"expires_at":  sess.ExpiresAt,
	})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	token := ""
	if cookie, err := r.Cookie("goassist_admin_session"); err == nil {
		token = cookie.Value
	}
	if token == "" {
		authHeader := r.Header.Get("Authorization")
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}
	}

	sess, ok := s.authMgr.ValidateSession(token)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"telegram_id":   sess.TelegramID,
		"created_at":    sess.CreatedAt,
		"expires_at":    sess.ExpiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("goassist_admin_session"); err == nil {
		s.authMgr.RevokeSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "goassist_admin_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type UpdateAddressPayload struct {
	BindAddress string `json:"bind_address"`
	Port        int    `json:"port"`
}

func (s *Server) handleUpdateAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UpdateAddressPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Payload JSON tidak valid"})
		return
	}

	targetPort := req.Port
	if targetPort == 0 {
		targetPort = s.GetPort()
	}
	if targetPort < 1024 || targetPort > 65535 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Port harus antara 1024 sampai 65535"})
		return
	}

	targetBind := strings.TrimSpace(req.BindAddress)
	if targetBind == "" {
		targetBind = s.GetBindAddress()
	}

	// Return success first, then rebind in background
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"port":         targetPort,
		"bind_address": targetBind,
		"message":      fmt.Sprintf("Alamat server berhasil diubah ke %s:%d", targetBind, targetPort),
	})

	go func(b string, p int) {
		time.Sleep(200 * time.Millisecond)
		if err := s.Restart(b, p); err != nil {
			log.Printf("⚠️ [WebAdmin] Gagal restart ke %s:%d: %v", b, p, err)
		}
	}(targetBind, targetPort)
}
