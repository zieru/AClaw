package admin

import (
	"context"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
	tele "gopkg.in/telebot.v3"
)

type TavilyStep int

const (
	TavilyStepIdle TavilyStep = iota
	TavilyStepInputKey
	TavilyStepInputTestQuery
)

type TavilySession struct {
	Step      TavilyStep
	CreatedAt time.Time
}

type TavilyUI struct {
	db       *storage.DB
	cfg      *config.AppConfig
	mu       sync.Mutex
	sessions map[int64]*TavilySession
}

func NewTavilyUI(db *storage.DB, cfg *config.AppConfig) *TavilyUI {
	return &TavilyUI{
		db:       db,
		cfg:      cfg,
		sessions: make(map[int64]*TavilySession),
	}
}

// RenderTavilyDashboard returns current Tavily AI Search configuration and control buttons
func (ui *TavilyUI) RenderTavilyDashboard() (string, *tele.ReplyMarkup) {
	apiKey := ""
	depth := "basic"
	maxRes := 5

	if cfg := config.Get(); cfg != nil {
		apiKey = cfg.Tavily.APIKey
		if cfg.Tavily.SearchDepth != "" {
			depth = cfg.Tavily.SearchDepth
		}
		if cfg.Tavily.MaxResults > 0 {
			maxRes = cfg.Tavily.MaxResults
		}
	}

	keyDisplay := "🔴 <i>(Belum dikonfigurasi)</i>"
	if apiKey != "" {
		if len(apiKey) > 8 {
			keyDisplay = fmt.Sprintf("🟢 <code>%s...%s</code>", apiKey[:4], apiKey[len(apiKey)-4:])
		} else {
			keyDisplay = "🟢 <code>********</code>"
		}
	}

	var sb strings.Builder
	sb.WriteString("🌐 <b>KONFIGURASI TAVILY AI WEB SEARCH</b>\n\n")
	sb.WriteString("Tavily adalah mesin pencari berbasis AI yang dioptimalkan untuk LLM Agent guna mendapatkan data internet secara real-time dan terstruktur.\n\n")
	sb.WriteString(fmt.Sprintf("• <b>API Key:</b> %s\n", keyDisplay))
	sb.WriteString(fmt.Sprintf("• <b>Search Depth:</b> <code>%s</code> (basic=cepat & hemat, advanced=mendalam)\n", html.EscapeString(depth)))
	sb.WriteString(fmt.Sprintf("• <b>Max Results:</b> <code>%d hasil</code> per pencarian\n\n", maxRes))
	sb.WriteString("💡 <i>Gunakan tombol di bawah untuk mengatur parameter pencarian Tavily:</i>")

	menu := &tele.ReplyMarkup{}
	btnSetKey := menu.Data("🔑 Set / Ganti API Key", "tav_set_key")

	btnDepth := menu.Data(fmt.Sprintf("⚡ Depth: %s", strings.ToUpper(depth)), "tav_toggle_depth")
	btnMax := menu.Data(fmt.Sprintf("🔢 Max: %d Hasil", maxRes), "tav_cycle_max")

	btnTest := menu.Data("🧪 Test Pencarian", "tav_test_query")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnSetKey),
		menu.Row(btnDepth, btnMax),
		menu.Row(btnTest),
		menu.Row(btnBack),
	)

	return sb.String(), menu
}

func (ui *TavilyUI) HandleMenu(c tele.Context) error {
	txt, kb := ui.RenderTavilyDashboard()
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *TavilyUI) HandleToggleDepth(c tele.Context) error {
	cfg := config.Get()
	if cfg == nil {
		return c.Reply("❌ Config tidak ditemukan.")
	}

	if cfg.Tavily.SearchDepth == "advanced" {
		cfg.Tavily.SearchDepth = "basic"
	} else {
		cfg.Tavily.SearchDepth = "advanced"
	}

	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Search depth diubah ke: %s", cfg.Tavily.SearchDepth)})
	txt, kb := ui.RenderTavilyDashboard()
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *TavilyUI) HandleCycleMaxResults(c tele.Context) error {
	cfg := config.Get()
	if cfg == nil {
		return c.Reply("❌ Config tidak ditemukan.")
	}

	switch cfg.Tavily.MaxResults {
	case 3:
		cfg.Tavily.MaxResults = 5
	case 5:
		cfg.Tavily.MaxResults = 10
	default:
		cfg.Tavily.MaxResults = 3
	}

	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("Max results diubah ke: %d", cfg.Tavily.MaxResults)})
	txt, kb := ui.RenderTavilyDashboard()
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *TavilyUI) PromptSetKey(c tele.Context) error {
	ui.mu.Lock()
	ui.sessions[c.Sender().ID] = &TavilySession{
		Step:      TavilyStepInputKey,
		CreatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := "🔑 <b>MASUKKAN TAVILY API KEY</b>\n\n" +
		"Silakan kirimkan API Key dari <a href=\"https://tavily.com\">tavily.com</a> (contoh: <code>tvly-xxxxxxxxxxxx</code>):"
	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "tav_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *TavilyUI) PromptTestQuery(c tele.Context) error {
	ui.mu.Lock()
	ui.sessions[c.Sender().ID] = &TavilySession{
		Step:      TavilyStepInputTestQuery,
		CreatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := "🧪 <b>TEST PENCARIAN TAVILY</b>\n\n" +
		"Ketik kata kunci atau pertanyaan yang ingin dicari di internet secara langsung:"
	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "tav_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

func (ui *TavilyUI) CancelSession(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.sessions, userID)
}

// HandleTextMessage handles interactive text inputs for Tavily settings
func (ui *TavilyUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}

	ui.mu.Lock()
	sess, ok := ui.sessions[c.Sender().ID]
	ui.mu.Unlock()

	if !ok || sess == nil {
		return false, nil
	}

	text := strings.TrimSpace(c.Message().Text)
	if text == "" || text[0] == '/' {
		return false, nil
	}

	switch sess.Step {
	case TavilyStepInputKey:
		ui.CancelSession(c.Sender().ID)
		cfg := config.Get()
		if cfg != nil {
			cfg.Tavily.APIKey = text
		}
		_ = c.Reply("✅ <b>Tavily API Key berhasil disimpan!</b>", tele.ModeHTML)
		txt, kb := ui.RenderTavilyDashboard()
		return true, c.Send(txt, kb, tele.ModeHTML)

	case TavilyStepInputTestQuery:
		ui.CancelSession(c.Sender().ID)
		_ = c.Notify(tele.Typing)
		msgWait, _ := c.Bot().Send(c.Chat(), fmt.Sprintf("🔍 <i>Mencari informasi di Tavily untuk: \"%s\"...</i>", html.EscapeString(text)), tele.ModeHTML)

		tool := &tools.TavilySearchTool{}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		result, err := tool.Execute(ctx, map[string]interface{}{
			"query": text,
		})

		if err != nil {
			errText := fmt.Sprintf("❌ <b>Gagal mencari via Tavily:</b>\n<code>%s</code>", html.EscapeString(err.Error()))
			if msgWait != nil {
				_, _ = c.Bot().Edit(msgWait, errText, tele.ModeHTML)
				return true, nil
			}
			return true, c.Reply(errText, tele.ModeHTML)
		}

		if len(result) > 3500 {
			result = result[:3500] + "\n...[dipotong untuk preview Telegram]"
		}

		resText := fmt.Sprintf("🌐 <b>HASIL PENCARIAN TAVILY:</b>\n\n%s", html.EscapeString(result))
		txt, kb := ui.RenderTavilyDashboard()

		if msgWait != nil {
			_, _ = c.Bot().Edit(msgWait, resText, tele.ModeHTML)
			return true, c.Send(txt, kb, tele.ModeHTML)
		}
		_ = c.Reply(resText, tele.ModeHTML)
		return true, c.Send(txt, kb, tele.ModeHTML)
	}

	return false, nil
}
