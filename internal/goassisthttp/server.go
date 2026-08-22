package goassisthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Server mengelola siklus hidup HTTP API Server GoAssist Dinamis
type Server struct {
	httpServer *http.Server
	port       int
	endpoints  []EndpointItem
}

// NewServer membuat instance baru Server GoAssist HTTP dengan endpoint dinamis
func NewServer(port int, endpointsFile string, readTimeout, writeTimeout time.Duration) *Server {
	if readTimeout <= 0 {
		readTimeout = 15 * time.Second
	}
	if writeTimeout <= 0 {
		writeTimeout = 45 * time.Second
	}

	mux := http.NewServeMux()

	// 1. Muat konfigurasi endpoint dinamis dari file YAML
	cfg, err := LoadEndpointsConfig(endpointsFile)
	var registeredEndpoints []EndpointItem
	if err != nil {
		log.Printf("⚠️ Gagal memuat %s: %v (menggunakan fallback endpoint default)", endpointsFile, err)
		// Fallback endpoint default jika file belum ada
		defaultEp := EndpointItem{
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
		}
		registeredEndpoints = []EndpointItem{defaultEp}
		mux.HandleFunc(defaultEp.Path, CreateDynamicHandler(defaultEp))
	} else {
		registeredEndpoints = cfg.Endpoints
		// Daftarkan setiap endpoint secara dinamis
		for _, ep := range cfg.Endpoints {
			endpointCopy := ep
			mux.HandleFunc(endpointCopy.Path, CreateDynamicHandler(endpointCopy))
			log.Printf("  📍 Route terdaftar: [%s] %s -> %s %s (Type: %s)",
				endpointCopy.Method, endpointCopy.Path, endpointCopy.Binary, endpointCopy.Command, endpointCopy.Type)
		}
	}

	// 2. Endpoint Discovery untuk melihat seluruh rute yang aktif
	mux.HandleFunc("/api/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"count":  len(registeredEndpoints),
			"routes": registeredEndpoints,
		})
	})

	// 3. Endpoint health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","time":"%s"}`, time.Now().Format(time.RFC3339))
	})

	// 4. Register OAuth routes for Gemini Web & Google Authentication
	mux.HandleFunc("/auth/gemini/login", func(w http.ResponseWriter, r *http.Request) {
		if globalOAuthMgr != nil {
			globalOAuthMgr.handleGeminiLogin(w, r)
		} else {
			http.Error(w, "OAuth Bridge belum diinisialisasi.", http.StatusServiceUnavailable)
		}
	})
	mux.HandleFunc("/auth/gemini/callback", func(w http.ResponseWriter, r *http.Request) {
		if globalOAuthMgr != nil {
			globalOAuthMgr.handleGeminiCallback(w, r)
		} else {
			http.Error(w, "OAuth Bridge belum diinisialisasi.", http.StatusServiceUnavailable)
		}
	})

	serverAddr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	return &Server{
		httpServer: srv,
		port:       port,
		endpoints:  registeredEndpoints,
	}
}

// Start menjalankan HTTP server secara asinkron (background goroutine)
func (s *Server) Start() {
	go func() {
		log.Printf("🌐 GoAssist Dynamic HTTP Server aktif di http://localhost:%d", s.port)
		log.Printf("📌 Discovery Routes : GET http://localhost:%d/api/routes", s.port)
		for _, ep := range s.endpoints {
			log.Printf("📌 Endpoint Aktif    : [%s] http://localhost:%d%s (%s)", ep.Method, s.port, ep.Path, ep.Type)
		}
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
