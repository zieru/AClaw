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

func TestHTMLTagsToWhatsApp(t *testing.T) {
	input := "⏳ <b>Waktu Tunggu Habis (Timeout)</b>\nServer AI butuh waktu lama. Gunakan <code>/reset</code> jika perlu."
	output := MarkdownToWhatsApp(input)

	if strings.Contains(output, "<b>") || strings.Contains(output, "</b>") {
		t.Errorf("Expected no <b> tags, got:\n%s", output)
	}
	if strings.Contains(output, "<code>") || strings.Contains(output, "</code>") {
		t.Errorf("Expected no <code> tags, got:\n%s", output)
	}
	if !strings.Contains(output, "*Waktu Tunggu Habis (Timeout)*") {
		t.Errorf("Expected '*Waktu Tunggu Habis (Timeout)*', got:\n%s", output)
	}
	if !strings.Contains(output, "`/reset`") {
		t.Errorf("Expected '`/reset`', got:\n%s", output)
	}
}

func TestMarkdownToWhatsApp_LaTeX(t *testing.T) {
	input := `(rumus Hukum Ohm: $\Delta V / \Delta I$) 
1. Hitung Spesifikasi Jika Diseri (10 Keping):
* Tegangan (Voltase): $5\text{V} \times 10 = \mathbf{50\text{ Volt}}$
* Total Daya (Watt): $50\text{V} \times 0.2\text{A} = \mathbf{10\text{ Watt}}$`

	output := MarkdownToWhatsApp(input)

	if !strings.Contains(output, "ΔV / ΔI") {
		t.Errorf("Expected ΔV / ΔI, got:\n%s", output)
	}
	if !strings.Contains(output, "5V × 10 = *50 Volt*") {
		t.Errorf("Expected converted math with WhatsApp bold, got:\n%s", output)
	}
	if !strings.Contains(output, "50V × 0.2A = *10 Watt*") {
		t.Errorf("Expected converted power math with WhatsApp bold, got:\n%s", output)
	}
	if strings.Contains(output, "$") || strings.Contains(output, `\text`) || strings.Contains(output, `\mathbf`) {
		t.Errorf("Expected no remaining raw LaTeX notation, got:\n%s", output)
	}
}
