package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/version"
)

type CommandRouter struct {
	cache *ResponseCache
}

func NewCommandRouter(cache *ResponseCache) *CommandRouter {
	return &CommandRouter{cache: cache}
}

// TryHandleLocal checks if the user prompt is a system/slash command that can be answered with 0 tokens
func (r *CommandRouter) TryHandleLocal(ctx context.Context, req UserRequest) (*AgentResponse, bool) {
	trimmed := strings.TrimSpace(req.UserPrompt)
	lower := strings.ToLower(trimmed)

	switch lower {
	case "/ping", "ping":
		return &AgentResponse{
			Text:         "🏓 <b>Pong!</b> GoAssistant aktif dan responsif.\n⏱ <i>Latensi internal: <1ms (0 Token)</i>",
			Latency:      1 * time.Millisecond,
			ProviderUsed: "local_router",
			ModelUsed:    "deterministic",
		}, true

	case "/version", "/ver":
		ver := version.Version
		if ver == "" {
			ver = "1.0.0-release"
		}
		return &AgentResponse{
			Text:         fmt.Sprintf("🤖 <b>GoAssistant</b> versi <code>%s</code>\n⚡ Arsitektur: Monolithic Multi-Agent & Token-Saver Engine", ver),
			Latency:      1 * time.Millisecond,
			ProviderUsed: "local_router",
			ModelUsed:    "deterministic",
		}, true

	case "/cache", "/cachestatus":
		stats := GetGlobalResponseCache().Stats()
		toolStats := GetGlobalResponseCache()
		_ = toolStats
		msg := fmt.Sprintf("⚡ <b>Status Cache Lokal GoAssistant:</b>\n\n"+
			"• Total Entries Aktif: <b>%d</b>\n"+
			"• Cache Hits: <b>%d</b>\n"+
			"• Cache Misses: <b>%d</b>\n"+
			"• Hit Rate: <b>%.1f%%</b>\n"+
			"• Estimasi Token Dihemat: <b>%d</b> tokens\n\n"+
			"💡 <i>Query yang sama persis dalam masa TTL dijawab instan dengan 0 token.</i>",
			stats.TotalEntries, stats.HitCount, stats.MissCount, stats.HitRate, stats.TokensSaved)
		return &AgentResponse{
			Text:         msg,
			Latency:      1 * time.Millisecond,
			ProviderUsed: "local_router",
			ModelUsed:    "deterministic",
		}, true

	case "/help", "/bantuan":
		helpText := "🤖 <b>Panduan Singkat GoAssistant:</b>\n\n" +
			"Kamu bisa langsung mengobrol, bertanya, meminta analisis kode, merangkum dokumen, atau melakukan pencarian web.\n\n" +
			"<b>Perintah Cepat:</b>\n" +
			"• <code>/ping</code> - Cek status koneksi bot\n" +
			"• <code>/cache</code> - Cek performa respon cache & token dihemat\n" +
			"• <code>/version</code> - Cek versi GoAssistant\n" +
			"• <code>/clear</code> - Bersihkan sesi riwayat percakapan\n" +
			"• <code>/help</code> - Menampilkan bantuan ini"
		return &AgentResponse{
			Text:         helpText,
			Latency:      1 * time.Millisecond,
			ProviderUsed: "local_router",
			ModelUsed:    "deterministic",
		}, true
	}

	return nil, false
}
