package waformat

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	codeBlockRegex   = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\n?(.*?)```")
	inlineCodeRegex  = regexp.MustCompile("`([^`\n]+)`")
	headerRegex      = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	boldItalicRegex  = regexp.MustCompile(`\*\*\*([^\*\n]+)\*\*\*`)
	boldRegex        = regexp.MustCompile(`\*\*([^\*\n]+)\*\*`)
	strikeRegex      = regexp.MustCompile(`~~([^~\n]+)~~`)
	linkRegex        = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s\)]+)\)`)
	hrRegex          = regexp.MustCompile(`(?m)^(\s*[-*_]\s*){3,}\s*$`)
)

// MarkdownToWhatsApp formats standard markdown into WhatsApp-compatible text.
func MarkdownToWhatsApp(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	var codeBlocks []string
	var inlineCodes []string

	// 1. Extract and protect fenced code blocks
	text := codeBlockRegex.ReplaceAllStringFunc(md, func(match string) string {
		sub := codeBlockRegex.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		codeContent := sub[2]
		// WhatsApp uses ```code```
		formatted := fmt.Sprintf("```\n%s\n```", strings.TrimSpace(codeContent))

		idx := len(codeBlocks)
		codeBlocks = append(codeBlocks, formatted)
		return fmt.Sprintf("___WA_CODE_BLOCK_%d___", idx)
	})

	// 2. Extract and protect inline code
	text = inlineCodeRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := inlineCodeRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		formatted := fmt.Sprintf("`%s`", sub[1])

		idx := len(inlineCodes)
		inlineCodes = append(inlineCodes, formatted)
		return fmt.Sprintf("___WA_INLINE_CODE_%d___", idx)
	})

	// 3. Convert Links: [title](url) -> title (url)
	text = linkRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := linkRegex.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		title := strings.TrimSpace(sub[1])
		url := strings.TrimSpace(sub[2])
		if title == url || title == "" {
			return url
		}
		return fmt.Sprintf("%s (%s)", title, url)
	})

	// 4. Convert Horizontal Rules (---, ***, ___) -> clean divider
	text = hrRegex.ReplaceAllString(text, "───────────────")

	// 5. Convert Headers: ### Title -> *Title*
	text = headerRegex.ReplaceAllString(text, `*$1*`)

	// 6. Convert Bold + Italic: ***text*** -> *_text_*
	text = boldItalicRegex.ReplaceAllString(text, `*_$1_*`)

	// 7. Convert Bold: **text** -> *text*
	text = boldRegex.ReplaceAllString(text, `*$1*`)

	// 8. Convert Strikethrough: ~~text~~ -> ~text~
	text = strikeRegex.ReplaceAllString(text, `~$1~`)

	// 9. Restore Inline Codes
	for i, code := range inlineCodes {
		placeholder := fmt.Sprintf("___WA_INLINE_CODE_%d___", i)
		text = strings.ReplaceAll(text, placeholder, code)
	}

	// 10. Restore Code Blocks
	for i, block := range codeBlocks {
		placeholder := fmt.Sprintf("___WA_CODE_BLOCK_%d___", i)
		text = strings.ReplaceAll(text, placeholder, block)
	}

	return strings.TrimSpace(text)
}
