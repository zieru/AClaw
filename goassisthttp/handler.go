package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// APIResponse mendefinisikan struktur response standar JSON
type APIResponse struct {
	Status  string      `json:"status"`            // "success" atau "error"
	Command string      `json:"command,omitempty"` // Command yang dijalankan
	Output  interface{} `json:"output,omitempty"`  // Output dari binary
	Message string      `json:"message,omitempty"` // Pesan error jika gagal
}

// FunnelingHandler menangani request GET /api/datafunneling/funneling
func FunnelingHandler(w http.ResponseWriter, r *http.Request) {
	// Pastikan hanya menerima HTTP GET
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			Status:  "error",
			Message: "Method tidak diizinkan. Gunakan GET.",
		})
		return
	}

	// Ambil query parameter opsional: select, limit, output
	query := r.URL.Query()
	selectParam := query.Get("select")
	limitParam := query.Get("limit")
	outputParam := query.Get("output")

	// Jalankan perintah binary g3a
	result, err := ExecuteG3AFunneling(r.Context(), selectParam, limitParam, outputParam)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Status:  "error",
			Command: result.Command,
			Message: err.Error(),
		})
		return
	}

	// Jika output dari binary berupa JSON valid, parse agar menjadi objek JSON rapi di response
	var parsedJSON interface{}
	trimmedOutput := strings.TrimSpace(result.Output)
	if (strings.HasPrefix(trimmedOutput, "{") && strings.HasSuffix(trimmedOutput, "}")) ||
		(strings.HasPrefix(trimmedOutput, "[") && strings.HasSuffix(trimmedOutput, "]")) {
		if err := json.Unmarshal([]byte(trimmedOutput), &parsedJSON); err == nil {
			writeJSON(w, http.StatusOK, APIResponse{
				Status:  "success",
				Command: result.Command,
				Output:  parsedJSON,
			})
			return
		}
	}

	// Jika output plain text / format lain (seperti ton atau table)
	writeJSON(w, http.StatusOK, APIResponse{
		Status:  "success",
		Command: result.Command,
		Output:  result.Output,
	})
}

// writeJSON adalah helper untuk serialize data ke format JSON dan mengirim HTTP status
func writeJSON(w http.ResponseWriter, statusCode int, data APIResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
