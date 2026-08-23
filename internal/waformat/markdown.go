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

	htmlPreRegex    = regexp.MustCompile(`(?is)<pre>(.*?)</pre>`)
	htmlCodeRegex   = regexp.MustCompile(`(?is)<code>(.*?)</code>`)
	htmlBoldRegex   = regexp.MustCompile(`(?is)<(?:b|strong)>(.*?)</(?:b|strong)>`)
	htmlItalicRegex = regexp.MustCompile(`(?is)<(?:i|em)>(.*?)</(?:i|em)>`)
	htmlStrikeRegex = regexp.MustCompile(`(?is)<(?:s|strike|del)>(.*?)</(?:s|strike|del)>`)
	htmlBrRegex     = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTagRegex    = regexp.MustCompile(`(?i)</?[a-zA-Z0-9_-]+(?:\s+[^>]*)?>`)
)

// MarkdownToWhatsApp formats standard markdown into WhatsApp-compatible text.
func MarkdownToWhatsApp(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	var codeBlocks []string
	var inlineCodes []string

	// 1. Convert HTML <pre> and <code> blocks before placeholder extraction
	text := htmlPreRegex.ReplaceAllString(md, "```\n$1\n```")
	text = htmlCodeRegex.ReplaceAllString(text, "`$1`")

	// 2. Extract and protect fenced code blocks
	text = codeBlockRegex.ReplaceAllStringFunc(text, func(match string) string {
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

	// 3. Extract and protect inline code
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

	// 4. Convert HTML Formatting Tags (<b>, <i>, <s>, <br>)
	text = htmlBoldRegex.ReplaceAllString(text, `*$1*`)
	text = htmlItalicRegex.ReplaceAllString(text, `_$1_`)
	text = htmlStrikeRegex.ReplaceAllString(text, `~$1~`)
	text = htmlBrRegex.ReplaceAllString(text, "\n")
	text = htmlTagRegex.ReplaceAllString(text, "") // Strip any remaining HTML tags

	// 5. Convert Links: [title](url) -> title (url)
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

	// 6. Convert Horizontal Rules (---, ***, ___) -> clean divider
	text = hrRegex.ReplaceAllString(text, "───────────────")

	// 7. Convert Markdown Tables to Beautiful WhatsApp Card Lists
	text = convertMarkdownTables(text)

	// 8. Convert Headers: ### Title -> *Title*
	text = headerRegex.ReplaceAllString(text, `*$1*`)

	// 9. Convert Bold + Italic: ***text*** -> *_text_*
	text = boldItalicRegex.ReplaceAllString(text, `*_$1_*`)

	// 10. Convert Bold: **text** -> *text*
	text = boldRegex.ReplaceAllString(text, `*$1*`)

	// 11. Convert Strikethrough: ~~text~~ -> ~text~
	text = strikeRegex.ReplaceAllString(text, `~$1~`)

	// 12. Restore Inline Codes
	for i, code := range inlineCodes {
		placeholder := fmt.Sprintf("___WA_INLINE_CODE_%d___", i)
		text = strings.ReplaceAll(text, placeholder, code)
	}

	// 13. Restore Code Blocks
	for i, block := range codeBlocks {
		placeholder := fmt.Sprintf("___WA_CODE_BLOCK_%d___", i)
		text = strings.ReplaceAll(text, placeholder, block)
	}

	return strings.TrimSpace(text)
}

// convertMarkdownTables detects markdown tables and formats them into readable WhatsApp card lists
func convertMarkdownTables(text string) string {
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
				newLines = append(newLines, formatTableAsWAList(headers, dataRows)...)
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
	// A separator contains only |, -, :, and spaces
	clean := strings.ReplaceAll(trimmed, "|", "")
	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ReplaceAll(clean, ":", "")
	clean = strings.ReplaceAll(clean, " ", "")
	return clean == ""
}

func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	// Remove outer pipes if present
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	rawCols := strings.Split(trimmed, "|")
	var cols []string
	for _, col := range rawCols {
		cols = append(cols, strings.TrimSpace(col))
	}
	return cols
}

func formatTableAsWAList(headers []string, rows [][]string) []string {
	var out []string
	out = append(out, "") // blank line before table

	for idx, row := range rows {
		if len(row) == 0 {
			continue
		}

		// Clean row items
		cols := make([]string, len(row))
		for i, c := range row {
			cols[i] = strings.TrimSpace(c)
		}

		if len(cols) == 2 {
			// Simple 2-column key-value format: • *Key:* Value
			k := cols[0]
			v := cols[1]
			out = append(out, fmt.Sprintf("• *%s:* %s", k, v))
		} else if len(cols) > 2 {
			// Multi-column format
			col1 := cols[0]
			col2 := cols[1]

			// Check if column 1 is a numeric index / item number
			isNumeric := isNumberOrIndex(col1)
			if isNumeric && len(headers) > 1 {
				out = append(out, fmt.Sprintf("• *#%s %s*", col1, col2))
				// Output remaining columns as sub-items
				for cIdx := 2; cIdx < len(cols); cIdx++ {
					headerName := ""
					if cIdx < len(headers) {
						headerName = headers[cIdx]
					}
					if headerName != "" {
						out = append(out, fmt.Sprintf("   — *%s:* %s", headerName, cols[cIdx]))
					} else {
						out = append(out, fmt.Sprintf("   — %s", cols[cIdx]))
					}
				}
			} else {
				// Column 1 as primary title
				out = append(out, fmt.Sprintf("• *%s*", col1))
				for cIdx := 1; cIdx < len(cols); cIdx++ {
					headerName := ""
					if cIdx < len(headers) {
						headerName = headers[cIdx]
					}
					if headerName != "" {
						out = append(out, fmt.Sprintf("   — *%s:* %s", headerName, cols[cIdx]))
					} else {
						out = append(out, fmt.Sprintf("   — %s", cols[cIdx]))
					}
				}
			}

			if idx < len(rows)-1 {
				out = append(out, "") // blank line between multi-column cards
			}
		} else if len(cols) == 1 {
			out = append(out, fmt.Sprintf("• %s", cols[0]))
		}
	}

	out = append(out, "") // blank line after table
	return out
}

func isNumberOrIndex(s string) bool {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
