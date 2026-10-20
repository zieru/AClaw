package tokensaver

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"goassistant/internal/provider"
)

// Legacy mode constants for backwards compatibility
const (
	ModeOff        = "off"
	ModeAuto       = "auto"
	ModeAggressive = "aggressive"
	ModeCaveman    = "caveman"
)

// Report contains statistics about token compression
type Report struct {
	Mode            string         `json:"mode"`
	OriginalTokens  int            `json:"original_tokens"`
	FinalTokens     int            `json:"final_tokens"`
	TokensSaved     int            `json:"tokens_saved"`
	SavingsPercent  float64        `json:"savings_percent"`
	ActiveEngines   []string       `json:"active_engines,omitempty"`
	EngineBreakdown map[string]int `json:"engine_breakdown,omitempty"`
	OutputStyle     string         `json:"output_style,omitempty"`
}

// GlobalStats tracks cumulative lifetime token savings
type GlobalStats struct {
	TotalOriginalTokens atomic.Int64
	TotalFinalTokens    atomic.Int64
	TotalTokensSaved    atomic.Int64
}

var (
	statsOnce   sync.Once
	globalStats *GlobalStats

	// Regex patterns for RTK compression
	diffHeaderRegex      = regexp.MustCompile(`(?m)^diff --git a\/.* b\/.*\nindex [0-9a-f]+\.\.[0-9a-f]+( \d+)?\n--- a\/.*\n\+\+\+ b\/.*`)
	diffHunkRegex        = regexp.MustCompile(`(?m)^@@ -\d+,\d+ \+\d+,\d+ @@.*$`)
	jsonDetectorRegex    = regexp.MustCompile(`^\s*[\{\[][\s\S]*[\}\]]\s*$`)
	timestampLogRegex    = regexp.MustCompile(`(?m)^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?\s*`)
	multiNewlineRegex    = regexp.MustCompile(`\n{3,}`)
	multiSpaceRegex      = regexp.MustCompile(`[ \t]{2,}`)
	fillerWordsRegex     = regexp.MustCompile(`(?i)\b(please|kindly|as an AI|as a helpful assistant|in order to|feel free to|let me know if you need anything else|I would be happy to)\b`)
)

// GetStats returns the singleton stats tracker
func GetStats() *GlobalStats {
	statsOnce.Do(func() {
		globalStats = &GlobalStats{}
	})
	return globalStats
}

// EstimateTokens calculates an approximate token count based on standard 4 chars/token heuristic
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	n := len(text)
	tokens := (n + 3) / 4
	if tokens < 1 && n > 0 {
		tokens = 1
	}
	return tokens
}

// CalculateTotalTokens sums tokens across all messages
func CalculateTotalTokens(messages []provider.ChatMessage) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.Content)
	}
	return total
}

// CompressMessagesPipeline runs messages through the 12-engine stack based on StackConfig
func CompressMessagesPipeline(messages []provider.ChatMessage, cfg *StackConfig) ([]provider.ChatMessage, Report) {
	if cfg == nil {
		cfg = DefaultStackConfig()
	}

	origTotal := CalculateTotalTokens(messages)

	if cfg.Preset == PresetOff {
		return messages, Report{
			Mode:            PresetOff,
			OriginalTokens:  origTotal,
			FinalTokens:     origTotal,
			TokensSaved:     0,
			SavingsPercent:  0,
			ActiveEngines:   nil,
			EngineBreakdown: make(map[string]int),
		}
	}

	// Extract latest user query for Relevance engine
	lastUserQuery := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == provider.RoleUser {
			lastUserQuery = messages[i].Content
			break
		}
	}

	pipelineCtx := &PipelineContext{
		Config:         cfg,
		OriginalTokens: origTotal,
		CurrentTokens:  origTotal,
		LastUserQuery:  lastUserQuery,
		EngineStats:    make(map[string]int),
	}

	currentMsgs := messages
	var activeEngines []string

	// Check if Adaptive Dial is active and needed
	if cfg.AdaptiveDial && cfg.ContextBudget > 0 && origTotal > cfg.ContextBudget {
		// Auto-escalate: activate more engines dynamically
		for _, engineID := range CanonicalEngines {
			if !cfg.IsEngineEnabled(engineID) {
				cfg.setEngine(engineID, true)
			}
		}
	}

	// Execute enabled engines in canonical order
	for _, engineID := range CanonicalEngines {
		if !cfg.IsEngineEnabled(engineID) {
			continue
		}

		engine := GetEngine(engineID)
		if engine == nil {
			continue
		}

		tokensBefore := CalculateTotalTokens(currentMsgs)
		processed, err := engine.Process(pipelineCtx, currentMsgs)
		if err == nil && processed != nil {
			currentMsgs = processed
			tokensAfter := CalculateTotalTokens(currentMsgs)
			savedByEngine := tokensBefore - tokensAfter
			if savedByEngine > 0 {
				pipelineCtx.EngineStats[engineID] = savedByEngine
			}
			activeEngines = append(activeEngines, engineID)
		}
	}

	// Apply Output-Axis Steering if specified
	if cfg.OutputStyle != "" && cfg.OutputStyle != StyleNone {
		currentMsgs = InjectOutputStyle(currentMsgs, cfg.OutputStyle, cfg.StyleIntensity)
	}

	finalTotal := CalculateTotalTokens(currentMsgs)
	saved := origTotal - finalTotal
	if saved < 0 {
		saved = 0
	}

	var percent float64
	if origTotal > 0 {
		percent = (float64(saved) / float64(origTotal)) * 100.0
	}

	// Update global stats
	stats := GetStats()
	stats.TotalOriginalTokens.Add(int64(origTotal))
	stats.TotalFinalTokens.Add(int64(finalTotal))
	stats.TotalTokensSaved.Add(int64(saved))

	return currentMsgs, Report{
		Mode:            cfg.Preset,
		OriginalTokens:  origTotal,
		FinalTokens:     finalTotal,
		TokensSaved:     saved,
		SavingsPercent:  percent,
		ActiveEngines:   activeEngines,
		EngineBreakdown: pipelineCtx.EngineStats,
		OutputStyle:     cfg.OutputStyle,
	}
}

// InjectOutputStyle adds deterministic, cache-safe response-shaping instructions to system prompt
func InjectOutputStyle(messages []provider.ChatMessage, style string, intensity string) []provider.ChatMessage {
	if style == "" || style == StyleNone {
		return messages
	}

	var instruction string
	switch style {
	case StyleTerseProse:
		if intensity == IntensityUltra {
			instruction = "\n[OUTPUT_STYLE: Ultra-terse mode. Zero filler, no articles, no polite hedging. Direct facts & bullet points only.]"
		} else {
			instruction = "\n[OUTPUT_STYLE: Terse prose. Drop conversational fillers, omit boilerplate, keep technical substance exact.]"
		}
	case StyleLessCode:
		instruction = "\n[OUTPUT_STYLE: Minimal code (YAGNI). Provide only the exact lines changed or smallest working diff. Avoid scaffolding.]"
	case StyleIndonesianRingkas:
		instruction = "\n[OUTPUT_STYLE: Bahasa Indonesia super ringkas, to-the-point, hilangkan basa-basi pembuka dan penutup.]"
	case StyleTroglodita:
		instruction = "\n[OUTPUT_STYLE: Troglodita mode. Estilo ultra-conciso, respostas diretas sem preâmbulos técnicos desnecessários.]"
	case StyleTerseCJK:
		instruction = "\n[OUTPUT_STYLE: 文言超简风格，极简扼要，直达核心。]"
	default:
		return messages
	}

	// Prepend or append to System message if exists, or add a system instruction
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem {
		result := make([]provider.ChatMessage, len(messages))
		copy(result, messages)
		result[0].Content = result[0].Content + instruction
		return result
	}

	// Otherwise prepend a system message
	return append([]provider.ChatMessage{
		{
			Role:    provider.RoleSystem,
			Content: strings.TrimSpace(instruction),
		},
	}, messages...)
}

// CompressMessages processes messages for backwards compatibility with previous API
func CompressMessages(messages []provider.ChatMessage, modeOrConfig string, budgetTokens int) ([]provider.ChatMessage, Report) {
	cfg := ParseStackConfig(modeOrConfig)
	if budgetTokens > 0 {
		cfg.ContextBudget = budgetTokens
		cfg.AdaptiveDial = true
	}
	return CompressMessagesPipeline(messages, cfg)
}

// CompressContent applies single-string compression for backward compatibility
func CompressContent(text string, modeOrConfig string) string {
	if text == "" {
		return text
	}
	cfg := ParseStackConfig(modeOrConfig)
	if cfg.Preset == PresetOff {
		return text
	}

	msgs := []provider.ChatMessage{
		{Role: provider.RoleUser, Content: text},
	}
	compressed, _ := CompressMessagesPipeline(msgs, cfg)
	if len(compressed) > 0 {
		return compressed[0].Content
	}
	return text
}

// CompressDiff strips unnecessary hunk line padding and metadata from git diffs
func CompressDiff(diff string, mode string) string {
	lines := strings.Split(diff, "\n")
	var result []string
	inHunk := false
	contextCount := 0
	maxContext := 2
	if mode == ModeAggressive || mode == ModeCaveman {
		maxContext = 1
	}

	for _, line := range lines {
		// Strip git diff headers that carry no code information
		if strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "similarity index ") || strings.HasPrefix(line, "new file mode ") {
			continue
		}

		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
			contextCount = 0
			result = append(result, line)
			continue
		}

		if inHunk {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				result = append(result, line)
				contextCount = 0
			} else {
				if contextCount < maxContext {
					result = append(result, line)
					contextCount++
				} else if contextCount == maxContext {
					result = append(result, "  ...")
					contextCount++
				}
			}
		} else {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// CompressTraceback condenses repetitive stack frames and errors
func CompressTraceback(trace string) string {
	lines := strings.Split(trace, "\n")
	if len(lines) <= 15 {
		return trace
	}

	head := lines[:5]
	tail := lines[len(lines)-6:]

	var sb strings.Builder
	sb.WriteString(strings.Join(head, "\n"))
	sb.WriteString(fmt.Sprintf("\n  ... [%d intermediate stack frames compressed] ...\n", len(lines)-11))
	sb.WriteString(strings.Join(tail, "\n"))
	return sb.String()
}

// CompressCaveman removes conversational padding and filler words
func CompressCaveman(text string) string {
	parts := strings.Split(text, "```")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = fillerWordsRegex.ReplaceAllString(parts[i], "")
		parts[i] = multiSpaceRegex.ReplaceAllString(parts[i], " ")
	}
	return strings.Join(parts, "```")
}

// FitToBudget ensures messages fit within budgetTokens, dropping oldest user/assistant turns if needed
func FitToBudget(messages []provider.ChatMessage, budgetTokens int) []provider.ChatMessage {
	if budgetTokens <= 0 || len(messages) <= 2 {
		return messages
	}

	total := CalculateTotalTokens(messages)
	if total <= budgetTokens {
		return messages
	}

	var sysMsg *provider.ChatMessage
	startIdx := 0
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem {
		sysMsg = &messages[0]
		startIdx = 1
	}

	middle := messages[startIdx:]
	for len(middle) > 1 && total > budgetTokens {
		droppedTokens := EstimateTokens(middle[0].Content)
		middle = middle[1:]
		total -= droppedTokens
	}

	var result []provider.ChatMessage
	if sysMsg != nil {
		result = append(result, *sysMsg)
	}
	result = append(result, middle...)
	return result
}
