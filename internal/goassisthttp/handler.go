package goassisthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/provider"
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
	Type       string          `json:"type,omitempty"`       // "pagination", "regular", atau "llm" / "ai"
	Pagination *PaginationMeta `json:"pagination,omitempty"` // Metadata pagination (jika tipe pagination)
	Output     interface{}     `json:"output,omitempty"`     // Output dari binary atau LLM
	Message    string          `json:"message,omitempty"`    // Pesan error jika gagal
}

// CreateDynamicHandler membuat http.HandlerFunc dinamis berdasarkan EndpointItem
func CreateDynamicHandler(ep EndpointItem) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Validasi HTTP Method
		if r.Method != ep.Method && !(ep.Method == "" && r.Method == http.MethodGet) {
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

		timeout := time.Duration(ep.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}

		// 3. Khusus endpoint bertipe LLM / AI yang menggunakan built-in global AI engine
		if strings.EqualFold(ep.Type, "llm") || strings.EqualFold(ep.Type, "ai") {
			handleLLMEndpoint(w, r, ep, flags, timeout)
			return
		}

		// 4. Logika khusus jika endpoint bertipe "pagination"
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

		// 5. Eksekusi binary
		result, err := ExecuteDynamicCommand(r.Context(), ep.Binary, ep.Command, flags, timeout)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, APIResponse{
				Status:  "error",
				Type:    ep.Type,
				Message: err.Error(),
			})
			return
		}

		// 6. Cek apakah output adalah JSON valid agar di-render sebagai JSON object terstruktur
		var parsedJSON interface{}
		trimmedOutput := strings.TrimSpace(result.Output)
		if (strings.HasPrefix(trimmedOutput, "{") && strings.HasSuffix(trimmedOutput, "}")) ||
			(strings.HasPrefix(trimmedOutput, "[") && strings.HasSuffix(trimmedOutput, "]")) {
			if err := json.Unmarshal([]byte(trimmedOutput), &parsedJSON); err == nil {
				writeJSON(w, http.StatusOK, APIResponse{
					Status:     "success",
					Type:       ep.Type,
					Pagination: paginationMeta,
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
			Output:     result.Output,
		})
	}
}

// handleLLMEndpoint menangani eksekusi endpoint berbasis built-in Global LLM GoAssistant
func handleLLMEndpoint(w http.ResponseWriter, r *http.Request, ep EndpointItem, flags map[string]string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	var dataContext string
	if ep.Command != "" {
		res, err := ExecuteDynamicCommand(ctx, ep.Binary, ep.Command, flags, timeout)
		if err == nil && res != nil {
			dataContext = strings.TrimSpace(res.Output)
		}
	}

	systemPrompt := ep.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "Anda adalah AI Analis Data Operasional dan Solusi Bisnis handal. Analisis data yang diberikan dan hasilkan output berupa format JSON yang rapi, tajam, dan langsung dapat ditindaklanjuti."
	}

	userPrompt := ep.Prompt
	if userPrompt == "" {
		userPrompt = "Berikan analisis paint points dan need support dari data berikut."
	}

	if dataContext != "" {
		userPrompt = fmt.Sprintf("%s\n\n[DATA SUMBER OPERASIONAL / G3A]:\n%s", userPrompt, dataContext)
	}

	messages := []provider.ChatMessage{
		{Role: provider.RoleSystem, Content: systemPrompt},
		{Role: provider.RoleUser, Content: userPrompt},
	}

	provMgr := provider.GetManager()
	chatReq := provider.ChatRequest{
		Model:       ep.Model,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   2000,
	}

	resp, err := provMgr.GenerateWithFallback(ctx, "", chatReq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{
			Status:  "error",
			Type:    ep.Type,
			Message: fmt.Sprintf("Gagal mengeksekusi built-in global LLM GoAssistant: %v", err),
		})
		return
	}

	rawContent := strings.TrimSpace(resp.Content)
	cleanContent := rawContent
	if strings.HasPrefix(cleanContent, "```json") {
		cleanContent = strings.TrimPrefix(cleanContent, "```json")
		cleanContent = strings.TrimSuffix(cleanContent, "```")
		cleanContent = strings.TrimSpace(cleanContent)
	} else if strings.HasPrefix(cleanContent, "```") {
		cleanContent = strings.TrimPrefix(cleanContent, "```")
		cleanContent = strings.TrimSuffix(cleanContent, "```")
		cleanContent = strings.TrimSpace(cleanContent)
	}

	var parsedJSON interface{}
	if err := json.Unmarshal([]byte(cleanContent), &parsedJSON); err == nil {
		writeJSON(w, http.StatusOK, APIResponse{
			Status: "success",
			Type:   ep.Type,
			Output: parsedJSON,
		})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Status: "success",
		Type:   ep.Type,
		Output: rawContent,
	})
}

// writeJSON adalah helper untuk serialize data ke format JSON dan mengirim HTTP status
func writeJSON(w http.ResponseWriter, statusCode int, data APIResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
