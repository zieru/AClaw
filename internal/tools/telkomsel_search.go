package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/search"
)

// TelkomselSearchTool provides focused searching exclusively on the official telkomsel.com domain
type TelkomselSearchTool struct {
	engine *search.Engine
}

// NewTelkomselSearchTool creates a new TelkomselSearchTool instance
func NewTelkomselSearchTool(engine *search.Engine) *TelkomselSearchTool {
	return &TelkomselSearchTool{
		engine: engine,
	}
}

func (t *TelkomselSearchTool) Name() string {
	return "search_telkomsel_web"
}

func (t *TelkomselSearchTool) Description() string {
	return "Mencari informasi resmi produk, paket internet, kuota, eSIM, roaming, tarif, syarat registrasi, dan panduan pelanggan langsung dari situs web resmi Telkomsel (telkomsel.com)."
}

func (t *TelkomselSearchTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"query": {
				Type:        "string",
				Description: "Kata kunci pencarian yang ingin dicari di website telkomsel.com (misal: 'syarat migrasi kartu halo ke prabayar', 'harga paket roaming umroh', 'cara aktivasi esim telkomsel').",
			},
		},
		Required: []string{"query"},
	}
}

func (t *TelkomselSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	q, ok := args["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("parameter 'query' wajib diisi")
	}
	q = strings.TrimSpace(q)

	// Check tool cache (30 minutes TTL for high efficiency)
	if cachedVal, found := GetGlobalToolCache().Get(t.Name(), args); found {
		return cachedVal, nil
	}

	eng := t.engine
	if eng == nil {
		eng = search.GetGlobalEngine()
	}

	// Scope search strictly to telkomsel.com
	scopedQuery := q
	if !strings.Contains(strings.ToLower(scopedQuery), "site:telkomsel.com") {
		scopedQuery = fmt.Sprintf("site:telkomsel.com %s", scopedQuery)
	}

	searchRes, err := eng.Search(ctx, scopedQuery)
	if err != nil {
		return "", fmt.Errorf("gagal melakukan pencarian di telkomsel.com: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 **Hasil Pencarian Resmi telkomsel.com** untuk: *%s*\n\n", q))

	if searchRes.Answer != "" {
		sb.WriteString(fmt.Sprintf("💡 **Ringkasan Informasi:**\n%s\n\n", searchRes.Answer))
	}

	if len(searchRes.Results) == 0 {
		sb.WriteString("Tidak ada artikel atau dokumen spesifik yang ditemukan di telkomsel.com untuk kata kunci tersebut.")
	} else {
		count := 0
		for _, res := range searchRes.Results {
			if count >= 4 {
				break
			}
			snippet := strings.TrimSpace(res.Snippet)
			if len(snippet) > 350 {
				snippet = snippet[:350] + "..."
			}
			count++
			if snippet != "" {
				sb.WriteString(fmt.Sprintf("%d. **%s**\n   🔗 URL: %s\n   📄 Ringkasan: %s\n\n", count, res.Title, res.URL, snippet))
			} else {
				sb.WriteString(fmt.Sprintf("%d. **%s**\n   🔗 URL: %s\n\n", count, res.Title, res.URL))
			}
		}
	}

	out := sb.String()
	GetGlobalToolCache().Set(t.Name(), args, out, 30*time.Minute)
	return out, nil
}
