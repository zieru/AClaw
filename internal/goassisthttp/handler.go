package goassisthttp

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PaginationMeta metadata informasi pagination di respons JSON
type PaginationMeta struct {
	Page   int `json:"page,omitempty"`
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// APIResponse mendefinisikan struktur response standar JSON
type APIResponse struct {
	Status     string          `json:"status"`               // "success" atau "error"
	Type       string          `json:"type,omitempty"`       // "pagination" atau "regular"
	Pagination *PaginationMeta `json:"pagination,omitempty"` // Metadata pagination (jika tipe pagination)
	Command    string          `json:"command,omitempty"`    // Command yang dijalankan
	Output     interface{}     `json:"output,omitempty"`     // Output dari binary
	Message    string          `json:"message,omitempty"`    // Pesan error jika gagal
}

// CreateDynamicHandler membuat http.HandlerFunc dinamis berdasarkan EndpointItem
func CreateDynamicHandler(ep EndpointItem) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Validasi HTTP Method
		if r.Method != ep.Method {
			writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
				Status:  "error",
				Message: "Method tidak diizinkan. Gunakan " + ep.Method,
			})
			return
		}

		// 1. Kumpulkan flags: mulai dari default values
		flags := make(map[string]string)
		for k, v := range ep.Defaults {
			flags[k] = v
		}

		// 2. Baca seluruh query params dari request URL dan masukkan ke flags
		query := r.URL.Query()
		for k, values := range query {
			if len(values) > 0 {
				flags[k] = values[0]
			}
		}

		// 3. Logika khusus jika endpoint bertipe "pagination"
		var paginationMeta *PaginationMeta
		if ep.Type == "pagination" {
			page := ep.Pagination.DefaultPage
			if pStr := query.Get("page"); pStr != "" {
				if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
					page = p
				}
			}

			limit := ep.Pagination.DefaultLimit
			if lStr := query.Get("limit"); lStr != "" {
				if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
					limit = l
				}
			}
			// Batasi jika melebihi max_limit
			if ep.Pagination.MaxLimit > 0 && limit > ep.Pagination.MaxLimit {
				limit = ep.Pagination.MaxLimit
			}

			offset := (page - 1) * limit
			if offStr := query.Get("offset"); offStr != "" {
				if off, err := strconv.Atoi(offStr); err == nil && off >= 0 {
					offset = off
				}
			}

			if ep.Pagination.PassAs == "offset" {
				flags["offset"] = strconv.Itoa(offset)
				flags["limit"] = strconv.Itoa(limit)
				delete(flags, "page")
				paginationMeta = &PaginationMeta{
					Limit:  limit,
					Offset: offset,
				}
			} else {
				// Default pass_as: "page"
				flags["page"] = strconv.Itoa(page)
				flags["limit"] = strconv.Itoa(limit)
				paginationMeta = &PaginationMeta{
					Page:  page,
					Limit: limit,
				}
			}
		}

		// 4. Eksekusi binary
		timeout := time.Duration(ep.TimeoutSeconds) * time.Second
		result, err := ExecuteDynamicCommand(r.Context(), ep.Binary, ep.Command, flags, timeout)
		if err != nil {
			var cmdStr string
			if result != nil {
				cmdStr = result.Command
			}
			writeJSON(w, http.StatusInternalServerError, APIResponse{
				Status:  "error",
				Type:    ep.Type,
				Command: cmdStr,
				Message: err.Error(),
			})
			return
		}

		// 5. Cek apakah output adalah JSON valid agar di-render sebagai JSON object terstruktur
		var parsedJSON interface{}
		trimmedOutput := strings.TrimSpace(result.Output)
		if (strings.HasPrefix(trimmedOutput, "{") && strings.HasSuffix(trimmedOutput, "}")) ||
			(strings.HasPrefix(trimmedOutput, "[") && strings.HasSuffix(trimmedOutput, "]")) {
			if err := json.Unmarshal([]byte(trimmedOutput), &parsedJSON); err == nil {
				writeJSON(w, http.StatusOK, APIResponse{
					Status:     "success",
					Type:       ep.Type,
					Pagination: paginationMeta,
					Command:    result.Command,
					Output:     parsedJSON,
				})
				return
			}
		}

		// Jika output plain text / format lain
		writeJSON(w, http.StatusOK, APIResponse{
			Status:     "success",
			Type:       ep.Type,
			Pagination: paginationMeta,
			Command:    result.Command,
			Output:     result.Output,
		})
	}
}

// writeJSON adalah helper untuk serialize data ke format JSON dan mengirim HTTP status
func writeJSON(w http.ResponseWriter, statusCode int, data APIResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
