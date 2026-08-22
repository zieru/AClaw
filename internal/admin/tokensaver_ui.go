package admin

import (
	"fmt"
	"strconv"
	"strings"

	"goassistant/internal/agent"
	"goassistant/internal/storage"
	"goassistant/internal/tokensaver"
	"goassistant/internal/tools"

	tele "gopkg.in/telebot.v3"
)

type TokenSaverUIHandler struct {
	db *storage.DB
}

func NewTokenSaverUIHandler(db *storage.DB) *TokenSaverUIHandler {
	return &TokenSaverUIHandler{db: db}
}

// HandleTokenSaverStatus displays Token Saver 12-Engine stack dashboard & inline interactive toggles
func (h *TokenSaverUIHandler) HandleTokenSaverStatus(c tele.Context) error {
	stats := tokensaver.GetStats()
	globPolicy := h.db.GetResolvedPolicy("", "")

	cfg := tokensaver.ParseStackConfig(globPolicy.TokenSaverMode)

	origTotal := stats.TotalOriginalTokens.Load()
	finalTotal := stats.TotalFinalTokens.Load()
	savedTotal := stats.TotalTokensSaved.Load()

	var percent float64
	if origTotal > 0 {
		percent = (float64(savedTotal) / float64(origTotal)) * 100.0
	}

	var sb strings.Builder
	sb.WriteString("🧱 <b>12-Engine Token Saver & Cache Dashboard</b>\n\n")
	sb.WriteString(fmt.Sprintf("Preset Aktif: <code>%s</code>\n", strings.ToUpper(cfg.Preset)))
	sb.WriteString(fmt.Sprintf("Output Style: <code>%s</code> (%s)\n", cfg.OutputStyle, cfg.StyleIntensity))
	sb.WriteString(fmt.Sprintf("Adaptive Dial: <code>%v</code> (Budget: %d tokens)\n\n", cfg.AdaptiveDial, cfg.ContextBudget))

	sb.WriteString("<b>Statistik Penghematan TokenSaver:</b>\n")
	sb.WriteString(fmt.Sprintf("• Total Original: <b>%s</b> tokens\n", formatNumber(int(origTotal))))
	sb.WriteString(fmt.Sprintf("• Total Terkompresi: <b>%s</b> tokens\n", formatNumber(int(finalTotal))))
	sb.WriteString(fmt.Sprintf("💰 <b>Hemat: %s (%.1f%%)</b>\n\n", formatNumber(int(savedTotal)), percent))

	// Cache Statistics
	cacheStats := agent.GetGlobalResponseCache().Stats()
	toolStats := tools.GetGlobalToolCache().Stats()
	cacheStatus := "✅ ON"
	if !globPolicy.ResponseCacheEnabled {
		cacheStatus = "❌ OFF"
	}
	sb.WriteString("<b>Performa Response & Tool Cache:</b>\n")
	sb.WriteString(fmt.Sprintf("• Response Cache: <b>%s</b> (TTL: %ds)\n", cacheStatus, globPolicy.ResponseCacheTTLSec))
	sb.WriteString(fmt.Sprintf("• Cache Entries: <b>%d</b> | Hits: <b>%d</b> | Misses: <b>%d</b> (Hit Rate: <b>%.1f%%</b>)\n", cacheStats.TotalEntries, cacheStats.HitCount, cacheStats.MissCount, cacheStats.HitRate))
	sb.WriteString(fmt.Sprintf("• Token Dihemat Cache: <b>%s</b> tokens\n", formatNumber(int(cacheStats.TokensSaved))))
	sb.WriteString(fmt.Sprintf("• Tool Cache Entries: <b>%d</b> | Hits: <b>%d</b>\n\n", toolStats.TotalEntries, toolStats.HitCount))

	sb.WriteString("<b>Status 12-Engine Pipeline:</b>\n")
	engineNames := map[string]string{
		tokensaver.EngineSessionDedup:        "1. Session-Dedup",
		tokensaver.EngineCCR:                 "2. CCR (Archive)",
		tokensaver.EngineLite:                "3. Lite (Whitespace/URL)",
		tokensaver.EngineRTK:                 "4. RTK (Diff/Logs/Tool)",
		tokensaver.EngineResponsesToolOutput: "5. Resp Tool Output",
		tokensaver.EngineHeadroom:            "6. Headroom (Tabular JSON)",
		tokensaver.EngineRelevance:           "7. Relevance Extractor",
		tokensaver.EngineCaveman:             "8. Caveman (Prose Filler)",
		tokensaver.EngineAggressive:          "9. Aggressive Aging",
		tokensaver.EngineLLMLingua2:          "10. LLMLingua-2 (Prune)",
		tokensaver.EngineUltra:               "11. Ultra (Micro-Syntax)",
		tokensaver.EngineOmniGlyph:           "12. OmniGlyph (Shorthand)",
	}

	for _, id := range tokensaver.CanonicalEngines {
		status := "❌ OFF"
		if cfg.IsEngineEnabled(id) {
			status = "✅ ON"
		}
		sb.WriteString(fmt.Sprintf("• <code>%-24s</code>: <b>%s</b>\n", engineNames[id], status))
	}

	sb.WriteString("\n💡 <i>Gunakan tombol di bawah untuk ganti preset atau toggle engine individual.</i>")

	menu := h.BuildInteractiveKeyboard(cfg)
	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// BuildInteractiveKeyboard creates inline buttons for presets and engine toggles
func (h *TokenSaverUIHandler) BuildInteractiveKeyboard(cfg *tokensaver.StackConfig) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}

	// Row 1: Presets
	btnLite := menu.Data("🪶 Lite", "ts_preset_lite")
	btnStd := menu.Data("🪨 Standard", "ts_preset_standard")
	btnAggr := menu.Data("⚡ Aggr", "ts_preset_aggressive")

	// Row 2: Advanced Presets
	btnRTK := menu.Data("🧰 RTK", "ts_preset_rtk")
	btnStacked := menu.Data("🔗 Stacked", "ts_preset_stacked")
	btnUltra := menu.Data("🔥 Ultra", "ts_preset_ultra")

	// Row 3: Output Styles
	btnStyleTerse := menu.Data("🪄 Terse", "ts_style_terse")
	btnStyleLessCode := menu.Data("🪄 LessCode", "ts_style_lesscode")
	btnStyleIndo := menu.Data("🪄 Indo", "ts_style_indo")
	btnStyleOff := menu.Data("🚫 Style Off", "ts_style_none")

	// Row 4: Engines 1-4
	btnE1 := menu.Data(engineBtnLabel("1.Dedup", cfg.IsEngineEnabled(tokensaver.EngineSessionDedup)), "ts_tgl_"+tokensaver.EngineSessionDedup)
	btnE2 := menu.Data(engineBtnLabel("2.CCR", cfg.IsEngineEnabled(tokensaver.EngineCCR)), "ts_tgl_"+tokensaver.EngineCCR)
	btnE3 := menu.Data(engineBtnLabel("3.Lite", cfg.IsEngineEnabled(tokensaver.EngineLite)), "ts_tgl_"+tokensaver.EngineLite)
	btnE4 := menu.Data(engineBtnLabel("4.RTK", cfg.IsEngineEnabled(tokensaver.EngineRTK)), "ts_tgl_"+tokensaver.EngineRTK)

	// Row 5: Engines 5-8
	btnE5 := menu.Data(engineBtnLabel("5.Resp", cfg.IsEngineEnabled(tokensaver.EngineResponsesToolOutput)), "ts_tgl_"+tokensaver.EngineResponsesToolOutput)
	btnE6 := menu.Data(engineBtnLabel("6.Head", cfg.IsEngineEnabled(tokensaver.EngineHeadroom)), "ts_tgl_"+tokensaver.EngineHeadroom)
	btnE7 := menu.Data(engineBtnLabel("7.Relev", cfg.IsEngineEnabled(tokensaver.EngineRelevance)), "ts_tgl_"+tokensaver.EngineRelevance)
	btnE8 := menu.Data(engineBtnLabel("8.Cave", cfg.IsEngineEnabled(tokensaver.EngineCaveman)), "ts_tgl_"+tokensaver.EngineCaveman)

	// Row 6: Engines 9-12
	btnE9 := menu.Data(engineBtnLabel("9.Aging", cfg.IsEngineEnabled(tokensaver.EngineAggressive)), "ts_tgl_"+tokensaver.EngineAggressive)
	btnE10 := menu.Data(engineBtnLabel("10.Lingua", cfg.IsEngineEnabled(tokensaver.EngineLLMLingua2)), "ts_tgl_"+tokensaver.EngineLLMLingua2)
	btnE11 := menu.Data(engineBtnLabel("11.Ultra", cfg.IsEngineEnabled(tokensaver.EngineUltra)), "ts_tgl_"+tokensaver.EngineUltra)
	btnE12 := menu.Data(engineBtnLabel("12.Glyph", cfg.IsEngineEnabled(tokensaver.EngineOmniGlyph)), "ts_tgl_"+tokensaver.EngineOmniGlyph)

	// Row 7: Cache Controls
	btnToggleCache := menu.Data("⚡ Toggle Cache", "ts_toggle_cache")
	btnFlushResp := menu.Data("🧹 Flush Resp Cache", "ts_flush_resp_cache")
	btnFlushTool := menu.Data("🧹 Flush Tool Cache", "ts_flush_tool_cache")

	// Row 8: General Controls
	btnDial := menu.Data("🎯 Toggle Dial", "ts_toggle_dial")
	btnOff := menu.Data("⏹️ Disable All (Off)", "ts_preset_off")
	btnRefresh := menu.Data("🔄 Refresh", "ts_refresh")

	menu.Inline(
		menu.Row(btnLite, btnStd, btnAggr),
		menu.Row(btnRTK, btnStacked, btnUltra),
		menu.Row(btnStyleTerse, btnStyleLessCode, btnStyleIndo, btnStyleOff),
		menu.Row(btnE1, btnE2, btnE3, btnE4),
		menu.Row(btnE5, btnE6, btnE7, btnE8),
		menu.Row(btnE9, btnE10, btnE11, btnE12),
		menu.Row(btnToggleCache, btnFlushResp, btnFlushTool),
		menu.Row(btnDial, btnOff, btnRefresh),
	)

	return menu
}

func engineBtnLabel(name string, enabled bool) string {
	if enabled {
		return "✅ " + name
	}
	return "❌ " + name
}

// HandleToggleCacheCallback toggles local ResponseCache ON/OFF
func (h *TokenSaverUIHandler) HandleToggleCacheCallback(c tele.Context) error {
	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{
			Scope:   "global",
			ScopeID: "system",
		}
	}
	globPol.ResponseCacheEnabled = !globPol.ResponseCacheEnabled
	_ = h.db.SavePolicy(globPol)
	return h.HandleTokenSaverStatus(c)
}

// HandleFlushCacheCallback flushes response cache
func (h *TokenSaverUIHandler) HandleFlushCacheCallback(c tele.Context) error {
	count := agent.GetGlobalResponseCache().Flush()
	_ = c.Respond(&tele.CallbackResponse{
		Text: fmt.Sprintf("🧹 %d Response Cache entries berhasil dibersihkan!", count),
	})
	return h.HandleTokenSaverStatus(c)
}

// HandleFlushToolCacheCallback flushes tool cache
func (h *TokenSaverUIHandler) HandleFlushToolCacheCallback(c tele.Context) error {
	count := tools.GetGlobalToolCache().Flush()
	_ = c.Respond(&tele.CallbackResponse{
		Text: fmt.Sprintf("🧹 %d Tool Cache entries berhasil dibersihkan!", count),
	})
	return h.HandleTokenSaverStatus(c)
}

// HandlePresetCallback handles 1-click preset switching
func (h *TokenSaverUIHandler) HandlePresetCallback(c tele.Context, preset string) error {
	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{
			Scope:   "global",
			ScopeID: "system",
		}
	}

	cfg := tokensaver.GetPresetConfig(preset)
	globPol.TokenSaverMode = cfg.SerializeToJSON()
	_ = h.db.SavePolicy(globPol)

	return h.HandleTokenSaverStatus(c)
}

// HandleToggleEngineCallback toggles a specific engine ON/OFF
func (h *TokenSaverUIHandler) HandleToggleEngineCallback(c tele.Context, engineID string) error {
	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{
			Scope:   "global",
			ScopeID: "system",
		}
	}

	cfg := tokensaver.ParseStackConfig(globPol.TokenSaverMode)
	currentState := cfg.IsEngineEnabled(engineID)
	cfg.SetEngineToggle(engineID, !currentState)

	globPol.TokenSaverMode = cfg.SerializeToJSON()
	_ = h.db.SavePolicy(globPol)

	return h.HandleTokenSaverStatus(c)
}

// HandleStyleCallback sets the output-axis steering style
func (h *TokenSaverUIHandler) HandleStyleCallback(c tele.Context, style string) error {
	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{
			Scope:   "global",
			ScopeID: "system",
		}
	}

	cfg := tokensaver.ParseStackConfig(globPol.TokenSaverMode)
	cfg.OutputStyle = style
	globPol.TokenSaverMode = cfg.SerializeToJSON()
	_ = h.db.SavePolicy(globPol)

	return h.HandleTokenSaverStatus(c)
}

// HandleToggleDialCallback toggles the adaptive context budget dial
func (h *TokenSaverUIHandler) HandleToggleDialCallback(c tele.Context) error {
	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{
			Scope:   "global",
			ScopeID: "system",
		}
	}

	cfg := tokensaver.ParseStackConfig(globPol.TokenSaverMode)
	cfg.AdaptiveDial = !cfg.AdaptiveDial
	globPol.TokenSaverMode = cfg.SerializeToJSON()
	_ = h.db.SavePolicy(globPol)

	return h.HandleTokenSaverStatus(c)
}

// HandleSetPresetCommand handles /tokensaverpreset <preset>
func (h *TokenSaverUIHandler) HandleSetPresetCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format: <code>/tokensaverpreset &lt;lite|standard|aggressive|ultra|rtk|stacked|off&gt;</code>", tele.ModeHTML)
	}

	preset := strings.ToLower(args[0])
	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
	}

	cfg := tokensaver.GetPresetConfig(preset)
	globPol.TokenSaverMode = cfg.SerializeToJSON()
	if err := h.db.SavePolicy(globPol); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan policy: %v", err), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Preset Token Saver diubah ke: <b>%s</b>", strings.ToUpper(preset)), tele.ModeHTML)
}

// HandleSetEngineCommand handles /tokensaverengine <engine_id> <on|off>
func (h *TokenSaverUIHandler) HandleSetEngineCommand(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format: <code>/tokensaverengine &lt;engine_id&gt; &lt;on|off&gt;</code>\nContoh: <code>/tokensaverengine rtk on</code>\nEngine IDs: <code>session_dedup, ccr, lite, rtk, responses_tool_output, headroom, relevance, caveman, aggressive, llmlingua2, ultra, omniglyph</code>", tele.ModeHTML)
	}

	engineID := strings.ToLower(args[0])
	state := strings.ToLower(args[1]) == "on"

	if tokensaver.GetEngine(engineID) == nil {
		return c.Reply(fmt.Sprintf("❌ Engine <code>%s</code> tidak ditemukan.", engineID), tele.ModeHTML)
	}

	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
	}

	cfg := tokensaver.ParseStackConfig(globPol.TokenSaverMode)
	cfg.SetEngineToggle(engineID, state)
	globPol.TokenSaverMode = cfg.SerializeToJSON()
	if err := h.db.SavePolicy(globPol); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan policy: %v", err), tele.ModeHTML)
	}

	statusStr := "OFF ❌"
	if state {
		statusStr = "ON ✅"
	}
	return c.Reply(fmt.Sprintf("✅ Engine <b>%s</b> berhasil diset: <b>%s</b>", engineID, statusStr), tele.ModeHTML)
}

// HandleSetStyleCommand handles /tokensaverstyle <style_name> [intensity]
func (h *TokenSaverUIHandler) HandleSetStyleCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format: <code>/tokensaverstyle &lt;terse_prose|less_code|id_ringkas|troglodita|none&gt; [lite|full|ultra]</code>", tele.ModeHTML)
	}

	style := strings.ToLower(args[0])
	intensity := tokensaver.IntensityFull
	if len(args) > 1 {
		intensity = strings.ToLower(args[1])
	}

	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
	}

	cfg := tokensaver.ParseStackConfig(globPol.TokenSaverMode)
	cfg.OutputStyle = style
	cfg.StyleIntensity = intensity
	globPol.TokenSaverMode = cfg.SerializeToJSON()
	if err := h.db.SavePolicy(globPol); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan policy: %v", err), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Output style diset: <b>%s</b> (%s)", style, intensity), tele.ModeHTML)
}

// HandleSetDialCommand handles /tokensaverdial <budget_tokens|off>
func (h *TokenSaverUIHandler) HandleSetDialCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format: <code>/tokensaverdial &lt;budget_tokens|off&gt;</code>\nContoh: <code>/tokensaverdial 4000</code>", tele.ModeHTML)
	}

	globPol, _ := h.db.GetPolicy("global", "system")
	if globPol == nil {
		globPol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
	}

	cfg := tokensaver.ParseStackConfig(globPol.TokenSaverMode)
	if strings.ToLower(args[0]) == "off" {
		cfg.AdaptiveDial = false
		cfg.ContextBudget = 0
	} else {
		val, err := strconv.Atoi(args[0])
		if err != nil || val < 100 {
			return c.Reply("❌ Budget tokens harus berupa angka > 100 atau 'off'", tele.ModeHTML)
		}
		cfg.AdaptiveDial = true
		cfg.ContextBudget = val
	}

	globPol.TokenSaverMode = cfg.SerializeToJSON()
	if err := h.db.SavePolicy(globPol); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan policy: %v", err), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Adaptive Dial diset: Dial=%v, Budget=%d tokens", cfg.AdaptiveDial, cfg.ContextBudget), tele.ModeHTML)
}

// HandleSetMode updates legacy mode (backwards compatible)
func (h *TokenSaverUIHandler) HandleSetMode(c tele.Context) error {
	return h.HandleSetPresetCommand(c)
}

func formatNumber(n int) string {
	in := fmt.Sprintf("%d", n)
	out := make([]byte, len(in)+(len(in)-1)/3)
	for i, j, k := len(in)-1, len(out)-1, 0; i >= 0; i, j = i-1, j-1 {
		out[j] = in[i]
		k++
		if k%3 == 0 && i > 0 {
			j--
			out[j] = ','
		}
	}
	return string(out)
}
