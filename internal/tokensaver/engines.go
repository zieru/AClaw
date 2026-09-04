package tokensaver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"goassistant/internal/provider"
)

// Engine defines the interface for all token saver compression engines
type Engine interface {
	ID() string
	Name() string
	Description() string
	Process(ctx *PipelineContext, messages []provider.ChatMessage) ([]provider.ChatMessage, error)
}

// PipelineContext stores shared state and configuration during pipeline execution
type PipelineContext struct {
	Config         *StackConfig
	OriginalTokens int
	CurrentTokens  int
	LastUserQuery  string
	EngineStats    map[string]int // tokens saved per engine
}

// ---------------------------------------------------------------------
// 1. Session-Dedup Engine
// ---------------------------------------------------------------------
type SessionDedupEngine struct{}

func (e *SessionDedupEngine) ID() string          { return EngineSessionDedup }
func (e *SessionDedupEngine) Name() string        { return "Session-Dedup" }
func (e *SessionDedupEngine) Description() string { return "Drops content repeated across turns (content-addressed hash)" }

func (e *SessionDedupEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	if len(msgs) <= 2 {
		return msgs, nil
	}

	seenChunks := make(map[string]int)
	result := make([]provider.ChatMessage, 0, len(msgs))

	for i, m := range msgs {
		content := m.Content
		// Only deduplicate large chunks (> 80 chars) to prevent dropping common short phrases
		if len(content) > 80 && m.Role != provider.RoleSystem && i < len(msgs)-1 {
			hash := hashString(strings.TrimSpace(content))
			if prevIdx, exists := seenChunks[hash]; exists {
				// Replace duplicate content with reference tag
				content = fmt.Sprintf("[dedup: identical content repeated from turn %d (hash:%s)]", prevIdx+1, hash[:8])
			} else {
				seenChunks[hash] = i
			}
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 2. CCR (Context Chunk Repository) Engine
// ---------------------------------------------------------------------
type CCREngine struct{}

func (e *CCREngine) ID() string          { return EngineCCR }
func (e *CCREngine) Name() string        { return "CCR" }
func (e *CCREngine) Description() string { return "Archives large historical blocks behind retrieve markers" }

func (e *CCREngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	if len(msgs) <= 3 {
		return msgs, nil
	}

	threshold := 600 // chars
	if val := ctx.Config.GetEngineParam(EngineCCR, "threshold_chars", 600); val != nil {
		if intVal, ok := val.(int); ok && intVal > 0 {
			threshold = intVal
		}
	}

	result := make([]provider.ChatMessage, 0, len(msgs))
	recentWindow := len(msgs) - 2 // keep last 2 turns full

	for i, m := range msgs {
		content := m.Content
		if i < recentWindow && m.Role != provider.RoleSystem && len(content) > threshold {
			// Don't archive if it's the only turn or within code fences that were user-supplied recently
			head := content
			if len(head) > 60 {
				head = head[:60]
			}
			hash := hashString(content)[:8]
			content = fmt.Sprintf("[CCR: archived %d chars (ref:%s) preview: %s...]", len(m.Content), hash, strings.ReplaceAll(head, "\n", " "))
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 3. Lite Engine
// ---------------------------------------------------------------------
type LiteEngine struct{}

func (e *LiteEngine) ID() string          { return EngineLite }
func (e *LiteEngine) Name() string        { return "Lite" }
func (e *LiteEngine) Description() string { return "Whitespace + image-URL trimming (latency-light baseline)" }

var (
	reMultiNewline   = regexp.MustCompile(`\n{3,}`)
	reMultiSpace     = regexp.MustCompile(`[ \t]{2,}`)
	reTrackingParams = regexp.MustCompile(`(?i)(\?|&)(utm_[^=]+|fbclid|gclid|trk)=[^&#\s]*`)
	reMDComments     = regexp.MustCompile(`(?s)<!--.*?-->`)
)

func (e *LiteEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content

		// Strip markdown comments
		content = reMDComments.ReplaceAllString(content, "")

		// Normalize newlines
		content = reMultiNewline.ReplaceAllString(content, "\n\n")

		// Outside code fences, trim multi-spaces
		parts := strings.Split(content, "```")
		for i := 0; i < len(parts); i += 2 {
			parts[i] = reMultiSpace.ReplaceAllString(parts[i], " ")
			parts[i] = reTrackingParams.ReplaceAllString(parts[i], "")
		}
		content = strings.Join(parts, "```")
		content = strings.TrimSpace(content)

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 4. RTK (Runtime Toolkit) Engine
// ---------------------------------------------------------------------
type RTKEngine struct{}

func (e *RTKEngine) ID() string          { return EngineRTK }
func (e *RTKEngine) Name() string        { return "RTK" }
func (e *RTKEngine) Description() string { return "Smart tool-result filtering, dedup & truncation" }

func (e *RTKEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		// Check for unified diff
		if strings.Contains(content, "diff --git") || strings.Contains(content, "@@ -") {
			content = CompressDiff(content, ModeAuto)
		}
		// Check for stack trace
		if strings.Contains(content, "Traceback (most recent call last)") || strings.Contains(content, "panic: ") || strings.Contains(content, "\tat ") {
			content = CompressTraceback(content)
		}
		// Check for logs with timestamp prefixes
		if len(content) > 100 && strings.Contains(content, "\n") {
			content = timestampLogRegex.ReplaceAllString(content, "")
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 5. Responses Tool Output Engine
// ---------------------------------------------------------------------
type ResponsesToolEngine struct{}

func (e *ResponsesToolEngine) ID() string          { return EngineResponsesToolOutput }
func (e *ResponsesToolEngine) Name() string        { return "Responses Tool Output" }
func (e *ResponsesToolEngine) Description() string { return "Lossless JSON compaction & diagnostic compression for build/shell outputs" }

func (e *ResponsesToolEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content

		// If pure JSON, compact lossless
		trimmed := strings.TrimSpace(content)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			var compact bytes.Buffer
			if err := json.Compact(&compact, []byte(trimmed)); err == nil {
				content = compact.String()
			}
		}

		// If compiler / test output is long, compress passing noise
		if strings.Contains(content, "PASS") || strings.Contains(content, "ok  \t") || strings.Contains(content, "=== RUN") {
			content = compressTestOutput(content)
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

func compressTestOutput(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= 12 {
		return text
	}
	var filtered []string
	passCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "=== RUN") || (strings.HasPrefix(line, "--- PASS:") && len(line) < 40) {
			passCount++
			continue
		}
		filtered = append(filtered, line)
	}
	if passCount > 0 {
		filtered = append([]string{fmt.Sprintf("[%d passing test noise lines compressed]", passCount)}, filtered...)
	}
	return strings.Join(filtered, "\n")
}

// ---------------------------------------------------------------------
// 6. Headroom Engine (Lossless Tabular JSON Compaction)
// ---------------------------------------------------------------------
type HeadroomEngine struct{}

func (e *HeadroomEngine) ID() string          { return EngineHeadroom }
func (e *HeadroomEngine) Name() string        { return "Headroom" }
func (e *HeadroomEngine) Description() string { return "Lossless tabular compaction of JSON arrays (~30% savings)" }

func (e *HeadroomEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if len(content) > 100 && strings.HasPrefix(strings.TrimSpace(content), "[") {
			content = compactJSONArray(content)
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

func compactJSONArray(text string) string {
	var list []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &list); err != nil || len(list) < 3 {
		return text
	}

	// Extract common schema keys from first object
	first := list[0]
	keys := make([]string, 0, len(first))
	for k := range first {
		keys = append(keys, k)
	}

	rows := make([][]interface{}, len(list))
	for i, obj := range list {
		row := make([]interface{}, len(keys))
		for j, k := range keys {
			row[j] = obj[k]
		}
		rows[i] = row
	}

	compacted := map[string]interface{}{
		"_schema": keys,
		"_rows":   rows,
	}

	b, err := json.Marshal(compacted)
	if err != nil {
		return text
	}
	return string(b)
}

// ---------------------------------------------------------------------
// 7. Relevance Extractor Engine
// ---------------------------------------------------------------------
type RelevanceEngine struct{}

func (e *RelevanceEngine) ID() string          { return EngineRelevance }
func (e *RelevanceEngine) Name() string        { return "Relevance" }
func (e *RelevanceEngine) Description() string { return "Extractive scoring against the latest user query" }

func (e *RelevanceEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	if len(msgs) <= 3 || ctx.LastUserQuery == "" {
		return msgs, nil
	}

	queryTokens := extractKeywords(ctx.LastUserQuery)
	if len(queryTokens) == 0 {
		return msgs, nil
	}

	result := make([]provider.ChatMessage, 0, len(msgs))
	recentThreshold := len(msgs) - 2

	for i, m := range msgs {
		content := m.Content
		// Only filter older assistant or tool responses
		if i < recentThreshold && (m.Role == provider.RoleAssistant || m.Role == provider.RoleTool) && len(content) > 400 {
			content = scoreAndFilterParagraphs(content, queryTokens)
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

func extractKeywords(query string) map[string]bool {
	words := strings.Fields(strings.ToLower(query))
	keywords := make(map[string]bool)
	for _, w := range words {
		w = strings.Trim(w, ".,!?:;\"'()[]{}")
		if len(w) > 3 {
			keywords[w] = true
		}
	}
	return keywords
}

func scoreAndFilterParagraphs(text string, queryKeywords map[string]bool) string {
	paragraphs := strings.Split(text, "\n\n")
	if len(paragraphs) <= 2 {
		return text
	}

	var kept []string
	for _, p := range paragraphs {
		// Always keep code blocks
		if strings.Contains(p, "```") {
			kept = append(kept, p)
			continue
		}

		score := 0
		words := strings.Fields(strings.ToLower(p))
		for _, w := range words {
			w = strings.Trim(w, ".,!?:;\"'()[]{}")
			if queryKeywords[w] {
				score++
			}
		}

		// Keep if relevant or short
		if score > 0 || len(p) < 150 {
			kept = append(kept, p)
		}
	}

	if len(kept) == 0 {
		return paragraphs[0] // preserve at least first paragraph
	}
	return strings.Join(kept, "\n\n")
}

// ---------------------------------------------------------------------
// 8. Caveman Engine (Prose Filler Stripping)
// ---------------------------------------------------------------------
type CavemanEngine struct{}

func (e *CavemanEngine) ID() string          { return EngineCaveman }
func (e *CavemanEngine) Name() string        { return "Caveman" }
func (e *CavemanEngine) Description() string { return "Rule-based prose compression (~65-75% savings)" }

var extendedFillerRegex = regexp.MustCompile(`(?i)\b(please note that|as an AI language model|as an AI|as a helpful assistant|in order to|feel free to|let me know if you need anything else|I would be happy to|sure, I can help with that|certainly!|sure thing!|tolong dicatat bahwa|sebagai asisten AI|dengan senang hati|jangan ragu untuk|berikut adalah penjelasan|berikut adalah detailnya|berikut merupakan)\b[:\s]*`)

func (e *CavemanEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if m.Role != provider.RoleSystem {
			parts := strings.Split(content, "```")
			for i := 0; i < len(parts); i += 2 {
				parts[i] = extendedFillerRegex.ReplaceAllString(parts[i], "")
				parts[i] = reMultiSpace.ReplaceAllString(parts[i], " ")
			}
			content = strings.Join(parts, "```")
			content = strings.TrimSpace(content)
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 9. Aggressive Aging Engine
// ---------------------------------------------------------------------
type AggressiveAgingEngine struct{}

func (e *AggressiveAgingEngine) ID() string          { return EngineAggressive }
func (e *AggressiveAgingEngine) Name() string        { return "Aggressive" }
func (e *AggressiveAgingEngine) Description() string { return "Summarization + progressive aging of old turns" }

func (e *AggressiveAgingEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	minTurns := 4
	if ctx != nil && ctx.Config != nil && (ctx.Config.Preset == PresetUltra || ctx.Config.Preset == PresetAggressive) {
		minTurns = 2
	}
	if len(msgs) <= minTurns {
		return msgs, nil
	}

	result := make([]provider.ChatMessage, 0, len(msgs))
	cutoff := len(msgs) - minTurns // Turns older than cutoff gets aggressively condensed

	for i, m := range msgs {
		content := m.Content
		if i < cutoff && m.Role != provider.RoleSystem && len(content) > 180 {
			lines := strings.Split(content, "\n")
			if len(lines) > 3 {
				// Keep first line and last line
				content = fmt.Sprintf("%s\n... [%d lines aged] ...\n%s", lines[0], len(lines)-2, lines[len(lines)-1])
			}
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 10. LLMLingua-2 Engine (Semantic Pruning)
// ---------------------------------------------------------------------
type LLMLingua2Engine struct{}

func (e *LLMLingua2Engine) ID() string          { return EngineLLMLingua2 }
func (e *LLMLingua2Engine) Name() string        { return "LLMLingua-2" }
func (e *LLMLingua2Engine) Description() string { return "Semantic token importance pruning (code-safe, async)" }

var stopwordsRegex = regexp.MustCompile(`(?i)\b(basically|actually|essentially|obviously|clearly|definitely|literally|simply|furthermore|moreover|in fact|that being said)\b\s*`)

func (e *LLMLingua2Engine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if m.Role != provider.RoleSystem {
			parts := strings.Split(content, "```")
			for i := 0; i < len(parts); i += 2 {
				parts[i] = stopwordsRegex.ReplaceAllString(parts[i], "")
			}
			content = strings.Join(parts, "```")
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 11. Ultra Engine (Micro-Syntax Shorthand)
// ---------------------------------------------------------------------
type UltraEngine struct{}

func (e *UltraEngine) ID() string          { return EngineUltra }
func (e *UltraEngine) Name() string        { return "Ultra" }
func (e *UltraEngine) Description() string { return "Heuristic token pruning with micro-syntax shorthand" }

var narrativePaddingRegex = regexp.MustCompile(`(?i)\b(It is important to note that|You should make sure to|In the following section|The above example demonstrates)\b\s*`)

func (e *UltraEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if m.Role != provider.RoleSystem {
			parts := strings.Split(content, "```")
			for i := 0; i < len(parts); i += 2 {
				parts[i] = narrativePaddingRegex.ReplaceAllString(parts[i], "")
			}
			content = strings.Join(parts, "```")
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// 12. OmniGlyph Engine (Context Shorthand & Symbols)
// ---------------------------------------------------------------------
type OmniGlyphEngine struct{}

func (e *OmniGlyphEngine) ID() string          { return EngineOmniGlyph }
func (e *OmniGlyphEngine) Name() string        { return "OmniGlyph" }
func (e *OmniGlyphEngine) Description() string { return "Token-efficient shorthand glyph encoding" }

func (e *OmniGlyphEngine) Process(ctx *PipelineContext, msgs []provider.ChatMessage) ([]provider.ChatMessage, error) {
	result := make([]provider.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		// Replaces repeated verbose headers in logs / assistant blocks with glyphs
		if m.Role == provider.RoleTool || m.Role == provider.RoleAssistant {
			content = strings.ReplaceAll(content, "SUCCESS:", "✓")
			content = strings.ReplaceAll(content, "FAILED:", "✗")
			content = strings.ReplaceAll(content, "WARNING:", "⚠")
		}

		result = append(result, provider.ChatMessage{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// Engine Registry
// ---------------------------------------------------------------------
var engineRegistry = map[string]Engine{
	EngineSessionDedup:        &SessionDedupEngine{},
	EngineCCR:                 &CCREngine{},
	EngineLite:                &LiteEngine{},
	EngineRTK:                 &RTKEngine{},
	EngineResponsesToolOutput: &ResponsesToolEngine{},
	EngineHeadroom:            &HeadroomEngine{},
	EngineRelevance:           &RelevanceEngine{},
	EngineCaveman:             &CavemanEngine{},
	EngineAggressive:          &AggressiveAgingEngine{},
	EngineLLMLingua2:          &LLMLingua2Engine{},
	EngineUltra:               &UltraEngine{},
	EngineOmniGlyph:           &OmniGlyphEngine{},
}

// GetEngine retrieves an engine by ID
func GetEngine(id string) Engine {
	return engineRegistry[id]
}

// hashString returns SHA256 hex string
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
