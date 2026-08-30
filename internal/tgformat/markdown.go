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

	// 0. Convert Markdown Tables to clean monospaced ASCII code blocks
	text := convertMarkdownTablesToPre(md)

	var codeBlocks []string
	var inlineCodes []string
	var validTags []string

	// 1. Extract and protect fenced code blocks
	text = codeBlockRegex.ReplaceAllStringFunc(text, func(match string) string {
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

	// 2c. Clean and convert LaTeX Math notation to readable Unicode
	text = CleanLaTeXMath(text)

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

// convertMarkdownTablesToPre detects markdown tables and wraps them into monospace ASCII code blocks
func convertMarkdownTablesToPre(text string) string {
	lines := strings.Split(text, "\n")
	var newLines []string
	i := 0

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if current line is a table header followed by a separator line
		if isTableLine(trimmed) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			headers := parseTableRow(trimmed)
			i += 2 // Skip header and separator

			var dataRows [][]string
			for i < len(lines) && isTableLine(lines[i]) {
				row := parseTableRow(lines[i])
				if len(row) > 0 {
					dataRows = append(dataRows, row)
				}
				i++
			}

			if len(headers) > 0 && len(dataRows) > 0 {
				newLines = append(newLines, formatTableAsAscii(headers, dataRows))
			}
			continue
		}

		newLines = append(newLines, line)
		i++
	}

	return strings.Join(newLines, "\n")
}

func isTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "|") && len(trimmed) > 1
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	clean := strings.ReplaceAll(trimmed, "|", "")
	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ReplaceAll(clean, ":", "")
	clean = strings.ReplaceAll(clean, " ", "")
	return clean == ""
}

func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	rawCols := strings.Split(trimmed, "|")
	var cols []string
	for _, col := range rawCols {
		cols = append(cols, strings.TrimSpace(col))
	}
	return cols
}

func formatTableAsAscii(headers []string, rows [][]string) string {
	numCols := len(headers)
	colWidths := make([]int, numCols)

	for j, h := range headers {
		if len(h) > colWidths[j] {
			colWidths[j] = len(h)
		}
	}

	for _, row := range rows {
		for j := 0; j < numCols && j < len(row); j++ {
			val := row[j]
			if len(val) > colWidths[j] {
				colWidths[j] = len(val)
			}
		}
	}

	// Build separator line
	var sepParts []string
	for _, w := range colWidths {
		sepParts = append(sepParts, strings.Repeat("-", w+2))
	}
	sep := "+" + strings.Join(sepParts, "+") + "+"

	var sb strings.Builder
	sb.WriteString("```\n")
	sb.WriteString(sep + "\n")

	// Header row
	var headerCells []string
	for j, h := range headers {
		pad := colWidths[j] - len(h)
		if pad < 0 {
			pad = 0
		}
		headerCells = append(headerCells, fmt.Sprintf(" %s%s ", h, strings.Repeat(" ", pad)))
	}
	sb.WriteString("|" + strings.Join(headerCells, "|") + "|\n")
	sb.WriteString(sep + "\n")

	// Data rows
	for _, row := range rows {
		var cells []string
		for j := 0; j < numCols; j++ {
			val := ""
			if j < len(row) {
				val = row[j]
			}
			pad := colWidths[j] - len(val)
			if pad < 0 {
				pad = 0
			}
			cells = append(cells, fmt.Sprintf(" %s%s ", val, strings.Repeat(" ", pad)))
		}
		sb.WriteString("|" + strings.Join(cells, "|") + "|\n")
	}
	sb.WriteString(sep + "\n")
	sb.WriteString("```")

	return sb.String()
}
