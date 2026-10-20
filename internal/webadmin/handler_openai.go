package webadmin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"goassistant/internal/provider"
)

// getProviderManager retrieves active provider manager instance
func (s *Server) getProviderManager() *provider.Manager {
	if s.orchestrator != nil && s.orchestrator.ProviderManager() != nil {
		return s.orchestrator.ProviderManager()
	}
	return provider.GetManager()
}

// handleOpenAIModels handles GET /v1/models (OpenAI compatibility)
func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Hanya method GET yang diizinkan untuk endpoint ini")
		return
	}

	pMgr := s.getProviderManager()
	var models []OpenAIModel
	now := time.Now().Unix()

	seen := make(map[string]bool)

	// 1. Ambil model dari seluruh provider yang terdaftar di Manager
	if pMgr != nil {
		for _, p := range pMgr.ListAll() {
			if p == nil {
				continue
			}
			owner := strings.ToLower(p.Type())
			if owner == "" {
				owner = strings.ToLower(p.Name())
			}

			// Model default provider
			defModel := strings.TrimSpace(p.DefaultModel())
			if defModel != "" && !seen[defModel] {
				seen[defModel] = true
				models = append(models, OpenAIModel{
					ID:      defModel,
					Object:  "model",
					Created: now,
					OwnedBy: owner,
				})
			}

			// Seluruh model lain yang didukung provider
			for _, m := range p.Models() {
				m = strings.TrimSpace(m)
				if m != "" && !seen[m] {
					seen[m] = true
					models = append(models, OpenAIModel{
						ID:      m,
						Object:  "model",
						Created: now,
						OwnedBy: owner,
					})
				}
			}
		}

		// 2. Ambil Model Combos jika ada
		for _, combo := range pMgr.ListCombos() {
			if combo == nil {
				continue
			}
			comboID := combo.ID
			if !seen[comboID] {
				seen[comboID] = true
				models = append(models, OpenAIModel{
					ID:      comboID,
					Object:  "model",
					Created: now,
					OwnedBy: "goassistant",
				})
			}
		}
	}

	// 3. Fallback jika list kosong
	if len(models) == 0 {
		models = append(models, OpenAIModel{
			ID:      "default",
			Object:  "model",
			Created: now,
			OwnedBy: "goassistant",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(OpenAIModelListResponse{
		Object: "list",
		Data:   models,
	})
}

// handleOpenAIChatCompletions handles POST /v1/chat/completions (OpenAI compatibility)
func (s *Server) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Hanya method POST yang diizinkan untuk endpoint ini")
		return
	}

	var req OpenAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Gagal parsing JSON request body: %v", err))
		return
	}

	if len(req.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Array 'messages' tidak boleh kosong")
		return
	}

	pMgr := s.getProviderManager()
	if pMgr == nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "Provider Manager AI belum diinisialisasi")
		return
	}

	// Tentukan model target
	targetModel := strings.TrimSpace(req.Model)
	if targetModel == "" || targetModel == "auto" {
		if s.cfg != nil && s.cfg.Defaults.DefaultModel != "" {
			targetModel = s.cfg.Defaults.DefaultModel
		} else {
			targetModel = "gemini-2.5-flash"
		}
	}

	// Konversi messages OpenAI ke provider.ChatMessage
	var chatMessages []provider.ChatMessage
	for _, m := range req.Messages {
		contentStr, images := m.GetContentString()
		role := provider.RoleUser
		switch strings.ToLower(m.Role) {
		case "system":
			role = provider.RoleSystem
		case "assistant":
			role = provider.RoleAssistant
		case "user":
			role = provider.RoleUser
		case "tool":
			role = provider.RoleTool
		}

		chatMessages = append(chatMessages, provider.ChatMessage{
			Role:       role,
			Content:    contentStr,
			Images:     images,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		})
	}

	// Parameter opsi
	temperature := 0.7
	if req.Temperature != nil {
		temperature = *req.Temperature
	}

	maxTokens := 2048
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}

	// Reasoning/Thinking configuration
	thinkingLevel := req.ReasoningEffort
	if thinkingLevel == "" {
		thinkingLevel = req.ThinkingLevel
	}
	thinkingBudget := 0
	if req.ThinkingBudget != nil {
		thinkingBudget = *req.ThinkingBudget
	}
	thinkingEnabled := thinkingLevel != "" && thinkingLevel != "disabled"

	chatReq := provider.ChatRequest{
		Model:           targetModel,
		Messages:        chatMessages,
		Temperature:     temperature,
		MaxTokens:       maxTokens,
		ThinkingEnabled: thinkingEnabled,
		ThinkingLevel:   thinkingLevel,
		ThinkingBudget:  thinkingBudget,
	}

	createdTime := time.Now().Unix()
	completionID := fmt.Sprintf("chatcmpl-%s", strings.ReplaceAll(uuid.New().String(), "-", "")[:16])

	ctx := r.Context()

	// 1. STREAMING MODE (SSE)
	if req.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "Streaming HTTP tidak didukung oleh lingkungan server")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		chatReq.Stream = true
		chatReq.StreamCallback = func(chunk provider.StreamChunk) {
			if chunk.Content == "" && chunk.Thinking == "" {
				return
			}

			// Kirim delta content
			streamChunk := OpenAIChatStreamChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: createdTime,
				Model:   targetModel,
				Choices: []OpenAIChatStreamChoice{
					{
						Index: 0,
						Delta: OpenAIChatStreamDelta{
							Role:    "assistant",
							Content: chunk.Content,
						},
						FinishReason: nil,
					},
				},
			}

			chunkBytes, err := json.Marshal(streamChunk)
			if err == nil {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunkBytes)
				flusher.Flush()
			}
		}

		resp, err := pMgr.GenerateWithFallback(ctx, "", chatReq)
		if err != nil {
			log.Printf("⚠️ [OpenAI API] Error streaming completion: %v", err)
			errPayload, _ := json.Marshal(map[string]interface{}{
				"error": map[string]interface{}{
					"message": err.Error(),
					"type":    "server_error",
				},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", errPayload)
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		// Kirim chunk penutup dengan finish_reason
		finishReason := "stop"
		finalModel := targetModel
		if resp != nil && resp.Model != "" {
			finalModel = resp.Model
		}

		finalChunk := OpenAIChatStreamChunk{
			ID:      completionID,
			Object:  "chat.completion.chunk",
			Created: createdTime,
			Model:   finalModel,
			Choices: []OpenAIChatStreamChoice{
				{
					Index:        0,
					Delta:        OpenAIChatStreamDelta{},
					FinishReason: &finishReason,
				},
			},
		}

		finalBytes, _ := json.Marshal(finalChunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", finalBytes)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	// 2. NON-STREAMING MODE (JSON Response)
	resp, err := pMgr.GenerateWithFallback(ctx, "", chatReq)
	if err != nil {
		log.Printf("⚠️ [OpenAI API] Error chat completion: %v", err)
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	finalModel := targetModel
	if resp != nil && resp.Model != "" {
		finalModel = resp.Model
	}

	content := ""
	promptTokens := 0
	completionTokens := 0
	totalTokens := 0
	if resp != nil {
		content = resp.Content
		promptTokens = resp.PromptTokens
		completionTokens = resp.CompletionTokens
		totalTokens = resp.TotalTokens
		if totalTokens == 0 {
			totalTokens = promptTokens + completionTokens
		}
	}

	openAIResp := OpenAIChatResponse{
		ID:      completionID,
		Object:  "chat.completion",
		Created: createdTime,
		Model:   finalModel,
		Choices: []OpenAIChoice{
			{
				Index: 0,
				Message: OpenAIChoiceMessage{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
		Usage: OpenAIUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openAIResp)
}

func writeOpenAIError(w http.ResponseWriter, statusCode int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
			"code":    statusCode,
		},
	})
}
