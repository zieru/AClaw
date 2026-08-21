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
