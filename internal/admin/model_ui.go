package admin

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

const modelsPerPage = 8

type ModelUIStep int

const (
	ModelUIStepNone ModelUIStep = iota
	ModelUIStepPickCombo
	ModelUIStepPickProvider
	ModelUIStepPickModel
)

type ModelUISession struct {
	Step             ModelUIStep
	SelectedProvider string
	Page             int
	CreatedAt        time.Time
}

type ModelUI struct {
	db              *storage.DB
	providerManager *provider.Manager
	mu              sync.RWMutex
	userScope       map[int64]string // "chat" (default) or "global"
	userPage        map[int64]int
	userProvider    map[int64]string
	sessions        map[int64]*ModelUISession
}

func NewModelUI(db *storage.DB, pm *provider.Manager) *ModelUI {
	return &ModelUI{
		db:              db,
		providerManager: pm,
		userScope:       make(map[int64]string),
		userPage:        make(map[int64]int),
		userProvider:    make(map[int64]string),
		sessions:        make(map[int64]*ModelUISession),
	}
}

func (ui *ModelUI) CancelSession(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.sessions, userID)
}

func (ui *ModelUI) setSession(userID int64, step ModelUIStep, prov string, page int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.sessions[userID] = &ModelUISession{
		Step:             step,
		SelectedProvider: prov,
		Page:             page,
		CreatedAt:        time.Now(),
	}
}

func (ui *ModelUI) getSession(userID int64) *ModelUISession {
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	if sess, ok := ui.sessions[userID]; ok {
		return sess
	}
	return nil
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

// formatModelDesc returns a friendly label for a model/combo override
func (ui *ModelUI) formatModelDesc(modelName string) string {
	if modelName == "" {
		return "🔄 <b>Default / Auto Router</b> (Mengikuti provider & model default sistem)"
	}
	if combo, ok := ui.providerManager.GetCombo(modelName); ok {
		var targets []string
		for _, t := range combo.Targets {
			targets = append(targets, fmt.Sprintf("<code>%s/%s</code>", html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
		}
		return fmt.Sprintf("🔀 <b>Combo:</b> <code>%s</code>\n   • <i>Chain:</i> %s", html.EscapeString(combo.Name), strings.Join(targets, " ➔ "))
	}
	return fmt.Sprintf("🎯 <b>Custom Model:</b> <code>%s</code>", html.EscapeString(modelName))
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

	globPol, _ := ui.db.GetPolicy("global", "system")
	chatPol, _ := ui.db.GetPolicy("chat", chatIDStr)

	globOverride := ""
	if globPol != nil {
		globOverride = globPol.ModelOverride
	}

	chatOverride := ""
	if chatPol != nil {
		chatOverride = chatPol.ModelOverride
	}

	// Determine active description based on selected target scope
	var activeDesc string
	if scope == "global" {
		activeDesc = ui.formatModelDesc(globOverride)
	} else {
		if chatOverride != "" {
			activeDesc = fmt.Sprintf("%s\n   • <i>Status:</i> ⚠️ <b>Meng-override Global khusus chat ini</b>", ui.formatModelDesc(chatOverride))
		} else {
			inheritedLabel := "Default Router Sistem"
			if globOverride != "" {
				inheritedLabel = fmt.Sprintf("Global (%s)", globOverride)
			}
			activeDesc = fmt.Sprintf("🔄 <b>Auto / Inherit Global</b>\n   • <i>Status:</i> Mengikuti <code>%s</code>", html.EscapeString(inheritedLabel))
		}
	}

	// Providers & Combos summary
	allProvs := ui.providerManager.ListAll()
	allCombos := ui.providerManager.ListCombos()

	var sb strings.Builder
	sb.WriteString("🎛️ <b>PENGATURAN MODEL & COMBO AI</b>\n\n")
	sb.WriteString(fmt.Sprintf("📌 <b>Target Scope Yang Diedit:</b> <code>%s</code>\n", scopeLabel))
	sb.WriteString(fmt.Sprintf("⚡ <b>Model Aktif Scope Ini:</b>\n%s\n\n", activeDesc))

	sb.WriteString("📊 <b>Ringkasan Hirarki Scope Saat Ini:</b>\n")
	globShort := "🔄 Default Auto"
	if globOverride != "" {
		globShort = fmt.Sprintf("<code>%s</code>", html.EscapeString(globOverride))
	}
	chatShort := "🔄 Inherit Global"
	if chatOverride != "" {
		chatShort = fmt.Sprintf("🎯 <code>%s</code> (Khusus Chat Ini)", html.EscapeString(chatOverride))
	}
	sb.WriteString(fmt.Sprintf("• 🌐 <b>Global:</b> %s\n", globShort))
	sb.WriteString(fmt.Sprintf("• 💬 <b>Chat Ini:</b> %s\n\n", chatShort))

	sb.WriteString("📦 <b>Ketersediaan Engine:</b>\n")
	sb.WriteString(fmt.Sprintf("• AI Providers Aktif: <code>%d provider</code>\n", len(allProvs)))
	sb.WriteString(fmt.Sprintf("• Fallback Combos: <code>%d combo</code>\n\n", len(allCombos)))

	sb.WriteString("💡 <i>Gunakan tombol di bawah untuk memilih model/combo untuk scope yang dipilih:</i>\n")
	sb.WriteString("• <code>/model default</code> - Reset scope ini ke auto/inherit\n")
	sb.WriteString("• <code>/model &lt;combo_name&gt;</code> - Aktifkan combo\n")
	sb.WriteString("• <code>/model &lt;provider&gt; &lt;model&gt;</code> - Pilih model")

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

const combosPerPage = 6

// RenderCombosList renders available combos for selection with pagination
func (ui *ModelUI) RenderCombosList(c tele.Context, page int) (string, *tele.ReplyMarkup) {
	combos := ui.providerManager.ListCombos()
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	totalCombos := len(combos)
	if totalCombos == 0 {
		if c.Sender() != nil {
			ui.setSession(c.Sender().ID, ModelUIStepPickCombo, "", 0)
		}
		var sb strings.Builder
		sb.WriteString("🔀 <b>PILIH MODEL COMBO / CHAIN</b>\n\n")
		sb.WriteString("<i>(Belum ada combo yang terdaftar. Buat dengan <code>/addcombo</code> atau <code>/combowizard</code>)</i>\n\n")
		btnBack := menu.Data("⬅️ Kembali ke Menu Model", "mod_main")
		menu.Inline(menu.Row(btnBack))
		return sb.String(), menu
	}

	totalPages := (totalCombos + combosPerPage - 1) / combosPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	if c.Sender() != nil {
		ui.setSession(c.Sender().ID, ModelUIStepPickCombo, "", page)
	}

	startIdx := page * combosPerPage
	endIdx := startIdx + combosPerPage
	if endIdx > totalCombos {
		endIdx = totalCombos
	}
	pageCombos := combos[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString("🔀 <b>PILIH MODEL COMBO / CHAIN</b>\n")
	if totalPages > 1 {
		sb.WriteString(fmt.Sprintf("Halaman <code>%d/%d</code> (Total: <code>%d combo</code>)\n\n", page+1, totalPages, totalCombos))
	} else {
		sb.WriteString("\n")
	}
	sb.WriteString("Pilih salah satu fallback combo di bawah untuk diterapkan:\n\n")

	var curRow []tele.Btn
	for i, cRec := range pageCombos {
		globalIdx := startIdx + i + 1
		var targets []string
		for _, t := range cRec.Targets {
			targets = append(targets, fmt.Sprintf("%s/%s", t.ProviderID, t.Model))
		}
		sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n   • <i>Targets:</i> <code>%s</code>\n", globalIdx, html.EscapeString(cRec.Name), html.EscapeString(strings.Join(targets, " ➔ "))))

		btn := menu.Data(fmt.Sprintf("🔀 %s", cRec.Name), fmt.Sprintf("mod_set_c_%s", cRec.Name))
		curRow = append(curRow, btn)
		if len(curRow) == 2 {
			rows = append(rows, menu.Row(curRow...))
			curRow = nil
		}
	}
	if len(curRow) > 0 {
		rows = append(rows, menu.Row(curRow...))
	}

	if totalPages > 1 {
		var navRow []tele.Btn
		if page > 0 {
			navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("mod_c_prev_%d", page-1)))
		}
		navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "mod_noop"))
		if page < totalPages-1 {
			navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("mod_c_next_%d", page+1)))
		}
		rows = append(rows, menu.Row(navRow...))
	}

	sb.WriteString("\n💡 <i>Klik tombol di bawah atau balas chat dengan nomor/nama combo pilihan Anda:</i>\n")

	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "mod_main")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return sb.String(), menu
}

// RenderProvidersList renders available providers to pick models from
func (ui *ModelUI) RenderProvidersList(c tele.Context) (string, *tele.ReplyMarkup) {
	if c.Sender() != nil {
		ui.setSession(c.Sender().ID, ModelUIStepPickProvider, "", 0)
	}

	providers := ui.providerManager.ListAll()
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	var sb strings.Builder
	sb.WriteString("🤖 <b>PILIH PROVIDER AI</b>\n\n")

	if len(providers) == 0 {
		sb.WriteString("<i>(Tidak ada provider AI aktif yang terdaftar)</i>\n\n")
	} else {
		sb.WriteString("Silakan pilih provider untuk melihat seluruh model yang tersedia:\n\n")
		var curRow []tele.Btn

		for i, p := range providers {
			allModels := ui.getAllModelsForProvider(p)
			sb.WriteString(fmt.Sprintf("%d. <b>%s</b> (<code>%d model</code>)\n", i+1, html.EscapeString(p.Name()), len(allModels)))
			btnText := fmt.Sprintf("🤖 %s (%d)", p.Name(), len(allModels))
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
		sb.WriteString("\n💡 <i>Klik tombol di bawah atau balas chat dengan nomor/nama provider pilihan Anda:</i>\n")
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "mod_main")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return sb.String(), menu
}

// RenderProviderModels renders full models available for a specific provider with pagination
func (ui *ModelUI) RenderProviderModels(c tele.Context, provName string, page int) (string, *tele.ReplyMarkup) {
	p, ok := ui.providerManager.Get(provName)
	menu := &tele.ReplyMarkup{}

	if !ok || p == nil {
		if c.Sender() != nil {
			ui.CancelSession(c.Sender().ID)
		}
		return "❌ Provider tidak ditemukan atau sedang nonaktif.", menu
	}

	allModels := ui.getAllModelsForProvider(p)
	totalModels := len(allModels)
	if totalModels == 0 {
		if c.Sender() != nil {
			ui.CancelSession(c.Sender().ID)
		}
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

	if c.Sender() != nil {
		ui.setSession(c.Sender().ID, ModelUIStepPickModel, provName, page)
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
	sb.WriteString("Pilih salah satu model di bawah untuk mengaktifkannya:\n\n")

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

	sb.WriteString("\n💡 <i>Klik tombol atau balas chat dengan nomor/nama model pilihan Anda:</i>\n")

	// Pagination Navigation row
	var navRow []tele.Btn
	if page > 0 {
		navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("mod_p_prev_%s_%d", provName, page-1)))
	}
	navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "mod_noop"))
	if page < totalPages-1 {
		navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("mod_p_next_%s_%d", provName, page+1)))
	}
	if len(navRow) > 0 {
		rows = append(rows, menu.Row(navRow...))
	}

	btnBackProv := menu.Data("⬅️ Daftar Provider", "mod_menu_providers")
	btnBackMain := menu.Data("🏠 Menu Model", "mod_main")
	rows = append(rows, menu.Row(btnBackProv, btnBackMain))

	menu.Inline(rows...)

	return sb.String(), menu
}

func (ui *ModelUI) getAllModelsForProvider(p provider.Provider) []string {
	seen := make(map[string]bool)
	var rest []string

	def := strings.TrimSpace(p.DefaultModel())
	if def != "" {
		seen[strings.ToLower(def)] = true
	}

	for _, m := range p.Models() {
		m = strings.TrimSpace(m)
		if m != "" && !seen[strings.ToLower(m)] {
			seen[strings.ToLower(m)] = true
			rest = append(rest, m)
		}
	}

	sort.Strings(rest)

	var list []string
	if def != "" {
		list = append(list, def)
	}
	list = append(list, rest...)
	return list
}

// HandleTextMessage handles interactive text messages when picking combos, providers, or models
func (ui *ModelUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID
	sess := ui.getSession(userID)
	if sess == nil || sess.Step == ModelUIStepNone {
		return false, nil
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "" {
		return false, nil
	}

	// Cancellation check
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") || msgText == "/stop" {
		ui.CancelSession(userID)
		_ = c.Reply("❌ Pemilihan model/combo dibatalkan.")
		return true, c.Send(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	switch sess.Step {
	case ModelUIStepPickCombo:
		combos := ui.providerManager.ListCombos()
		if len(combos) == 0 {
			ui.CancelSession(userID)
			return false, nil
		}

		// Try matching by 1-based number index
		if num, err := strconv.Atoi(msgText); err == nil && num >= 1 && num <= len(combos) {
			targetCombo := combos[num-1]
			ui.CancelSession(userID)
			msg, err := ui.saveModelOverride(scope, chatIDStr, targetCombo.Name)
			if err != nil {
				return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan combo: %v", err))
			}
			_ = c.Reply(msg, tele.ModeHTML)
			return true, c.Send(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
		}

		// Try matching by combo name (case-insensitive)
		for _, cRec := range combos {
			if strings.EqualFold(cRec.Name, msgText) {
				ui.CancelSession(userID)
				msg, err := ui.saveModelOverride(scope, chatIDStr, cRec.Name)
				if err != nil {
					return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan combo: %v", err))
				}
				_ = c.Reply(msg, tele.ModeHTML)
				return true, c.Send(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
			}
		}

		return true, c.Reply(fmt.Sprintf("⚠️ Nomor atau nama combo tidak ditemukan.\n<i>Silakan ketik nomor (1-%d), nama combo, atau ketik <code>/cancel</code> untuk membatalkan.</i>", len(combos)), tele.ModeHTML)

	case ModelUIStepPickProvider:
		providers := ui.providerManager.ListAll()
		if len(providers) == 0 {
			ui.CancelSession(userID)
			return false, nil
		}

		var selectedProv provider.Provider
		// Try matching by 1-based number index
		if num, err := strconv.Atoi(msgText); err == nil && num >= 1 && num <= len(providers) {
			selectedProv = providers[num-1]
		} else {
			// Try matching by provider name
			for _, p := range providers {
				if strings.EqualFold(p.Name(), msgText) {
					selectedProv = p
					break
				}
			}
		}

		if selectedProv != nil {
			txt, kb := ui.RenderProviderModels(c, selectedProv.Name(), 0)
			return true, c.Send(txt, kb, tele.ModeHTML)
		}

		return true, c.Reply(fmt.Sprintf("⚠️ Nomor atau nama provider tidak ditemukan.\n<i>Silakan ketik nomor (1-%d), nama provider, atau ketik <code>/cancel</code> untuk membatalkan.</i>", len(providers)), tele.ModeHTML)

	case ModelUIStepPickModel:
		p, ok := ui.providerManager.Get(sess.SelectedProvider)
		if !ok || p == nil {
			ui.CancelSession(userID)
			return true, c.Reply("❌ Provider tidak ditemukan atau sedang nonaktif.")
		}

		allModels := ui.getAllModelsForProvider(p)
		if len(allModels) == 0 {
			ui.CancelSession(userID)
			return false, nil
		}

		var chosenModel string
		// Try matching by number index
		if num, err := strconv.Atoi(msgText); err == nil {
			// Check if index matches 1-based global list
			if num >= 1 && num <= len(allModels) {
				chosenModel = allModels[num-1]
			}
		}

		// Try matching by exact or case-insensitive model name
		if chosenModel == "" {
			for _, m := range allModels {
				if strings.EqualFold(m, msgText) {
					chosenModel = m
					break
				}
			}
		}

		if chosenModel != "" {
			ui.CancelSession(userID)
			msg, err := ui.saveModelOverride(scope, chatIDStr, chosenModel)
			if err != nil {
				return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan model: %v", err))
			}
			_ = c.Reply(msg, tele.ModeHTML)
			return true, c.Send(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
		}

		return true, c.Reply(fmt.Sprintf("⚠️ Model <code>%s</code> tidak ditemukan untuk provider <b>%s</b>.\n<i>Silakan ketik nomor model, nama model yang terdaftar, atau <code>/cancel</code> untuk batal.</i>", html.EscapeString(msgText), html.EscapeString(p.Name())), tele.ModeHTML)
	}

	return false, nil
}

// HandleModelCommand handles `/model` and `/models` CLI command
func (ui *ModelUI) HandleModelCommand(c tele.Context) error {
	args := c.Args()
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)

	ui.CancelSession(userID)

	if len(args) == 0 {
		return c.Reply(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	scope := ui.getScope(userID)

	// Check if first arg specifies scope
	if strings.EqualFold(args[0], "global") {
		if len(args) == 1 {
			ui.setScope(userID, "global")
			return c.Reply("🌐 Target scope diset ke <b>Global</b>.", tele.ModeHTML)
		}
		scope = "global"
		args = args[1:]
	} else if strings.EqualFold(args[0], "chat") || strings.EqualFold(args[0], "pm") {
		if len(args) == 1 {
			ui.setScope(userID, "chat")
			return c.Reply("💬 Target scope diset ke <b>Chat Ini</b>.", tele.ModeHTML)
		}
		scope = "chat"
		args = args[1:]
	}

	if len(args) == 0 {
		return c.Reply(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	target := strings.TrimSpace(args[0])

	// 1. Reset / Default
	if strings.EqualFold(target, "default") || strings.EqualFold(target, "reset") || strings.EqualFold(target, "auto") {
		return ui.applyModelOverride(c, scope, chatIDStr, "")
	}

	// 2. Combo prefix / keyword: `/model combo <name>`
	if strings.EqualFold(target, "combo") && len(args) >= 2 {
		comboName := strings.TrimSpace(args[1])
		return ui.applyModelOverride(c, scope, chatIDStr, comboName)
	}

	// 3. Provider + Model (e.g. `/model gemini gemini-2.0-flash` or `/model 9router gpt-4o`)
	if len(args) >= 2 {
		provName := args[0]
		modelName := strings.TrimSpace(args[1])
		if _, ok := ui.providerManager.Get(provName); ok {
			return ui.applyModelOverride(c, scope, chatIDStr, modelName)
		}
		// If 2 args provided and args[0] didn't match provider directly, treat args[1] as the model name
		return ui.applyModelOverride(c, scope, chatIDStr, modelName)
	}

	// 4. Single argument: check if it's a provider name without model (e.g. `/model gemini`)
	if p, ok := ui.providerManager.Get(target); ok {
		// If user typed provider name e.g. "/model gemini", set to its default model
		return ui.applyModelOverride(c, scope, chatIDStr, p.DefaultModel())
	}

	// 5. Direct combo name or model name (e.g. `/model smart` or `/model gemini-2.0-flash`)
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
	ui.CancelSession(userID)
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
	ui.CancelSession(userID)
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
	ui.CancelSession(userID)
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
