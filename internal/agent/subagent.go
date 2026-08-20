package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/provider"
	"goassistant/internal/tokensaver"
	"goassistant/internal/tools"
)

// SubagentTool implements tools.Tool for delegating subtasks to specialized subagents
type SubagentTool struct {
	promptBuilder   *PromptBuilder
	toolRegistry    *tools.Registry
	providerManager *provider.Manager
}

// NewSubagentTool creates a new subagent delegation tool
func NewSubagentTool(pb *PromptBuilder, tr *tools.Registry, pm *provider.Manager) *SubagentTool {
	return &SubagentTool{
		promptBuilder:   pb,
		toolRegistry:    tr,
		providerManager: pm,
	}
}

func (s *SubagentTool) Name() string {
	return "delegate_task"
}

func (s *SubagentTool) Description() string {
	return "Mendelegasikan sub-tugas spesifik kepada sub-agen spesialis (misal: 'coder', 'researcher', 'secretary', 'analyst') dengan konteks terisolasi dan fokus. Cocok untuk memecah masalah besar, riset terpisah, analisis data, atau penulisan kode tanpa membebani memori agen utama."
}

func (s *SubagentTool) Parameters() tools.ParametersSchema {
	return tools.ParametersSchema{
		Type: "object",
		Properties: map[string]tools.ParameterProperty{
			"role": {
				Type:        "string",
				Description: "Peran sub-agen spesialis (contoh: 'coder', 'researcher', 'secretary', 'analyst', atau peran kustom lainnya).",
			},
			"instruction": {
				Type:        "string",
				Description: "Instruksi tugas yang spesifik, terarah, dan jelas untuk diselesaikan oleh sub-agen.",
			},
			"context_data": {
				Type:        "string",
				Description: "Potongan data, dokumen, atau konteks relevan yang dibutuhkan sub-agen untuk menyelesaikan tugas (hanya yang diperlukan).",
			},
			"model": {
				Type:        "string",
				Description: "Nama model AI spesifik jika ingin mengarahkan sub-tugas ke model tertentu (opsional).",
			},
		},
		Required: []string{"role", "instruction"},
	}
}

func (s *SubagentTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	role, _ := args["role"].(string)
	if strings.TrimSpace(role) == "" {
		role = "general"
	}
	role = strings.ToLower(strings.TrimSpace(role))

	instruction, _ := args["instruction"].(string)
	if strings.TrimSpace(instruction) == "" {
		return "", fmt.Errorf("argumen 'instruction' wajib diisi untuk delegasi sub-agen")
	}

	contextData, _ := args["context_data"].(string)
	modelOverride, _ := args["model"].(string)

	// 1. Build Isolated Subagent System Prompt
	sysPrompt, err := s.promptBuilder.BuildSubagentPrompt(role)
	if err != nil || sysPrompt == "" {
		sysPrompt = fmt.Sprintf("Kamu adalah Sub-Agen Ahli dengan spesialisasi peran: %s.\nFokus hanya pada instruksi tugas yang didelegasikan kepadamu secara ringkas, akurat, dan langsung pada inti tugas.", role)
	}

	// 2. Prepare subagent user prompt with isolated context
	var userPromptBuilder strings.Builder
	userPromptBuilder.WriteString(fmt.Sprintf("### TUGAS SUB-AGEN (@%s):\n%s\n", role, instruction))
	if strings.TrimSpace(contextData) != "" {
		userPromptBuilder.WriteString("\n### KONTEKS TERISOLASI:\n")
		userPromptBuilder.WriteString(strings.TrimSpace(contextData))
		userPromptBuilder.WriteString("\n")
	}

	messages := []provider.ChatMessage{
		{
			Role:    provider.RoleSystem,
			Content: sysPrompt,
		},
		{
			Role:    provider.RoleUser,
			Content: userPromptBuilder.String(),
		},
	}

	// 3. Resolve tools for subagent (prevent recursive delegate_task to avoid infinite loops)
	subagentAllowedMap := map[string]bool{
		"delegate_task": false, // Disallow nested subagent delegation
	}
	allowedTools := s.toolRegistry.ListAllowed(subagentAllowedMap)

	// 4. Execute subagent inference loop (up to 3 turns)
	maxTurns := 3
	var finalOutput string

	// Create child context with timeout if not present
	subCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	for turn := 0; turn < maxTurns; turn++ {
		compressedMsgs, _ := tokensaver.CompressMessages(messages, "auto", 4000)

		chatReq := provider.ChatRequest{
			Model:       modelOverride,
			Messages:    compressedMsgs,
			Tools:       allowedTools,
			Temperature: 0.5,
			MaxTokens:   2048,
		}

		resp, err := s.providerManager.GenerateWithFallback(subCtx, "", chatReq)
		if err != nil {
			return "", fmt.Errorf("sub-agen @%s gagal mengeksekusi tugas: %w", role, err)
		}

		if len(resp.ToolCalls) == 0 {
			finalOutput = resp.Content
			break
		}

		// Handle subagent tool calls
		assistantMsg := provider.ChatMessage{
			Role:      provider.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		messages = append(messages, assistantMsg)

		for _, tc := range resp.ToolCalls {
			if tc.Name == "delegate_task" {
				continue // Guard
			}
			toolOut, toolErr := s.toolRegistry.Execute(subCtx, tc.Name, tc.Arguments)
			if toolErr != nil {
				toolOut = fmt.Sprintf("Error tool %s: %v", tc.Name, toolErr)
			}
			toolMsg := provider.ChatMessage{
				Role:       provider.RoleTool,
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    toolOut,
			}
			messages = append(messages, toolMsg)
		}
	}

	if finalOutput == "" {
		finalOutput = "(Sub-agen selesai tanpa menghasilkan teks)"
	}

	return fmt.Sprintf("=== [HASIL SUB-AGEN @%s] ===\n%s", role, finalOutput), nil
}
