package webadmin

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"path/filepath"
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

	authMgr := NewAuthManager(cfg, sender, db)

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

	bindAddress = strings.TrimSpace(bindAddress)
	if bindAddress == "" {
		bindAddress = s.bindAddress
	}
	if bindAddress == "" {
		bindAddress = "0.0.0.0"
	}

	// Normalisasi: "localhost" bukan address yang valid untuk net.Listen di semua platform
	if bindAddress == "localhost" {
		bindAddress = "127.0.0.1"
	}

	// Validasi format address lebih awal agar gagal cepat & jelas
	if net.ParseIP(bindAddress) == nil {
		return fmt.Errorf("alamat binding tidak valid: %q (gunakan IP, mis. 0.0.0.0 atau 127.0.0.1)", bindAddress)
	}

	// 1. Tutup listener lama TERLEBIH DAHULU agar alamat yang sama bisa di-rebind
	//    (mis. pindah 0.0.0.0 -> 127.0.0.1 pada port yang sama tidak gagal "address in use").
	oldListener := s.listener
	oldServer := s.httpServer
	if oldListener != nil {
		_ = oldListener.Close()
	}

	// 2. Bind alamat baru
	newAddr := fmt.Sprintf("%s:%d", bindAddress, newPort)
	newLn, err := net.Listen("tcp", newAddr)
	if err != nil {
		// Rollback: buka kembali listener lama supaya web admin tidak mati
		if oldServer != nil && oldListener != nil {
			s.httpServer = oldServer
			s.listener = oldListener
			go func(ln net.Listener, srv *http.Server) {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					log.Printf("⚠️ [WebAdmin] Rollback server error: %v", err)
				}
			}(oldListener, oldServer)
		}
		return fmt.Errorf("alamat %s:%d sedang digunakan atau tidak dapat dibuka: %w", bindAddress, newPort, err)
	}

	// 3. Shut down old http server gracefully (setelah bind baru sukses)
	if oldServer != nil && oldServer != s.httpServer {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = oldServer.Shutdown(shutdownCtx)
		cancel()
	}

	// 4. Save new settings to DB
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
		serveStatic := func(w http.ResponseWriter, r *http.Request) {
			relPath := strings.TrimPrefix(r.URL.Path, "/admin/")
			if relPath == "" || relPath == "/" {
				relPath = "index.html"
			}

			acceptsGzip := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")

			// 1. Try serving pre-gzipped version (.gz) if client supports gzip
			if acceptsGzip {
				gzPath := relPath + ".gz"
				if fGz, err := uiFs.Open(gzPath); err == nil {
					defer fGz.Close()
					if stat, err := fGz.Stat(); err == nil {
						if seeker, ok := fGz.(io.ReadSeeker); ok {
							mimeType := mime.TypeByExtension(filepath.Ext(relPath))
							if mimeType == "" {
								if strings.HasSuffix(relPath, ".js") {
									mimeType = "application/javascript"
								} else if strings.HasSuffix(relPath, ".css") {
									mimeType = "text/css"
								} else if strings.HasSuffix(relPath, ".html") {
									mimeType = "text/html; charset=utf-8"
								} else {
									mimeType = "application/octet-stream"
								}
							}
							w.Header().Set("Content-Encoding", "gzip")
							w.Header().Set("Content-Type", mimeType)
							w.Header().Set("Vary", "Accept-Encoding")
							http.ServeContent(w, r, relPath, stat.ModTime(), seeker)
							return
						}
					}
				}
			}

			// 2. Fallback to normal file or index.html for SPA routes
			f, err := uiFs.Open(relPath)
			if err != nil {
				// Fallback to index.html for client-side Nuxt SPA routing
				indexFile, indexErr := uiFs.Open("index.html")
				if indexErr == nil {
					defer indexFile.Close()
					if stat, err := indexFile.Stat(); err == nil {
						if seeker, ok := indexFile.(io.ReadSeeker); ok {
							w.Header().Set("Content-Type", "text/html; charset=utf-8")
							http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
							return
						}
					}
				}
			} else {
				_ = f.Close()
			}
			http.StripPrefix("/admin", fileServer).ServeHTTP(w, r)
		}

		mux.HandleFunc("/admin/", serveStatic)
		mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
				return
			}
			serveStatic(w, r)
		})
	} else {
		log.Printf("⚠️ [WebAdmin] Gagal memuat UI embedded filesystem: %v", err)
		fallback503 := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>GoAssistant Admin - Service Unavailable</title></head><body style="font-family:sans-serif;text-align:center;padding:50px;"><h2>⚠️ Service Unavailable (503)</h2><p>Filesystem Web Admin UI belum dibangun atau gagal dimuat.</p></body></html>`))
		}
		mux.HandleFunc("/admin/", fallback503)
		mux.HandleFunc("/admin", fallback503)
		mux.HandleFunc("/", fallback503)
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

	return s.corsMiddleware(s.gzipMiddleware(mux))
}

func (s *Server) gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		// Skip gzip for SSE streaming endpoint or WebSocket
		if r.URL.Path == "/api/chat" || r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}

		gzw := &gzipResponseWriter{ResponseWriter: w}
		defer func() {
			if gzw.gzWriter != nil {
				_ = gzw.gzWriter.Close()
			}
		}()

		next.ServeHTTP(gzw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gzWriter    *gzip.Writer
	wroteHeader bool
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzWriter != nil {
		return w.gzWriter.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	ct := w.Header().Get("Content-Type")
	ce := w.Header().Get("Content-Encoding")

	shouldCompress := ce == "" &&
		!strings.Contains(ct, "text/event-stream") &&
		!strings.Contains(ct, "font/woff2") &&
		!strings.Contains(ct, "image/png") &&
		!strings.Contains(ct, "image/jpeg") &&
		!strings.Contains(ct, "image/webp") &&
		!strings.Contains(ct, "application/gzip") &&
		!strings.Contains(ct, "application/zip")

	if shouldCompress {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		gz, err := gzip.NewWriterLevel(w.ResponseWriter, gzip.DefaultCompression)
		if err == nil {
			w.gzWriter = gz
		}
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *gzipResponseWriter) Flush() {
	if w.gzWriter != nil {
		_ = w.gzWriter.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
