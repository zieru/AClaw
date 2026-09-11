package provider

import (
	"regexp"
	"strings"
	"sync"
)

var (
	thinkTagRegex      = regexp.MustCompile(`(?s)<think>(.*?)(?:</think>|$)`)
	thoughtTagRegex    = regexp.MustCompile(`(?s)<thought>(.*?)(?:</thought>|$)`)
	reasoningTagRegex  = regexp.MustCompile(`(?s)<reasoning>(.*?)(?:</reasoning>|$)`)

	openThinkTags  = []string{"<think>", "<thought>", "<reasoning>"}
	closeThinkTags = []string{"</think>", "</thought>", "</reasoning>"}
)

// ExtractThinkingTags extracts <think>...</think>, <thought>...</thought>, or <reasoning>...</reasoning>
// from raw text, returning the cleaned content and the extracted thinking text.
func ExtractThinkingTags(text string) (string, string) {
	var thinkingParts []string

	extract := func(re *regexp.Regexp) {
		matches := re.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				t := strings.TrimSpace(m[1])
				if t != "" {
					thinkingParts = append(thinkingParts, t)
				}
			}
		}
		text = re.ReplaceAllString(text, "")
	}

	extract(thinkTagRegex)
	extract(thoughtTagRegex)
	extract(reasoningTagRegex)

	return strings.TrimSpace(text), strings.TrimSpace(strings.Join(thinkingParts, "\n\n"))
}

// StreamingThinkingFilter intercepts stream chunks, routing text inside <think>...</think>
// to StreamChunk.Thinking, and text outside to StreamChunk.Content.
type StreamingThinkingFilter struct {
	mu              sync.Mutex
	onChunk         StreamCallback
	inThinking      bool
	buf             strings.Builder
	thinkingBuilder strings.Builder
	contentBuilder  strings.Builder
}

// NewStreamingThinkingFilter creates a new streaming thinking filter.
func NewStreamingThinkingFilter(callback StreamCallback) *StreamingThinkingFilter {
	return &StreamingThinkingFilter{
		onChunk: callback,
	}
}

// Feed processes incoming delta content and native reasoning content.
func (f *StreamingThinkingFilter) Feed(deltaContent, deltaReasoning string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 1. If provider explicitly gave native reasoning content, route directly to Thinking
	if deltaReasoning != "" {
		f.thinkingBuilder.WriteString(deltaReasoning)
		if f.onChunk != nil {
			f.onChunk(StreamChunk{Thinking: deltaReasoning})
		}
	}

	if deltaContent == "" {
		return
	}

	f.buf.WriteString(deltaContent)
	f.processBuffer()
}

func (f *StreamingThinkingFilter) processBuffer() {
	for {
		cur := f.buf.String()
		if cur == "" {
			break
		}

		if !f.inThinking {
			// Search for any opening tag: <think>, <thought>, <reasoning>
			tagIdx := -1
			foundTag := ""
			for _, tag := range openThinkTags {
				idx := strings.Index(cur, tag)
				if idx != -1 && (tagIdx == -1 || idx < tagIdx) {
					tagIdx = idx
					foundTag = tag
				}
			}

			if tagIdx != -1 {
				// Text before tag is regular content
				before := cur[:tagIdx]
				if before != "" {
					f.contentBuilder.WriteString(before)
					if f.onChunk != nil {
						f.onChunk(StreamChunk{Content: before})
					}
				}
				// Enter thinking mode and consume tag
				f.inThinking = true
				rem := cur[tagIdx+len(foundTag):]
				f.buf.Reset()
				f.buf.WriteString(rem)
				continue
			}

			// Check if buffer ends with a partial opening tag (e.g. "<th", "<thought")
			suffix := findLongestTagSuffix(cur, openThinkTags)
			if suffix != "" {
				before := cur[:len(cur)-len(suffix)]
				if before != "" {
					f.contentBuilder.WriteString(before)
					if f.onChunk != nil {
						f.onChunk(StreamChunk{Content: before})
					}
				}
				f.buf.Reset()
				f.buf.WriteString(suffix)
				break
			}

			// No tag and no partial tag: all current buffer is regular content
			f.contentBuilder.WriteString(cur)
			if f.onChunk != nil {
				f.onChunk(StreamChunk{Content: cur})
			}
			f.buf.Reset()
			break

		} else {
			// In thinking mode: search for closing tag: </think>, </thought>, </reasoning>
			tagIdx := -1
			foundTag := ""
			for _, tag := range closeThinkTags {
				idx := strings.Index(cur, tag)
				if idx != -1 && (tagIdx == -1 || idx < tagIdx) {
					tagIdx = idx
					foundTag = tag
				}
			}

			if tagIdx != -1 {
				// Text before closing tag is thinking
				before := cur[:tagIdx]
				if before != "" {
					f.thinkingBuilder.WriteString(before)
					if f.onChunk != nil {
						f.onChunk(StreamChunk{Thinking: before})
					}
				}
				// Exit thinking mode and consume closing tag
				f.inThinking = false
				rem := cur[tagIdx+len(foundTag):]
				f.buf.Reset()
				f.buf.WriteString(rem)
				continue
			}

			// Check if buffer ends with a partial closing tag (e.g. "</", "</th")
			suffix := findLongestTagSuffix(cur, closeThinkTags)
			if suffix != "" {
				before := cur[:len(cur)-len(suffix)]
				if before != "" {
					f.thinkingBuilder.WriteString(before)
					if f.onChunk != nil {
						f.onChunk(StreamChunk{Thinking: before})
					}
				}
				f.buf.Reset()
				f.buf.WriteString(suffix)
				break
			}

			// No closing tag and no partial tag: all current buffer is thinking
			f.thinkingBuilder.WriteString(cur)
			if f.onChunk != nil {
				f.onChunk(StreamChunk{Thinking: cur})
			}
			f.buf.Reset()
			break
		}
	}
}

// Flush flushes any remaining buffered text when stream ends.
func (f *StreamingThinkingFilter) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()

	rem := f.buf.String()
	if rem == "" {
		return
	}

	if f.inThinking {
		f.thinkingBuilder.WriteString(rem)
		if f.onChunk != nil {
			f.onChunk(StreamChunk{Thinking: rem})
		}
	} else {
		f.contentBuilder.WriteString(rem)
		if f.onChunk != nil {
			f.onChunk(StreamChunk{Content: rem})
		}
	}
	f.buf.Reset()
}

// Content returns the accumulated clean response content.
func (f *StreamingThinkingFilter) Content() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.TrimSpace(f.contentBuilder.String())
}

// Thinking returns the accumulated clean thinking content.
func (f *StreamingThinkingFilter) Thinking() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.TrimSpace(f.thinkingBuilder.String())
}

func findLongestTagSuffix(s string, tags []string) string {
	var longest string
	for _, tag := range tags {
		for i := len(tag) - 1; i >= 1; i-- {
			prefix := tag[:i]
			if strings.HasSuffix(s, prefix) {
				if len(prefix) > len(longest) {
					longest = prefix
				}
			}
		}
	}
	return longest
}
