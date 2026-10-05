package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goassistant/internal/version"
)

type CommandRouter struct{}

func NewCommandRouter() *CommandRouter {
	return &CommandRouter{}
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

	case "/help", "/bantuan":
		helpText := "🤖 <b>Panduan Singkat GoAssistant:</b>\n\n" +
			"Kamu bisa langsung mengobrol, bertanya, meminta analisis kode, merangkum dokumen, atau melakukan pencarian web.\n\n" +
			"<b>Perintah Cepat:</b>\n" +
			"• <code>/ping</code> - Cek status koneksi bot\n" +
			"• <code>/version</code> - Cek versi GoAssistant\n" +
			"• <code>/tokensaver</code> - Cek status 12-Engine Token Saver\n" +
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
