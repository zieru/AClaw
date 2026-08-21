package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/provider"
	"goassistant/internal/tokensaver"
	"goassistant/internal/tools"
)

// SubTask represents a single task to be delegated to a sub-agent
type SubTask struct {
	Role        string `json:"role"`
	Instruction string `json:"instruction"`
	ContextData string `json:"context_data,omitempty"`
}

// SubTaskResult holds the result from a single sub-agent execution
type SubTaskResult struct {
	Role       string        `json:"role"`
	Output     string        `json:"output"`
	Error      string        `json:"error,omitempty"`
	Tokens     int           `json:"tokens"`
	Latency    time.Duration `json:"latency"`
	Success    bool          `json:"success"`
}

// SubagentTool implements tools.Tool for delegating subtasks to specialized subagents.
// Supports both single-task and parallel multi-task delegation for token efficiency.
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
	return `Mendelegasikan sub-tugas ke sub-agen spesialis dengan konteks terisolasi.

SINGLE TASK: Kirim satu tugas dengan "role" dan "instruction".
PARALLEL MULTI-TASK: Kirim array "tasks" berisi beberapa sub-tugas sekaligus.

Sub-agen spesialis efisien karena: konteks terisolasi (hemat token), bisa berjalan paralel, dan masing-masing fokus pada satu aspek.

Gunakan untuk: analisis data multi-aspek, riset terpisah, penulisan kode, review, atau tugas apapun yang bisa dipecah.

Contoh peran: 'coder', 'researcher', 'analyst', 'secretary', 'reviewer', 'writer', 'translator'.`
}

func (s *SubagentTool) Parameters() tools.ParametersSchema {
	return tools.ParametersSchema{
		Type: "object",
		Properties: map[string]tools.ParameterProperty{
			"role": {
				Type:        "string",
				Description: "Peran sub-agen untuk single task (contoh: 'coder', 'researcher', 'analyst').",
			},
			"instruction": {
				Type:        "string",
				Description: "Instruksi tugas spesifik untuk single task.",
			},
			"context_data": {
				Type:        "string",
				Description: "Data/konteks relevan yang dibutuhkan sub-agen (hanya yang diperlukan).",
			},
			"model": {
				Type:        "string",
				Description: "Model AI spesifik untuk sub-tugas (opsional).",
			},
			"tasks": {
				Type:        "string",
				Description: "JSON array sub-tugas untuk parallel execution. Format: [{\"role\":\"analyst\",\"instruction\":\"...\",\"context_data\":\"...\"},...]. Jika diisi, 'role' dan 'instruction' di atas diabaikan.",
			},
		},
		Required: []string{},
	}
}

func (s *SubagentTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	modelOverride, _ := args["model"].(string)

	// Check if this is a parallel multi-task request
	tasksJSON, _ := args["tasks"].(string)
	if strings.TrimSpace(tasksJSON) != "" {
		return s.executeParallel(ctx, tasksJSON, modelOverride)
	}

	// Single task execution
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

	result := s.executeSingleTask(ctx, SubTask{
		Role:        role,
		Instruction: instruction,
		ContextData: contextData,
	}, modelOverride)

	if !result.Success {
		return "", fmt.Errorf("sub-agen @%s gagal: %s", role, result.Error)
	}

	return fmt.Sprintf("=== [HASIL SUB-AGEN @%s] ===\n%s", role, result.Output), nil
}

// executeParallel runs multiple sub-tasks concurrently with semaphore control
func (s *SubagentTool) executeParallel(ctx context.Context, tasksJSON string, modelOverride string) (string, error) {
	// Parse tasks array
	var tasks []SubTask

	// Try JSON parsing
	tasksJSON = strings.TrimSpace(tasksJSON)
	if err := parseJSONTasks(tasksJSON, &tasks); err != nil {
		return "", fmt.Errorf("gagal parsing tasks JSON: %w", err)
	}

	if len(tasks) == 0 {
		return "", fmt.Errorf("array 'tasks' kosong")
	}

	// Get concurrency limit from config
	cfg := config.Get()
	maxParallel := 3
	if cfg != nil && cfg.SubAgent.MaxParallel > 0 {
		maxParallel = cfg.SubAgent.MaxParallel
	}
	if maxParallel > len(tasks) {
		maxParallel = len(tasks)
	}

	// Create semaphore for concurrency control
	sem := make(chan struct{}, maxParallel)
	results := make([]SubTaskResult, len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t SubTask) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			results[idx] = s.executeSingleTask(ctx, t, modelOverride)
		}(i, task)
	}

	wg.Wait()

	// Aggregate results
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== [HASIL %d SUB-AGEN PARALEL] ===\n\n", len(tasks)))

	totalTokens := 0
	successCount := 0
	for i, result := range results {
		sb.WriteString(fmt.Sprintf("--- Sub-Agen #%d @%s ---\n", i+1, tasks[i].Role))
		if result.Success {
			successCount++
			sb.WriteString(result.Output)
		} else {
			sb.WriteString(fmt.Sprintf("❌ Error: %s", result.Error))
		}
		totalTokens += result.Tokens
		sb.WriteString(fmt.Sprintf("\n[⚡ %s | 🪙 ~%d tokens]\n\n", result.Latency.Round(time.Millisecond), result.Tokens))
	}

	sb.WriteString(fmt.Sprintf("=== [RINGKASAN: %d/%d berhasil | ~%d total tokens] ===", successCount, len(tasks), totalTokens))

	return sb.String(), nil
}

// executeSingleTask runs one sub-agent task with isolated context
func (s *SubagentTool) executeSingleTask(ctx context.Context, task SubTask, modelOverride string) SubTaskResult {
	start := time.Now()
	role := task.Role
	if role == "" {
		role = "general"
	}

	// Get timeout from config
	cfg := config.Get()
	timeout := 90 * time.Second
	tokenBudget := 2048
	if cfg != nil {
		if cfg.SubAgent.TimeoutSeconds > 0 {
			timeout = time.Duration(cfg.SubAgent.TimeoutSeconds) * time.Second
		}
		if cfg.SubAgent.TokenBudgetPerTask > 0 {
			tokenBudget = cfg.SubAgent.TokenBudgetPerTask
		}
	}

	// 1. Build Isolated Subagent System Prompt
	sysPrompt, err := s.promptBuilder.BuildSubagentPrompt(role)
	if err != nil || sysPrompt == "" {
		sysPrompt = fmt.Sprintf("Kamu adalah Sub-Agen Ahli dengan spesialisasi peran: %s.\nFokus hanya pada instruksi tugas yang didelegasikan kepadamu secara ringkas, akurat, dan langsung pada inti tugas.", role)
	}

	// 2. Prepare subagent user prompt with isolated context
	var userPromptBuilder strings.Builder
	userPromptBuilder.WriteString(fmt.Sprintf("### TUGAS SUB-AGEN (@%s):\n%s\n", role, task.Instruction))
	if strings.TrimSpace(task.ContextData) != "" {
		userPromptBuilder.WriteString("\n### KONTEKS TERISOLASI:\n")
		userPromptBuilder.WriteString(strings.TrimSpace(task.ContextData))
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

	// 3. Resolve tools for subagent (prevent recursive delegate_task)
	subagentAllowedMap := map[string]bool{
		"delegate_task": false, // Disallow nested subagent delegation
	}
	allowedTools := s.toolRegistry.ListAllowed(subagentAllowedMap)

	// 4. Execute subagent inference loop (up to 3 turns)
	maxTurns := 3
	var finalOutput string
	totalTokens := 0

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for turn := 0; turn < maxTurns; turn++ {
		compressedMsgs, _ := tokensaver.CompressMessages(messages, "auto", tokenBudget*4)

		chatReq := provider.ChatRequest{
			Model:       modelOverride,
			Messages:    compressedMsgs,
			Tools:       allowedTools,
			Temperature: 0.5,
			MaxTokens:   tokenBudget,
		}

		resp, err := s.providerManager.GenerateWithFallback(subCtx, "", chatReq)
		if err != nil {
			return SubTaskResult{
				Role:    role,
				Error:   fmt.Sprintf("gagal: %v", err),
				Tokens:  totalTokens,
				Latency: time.Since(start),
				Success: false,
			}
		}

		totalTokens += resp.TotalTokens

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
				continue // Guard against recursion
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

	return SubTaskResult{
		Role:    role,
		Output:  finalOutput,
		Tokens:  totalTokens,
		Latency: time.Since(start),
		Success: true,
	}
}

// parseJSONTasks parses JSON array of tasks with error handling
func parseJSONTasks(jsonStr string, tasks *[]SubTask) error {
	jsonStr = strings.TrimSpace(jsonStr)

	// Try direct JSON array parsing
	if err := json.Unmarshal([]byte(jsonStr), tasks); err == nil && len(*tasks) > 0 {
		return nil
	}

	return fmt.Errorf("format tasks tidak valid: harus JSON array [{\"role\":\"...\",\"instruction\":\"...\"}]")
}
