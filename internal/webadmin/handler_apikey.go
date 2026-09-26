package webadmin

import (
	"encoding/json"
	"net/http"
	"strings"
)

type APIKeyPayload struct {
	APIKey   string `json:"api_key"`
	Generate bool   `json:"generate"`
}

// handleAPIKey handles GET and POST /api/system/apikey for WebAdmin
func (s *Server) handleAPIKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		currentKey := s.authMgr.GetAPIKey()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"api_key": currentKey,
			"prefix":  "sk-",
		})

	case http.MethodPost:
		var payload APIKeyPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Payload JSON tidak valid"})
			return
		}

		var finalKey string
		var err error

		if payload.Generate {
			finalKey, err = s.authMgr.GenerateAPIKey()
		} else {
			targetKey := strings.TrimSpace(payload.APIKey)
			if targetKey == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Field 'api_key' tidak boleh kosong"})
				return
			}
			finalKey, err = s.authMgr.SetAPIKey(targetKey)
		}

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"api_key": finalKey,
			"message": "API Key berhasil diperbarui dan disimpan.",
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
	}
}
