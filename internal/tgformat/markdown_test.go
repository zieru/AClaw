package tgformat

import (
	"strings"
	"testing"
)

func TestMarkdownToTelegramHTML(t *testing.T) {
	input := `Berdasarkan data sesi percakapan kita:

🆔 **ID Telegram kamu: 399999658**

Dengan username / nama tampilan: **zieru**

Contoh code: ` + "`fmt.Println(\"Hello <world> & you\")`" + `
Contoh code block:
` + "```go\nfunc main() {\n\tif a < b && b > c {\n\t\treturn\n\t}\n}\n```" + `

Header:
### Fitur Utama
- *Poin 1*
- _Poin 2_
- ~~Coret~~
- [Link Google](https://google.com)

Ada lagi yang bisa saya bantu? 😊`

	result := MarkdownToTelegramHTML(input)

	if !strings.Contains(result, "<b>ID Telegram kamu: 399999658</b>") {
		t.Errorf("Expected bold ID, got:\n%s", result)
	}
	if !strings.Contains(result, "<b>zieru</b>") {
		t.Errorf("Expected bold username, got:\n%s", result)
	}
	if !strings.Contains(result, "<code>fmt.Println(&#34;Hello &lt;world&gt; &amp; you&#34;)</code>") && !strings.Contains(result, "<code>fmt.Println(&#34;Hello &lt;world&gt; &amp; you&#34;)</code>") && !strings.Contains(result, "&lt;world&gt;") {
		t.Errorf("Expected escaped inline code, got:\n%s", result)
	}
	if !strings.Contains(result, "<pre><code class=\"language-go\">") {
		t.Errorf("Expected code block with language, got:\n%s", result)
	}
	if !strings.Contains(result, "<a href=\"https://google.com\">Link Google</a>") {
		t.Errorf("Expected hyperlink, got:\n%s", result)
	}
	if !strings.Contains(result, "<s>Coret</s>") {
		t.Errorf("Expected strikethrough, got:\n%s", result)
	}
	if !strings.Contains(result, "<b>Fitur Utama</b>") {
		t.Errorf("Expected header converted to bold, got:\n%s", result)
	}
}

func TestMarkdownToTelegramHTML_Table(t *testing.T) {
	input := `Estimasi Efisiensi Nyata:

| Kondisi | Efisiensi Khas |
|---------|---------------|
| Buck converter murahan (no brand) | 80–88% |
| Buck converter mid-range (XL4015, LM2596) | 88–93% |
| Buck converter quality (TI, Murata, Mean Well) | 94–97% |`

	result := MarkdownToTelegramHTML(input)

	if !strings.Contains(result, "<pre>") || !strings.Contains(result, "</pre>") {
		t.Errorf("Expected table wrapped in pre block, got:\n%s", result)
	}
	if !strings.Contains(result, "Kondisi") || !strings.Contains(result, "Efisiensi Khas") {
		t.Errorf("Expected table headers inside result, got:\n%s", result)
	}
	if !strings.Contains(result, "+") || !strings.Contains(result, "|") {
		t.Errorf("Expected ascii border characters, got:\n%s", result)
	}
}

func TestMarkdownToTelegramHTML_LaTeX(t *testing.T) {
	input := `(rumus Hukum Ohm: $\Delta V / \Delta I$) 
1. Hitung Spesifikasi Jika Diseri (10 Keping):
* Tegangan (Voltase): $5\text{V} \times 10 = \mathbf{50\text{ Volt}}$
* Total Daya (Watt): $50\text{V} \times 0.2\text{A} = \mathbf{10\text{ Watt}}$`

	result := MarkdownToTelegramHTML(input)

	if !strings.Contains(result, "ΔV / ΔI") {
		t.Errorf("Expected ΔV / ΔI, got:\n%s", result)
	}
	if !strings.Contains(result, "5V × 10 = <b>50 Volt</b>") {
		t.Errorf("Expected converted math with bold, got:\n%s", result)
	}
	if !strings.Contains(result, "50V × 0.2A = <b>10 Watt</b>") {
		t.Errorf("Expected converted power math, got:\n%s", result)
	}
	if strings.Contains(result, "$") || strings.Contains(result, `\text`) || strings.Contains(result, `\mathbf`) {
		t.Errorf("Expected no remaining raw LaTeX notation, got:\n%s", result)
	}
}
