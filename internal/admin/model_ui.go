package admin

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

const modelsPerPage = 8

type ModelUI struct {
	db              *storage.DB
	providerManager *provider.Manager
	mu              sync.RWMutex
	userScope       map[int64]string // "chat" (default) or "global"
	userPage        map[int64]int
	userProvider    map[int64]string
}

func NewModelUI(db *storage.DB, pm *provider.Manager) *ModelUI {
	return &ModelUI{
		db:              db,
		providerManager: pm,
		userScope:       make(map[int64]string),
		userPage:        make(map[int64]int),
		userProvider:    make(map[int64]string),
	}
}

func (ui *ModelUI) getScope(userID int64) string {
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	if s, ok := ui.userScope[userID]; ok && s != "" {
		return s
	}
	return "chat"
}

func (ui *ModelUI) setScope(userID int64, scope string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.userScope[userID] = scope
}

// RenderModelDashboard returns HTML formatted model selector dashboard
func (ui *ModelUI) RenderModelDashboard(c tele.Context) string {
	userID := int64(0)
	chatIDStr := ""
	if c.Sender() != nil {
		userID = c.Sender().ID
	}
	if c.Chat() != nil {
		chatIDStr = fmt.Sprintf("%d", c.Chat().ID)
	}

	scope := ui.getScope(userID)
	scopeLabel := "💬 Chat Ini (Sesi PM Admin)"
	if scope == "global" {
		scopeLabel = "🌐 Global (Semua Channel/Chat)"
	}

	policy := ui.db.GetResolvedPolicy("admin", chatIDStr)
	currentOverride := policy.ModelOverride

	// Check if override is currently empty, a combo, or a specific model
	var activeDesc string
	if currentOverride == "" {
		activeDesc = "🔄 <b>Default / Auto Router</b> (Mengikuti default provider & model sistem)"
	} else if combo, ok := ui.providerManager.GetCombo(currentOverride); ok {
		var targets []string
		for _, t := range combo.Targets {
			targets = append(targets, fmt.Sprintf("<code>%s/%s</code>", html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
		}
		activeDesc = fmt.Sprintf("🔀 <b>Combo:</b> <code>%s</code>\n   • <i>Chain:</i> %s", html.EscapeString(combo.Name), strings.Join(targets, " ➔ "))
	} else {
		activeDesc = fmt.Sprintf("🎯 <b>Custom Model:</b> <code>%s</code>", html.EscapeString(currentOverride))
	}

	// Providers & Combos summary
	allProvs := ui.providerManager.ListAll()
	allCombos := ui.providerManager.ListCombos()

	var sb strings.Builder
	sb.WriteString("🎛️ <b>PENGATURAN MODEL & COMBO AI</b>\n\n")
	sb.WriteString(fmt.Sprintf("📌 <b>Target Scope:</b> <code>%s</code>\n", scopeLabel))
	sb.WriteString(fmt.Sprintf("⚡ <b>Model Aktif:</b>\n%s\n\n", activeDesc))

	sb.WriteString("📦 <b>Ketersediaan Engine:</b>\n")
	sb.WriteString(fmt.Sprintf("• AI Providers Aktif: <code>%d provider</code>\n", len(allProvs)))
	sb.WriteString(fmt.Sprintf("• Fallback Combos: <code>%d combo</code>\n\n", len(allCombos)))

	sb.WriteString("💡 <i>Pilih opsi di bawah untuk mengganti model secara instan atau gunakan perintah langsung:</i>\n")
	sb.WriteString("• <code>/model default</code> - Reset ke auto / default router\n")
	sb.WriteString("• <code>/model &lt;combo_name&gt;</code> - Aktifkan combo tertentu\n")
	sb.WriteString("• <code>/model &lt;provider&gt; &lt;model&gt;</code> - Pilih model spesifik")

	return sb.String()
}

// ModelMenuKeyboard builds interactive inline keyboard for /model
func (ui *ModelUI) ModelMenuKeyboard(userID int64) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	scope := ui.getScope(userID)

	scopeBtnText := "🌐 Ubah ke Scope: Global"
	if scope == "global" {
		scopeBtnText = "💬 Ubah ke Scope: Chat Ini"
	}
	btnScope := menu.Data(scopeBtnText, "mod_toggle_scope")

	btnDefault := menu.Data("🔄 Gunakan Default / Auto", "mod_set_default")
	btnCombos := menu.Data("🔀 Pilih Model Combo", "mod_menu_combos")
	btnProviders := menu.Data("🤖 Pilih Provider & Model", "mod_menu_providers")
	btnRefresh := menu.Data("🔄 Refresh", "mod_refresh")
	btnBack := menu.Data("⬅️ Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnDefault),
		menu.Row(btnCombos, btnProviders),
		menu.Row(btnScope),
		menu.Row(btnRefresh, btnBack),
	)
	return menu
}

// RenderCombosList renders available combos for selection
func (ui *ModelUI) RenderCombosList(c tele.Context) (string, *tele.ReplyMarkup) {
	combos := ui.providerManager.ListCombos()
	menu := &tele.ReplyMarkup{}

	var sb strings.Builder
	sb.WriteString("🔀 <b>PILIH MODEL COMBO / CHAIN</b>\n\n")

	if len(combos) == 0 {
		sb.WriteString("<i>(Belum ada combo yang terdaftar. Buat dengan <code>/addcombo</code> atau <code>/combowizard</code>)</i>\n\n")
	} else {
		sb.WriteString("Pilih salah satu fallback combo di bawah untuk diterapkan:\n\n")
		var rows []tele.Row
		var curRow []tele.Btn

		for i, cRec := range combos {
			var targets []string
			for _, t := range cRec.Targets {
				targets = append(targets, fmt.Sprintf("%s/%s", t.ProviderID, t.Model))
			}
			sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n   • <i>Targets:</i> <code>%s</code>\n", i+1, html.EscapeString(cRec.Name), html.EscapeString(strings.Join(targets, " ➔ "))))

			btn := menu.Data(fmt.Sprintf("🔀 %s", cRec.Name), fmt.Sprintf("mod_set_c_%s", cRec.Name))
			curRow = append(curRow, btn)
			if len(curRow) == 2 {
				rows = append(rows, menu.Row(curRow...))
				curRow = []tele.Btn{}
			}
		}
		if len(curRow) > 0 {
			rows = append(rows, menu.Row(curRow...))
		}
		for _, r := range rows {
			menu.Inline(r)
		}
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "mod_main")
	menu.Inline(menu.Row(btnBack))

	return sb.String(), menu
}

// RenderProvidersList renders available providers to pick models from
func (ui *ModelUI) RenderProvidersList(c tele.Context) (string, *tele.ReplyMarkup) {
	providers := ui.providerManager.ListAll()
	menu := &tele.ReplyMarkup{}

	var sb strings.Builder
	sb.WriteString("🤖 <b>PILIH PROVIDER AI</b>\n\n")

	if len(providers) == 0 {
		sb.WriteString("<i>(Tidak ada provider AI aktif yang terdaftar)</i>\n\n")
	} else {
		sb.WriteString("Silakan pilih provider untuk melihat seluruh model yang tersedia:\n\n")

		var rows []tele.Row
		var curRow []tele.Btn

		for _, p := range providers {
			allModels := ui.getAllModelsForProvider(p)
			btnText := fmt.Sprintf("🤖 %s (%d models)", p.Name(), len(allModels))
			btn := menu.Data(btnText, fmt.Sprintf("mod_prov_%s", p.Name()))
			curRow = append(curRow, btn)
			if len(curRow) == 2 {
				rows = append(rows, menu.Row(curRow...))
				curRow = []tele.Btn{}
			}
		}
		if len(curRow) > 0 {
			rows = append(rows, menu.Row(curRow...))
		}
		for _, r := range rows {
			menu.Inline(r)
		}
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "mod_main")
	menu.Inline(menu.Row(btnBack))

	return sb.String(), menu
}

// RenderProviderModels renders full models available for a specific provider with pagination
func (ui *ModelUI) RenderProviderModels(c tele.Context, provName string, page int) (string, *tele.ReplyMarkup) {
	p, ok := ui.providerManager.Get(provName)
	menu := &tele.ReplyMarkup{}

	if !ok || p == nil {
		return "❌ Provider tidak ditemukan atau sedang nonaktif.", menu
	}

	allModels := ui.getAllModelsForProvider(p)
	totalModels := len(allModels)
	if totalModels == 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🤖 <b>PROVIDER: %s</b>\n\n", html.EscapeString(provName)))
		sb.WriteString("<i>(Tidak ada model yang terdaftar untuk provider ini)</i>\n")
		btnBack := menu.Data("⬅️ Pilih Provider Lain", "mod_menu_providers")
		menu.Inline(menu.Row(btnBack))
		return sb.String(), menu
	}

	totalPages := (totalModels + modelsPerPage - 1) / modelsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	startIdx := page * modelsPerPage
	endIdx := startIdx + modelsPerPage
	if endIdx > totalModels {
		endIdx = totalModels
	}

	pageModels := allModels[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🤖 <b>DAFTAR MODEL TERSEDIA: %s</b>\n", html.EscapeString(strings.ToUpper(provName))))
	sb.WriteString(fmt.Sprintf("Halaman <code>%d/%d</code> (Total: <code>%d model</code>)\n\n", page+1, totalPages, totalModels))
	sb.WriteString("Klik salah satu model di bawah untuk mengaktifkannya:\n\n")

	var rows []tele.Row
	for i, m := range pageModels {
		globalIdx := startIdx + i + 1
		isDef := strings.EqualFold(m, p.DefaultModel())
		defTag := ""
		if isDef {
			defTag = " ⭐ (Default)"
		}
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>%s\n", globalIdx, html.EscapeString(m), defTag))

		// Create selection button
		btnLabel := m
		if len(btnLabel) > 28 {
			btnLabel = btnLabel[:25] + "..."
		}
		if isDef {
			btnLabel = "⭐ " + btnLabel
		}
		btn := menu.Data(btnLabel, fmt.Sprintf("mod_set_m_%s__%d", provName, startIdx+i))
		rows = append(rows, menu.Row(btn))
	}

	for _, r := range rows {
		menu.Inline(r)
	}

	// Pagination Navigation row
	var navRow []tele.Btn
	if page > 0 {
		navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("mod_p_prev_%s_%d", provName, page-1)))
	}
	navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "mod_noop"))
	if page < totalPages-1 {
		navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("mod_p_next_%s_%d", provName, page+1)))
	}
	menu.Inline(menu.Row(navRow...))

	btnBackProv := menu.Data("⬅️ Daftar Provider", "mod_menu_providers")
	btnBackMain := menu.Data("🏠 Menu Model", "mod_main")
	menu.Inline(menu.Row(btnBackProv, btnBackMain))

	return sb.String(), menu
}

func (ui *ModelUI) getAllModelsForProvider(p provider.Provider) []string {
	seen := make(map[string]bool)
	var list []string

	if def := strings.TrimSpace(p.DefaultModel()); def != "" {
		list = append(list, def)
		seen[strings.ToLower(def)] = true
	}

	for _, m := range p.Models() {
		m = strings.TrimSpace(m)
		if m != "" && !seen[strings.ToLower(m)] {
			seen[strings.ToLower(m)] = true
			list = append(list, m)
		}
	}

	sort.Strings(list)
	return list
}

// HandleModelCommand handles `/model` and `/models` CLI command
func (ui *ModelUI) HandleModelCommand(c tele.Context) error {
	args := c.Args()
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)

	if len(args) == 0 {
		return c.Reply(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	target := strings.TrimSpace(args[0])
	scope := ui.getScope(userID)

	// Check if first arg specifies scope
	if strings.EqualFold(target, "global") {
		if len(args) == 1 {
			ui.setScope(userID, "global")
			return c.Reply("🌐 Target scope diset ke <b>Global</b>.", tele.ModeHTML)
		}
		scope = "global"
		target = strings.TrimSpace(args[1])
	} else if strings.EqualFold(target, "chat") || strings.EqualFold(target, "pm") {
		if len(args) == 1 {
			ui.setScope(userID, "chat")
			return c.Reply("💬 Target scope diset ke <b>Chat Ini</b>.", tele.ModeHTML)
		}
		scope = "chat"
		target = strings.TrimSpace(args[1])
	}

	// 1. Reset / Default
	if strings.EqualFold(target, "default") || strings.EqualFold(target, "reset") || strings.EqualFold(target, "auto") {
		return ui.applyModelOverride(c, scope, chatIDStr, "")
	}

	// 2. Combo
	if strings.EqualFold(target, "combo") && len(args) >= 2 {
		comboName := strings.TrimSpace(args[1])
		return ui.applyModelOverride(c, scope, chatIDStr, comboName)
	}

	// 3. Provider + Model (e.g. `/model 9router gpt-4o`)
	if len(args) >= 2 {
		provName := args[0]
		modelName := strings.TrimSpace(args[1])
		if _, ok := ui.providerManager.Get(provName); ok {
			return ui.applyModelOverride(c, scope, chatIDStr, modelName)
		}
	}

	// 4. Direct combo name or model name
	return ui.applyModelOverride(c, scope, chatIDStr, target)
}

func (ui *ModelUI) saveModelOverride(scope, chatIDStr, modelOverride string) (string, error) {
	scopeType := "chat"
	scopeID := chatIDStr
	scopeLabel := "Chat PM Admin Ini"

	if scope == "global" {
		scopeType = "global"
		scopeID = "system"
		scopeLabel = "Global (Seluruh Sistem)"
	}

	pol, err := ui.db.GetPolicy(scopeType, scopeID)
	if err != nil || pol == nil {
		pol = &storage.PolicyRecord{
			Scope:   scopeType,
			ScopeID: scopeID,
		}
	}

	pol.ModelOverride = modelOverride
	if err := ui.db.SavePolicy(pol); err != nil {
		return "", err
	}

	if modelOverride == "" {
		return fmt.Sprintf("✅ Model untuk <b>%s</b> berhasil direset ke <b>Default / Auto Router</b>!", scopeLabel), nil
	}

	if combo, ok := ui.providerManager.GetCombo(modelOverride); ok {
		return fmt.Sprintf("✅ Model untuk <b>%s</b> berhasil diubah ke Combo: <code>%s</code> (%d targets)!", scopeLabel, html.EscapeString(combo.Name), len(combo.Targets)), nil
	}

	return fmt.Sprintf("✅ Model untuk <b>%s</b> berhasil diubah ke model: <code>%s</code>!", scopeLabel, html.EscapeString(modelOverride)), nil
}

func (ui *ModelUI) applyModelOverride(c tele.Context, scope, chatIDStr, modelOverride string) error {
	msg, err := ui.saveModelOverride(scope, chatIDStr, modelOverride)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan konfigurasi model: %v", html.EscapeString(err.Error())))
	}
	return c.Reply(msg, tele.ModeHTML)
}

// HandleSetDefaultCallback resets model override
func (ui *ModelUI) HandleSetDefaultCallback(c tele.Context) error {
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	_, err := ui.saveModelOverride(scope, chatIDStr, "")
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Model direset ke Default"})
	}
	return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
}

// HandleSetComboCallback sets combo as active override
func (ui *ModelUI) HandleSetComboCallback(c tele.Context, comboName string) error {
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	_, err := ui.saveModelOverride(scope, chatIDStr, comboName)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🔀 Combo '%s' aktif!", comboName)})
	}
	return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
}

// HandleSetModelCallback sets specific model from provider as active override
func (ui *ModelUI) HandleSetModelCallback(c tele.Context, provName string, modelIndex int) error {
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	p, ok := ui.providerManager.Get(provName)
	if !ok || p == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Provider tidak ditemukan"})
		return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	allModels := ui.getAllModelsForProvider(p)
	if modelIndex < 0 || modelIndex >= len(allModels) {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Model tidak valid"})
		return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	chosenModel := allModels[modelIndex]
	_, err := ui.saveModelOverride(scope, chatIDStr, chosenModel)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🎯 Model '%s' aktif!", chosenModel)})
	}
	return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
}

// HandleToggleScopeCallback toggles between chat PM and global scope
func (ui *ModelUI) HandleToggleScopeCallback(c tele.Context) error {
	userID := c.Sender().ID
	scope := ui.getScope(userID)
	if scope == "chat" {
		ui.setScope(userID, "global")
		_ = c.Respond(&tele.CallbackResponse{Text: "🌐 Scope: Global"})
	} else {
		ui.setScope(userID, "chat")
		_ = c.Respond(&tele.CallbackResponse{Text: "💬 Scope: Chat Ini"})
	}
	return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
}
