package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/config"
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
	commandRouter   *CommandRouter
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
		commandRouter:   NewCommandRouter(GetGlobalResponseCache()),
	}
}

type UserRequest struct {
	ChannelType    string
	ChannelID      string
	ChannelName    string
	ChatID         string
	UserID         string
	UserName       string
	UserPrompt     string
	AttachedFileMB float64
	PreferredRole  string
	PreferredProv  string
	OnProgress     func(status string)
}

type MediaAttachment struct {
	FilePath string
	Caption  string
}

type AgentResponse struct {
	Text             string
	RawText          string
	ThinkingContent  string // Thinking/reasoning output from model
	Footer           string
	MediaFiles       []MediaAttachment
	ToolsUsed        []string
	PromptTokens     int
	CompletionTokens int
	ThinkingTokens   int
	TotalTokens      int
	TokensSaved      int
	Latency          time.Duration
	ProviderUsed     string
	ModelUsed        string
}

// ProcessMessage handles the complete pipeline of governance, context building, tool execution, and logging
func (o *Orchestrator) ProcessMessage(ctx context.Context, req UserRequest) (*AgentResponse, error) {
	start := time.Now()

	// 0. Handle Local Deterministic Commands (0 Token, Instant)
	if localResp, handled := o.commandRouter.TryHandleLocal(ctx, req); handled {
		return localResp, nil
	}

	// 1. Resolve Governance Policy (Hierarchical: Chat -> Channel -> Global -> Default)
	policy := o.db.GetResolvedPolicy(req.ChannelID, req.ChatID)

	// 2. Guard: Max Upload File Size
	if req.AttachedFileMB > 0 && req.AttachedFileMB > float64(policy.MaxUploadFileMB) {
		return &AgentResponse{
			Text: fmt.Sprintf("⚠️ File yang diunggah (%.1f MB) melebihi batas maksimal untuk channel/grup ini (%d MB). Silakan unggah file dengan ukuran lebih kecil.", req.AttachedFileMB, policy.MaxUploadFileMB),
		}, nil
	}

	// 3. Resolve Active Provider & Model Early
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

	// 4. Exact Response Cache Check (0 Token, Instant Delivery)
	if policy.ResponseCacheEnabled && req.AttachedFileMB <= 0 {
		if cachedResp, hit := GetGlobalResponseCache().Get(req.ChannelID, activeModelName, req.UserPrompt); hit {
			return cachedResp, nil
		}
	}

	// 5. Get or Create Session
	session, err := o.sessionManager.GetOrCreate(req.ChannelID, req.ChatID, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("gagal inisialisasi sesi percakapan: %w", err)
	}

	// 6. Auto-Compaction Check
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

	// 7. Build Memory & System Prompt
	memContext, _ := o.memoryManager.GetContextMemory(req.ChannelID, req.UserID)

	sysPrompt, err := o.promptBuilder.BuildSystemPrompt(PromptContext{
		ChannelID:      req.ChannelID,
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
	var finalThinking string
	var allToolsCalled []string
	var totalPromptTokens int
	var totalCompletionTokens int
	var totalThinkingTokens int
	var totalTokensUsed int
	var totalTokensSaved int
	var totalCostUSD float64
	var lastModel string
	var lastProviderName string

	var mediaFiles []MediaAttachment

	extractAttachments := func(s string) {
		for {
			idx := strings.Index(s, "[ATTACH_FILE:")
			if idx == -1 {
				break
			}
			endIdx := strings.Index(s[idx:], "]")
			if endIdx == -1 {
				break
			}
			tag := s[idx+len("[ATTACH_FILE:") : idx+endIdx]
			parts := strings.SplitN(tag, "|CAPTION:", 2)
			fPath := strings.TrimSpace(parts[0])
			capText := ""
			if len(parts) == 2 {
				capText = strings.TrimSpace(parts[1])
			}
			if fPath != "" {
				mediaFiles = append(mediaFiles, MediaAttachment{FilePath: fPath, Caption: capText})
			}
			s = s[idx+endIdx+1:]
		}
	}

	maxTurns := 8
	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Run Token Saver RTK / Caveman compression pipeline
		compressedMsgs, saverReport := tokensaver.CompressMessages(messages, policy.TokenSaverMode, policy.MaxTokens*4)
		totalTokensSaved += saverReport.TokensSaved

		chatReq := provider.ChatRequest{
			Model:           policy.ModelOverride,
			Messages:        compressedMsgs,
			Tools:           allowedTools,
			Temperature:     0.7,
			MaxTokens:       policy.MaxTokens,
			ThinkingEnabled: policy.ThinkingEnabled,
			OnProgress:      req.OnProgress,
		}

		resp, err := o.providerManager.GenerateWithFallback(ctx, req.PreferredProv, chatReq)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Check if error is timeout, context length exceeded, or network issue with long context
			errStr := strings.ToLower(err.Error())
			isContextOrTimeout := strings.Contains(errStr, "context deadline exceeded") ||
				strings.Contains(errStr, "timeout") ||
				strings.Contains(errStr, "context_length_exceeded") ||
				strings.Contains(errStr, "maximum context length") ||
				strings.Contains(errStr, "token limit") ||
				strings.Contains(errStr, "rate limit")

			// If error happened and we had history turns in the request, try auto-compacting and retrying once with minimal context
			if isContextOrTimeout && len(history) > 0 {
				if req.OnProgress != nil {
					req.OnProgress("🧹 <i>Konteks percakapan penuh/timeout, merampingkan riwayat & mencoba ulang...</i>")
				}

				// Auto clean old session messages in DB (keep only the very latest 2 messages)
				_ = o.db.TruncateOldMessages(session.ID, 2)
				history = nil // Clear history from memory for this attempt

				// Rebuild clean messages: system prompt + user prompt
				messages = []provider.ChatMessage{
					{
						Role:    provider.RoleSystem,
						Content: sysPrompt,
					},
					{
						Role:    provider.RoleUser,
						Content: req.UserPrompt,
					},
				}

				retryCompressedMsgs, retrySaverReport := tokensaver.CompressMessages(messages, "aggressive", policy.MaxTokens*2)
				totalTokensSaved += retrySaverReport.TokensSaved

				retryChatReq := provider.ChatRequest{
					Model:       policy.ModelOverride,
					Messages:    retryCompressedMsgs,
					Tools:       allowedTools,
					Temperature: 0.7,
					MaxTokens:   policy.MaxTokens,
					OnProgress:  req.OnProgress,
				}

				// Fresh 2-minute context if original ctx was timed out
				retryCtx, cancelRetry := context.WithTimeout(context.Background(),
					time.Duration(config.Get().Timeouts.RetrySeconds)*time.Second)
				resp, err = o.providerManager.GenerateWithFallback(retryCtx, req.PreferredProv, retryChatReq)
				cancelRetry()
			}

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
		}

		totalPromptTokens += resp.PromptTokens
		totalCompletionTokens += resp.CompletionTokens
		totalThinkingTokens += resp.ThinkingTokens
		totalTokensUsed += resp.TotalTokens
		totalTokensSaved += resp.CacheReadTokens
		totalCostUSD += resp.CostUSD
		lastModel = resp.Model
		lastProviderName = resp.ProviderName
		if resp.Thinking != "" {
			finalThinking = resp.Thinking
		}

		if len(resp.ToolCalls) == 0 {
			// No more tool calls; final response reached
			finalContent = resp.Content
			if resp.Thinking != "" {
				finalThinking = resp.Thinking
			}
			extractAttachments(finalContent)

			// If the model produced empty text response (e.g. only thinking or finished tools without text),
			// attempt an auto-continue prompt to prompt the model for its final text answer
			if strings.TrimSpace(finalContent) == "" && strings.TrimSpace(finalThinking) == "" && turn < maxTurns-1 {
				assistantContent := resp.Content
				if assistantContent == "" {
					assistantContent = "..."
				}
				messages = append(messages, provider.ChatMessage{
					Role:    provider.RoleAssistant,
					Content: assistantContent,
				})
				messages = append(messages, provider.ChatMessage{
					Role:    provider.RoleUser,
					Content: "Lanjutkan dan berikan jawaban teks akhirmu berdasarkan analisis atau pemikiran di atas secara lengkap dan jelas.",
				})
				if req.OnProgress != nil {
					req.OnProgress("✍️ <i>Menyusun respon akhir...</i>")
				}
				continue
			}
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

			extractAttachments(toolOut)

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

	// Jika loop tool selesai tapi teks respon masih kosong (misal model hanya menjalankan tools beruntun),
	// lakukan final synthesis call tanpa tools agar model merumuskan jawaban lengkap
	if strings.TrimSpace(finalContent) == "" {
		if req.OnProgress != nil {
			req.OnProgress("✍️ <i>Menyusun kesimpulan & analisis akhir...</i>")
		}

		synthMsgs := append([]provider.ChatMessage{}, messages...)
		synthMsgs = append(synthMsgs, provider.ChatMessage{
			Role:    provider.RoleUser,
			Content: "Berdasarkan seluruh hasil dan data yang telah diperoleh dari eksekusi tool di atas, berikan kesimpulan analisis lengkap, terstruktur, dan jawaban akhirmu sekarang secara jelas dalam format teks.",
		})

		synthCompressedMsgs, synthSaverReport := tokensaver.CompressMessages(synthMsgs, policy.TokenSaverMode, policy.MaxTokens*4)
		totalTokensSaved += synthSaverReport.TokensSaved

		synthReq := provider.ChatRequest{
			Model:           policy.ModelOverride,
			Messages:        synthCompressedMsgs,
			Tools:           nil, // Paksa hasil berupa teks (tanpa pemanggilan tool lagi)
			Temperature:     0.7,
			MaxTokens:       policy.MaxTokens,
			ThinkingEnabled: policy.ThinkingEnabled,
			OnProgress:      req.OnProgress,
		}

		synthResp, synthErr := o.providerManager.GenerateWithFallback(ctx, req.PreferredProv, synthReq)
		if synthErr == nil && synthResp != nil {
			if strings.TrimSpace(synthResp.Content) != "" {
				finalContent = synthResp.Content
			} else if strings.TrimSpace(synthResp.Thinking) != "" {
				finalContent = synthResp.Thinking
			}
			if synthResp.Thinking != "" {
				finalThinking = synthResp.Thinking
			}
			totalPromptTokens += synthResp.PromptTokens
			totalCompletionTokens += synthResp.CompletionTokens
			totalThinkingTokens += synthResp.ThinkingTokens
			totalTokensUsed += synthResp.TotalTokens
			totalTokensSaved += synthResp.CacheReadTokens
			totalCostUSD += synthResp.CostUSD
			lastModel = synthResp.Model
			lastProviderName = synthResp.ProviderName
			extractAttachments(finalContent)
		}
	}

	if strings.TrimSpace(finalContent) == "" {
		if strings.TrimSpace(finalThinking) != "" {
			// Fallback: use thinking/reasoning content as the main text
			finalContent = strings.TrimSpace(finalThinking)
		} else {
			finalContent = "(Tidak ada respon teks dari model)"
		}
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
	footer := FormatFooter(policy.FooterMode, totalPromptTokens, totalCompletionTokens, totalThinkingTokens, totalTokensUsed, totalTokensSaved, latency, lastModel, lastProviderName, allToolsCalled)
	finalText := cleanText

	// Prepend thinking content if enabled and available (and not already used as main content fallback)
	if finalThinking != "" && policy.ThinkingEnabled && cleanText != strings.TrimSpace(finalThinking) {
		switch strings.ToLower(policy.ThinkingDisplay) {
		case "full":
			finalText = "💭 <b>Proses Berpikir:</b>\n<blockquote>" + strings.TrimSpace(finalThinking) + "</blockquote>\n\n" + finalText
		case "summary":
			// Truncate thinking to first 500 chars
			thinkPreview := strings.TrimSpace(finalThinking)
			if len(thinkPreview) > 500 {
				thinkPreview = thinkPreview[:500] + "..."
			}
			finalText = "💭 <i>" + thinkPreview + "</i>\n\n" + finalText
			// "hidden" — don't show thinking
		}
	}

	if footer != "" {
		finalText = finalText + "\n\n" + footer
	}

	agentResp := &AgentResponse{
		Text:             finalText,
		RawText:          cleanText,
		ThinkingContent:  finalThinking,
		Footer:           footer,
		MediaFiles:       mediaFiles,
		ToolsUsed:        allToolsCalled,
		PromptTokens:     totalPromptTokens,
		CompletionTokens: totalCompletionTokens,
		ThinkingTokens:   totalThinkingTokens,
		TotalTokens:      totalTokensUsed,
		TokensSaved:      totalTokensSaved,
		Latency:          latency,
		ProviderUsed:     lastProviderName,
		ModelUsed:        lastModel,
	}

	// 13. Save to Exact Response Cache if enabled, no tools called, and no files
	if policy.ResponseCacheEnabled && len(allToolsCalled) == 0 && len(mediaFiles) == 0 {
		ttl := time.Duration(policy.ResponseCacheTTLSec) * time.Second
		GetGlobalResponseCache().Set(req.ChannelID, activeModelName, req.UserPrompt, agentResp, ttl)
	}

	return agentResp, nil
}

// FormatFooter formats the footer according to policy mode ('off', 'tokens', 'full')
func FormatFooter(mode string, promptTokens, completionTokens, thinkingTokens, totalTokens, tokensSaved int, latency time.Duration, model, providerName string, toolsCalled []string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "tokens":
		tokDisplay := totalTokens
		if tokDisplay <= 0 {
			tokDisplay = promptTokens + completionTokens
		}
		var result string
		if tokDisplay > 0 {
			result = fmt.Sprintf("🪙 %s tokens", formatThousands(tokDisplay))
		} else {
			result = "🪙 (tokens recorded)"
		}
		if thinkingTokens > 0 {
			result += fmt.Sprintf(" (💭 %s)", formatThousands(thinkingTokens))
		}
		if tokensSaved > 0 {
			result += fmt.Sprintf(" • 🌿 hemat: %s", formatThousands(tokensSaved))
		}
		return "—\n" + result

	case "full":
		var parts []string

		// Latency
		ms := latency.Milliseconds()
		if ms > 0 {
			if ms < 1000 {
				parts = append(parts, fmt.Sprintf("⚡ %dms", ms))
			} else {
				parts = append(parts, fmt.Sprintf("⚡ %.1fs", float64(ms)/1000.0))
			}
		}

		// Tokens
		tokDisplay := totalTokens
		if tokDisplay <= 0 {
			tokDisplay = promptTokens + completionTokens
		}
		if tokDisplay > 0 {
			if promptTokens > 0 && completionTokens > 0 {
				tokStr := fmt.Sprintf("🪙 %s (in: %s / out: %s)", formatThousands(tokDisplay), formatThousands(promptTokens), formatThousands(completionTokens))
				if thinkingTokens > 0 {
					tokStr += fmt.Sprintf(" 💭 %s", formatThousands(thinkingTokens))
				}
				if tokensSaved > 0 {
					tokStr += fmt.Sprintf(" • 🌿 hemat %s", formatThousands(tokensSaved))
				}
				parts = append(parts, tokStr)
			} else {
				tokStr := fmt.Sprintf("🪙 %s tokens", formatThousands(tokDisplay))
				if tokensSaved > 0 {
					tokStr += fmt.Sprintf(" • 🌿 hemat %s", formatThousands(tokensSaved))
				}
				parts = append(parts, tokStr)
			}
		} else if tokensSaved > 0 {
			parts = append(parts, fmt.Sprintf("🌿 hemat %s tokens", formatThousands(tokensSaved)))
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
			return "—\n⚡ respons selesai"
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

// FormatUserFriendlyError converts internal technical error traces into clean, polite messages for end-users
func FormatUserFriendlyError(err error) string {
	if err == nil {
		return ""
	}
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "context canceled") || strings.Contains(errStr, "canceled"):
		return "🛑 **Proses Dibatalkan**\nEksekusi permintaan atau proses telah dihentikan oleh pengguna."
	case strings.Contains(errStr, "context deadline exceeded") || strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline"):
		return "⏳ **Waktu Tunggu Habis (Timeout)**\nServer AI membutuhkan waktu terlalu lama untuk memproses (beban server tinggi atau konteks terlalu panjang). Riwayat percakapan telah disederhanakan secara otomatis. Silakan coba kirim ulang pertanyaan Anda, atau gunakan `/reset` jika kendala berlanjut."
	case strings.Contains(errStr, "context_length_exceeded") || strings.Contains(errStr, "maximum context length") || strings.Contains(errStr, "token limit"):
		return "📏 **Batas Konteks Terlampaui**\nRiwayat percakapan melebihi kapasitas memori model AI. Riwayat telah dibersihkan otomatis. Silakan kirim ulang pertanyaan Anda."
	case strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") || strings.Contains(errStr, "quota"):
		return "⚠️ **Batas Kuota / Rate Limit**\nLayanan AI sedang mencapai batas frekuensi panggilan atau kuota provider telah habis. Silakan coba beberapa saat lagi atau hubungi admin."
	case strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no such host") || strings.Contains(errStr, "dial tcp"):
		return "🔌 **Koneksi Terputus**\nGagal terhubung ke endpoint server AI. Mohon periksa koneksi jaringan atau coba beberapa saat lagi."
	case strings.Contains(errStr, "seluruh target gagal"):
		return "❌ **Layanan AI Sedang Gangguan**\nTarget provider/model AI saat ini tidak dapat merespons. Silakan coba lagi nanti atau hubungi admin."
	default:
		return "❌ **Maaf, terjadi kendala teknis pada layanan AI.**\nSilakan coba lagi beberapa saat lagi atau gunakan `/reset` untuk memulai percakapan baru."
	}
}
