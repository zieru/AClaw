package goassisthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// Server mengelola siklus hidup HTTP API Server GoAssist Dinamis dengan fitur Hot Reload
type Server struct {
	httpServer    *http.Server
	port          int
	endpointsFile string
	mu            sync.RWMutex
	lastModTime   time.Time
	endpoints     []EndpointItem
}

// NewServer membuat instance baru Server GoAssist HTTP dengan endpoint dinamis & hot reload
func NewServer(port int, endpointsFile string, readTimeout, writeTimeout time.Duration) *Server {
	if readTimeout <= 0 {
		readTimeout = 15 * time.Second
	}
	if writeTimeout <= 0 {
		writeTimeout = 45 * time.Second
	}

	s := &Server{
		port:          port,
		endpointsFile: endpointsFile,
	}

	// Inisialisasi awal endpoints
	s.reloadEndpoints()

	// Dynamic Handler yang mendukung hot reload otomatis pada setiap request
	handler := s.buildDynamicHandler()

	serverAddr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      corsMiddleware(handler),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	s.httpServer = srv

	return s
}

// reloadEndpoints memuat konfigurasi endpoints.yaml ke memory
func (s *Server) reloadEndpoints() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.endpointsFile == "" {
		s.endpointsFile = "configs/endpoints.yaml"
	}

	fi, err := os.Stat(s.endpointsFile)
	if err != nil {
		log.Printf("⚠️ [HotReload] Gagal membaca info file %s: %v", s.endpointsFile, err)
		if len(s.endpoints) == 0 {
			// Fallback endpoint default jika file belum ada
			s.endpoints = []EndpointItem{
				{
					Path:           "/api/datafunneling/funneling",
					Method:         "GET",
					Binary:         "g3a",
					Command:        "/datafunneling/funneling",
					Type:           "pagination",
					TimeoutSeconds: 30,
					Defaults:       map[string]string{"output": "json"},
					Pagination: PaginationConfig{
						DefaultPage:  1,
						DefaultLimit: 10,
						MaxLimit:     100,
						PassAs:       "page",
					},
				},
			}
		}
		return
	}

	cfg, err := LoadEndpointsConfig(s.endpointsFile)
	if err != nil {
		log.Printf("⚠️ [HotReload] Gagal memuat %s: %v", s.endpointsFile, err)
		return
	}

	s.lastModTime = fi.ModTime()
	s.endpoints = cfg.Endpoints
	log.Printf("🔄 [HotReload] Memuat %d endpoints aktif dari %s (ModTime: %s)",
		len(s.endpoints), s.endpointsFile, s.lastModTime.Format("15:04:05"))
}

// checkAndReload memeriksa apakah file endpoints.yaml telah berubah dan memuat ulang secara otomatis
func (s *Server) checkAndReload() []EndpointItem {
	if s.endpointsFile == "" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.endpoints
	}

	fi, err := os.Stat(s.endpointsFile)
	if err != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.endpoints
	}

	s.mu.RLock()
	isModified := fi.ModTime().After(s.lastModTime)
	s.mu.RUnlock()

	if isModified {
		s.reloadEndpoints()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.endpoints
}

// buildDynamicHandler menyusun router dinamis yang memetakan request ke endpoint terdaftar
func (s *Server) buildDynamicHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Health check
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"ok","time":"%s"}`, time.Now().Format(time.RFC3339))
			return
		}

		// Periksa perubahan file dan ambil list endpoint terbaru
		currentEndpoints := s.checkAndReload()

		// 2. Endpoint Discovery
		if r.URL.Path == "/api/routes" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"count":  len(currentEndpoints),
				"routes": currentEndpoints,
			})
			return
		}

		// 3. Cocokkan request path ke endpoint terdaftar
		for _, ep := range currentEndpoints {
			if ep.Path == r.URL.Path {
				CreateDynamicHandler(ep)(w, r)
				return
			}
		}

		// 4. Jika endpoint tidak ditemukan
		writeJSON(w, http.StatusNotFound, APIResponse{
			Status:  "error",
			Message: fmt.Sprintf("Endpoint %s tidak ditemukan", r.URL.Path),
		})
	})
}

// corsMiddleware menangani CORS header dan HTTP OPTIONS preflight request
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, X-Requested-With, Origin")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Start menjalankan HTTP server secara asinkron (background goroutine)
func (s *Server) Start() {
	go func() {
		log.Printf("🌐 GoAssist Dynamic HTTP Server aktif di http://localhost:%d", s.port)
		log.Printf("📌 Discovery Routes : GET http://localhost:%d/api/routes", s.port)
		s.mu.RLock()
		for _, ep := range s.endpoints {
			log.Printf("📌 Endpoint Aktif    : [%s] http://localhost:%d%s (%s)", ep.Method, s.port, ep.Path, ep.Type)
		}
		s.mu.RUnlock()
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ GoAssist HTTP Server error: %v", err)
		}
	}()
}

// Shutdown menghentikan HTTP server secara aman (graceful shutdown)
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("🛑 Menghentikan GoAssist HTTP Server...")
	return s.httpServer.Shutdown(ctx)
}

