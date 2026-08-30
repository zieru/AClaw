package tgformat

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	codeBlockRegex  = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\n?(.*?)```")
	inlineCodeRegex = regexp.MustCompile("`([^`\n]+)`")
	headerRegex     = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	boldItalicRegex = regexp.MustCompile(`\*\*\*([^\*\n]+)\*\*\*`)
	boldRegex       = regexp.MustCompile(`\*\*([^\*\n]+)\*\*`)
	strikeRegex     = regexp.MustCompile(`~~([^~\n]+)~~`)
	linkRegex       = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s\)]+)\)`)
)

// MarkdownToTelegramHTML converts standard markdown (from LLM) to Telegram-supported HTML.
func MarkdownToTelegramHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	var codeBlocks []string
	var inlineCodes []string
	var validTags []string

	// 1. Extract and protect fenced code blocks
	text := codeBlockRegex.ReplaceAllStringFunc(md, func(match string) string {
		sub := codeBlockRegex.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		lang := strings.TrimSpace(sub[1])
		codeContent := sub[2]
		escapedCode := html.EscapeString(codeContent)

		var formatted string
		if lang != "" {
			formatted = fmt.Sprintf("<pre><code class=\"language-%s\">%s</code></pre>", html.EscapeString(lang), escapedCode)
		} else {
			formatted = fmt.Sprintf("<pre>%s</pre>", escapedCode)
		}

		idx := len(codeBlocks)
		codeBlocks = append(codeBlocks, formatted)
		return fmt.Sprintf("___TG_CODE_BLOCK_%d___", idx)
	})

	// 2. Extract and protect inline code
	text = inlineCodeRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := inlineCodeRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		escaped := html.EscapeString(sub[1])
		formatted := fmt.Sprintf("<code>%s</code>", escaped)

		idx := len(inlineCodes)
		inlineCodes = append(inlineCodes, formatted)
		return fmt.Sprintf("___TG_INLINE_CODE_%d___", idx)
	})

	// 2b. Extract and protect pre-existing valid Telegram HTML tags (like <blockquote>, <b>, <i>, etc.)
	validTagRegex := regexp.MustCompile(`(?i)</?(?:b|i|s|u|blockquote|expandable-blockquote|tg-spoiler|a(?:\s+href="[^"]*")?)\s*/?>`)
	text = validTagRegex.ReplaceAllStringFunc(text, func(match string) string {
		idx := len(validTags)
		validTags = append(validTags, match)
		return fmt.Sprintf("___TG_TAG_%d___", idx)
	})

	// 3. Escape general HTML characters (&, <, >) in remaining text
	text = html.EscapeString(text)

	// 4. Convert Links: [text](url) -> <a href="url">text</a>
	text = linkRegex.ReplaceAllString(text, `<a href="$2">$1</a>`)

	// 5. Convert Headers: ### Title -> <b>Title</b>
	text = headerRegex.ReplaceAllString(text, `<b>$1</b>`)

	// 6. Convert Bold + Italic: ***text*** -> <b><i>$1</i></b>
	text = boldItalicRegex.ReplaceAllString(text, `<b><i>$1</i></b>`)

	// 7. Convert Bold: **text** -> <b>text</b>
	text = boldRegex.ReplaceAllString(text, `<b>$1</b>`)

	// 8. Convert Strikethrough: ~~text~~ -> <s>text</s>
	text = strikeRegex.ReplaceAllString(text, `<s>$1</s>`)

	// 9. Convert Italic: *text* -> <i>text</i> or _text_ -> <i>text</i>
	text = replaceItalic(text)

	// 10. Restore Valid Pre-existing Tags
	for i, tag := range validTags {
		placeholder := fmt.Sprintf("___TG_TAG_%d___", i)
		text = strings.ReplaceAll(text, placeholder, tag)
	}

	// 11. Restore Inline Codes
	for i, code := range inlineCodes {
		placeholder := fmt.Sprintf("___TG_INLINE_CODE_%d___", i)
		text = strings.ReplaceAll(text, placeholder, code)
	}

	// 12. Restore Code Blocks
	for i, block := range codeBlocks {
		placeholder := fmt.Sprintf("___TG_CODE_BLOCK_%d___", i)
		text = strings.ReplaceAll(text, placeholder, block)
	}

	return text
}

// replaceItalic carefully replaces *italic* or _italic_ without breaking internal variable names like `user_id`
func replaceItalic(text string) string {
	// Simple *italic* matching: *word*
	asteriskItalic := regexp.MustCompile(`\*([^\*\n<]+)\*`)
	text = asteriskItalic.ReplaceAllString(text, `<i>$1</i>`)

	// Underscore italic matching: must be separated by whitespace/punctuation to avoid snake_case
	underscoreItalic := regexp.MustCompile(`(^|[\s(\[{])_([^_\n<]+)_([\s)\]}.,!?:;]|$)`)
	text = underscoreItalic.ReplaceAllString(text, `${1}<i>${2}</i>${3}`)

	return text
}
