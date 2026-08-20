package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/memory"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
	"goassistant/internal/tokensaver"
	"goassistant/internal/tools"
)

type Orchestrator struct {
	db              *storage.DB
	sessionManager  *memory.SessionManager
	memoryManager   *memory.Manager
	promptBuilder   *PromptBuilder
	toolRegistry    *tools.Registry
	providerManager *provider.Manager
}

func NewOrchestrator(
	db *storage.DB,
	sm *memory.SessionManager,
	mm *memory.Manager,
	pb *PromptBuilder,
	tr *tools.Registry,
	pm *provider.Manager,
) *Orchestrator {
	return &Orchestrator{
		db:              db,
		sessionManager:  sm,
		memoryManager:   mm,
		promptBuilder:   pb,
		toolRegistry:    tr,
		providerManager: pm,
	}
}

type UserRequest struct {
	ChannelType     string
	ChannelID       string
	ChannelName     string
	ChatID          string
	UserID          string
	UserName        string
	UserPrompt      string
	AttachedFileMB  float64
	PreferredRole   string
	PreferredProv   string
	OnProgress      func(status string)
}

type AgentResponse struct {
	Text             string
	RawText          string
	Footer           string
	ToolsUsed        []string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	TokensSaved      int
	Latency          time.Duration
	ProviderUsed     string
	ModelUsed        string
}

// ProcessMessage handles the complete pipeline of governance, context building, tool execution, and logging
func (o *Orchestrator) ProcessMessage(ctx context.Context, req UserRequest) (*AgentResponse, error) {
	start := time.Now()

	// 1. Resolve Governance Policy (Hierarchical: Chat -> Channel -> Global -> Default)
	policy := o.db.GetResolvedPolicy(req.ChannelID, req.ChatID)

	// 2. Guard: Max Upload File Size
	if req.AttachedFileMB > 0 && req.AttachedFileMB > float64(policy.MaxUploadFileMB) {
		return &AgentResponse{
			Text: fmt.Sprintf("⚠️ File yang diunggah (%.1f MB) melebihi batas maksimal untuk channel/grup ini (%d MB). Silakan unggah file dengan ukuran lebih kecil.", req.AttachedFileMB, policy.MaxUploadFileMB),
		}, nil
	}

	// 3. Get or Create Session
	session, err := o.sessionManager.GetOrCreate(req.ChannelID, req.ChatID, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("gagal inisialisasi sesi percakapan: %w", err)
	}

	// 4. Auto-Compaction Check
	msgCount, _ := o.db.CountSessionMessages(session.ID)
	if policy.AutoCompaction && msgCount >= policy.CompactionThreshold {
		// Pick active provider for compaction
		activeProviders := o.providerManager.ListAll()
		if len(activeProviders) > 0 {
			compactionProv := activeProviders[0]
			summary, compErr := o.sessionManager.SummarizeSession(ctx, session.ID, compactionProv)
			if compErr == nil && summary != "" {
				// Truncate old messages, keep recent half
				keepCount := policy.MaxHistoryTurns / 2
				if keepCount < 4 {
					keepCount = 4
				}
				_ = o.db.TruncateOldMessages(session.ID, keepCount)
				session.Summary = summary
			}
		}
	}

	// 5. Build Memory & System Prompt
	memContext, _ := o.memoryManager.GetContextMemory(req.ChannelID, req.UserID)

	// Resolve active provider and model for prompt context
	activeProvName := req.PreferredProv
	var activeProv provider.Provider
	if activeProvName != "" {
		activeProv, _ = o.providerManager.Get(activeProvName)
	}
	if activeProv == nil {
		allProvs := o.providerManager.ListAll()
		if len(allProvs) > 0 {
			activeProv = allProvs[0]
			activeProvName = activeProv.Name()
		}
	}

	activeModelName := policy.ModelOverride
	if activeModelName == "" && activeProv != nil {
		activeModelName = activeProv.DefaultModel()
	}

	sysPrompt, err := o.promptBuilder.BuildSystemPrompt(PromptContext{
		AgentRole:      req.PreferredRole,
		ChannelType:    req.ChannelType,
		ChannelName:    req.ChannelName,
		UserName:       req.UserName,
		UserID:         req.UserID,
		MemoryContext:  memContext,
		SessionSummary: session.Summary,
		ActiveModel:    activeModelName,
		ActiveProvider: activeProvName,
	})
	if err != nil {
		return nil, fmt.Errorf("gagal membangun prompt: %w", err)
	}

	// 6. Retrieve History
	history, err := o.sessionManager.GetHistory(session.ID, policy.MaxHistoryTurns)
	if err != nil {
		return nil, fmt.Errorf("gagal mengambil riwayat sesi: %w", err)
	}

	// 7. Resolve Allowed Tools Matrix
	perms, _ := o.db.GetChannelToolPerms(req.ChannelID)
	allowedTools := o.toolRegistry.ListAllowed(perms)

	// 8. Prepare Messages for LLM
	var messages []provider.ChatMessage
	messages = append(messages, provider.ChatMessage{
		Role:    provider.RoleSystem,
		Content: sysPrompt,
	})
	messages = append(messages, history...)
	messages = append(messages, provider.ChatMessage{
		Role:    provider.RoleUser,
		Content: req.UserPrompt,
	})

	// 9. Execute with Tool Loop (up to 5 turns)
	var finalContent string
	var allToolsCalled []string
	var totalPromptTokens int
	var totalCompletionTokens int
	var totalTokensUsed int
	var totalTokensSaved int
	var totalCostUSD float64
	var lastModel string
	var lastProviderName string

	maxTurns := 5
	for turn := 0; turn < maxTurns; turn++ {
		// Run Token Saver RTK / Caveman compression pipeline
		compressedMsgs, saverReport := tokensaver.CompressMessages(messages, policy.TokenSaverMode, policy.MaxTokens*4)
		totalTokensSaved += saverReport.TokensSaved

		chatReq := provider.ChatRequest{
			Model:       policy.ModelOverride,
			Messages:    compressedMsgs,
			Tools:       allowedTools,
			Temperature: 0.7,
			MaxTokens:   policy.MaxTokens,
		}

		resp, err := o.providerManager.GenerateWithFallback(ctx, req.PreferredProv, chatReq)
		if err != nil {
			// Log failure to audit
			fullPayloadJSON, _ := json.Marshal(compressedMsgs)
			_ = o.db.InsertAuditLog(&storage.AuditLogRecord{
				ChannelType:        req.ChannelType,
				ChannelID:          req.ChannelID,
				ChatID:             req.ChatID,
				UserID:             req.UserID,
				UserName:           req.UserName,
				Provider:           req.PreferredProv,
				Model:              policy.ModelOverride,
				TokensSaved:        totalTokensSaved,
				LatencyMs:          int(time.Since(start).Milliseconds()),
				ClientRequest:      req.UserPrompt,
				SystemPrompt:       sysPrompt,
				FullRequestPayload: string(fullPayloadJSON),
				Status:             "error",
				ErrorMessage:       err.Error(),
			})
			return nil, err
		}

		totalPromptTokens += resp.PromptTokens
		totalCompletionTokens += resp.CompletionTokens
		totalTokensUsed += resp.TotalTokens
		totalCostUSD += resp.CostUSD
		lastModel = resp.Model
		lastProviderName = resp.ProviderName

		if len(resp.ToolCalls) == 0 {
			// No more tool calls; final response reached
			finalContent = resp.Content
			break
		}

		// Assistant called tools
		assistantMsg := provider.ChatMessage{
			Role:      provider.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistantMsg)

		for _, tc := range resp.ToolCalls {
			allToolsCalled = append(allToolsCalled, tc.Name)
			if req.OnProgress != nil {
				req.OnProgress(fmt.Sprintf("🔍 <i>Sedang menjalankan tool: <b>%s</b>...</i>", tc.Name))
			}
			toolOut, toolErr := o.toolRegistry.Execute(ctx, tc.Name, tc.Arguments)
			if toolErr != nil {
				toolOut = fmt.Sprintf("Error eksekusi tool %s: %v", tc.Name, toolErr)
			}

			// Pre-compress tool output before appending to message history
			compressedToolOut := tokensaver.CompressContent(toolOut, policy.TokenSaverMode)

			toolMsg := provider.ChatMessage{
				Role:       provider.RoleTool,
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    compressedToolOut,
			}
			messages = append(messages, toolMsg)
		}

		if req.OnProgress != nil && len(resp.ToolCalls) > 0 {
			req.OnProgress("🤔 <i>Menganalisis hasil data...</i>")
		}
	}

	if finalContent == "" {
		finalContent = "(Tidak ada respon teks dari model)"
	}

	// 10. Persist User & Assistant Messages (clean content without metadata footer)
	_ = o.sessionManager.AddMessage(session.ID, "user", req.UserPrompt, len(req.UserPrompt)/4)
	_ = o.sessionManager.AddMessage(session.ID, "assistant", finalContent, len(finalContent)/4)

	// 11. Audit Logging
	fullPayloadJSON, _ := json.Marshal(messages)
	toolsJSON, _ := json.Marshal(allToolsCalled)
	latency := time.Since(start)
	_ = o.db.InsertAuditLog(&storage.AuditLogRecord{
		ChannelType:        req.ChannelType,
		ChannelID:          req.ChannelID,
		ChatID:             req.ChatID,
		UserID:             req.UserID,
		UserName:           req.UserName,
		Provider:           lastProviderName,
		Model:              lastModel,
		PromptTokens:       totalPromptTokens,
		CompletionTokens:   totalCompletionTokens,
		TotalTokens:        totalTokensUsed,
		TokensSaved:        totalTokensSaved,
		LatencyMs:          int(latency.Milliseconds()),
		CostUSD:            totalCostUSD,
		ToolsCalled:        string(toolsJSON),
		ClientRequest:      req.UserPrompt,
		SystemPrompt:       sysPrompt,
		FullRequestPayload: string(fullPayloadJSON),
		ProviderResponse:   finalContent,
		Status:             "success",
	})

	// 12. Format footer according to policy
	cleanText := strings.TrimSpace(finalContent)
	footer := FormatFooter(policy.FooterMode, totalPromptTokens, totalCompletionTokens, totalTokensUsed, totalTokensSaved, latency, lastModel, lastProviderName, allToolsCalled)
	finalText := cleanText
	if footer != "" {
		finalText = finalText + "\n\n" + footer
	}

	return &AgentResponse{
		Text:             finalText,
		RawText:          cleanText,
		Footer:           footer,
		ToolsUsed:        allToolsCalled,
		PromptTokens:     totalPromptTokens,
		CompletionTokens: totalCompletionTokens,
		TotalTokens:      totalTokensUsed,
		TokensSaved:      totalTokensSaved,
		Latency:          latency,
		ProviderUsed:     lastProviderName,
		ModelUsed:        lastModel,
	}, nil
}

// FormatFooter formats the footer according to policy mode ('off', 'tokens', 'full')
func FormatFooter(mode string, promptTokens, completionTokens, totalTokens, tokensSaved int, latency time.Duration, model, providerName string, toolsCalled []string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "tokens":
		if totalTokens <= 0 {
			return ""
		}
		if tokensSaved > 0 {
			return fmt.Sprintf("🪙 %s tokens (🌿 hemat: %s)", formatThousands(totalTokens), formatThousands(tokensSaved))
		}
		return fmt.Sprintf("🪙 %s tokens", formatThousands(totalTokens))

	case "full":
		var parts []string

		// Latency
		ms := latency.Milliseconds()
		if ms < 1000 {
			parts = append(parts, fmt.Sprintf("⚡ %dms", ms))
		} else {
			parts = append(parts, fmt.Sprintf("⚡ %.1fs", float64(ms)/1000.0))
		}

		// Tokens
		if totalTokens > 0 {
			if promptTokens > 0 && completionTokens > 0 {
				tokStr := fmt.Sprintf("🪙 %s (in: %s / out: %s)", formatThousands(totalTokens), formatThousands(promptTokens), formatThousands(completionTokens))
				if tokensSaved > 0 {
					tokStr += fmt.Sprintf(" • 🌿 hemat %s", formatThousands(tokensSaved))
				}
				parts = append(parts, tokStr)
			} else {
				tokStr := fmt.Sprintf("🪙 %s tokens", formatThousands(totalTokens))
				if tokensSaved > 0 {
					tokStr += fmt.Sprintf(" • 🌿 hemat %s", formatThousands(tokensSaved))
				}
				parts = append(parts, tokStr)
			}
		}

		// Tools
		if len(toolsCalled) > 0 {
			parts = append(parts, fmt.Sprintf("🔧 %d tools", len(toolsCalled)))
		}

		// Model / Provider
		if model != "" {
			parts = append(parts, fmt.Sprintf("🤖 %s", model))
		} else if providerName != "" {
			parts = append(parts, fmt.Sprintf("🤖 %s", providerName))
		}

		if len(parts) == 0 {
			return ""
		}

		return "—\n" + strings.Join(parts, " • ")

	default: // "off", "none", "", etc.
		return ""
	}
}

func formatThousands(n int) string {
	in := fmt.Sprintf("%d", n)
	out := make([]byte, len(in)+(len(in)-1)/3)
	for i, j, k := len(in)-1, len(out)-1, 0; i >= 0; i, j = i-1, j-1 {
		out[j] = in[i]
		k++
		if k%3 == 0 && i > 0 {
			j--
			out[j] = ','
		}
	}
	return string(out)
}
