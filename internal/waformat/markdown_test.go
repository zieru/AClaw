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

func TestMarkdownTableToWhatsApp(t *testing.T) {
	input := `Berikut adalah daftar server kami:

| No | Server Name | Status | CPU |
| --- | --- | --- | --- |
| 1 | Web-Primary | Online | 45% |
| 2 | DB-Replica | Standby | 12% |

Dan berikut konfigurasi simpel:

| Param | Value |
| --- | --- |
| Port | 8080 |
| Host | localhost |
`

	output := MarkdownToWhatsApp(input)

	// Check table 1
	if !strings.Contains(output, "*#1 Web-Primary*") {
		t.Errorf("Expected '*#1 Web-Primary*', got:\n%s", output)
	}
	if !strings.Contains(output, "*Status:* Online") {
		t.Errorf("Expected '*Status:* Online', got:\n%s", output)
	}
	if !strings.Contains(output, "*CPU:* 45%") {
		t.Errorf("Expected '*CPU:* 45%%', got:\n%s", output)
	}

	// Check 2-column table
	if !strings.Contains(output, "*Port:* 8080") {
		t.Errorf("Expected '*Port:* 8080', got:\n%s", output)
	}
	if !strings.Contains(output, "*Host:* localhost") {
		t.Errorf("Expected '*Host:* localhost', got:\n%s", output)
	}
}
