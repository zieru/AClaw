package waformat

import (
	"strings"
	"testing"
)

func TestMarkdownToWhatsApp(t *testing.T) {
	input := `# Header 1
## Header 2
### Header 3

Here is **bold text** and ***bold italic*** and ~~strikethrough~~.
Check [Google Search](https://google.com) for more.

---

` + "```python\ndef hello():\n    print('world')\n```" + `

Here is inline code: ` + "`my_variable`" + ` in text.`

	output := MarkdownToWhatsApp(input)

	if !strings.Contains(output, "*Header 1*") {
		t.Errorf("Expected *Header 1*, got %s", output)
	}
	if !strings.Contains(output, "*Header 3*") {
		t.Errorf("Expected *Header 3*, got %s", output)
	}
	if !strings.Contains(output, "*bold text*") {
		t.Errorf("Expected *bold text*, got %s", output)
	}
	if !strings.Contains(output, "~strikethrough~") {
		t.Errorf("Expected ~strikethrough~, got %s", output)
	}
	if !strings.Contains(output, "Google Search (https://google.com)") {
		t.Errorf("Expected Google Search link formatted, got %s", output)
	}
	if !strings.Contains(output, "───────────────") {
		t.Errorf("Expected HR divider, got %s", output)
	}
	if !strings.Contains(output, "`my_variable`") {
		t.Errorf("Expected inline code preserved, got %s", output)
	}
	if !strings.Contains(output, "```\ndef hello():\n    print('world')\n```") {
		t.Errorf("Expected code block preserved, got %s", output)
	}
}
