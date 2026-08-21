package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	// Flag konfigurasi port server (default :8080)
	port := flag.Int("port", 8080, "Port untuk HTTP Server")
	flag.Parse()

	// Inisialisasi router net/http standar
	mux := http.NewServeMux()

	// Daftarkan endpoint API
	mux.HandleFunc("/api/datafunneling/funneling", FunnelingHandler)

	// Endpoint health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","time":"%s"}`, time.Now().Format(time.RFC3339))
	})

	serverAddr := fmt.Sprintf(":%d", *port)
	server := &http.Server{
		Addr:         serverAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 45 * time.Second, // Memberikan waktu untuk binary timeout (30s) + processing
	}

	log.Println("==========================================================")
	log.Printf("🌐 GoAssist HTTP Server berjalan di port %d", *port)
	log.Printf("📌 URL Base : http://localhost%s", serverAddr)
	log.Printf("📌 Endpoint : GET http://localhost%s/api/datafunneling/funneling", serverAddr)
	log.Println("==========================================================")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("❌ Gagal menjalankan server: %v", err)
	}
}
