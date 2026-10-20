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
	ModelUIStepPickFallbackVisionProvider
	ModelUIStepPickFallbackVisionModel
	ModelUIStepPickFallbackTTSProvider
	ModelUIStepPickFallbackTTSModel
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

	lower := strings.ToLower(modelName)
	// Mode Resilient Provider: "provider:dahl" or "resilient:dahl"
	if strings.HasPrefix(lower, "provider:") || strings.HasPrefix(lower, "resilient:") {
		colonIdx := strings.Index(modelName, ":")
		provPart := strings.TrimSpace(modelName[colonIdx+1:])
		if p, ok := ui.providerManager.Get(provPart); ok && p != nil {
			allModels := ui.getAllModelsForProvider(p)
			var backupModels []string
			for _, m := range allModels {
				if !strings.EqualFold(m, p.DefaultModel()) {
					backupModels = append(backupModels, m)
				}
			}
			backupStr := "Tidak ada cadangan"
			if len(backupModels) > 0 {
				backupStr = fmt.Sprintf("<code>%s</code>", html.EscapeString(backupModels[0]))
				if len(backupModels) > 1 {
					backupStr += fmt.Sprintf(" (+%d model lainnya)", len(backupModels)-1)
				}
			}
			return fmt.Sprintf("🛡️ <b>Provider Resilient:</b> <code>%s</code> (Auto Model Failback)\n   • <i>Model Utama:</i> <code>%s</code> (Default)\n   • <i>Cadangan:</i> %s",
				html.EscapeString(p.Name()), html.EscapeString(p.DefaultModel()), backupStr)
		}
		return fmt.Sprintf("🛡️ <b>Provider Resilient:</b> <code>%s</code> (Auto Model Failback)", html.EscapeString(provPart))
	}

	// Specific Model Binding: "provider:model" (e.g. "dahl:deepseek-ai/DeepSeek-V4-Flash-0731")
	if strings.Contains(modelName, ":") && !strings.HasPrefix(lower, "combo:") {
		parts := strings.SplitN(modelName, ":", 2)
		if p, ok := ui.providerManager.Get(parts[0]); ok && p != nil {
			return fmt.Sprintf("🎯 <b>Custom Model:</b> <code>%s</code>\n   • <i>Provider:</i> 🤖 <b>%s</b>", html.EscapeString(parts[1]), html.EscapeString(p.Name()))
		}
	}

	return fmt.Sprintf("🎯 <b>Custom Model:</b> <code>%s</code>", html.EscapeString(modelName))
}

func (ui *ModelUI) formatShortDesc(modelName string) string {
	if modelName == "" {
		return "🔄 Default Auto"
	}
	if combo, ok := ui.providerManager.GetCombo(modelName); ok {
		return fmt.Sprintf("🔀 <code>%s</code> (Combo)", html.EscapeString(combo.Name))
	}
	lower := strings.ToLower(modelName)
	if strings.HasPrefix(lower, "provider:") || strings.HasPrefix(lower, "resilient:") {
		colonIdx := strings.Index(modelName, ":")
		provPart := strings.TrimSpace(modelName[colonIdx+1:])
		return fmt.Sprintf("🛡️ <code>%s</code> (Resilient)", html.EscapeString(provPart))
	}
	if strings.Contains(modelName, ":") && !strings.HasPrefix(lower, "combo:") {
		parts := strings.SplitN(modelName, ":", 2)
		return fmt.Sprintf("🎯 <code>%s</code> [%s]", html.EscapeString(parts[1]), html.EscapeString(parts[0]))
	}
	return fmt.Sprintf("🎯 <code>%s</code>", html.EscapeString(modelName))
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
	globShort := ui.formatShortDesc(globOverride)
	chatShort := "🔄 Inherit Global"
	if chatOverride != "" {
		chatShort = fmt.Sprintf("%s (Khusus Chat Ini)", ui.formatShortDesc(chatOverride))
	}
	sb.WriteString(fmt.Sprintf("• 🌐 <b>Global:</b> %s\n", globShort))
	sb.WriteString(fmt.Sprintf("• 💬 <b>Chat Ini:</b> %s\n\n", chatShort))

	// Fallback Models Summary
	sb.WriteString("🛡️ <b>Model Failback Terkonfigurasi:</b>\n")
	visLabel := "<i>(Belum Ditetapkan)</i>"
	if scope == "global" {
		if globPol != nil && globPol.FallbackVisionModel != "" {
			visLabel = fmt.Sprintf("<code>%s</code>", html.EscapeString(globPol.FallbackVisionModel))
		}
	} else {
		if chatPol != nil && chatPol.FallbackVisionModel != "" {
			visLabel = fmt.Sprintf("<code>%s</code> (Khusus Chat Ini)", html.EscapeString(chatPol.FallbackVisionModel))
		} else if globPol != nil && globPol.FallbackVisionModel != "" {
			visLabel = fmt.Sprintf("<code>%s</code> <i>(Inherit Global)</i>", html.EscapeString(globPol.FallbackVisionModel))
		}
	}
	sb.WriteString(fmt.Sprintf("• 👁️ <b>Vision Fallback:</b> %s\n", visLabel))

	ttsLabel := "<i>(Belum Ditetapkan)</i>"
	if scope == "global" {
		if globPol != nil && globPol.FallbackAudioModel != "" {
			ttsLabel = fmt.Sprintf("<code>%s</code>", html.EscapeString(globPol.FallbackAudioModel))
		}
	} else {
		if chatPol != nil && chatPol.FallbackAudioModel != "" {
			ttsLabel = fmt.Sprintf("<code>%s</code> (Khusus Chat Ini)", html.EscapeString(chatPol.FallbackAudioModel))
		} else if globPol != nil && globPol.FallbackAudioModel != "" {
			ttsLabel = fmt.Sprintf("<code>%s</code> <i>(Inherit Global)</i>", html.EscapeString(globPol.FallbackAudioModel))
		}
	}
	sb.WriteString(fmt.Sprintf("• 🎙️ <b>TTS Fallback:</b> %s\n\n", ttsLabel))

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
	btnFallback := menu.Data("🛡️ Model Failback (Vision & TTS)", "mod_menu_fallback")
	btnRefresh := menu.Data("🔄 Refresh", "mod_refresh")
	btnBack := menu.Data("⬅️ Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnDefault),
		menu.Row(btnCombos, btnProviders),
		menu.Row(btnFallback),
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
	sb.WriteString("🛡️ <i>Klik tombol <b>Auto Resilient</b> untuk rotasi failback otomatis saat limit (429), atau pilih salah satu model spesifik di bawah:</i>\n\n")

	var rows []tele.Row

	// Tombol Utama: Gunakan Provider Ini (Auto Resilient Mode)
	btnResilient := menu.Data("🛡️ Gunakan Provider Ini (Auto Resilient)", fmt.Sprintf("mod_set_prov_%s", provName))
	rows = append(rows, menu.Row(btnResilient))

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

	enabledMap := make(map[string]bool)
	for _, m := range p.Models() {
		enabledMap[strings.ToLower(strings.TrimSpace(m))] = true
	}

	def := strings.TrimSpace(p.DefaultModel())
	defEnabled := def != "" && (len(enabledMap) == 0 || enabledMap[strings.ToLower(def)])
	if defEnabled {
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
	if defEnabled {
		list = append(list, def)
	}
	list = append(list, rest...)
	if len(list) == 0 && def != "" {
		list = []string{def}
	}
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
			if msgText == "0" || strings.EqualFold(msgText, "auto") || strings.EqualFold(msgText, "resilient") {
				ui.CancelSession(userID)
				overrideVal := fmt.Sprintf("provider:%s", p.Name())
				msg, err := ui.saveModelOverride(scope, chatIDStr, overrideVal)
				if err != nil {
					return true, c.Reply(fmt.Sprintf("❌ Gagal: %v", err))
				}
				_ = c.Reply(msg, tele.ModeHTML)
				return true, c.Send(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
			}

			for _, m := range allModels {
				if strings.EqualFold(m, msgText) {
					chosenModel = m
					break
				}
			}
		}

		if chosenModel != "" {
			ui.CancelSession(userID)
			overrideVal := fmt.Sprintf("%s:%s", p.Name(), chosenModel)
			msg, err := ui.saveModelOverride(scope, chatIDStr, overrideVal)
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

	// 3. Provider + Model (e.g. `/model dahl deepseek-ai/DeepSeek-V4-Flash-0731` or `/model 9router gpt-4o`)
	if len(args) >= 2 {
		provName := args[0]
		modelName := strings.TrimSpace(args[1])
		if strings.EqualFold(provName, "resilient") || strings.EqualFold(provName, "provider") {
			if p, ok := ui.providerManager.Get(modelName); ok && p != nil {
				return ui.applyModelOverride(c, scope, chatIDStr, fmt.Sprintf("provider:%s", p.Name()))
			}
		}
		if p, ok := ui.providerManager.Get(provName); ok && p != nil {
			return ui.applyModelOverride(c, scope, chatIDStr, fmt.Sprintf("%s:%s", p.Name(), modelName))
		}
		// If 2 args provided and args[0] didn't match provider directly, treat args[1] as the model name
		return ui.applyModelOverride(c, scope, chatIDStr, modelName)
	}

	// 4. Single argument: check if it's a provider name without model (e.g. `/model dahl` or `/model gemini`)
	if p, ok := ui.providerManager.Get(target); ok && p != nil {
		// Set to Provider Resilient mode (e.g. "provider:dahl")
		return ui.applyModelOverride(c, scope, chatIDStr, fmt.Sprintf("provider:%s", p.Name()))
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

	pol := ui.db.GetOrCreatePolicy(scopeType, scopeID)

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

	lowerOverride := strings.ToLower(modelOverride)
	if strings.HasPrefix(lowerOverride, "provider:") || strings.HasPrefix(lowerOverride, "resilient:") {
		colonIdx := strings.Index(modelOverride, ":")
		provPart := strings.TrimSpace(modelOverride[colonIdx+1:])
		return fmt.Sprintf("✅ <b>%s</b> berhasil diubah ke <b>Mode Resilient: %s</b> (Auto Model Failback)!", scopeLabel, html.EscapeString(provPart)), nil
	}

	if strings.Contains(modelOverride, ":") && !strings.HasPrefix(lowerOverride, "combo:") {
		parts := strings.SplitN(modelOverride, ":", 2)
		return fmt.Sprintf("✅ Model untuk <b>%s</b> berhasil diubah ke <code>%s</code> (Provider: <b>%s</b>)!", scopeLabel, html.EscapeString(parts[1]), html.EscapeString(parts[0])), nil
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
	overrideVal := fmt.Sprintf("%s:%s", p.Name(), chosenModel)
	_, err := ui.saveModelOverride(scope, chatIDStr, overrideVal)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🎯 Model '%s' aktif (%s)!", chosenModel, p.Name())})
	}
	return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
}

// HandleSetProviderResilientCallback sets entire provider in auto resilient mode
func (ui *ModelUI) HandleSetProviderResilientCallback(c tele.Context, provName string) error {
	userID := c.Sender().ID
	ui.CancelSession(userID)
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	p, ok := ui.providerManager.Get(provName)
	if !ok || p == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Provider tidak ditemukan"})
		return c.EditOrSend(ui.RenderModelDashboard(c), ui.ModelMenuKeyboard(userID), tele.ModeHTML)
	}

	overrideVal := fmt.Sprintf("provider:%s", p.Name())
	_, err := ui.saveModelOverride(scope, chatIDStr, overrideVal)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🛡️ Mode Resilient: %s aktif!", p.Name())})
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

// ==============================================================================
// MODEL FAILBACK (VISION & TTS) UI METHODS
// ==============================================================================

// RenderFallbackDashboard renders the interactive menu for configuring Vision and TTS fallbacks
func (ui *ModelUI) RenderFallbackDashboard(c tele.Context) (string, *tele.ReplyMarkup) {
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

	visVal := "<i>(Belum Ditetapkan / Nonaktif)</i>"
	if scope == "global" {
		if globPol != nil && globPol.FallbackVisionModel != "" {
			visVal = fmt.Sprintf("<code>%s</code>", html.EscapeString(globPol.FallbackVisionModel))
		}
	} else {
		if chatPol != nil && chatPol.FallbackVisionModel != "" {
			visVal = fmt.Sprintf("<code>%s</code> (Khusus Chat Ini)", html.EscapeString(chatPol.FallbackVisionModel))
		} else if globPol != nil && globPol.FallbackVisionModel != "" {
			visVal = fmt.Sprintf("<code>%s</code> <i>(Inherit Global)</i>", html.EscapeString(globPol.FallbackVisionModel))
		}
	}

	ttsVal := "<i>(Belum Ditetapkan / Nonaktif)</i>"
	if scope == "global" {
		if globPol != nil && globPol.FallbackAudioModel != "" {
			ttsVal = fmt.Sprintf("<code>%s</code>", html.EscapeString(globPol.FallbackAudioModel))
		}
	} else {
		if chatPol != nil && chatPol.FallbackAudioModel != "" {
			ttsVal = fmt.Sprintf("<code>%s</code> (Khusus Chat Ini)", html.EscapeString(chatPol.FallbackAudioModel))
		} else if globPol != nil && globPol.FallbackAudioModel != "" {
			ttsVal = fmt.Sprintf("<code>%s</code> <i>(Inherit Global)</i>", html.EscapeString(globPol.FallbackAudioModel))
		}
	}

	var sb strings.Builder
	sb.WriteString("🛡️ <b>PENGATURAN MODEL FAILBACK (VISION & TTS)</b>\n\n")
	sb.WriteString(fmt.Sprintf("📌 <b>Target Scope:</b> <code>%s</code>\n\n", scopeLabel))
	sb.WriteString("Jika model aktif berbasis teks murni (contoh: <code>deepseek-v4-flash</code>) dan pengguna mengirim gambar atau audio, sistem akan otomatis mengalihkan eksekusi ke model failback ini:\n\n")

	sb.WriteString(fmt.Sprintf("👁️ <b>Failback Vision (Gambar/Foto):</b>\n• Status: %s\n\n", visVal))
	sb.WriteString(fmt.Sprintf("🎙️ <b>Failback TTS (Suara/Audio):</b>\n• Status: %s\n\n", ttsVal))
	sb.WriteString("💡 <i>Menu pemilihan model di bawah HANYA menampilkan model-model yang kompatibel dengan kapabilitas terkait:</i>\n")

	menu := &tele.ReplyMarkup{}
	btnSetVis := menu.Data("👁️ Atur Failback Vision", "mod_fb_vis_provs")
	btnSetTTS := menu.Data("🎙️ Atur Failback TTS", "mod_fb_tts_provs")
	btnResetVis := menu.Data("❌ Reset Vision", "mod_fb_reset_vis")
	btnResetTTS := menu.Data("❌ Reset TTS", "mod_fb_reset_tts")
	btnBack := menu.Data("⬅️ Kembali ke Menu Model", "mod_main")

	menu.Inline(
		menu.Row(btnSetVis, btnSetTTS),
		menu.Row(btnResetVis, btnResetTTS),
		menu.Row(btnBack),
	)

	return sb.String(), menu
}

// RenderFallbackProvidersList lists providers that contain models supporting the requested mode (vis or tts)
func (ui *ModelUI) RenderFallbackProvidersList(c tele.Context, mode string) (string, *tele.ReplyMarkup) {
	cat := provider.GetCatalog()
	allProvs := ui.providerManager.ListAll()
	menu := &tele.ReplyMarkup{}

	modeTitle := "VISION (GAMBAR)"
	modeIcon := "👁️"
	if mode == "tts" {
		modeTitle = "TTS (SUARA/AUDIO)"
		modeIcon = "🎙️"
	}

	var validProvs []provider.Provider
	var counts []int

	for _, p := range allProvs {
		models := ui.getAllModelsForProvider(p)
		var matched []string
		if mode == "vis" {
			matched = cat.FilterVisionModels(models)
		} else {
			matched = cat.FilterAudioModels(models)
		}
		if len(matched) > 0 {
			validProvs = append(validProvs, p)
			counts = append(counts, len(matched))
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>PILIH PROVIDER FAILBACK %s</b>\n\n", modeIcon, modeTitle))

	if len(validProvs) == 0 {
		sb.WriteString("<i>(Tidak ada provider aktif yang memiliki model dengan kapabilitas ini)</i>\n\n")
		btnBack := menu.Data("⬅️ Kembali ke Menu Failback", "mod_menu_fallback")
		menu.Inline(menu.Row(btnBack))
		return sb.String(), menu
	}

	sb.WriteString("Pilih salah satu provider AI di bawah untuk melihat daftar model yang mendukung:\n\n")

	var rows []tele.Row
	var curRow []tele.Btn

	for i, p := range validProvs {
		count := counts[i]
		sb.WriteString(fmt.Sprintf("%d. <b>%s</b> (<code>%d model kompatibel</code>)\n", i+1, html.EscapeString(p.Name()), count))
		btnText := fmt.Sprintf("🤖 %s (%d)", p.Name(), count)
		btn := menu.Data(btnText, fmt.Sprintf("mod_fb_prov_%s_%s", mode, p.Name()))
		curRow = append(curRow, btn)
		if len(curRow) == 2 {
			rows = append(rows, menu.Row(curRow...))
			curRow = []tele.Btn{}
		}
	}
	if len(curRow) > 0 {
		rows = append(rows, menu.Row(curRow...))
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Failback", "mod_menu_fallback")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return sb.String(), menu
}

// RenderFallbackProviderModels renders strictly filtered models for the selected provider
func (ui *ModelUI) RenderFallbackProviderModels(c tele.Context, mode, provName string, page int) (string, *tele.ReplyMarkup) {
	p, ok := ui.providerManager.Get(provName)
	menu := &tele.ReplyMarkup{}

	if !ok || p == nil {
		return "❌ Provider tidak ditemukan atau sedang nonaktif.", menu
	}

	modeTitle := "VISION (GAMBAR)"
	modeIcon := "👁️"
	if mode == "tts" {
		modeTitle = "TTS (SUARA/AUDIO)"
		modeIcon = "🎙️"
	}

	allModels := ui.getAllModelsForProvider(p)
	cat := provider.GetCatalog()

	// STRICT CAPABILITY FILTERING
	var filteredModels []string
	if mode == "vis" {
		filteredModels = cat.FilterVisionModels(allModels)
	} else {
		filteredModels = cat.FilterAudioModels(allModels)
	}

	totalModels := len(filteredModels)
	if totalModels == 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%s <b>PROVIDER: %s</b>\n\n", modeIcon, html.EscapeString(provName)))
		sb.WriteString(fmt.Sprintf("<i>(Tidak ada model %s yang terdeteksi pada provider ini)</i>\n", modeTitle))
		btnBack := menu.Data("⬅️ Pilih Provider Lain", fmt.Sprintf("mod_fb_%s_provs", mode))
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

	pageModels := filteredModels[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>MODEL FAILBACK %s: %s</b>\n", modeIcon, modeTitle, html.EscapeString(strings.ToUpper(provName))))
	sb.WriteString(fmt.Sprintf("Halaman <code>%d/%d</code> (Total: <code>%d model kompatibel</code>)\n\n", page+1, totalPages, totalModels))
	sb.WriteString("✅ <i>Daftar di bawah telah disaring otomatis hanya menampilkan model yang mendukung fitur ini:</i>\n\n")

	var rows []tele.Row

	for i, m := range pageModels {
		globalIdx := startIdx + i + 1
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", globalIdx, html.EscapeString(m)))

		btnLabel := m
		if len(btnLabel) > 26 {
			btnLabel = btnLabel[:23] + "..."
		}
		btnLabel = modeIcon + " " + btnLabel

		btn := menu.Data(btnLabel, fmt.Sprintf("mod_fb_set_%s_%s__%d", mode, provName, startIdx+i))
		rows = append(rows, menu.Row(btn))
	}

	// Pagination buttons
	if totalPages > 1 {
		var navRow []tele.Btn
		if page > 0 {
			navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("mod_fb_p_prev_%s_%s_%d", mode, provName, page-1)))
		}
		navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "mod_noop"))
		if page < totalPages-1 {
			navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("mod_fb_p_next_%s_%s_%d", mode, provName, page+1)))
		}
		rows = append(rows, navRow)
	}

	btnBackProv := menu.Data("⬅️ Daftar Provider", fmt.Sprintf("mod_fb_%s_provs", mode))
	btnBackMain := menu.Data("🏠 Menu Failback", "mod_menu_fallback")
	rows = append(rows, menu.Row(btnBackProv, btnBackMain))
	menu.Inline(rows...)

	return sb.String(), menu
}

// HandleSetFallbackModelCallback saves the selected fallback model
func (ui *ModelUI) HandleSetFallbackModelCallback(c tele.Context, mode, provName string, modelIndex int) error {
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	p, ok := ui.providerManager.Get(provName)
	if !ok || p == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Provider tidak ditemukan"})
		txt, kb := ui.RenderFallbackDashboard(c)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}

	allModels := ui.getAllModelsForProvider(p)
	cat := provider.GetCatalog()

	var filtered []string
	if mode == "vis" {
		filtered = cat.FilterVisionModels(allModels)
	} else {
		filtered = cat.FilterAudioModels(allModels)
	}

	if modelIndex < 0 || modelIndex >= len(filtered) {
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Model tidak valid"})
		txt, kb := ui.RenderFallbackDashboard(c)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}

	chosenModel := filtered[modelIndex]
	fullVal := fmt.Sprintf("%s:%s", p.Name(), chosenModel)

	msg, err := ui.saveFallbackOverride(scope, chatIDStr, mode, fullVal)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("✅ Failback diset ke %s!", chosenModel)})
		_ = c.Reply(msg, tele.ModeHTML)
	}

	txt, kb := ui.RenderFallbackDashboard(c)
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

// HandleResetFallbackCallback resets fallback model to empty (inherit/disabled)
func (ui *ModelUI) HandleResetFallbackCallback(c tele.Context, mode string) error {
	userID := c.Sender().ID
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	scope := ui.getScope(userID)

	msg, err := ui.saveFallbackOverride(scope, chatIDStr, mode, "")
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("❌ Gagal: %v", err)})
	} else {
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Failback berhasil direset"})
		_ = c.Reply(msg, tele.ModeHTML)
	}

	txt, kb := ui.RenderFallbackDashboard(c)
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *ModelUI) saveFallbackOverride(scope, chatIDStr, mode, val string) (string, error) {
	scopeType := "chat"
	scopeID := chatIDStr
	scopeLabel := "Chat PM Admin Ini"

	if scope == "global" {
		scopeType = "global"
		scopeID = "system"
		scopeLabel = "Global (Seluruh Sistem)"
	}

	pol := ui.db.GetOrCreatePolicy(scopeType, scopeID)

	modeName := "Vision (Gambar)"
	if mode == "vis" {
		pol.FallbackVisionModel = val
	} else {
		modeName = "TTS (Suara)"
		pol.FallbackAudioModel = val
	}

	if err := ui.db.SavePolicy(pol); err != nil {
		return "", err
	}

	if val == "" {
		return fmt.Sprintf("✅ Model Failback <b>%s</b> untuk <b>%s</b> berhasil direset ke <b>Default / Nonaktif</b>!", modeName, scopeLabel), nil
	}

	return fmt.Sprintf("✅ Model Failback <b>%s</b> untuk <b>%s</b> berhasil ditetapkan ke: <code>%s</code>!", modeName, scopeLabel, html.EscapeString(val)), nil
}
