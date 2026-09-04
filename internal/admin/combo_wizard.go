package admin

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"goassistant/internal/provider"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type ComboWizardStep int

const (
	StepComboNone ComboWizardStep = iota
	StepComboEnterName
	StepComboPickProvider
	StepComboPickModel
	StepComboPickStrategy
	StepComboEditDesc
	StepComboEditAddTargetPickProv
	StepComboEditAddTargetPickMod
)

type ComboWizardSession struct {
	Step             ComboWizardStep
	IsEditing        bool
	EditingComboName string
	Name             string
	Description      string
	Targets          []storage.ComboTarget
	SelectedProvider *storage.ProviderRecord
	Strategy         string
	ProvPage         int
	ModelPage        int
	CreatedAt        time.Time
}

type ComboWizard struct {
	mu              sync.RWMutex
	db              *storage.DB
	providerManager *provider.Manager
	bot             *tele.Bot
	sessions        map[int64]*ComboWizardSession
}

func NewComboWizard(db *storage.DB, pm *provider.Manager, bot *tele.Bot) *ComboWizard {
	return &ComboWizard{
		db:              db,
		providerManager: pm,
		bot:             bot,
		sessions:        make(map[int64]*ComboWizardSession),
	}
}

// StartWizard starts the interactive combo creator
func (w *ComboWizard) StartWizard(c tele.Context) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.Lock()
	w.sessions[userID] = &ComboWizardSession{
		Step:      StepComboEnterName,
		Strategy:  "failsafe",
		CreatedAt: time.Now(),
	}
	w.mu.Unlock()

	text := "🔀 <b>WIZARD PEMBUATAN MODEL COMBO (FAILSAFE / ROTASI)</b>\n\n" +
		"Combo memungkinkan Anda menggabungkan beberapa provider & model AI ke dalam satu rantai pintar (misalnya: coba Dahl dulu, jika limit/down otomatis fallback ke 9Router).\n\n" +
		"Silakan kirimkan <b>Nama Combo</b> yang diinginkan (satu kata, huruf kecil/angka):\n" +
		"<i>Contoh: <code>smart</code>, <code>mix</code>, <code>fast</code>, <code>dahl_backup</code></i>"

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "cwiz_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// StartEditWizard starts the interactive combo editor
func (w *ComboWizard) StartEditWizard(c tele.Context, targetComboName string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	if targetComboName != "" {
		combo, err := w.db.GetCombo(strings.ToLower(targetComboName))
		if err != nil || combo == nil {
			return c.Reply(fmt.Sprintf("❌ Combo dengan nama '<code>%s</code>' tidak ditemukan.", html.EscapeString(targetComboName)), tele.ModeHTML)
		}
		w.mu.Lock()
		w.sessions[userID] = &ComboWizardSession{
			IsEditing:        true,
			EditingComboName: combo.Name,
			CreatedAt:        time.Now(),
		}
		w.mu.Unlock()
		return w.RenderComboEditDashboard(c, combo)
	}

	// Show list of existing combos
	combos, err := w.db.ListCombos()
	if err != nil || len(combos) == 0 {
		return c.Reply("⚠️ Belum ada Model Combo yang dibuat. Gunakan tombol <b>🧙‍♂️ Buat Combo Baru</b> untuk membuatnya.", tele.ModeHTML)
	}

	w.mu.Lock()
	w.sessions[userID] = &ComboWizardSession{
		IsEditing: true,
		CreatedAt: time.Now(),
	}
	w.mu.Unlock()

	text := "✏️ <b>WIZARD EDIT MODEL COMBO</b>\n\nPilih combo yang ingin Anda ubah:"

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, cb := range combos {
		cbCopy := cb
		statusIcon := "🟢"
		if !cbCopy.IsActive {
			statusIcon = "🔴"
		}
		btn := menu.Data(fmt.Sprintf("%s %s (%d target)", statusIcon, cbCopy.Name, len(cbCopy.Targets)), fmt.Sprintf("cwiz_ed_pick_%s", cbCopy.Name))
		rows = append(rows, menu.Row(btn))
	}

	btnCancel := menu.Data("⬅️ Kembali ke Menu Combos", "menu_combos")
	rows = append(rows, menu.Row(btnCancel))
	menu.Inline(rows...)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderComboEditDashboard renders the full interactive dashboard for a combo
func (w *ComboWizard) RenderComboEditDashboard(c tele.Context, combo *storage.ModelComboRecord) error {
	statusText := "🟢 <b>Aktif</b>"
	if !combo.IsActive {
		statusText = "🔴 <b>Nonaktif</b>"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔀 <b>EDIT COMBO: %s</b>\n\n", html.EscapeString(combo.Name)))
	sb.WriteString(fmt.Sprintf("• <b>Status:</b> %s\n", statusText))
	if combo.Description != "" {
		sb.WriteString(fmt.Sprintf("• <b>Deskripsi:</b> <i>%s</i>\n", html.EscapeString(combo.Description)))
	}
	sb.WriteString(fmt.Sprintf("• <b>Strategi:</b> <code>%s</code>\n", html.EscapeString(combo.Strategy)))
	sb.WriteString(fmt.Sprintf("• <b>Total Target:</b> %d step\n\n", len(combo.Targets)))

	sb.WriteString("📋 <b>Rantai Eksekusi Saat Ini:</b>\n")
	if len(combo.Targets) == 0 {
		sb.WriteString("<i>(Rantai kosong - belum ada target)</i>\n")
	} else {
		for i, t := range combo.Targets {
			sb.WriteString(fmt.Sprintf("  %d. <b>%s</b> ➔ <code>%s</code>\n", i+1, html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
		}
	}

	sb.WriteString("\nPilih aksi di bawah untuk mengubah combo ini:")

	menu := &tele.ReplyMarkup{}
	btnAddTarget := menu.Data("➕ Tambah Target", "cwiz_ed_add_target")
	btnDelTarget := menu.Data("➖ Hapus Target", "cwiz_ed_del_target")
	btnReorder := menu.Data("🔄 Susun Ulang Chain", "cwiz_ed_reorder")
	btnStrat := menu.Data("🔀 Ganti Strategi", "cwiz_ed_strat")
	btnDesc := menu.Data("📝 Ubah Deskripsi", "cwiz_ed_desc")
	btnToggle := menu.Data("🔘 Toggle Aktif/Nonaktif", "cwiz_ed_toggle")
	btnDel := menu.Data("🗑️ Hapus Combo", "cwiz_ed_del")
	btnBack := menu.Data("⬅️ Selesai / Kembali", "menu_combos")

	menu.Inline(
		menu.Row(btnAddTarget, btnDelTarget),
		menu.Row(btnReorder, btnStrat),
		menu.Row(btnDesc, btnToggle),
		menu.Row(btnDel),
		menu.Row(btnBack),
	)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// CancelWizard cancels active session
func (w *ComboWizard) CancelWizard(userID int64) {
	w.mu.Lock()
	delete(w.sessions, userID)
	w.mu.Unlock()
}

// HandleTextMessage intercepts text input during wizard state
func (w *ComboWizard) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.Step == StepComboNone {
		return false, nil
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") {
		comboName := sess.EditingComboName
		w.CancelWizard(userID)
		if sess.IsEditing && comboName != "" {
			combo, _ := w.db.GetCombo(comboName)
			if combo != nil {
				return true, w.RenderComboEditDashboard(c, combo)
			}
		}
		return true, c.Reply("❌ Operasi combo wizard dibatalkan.")
	}

	switch sess.Step {
	case StepComboEnterName:
		cleanName := strings.ToLower(strings.TrimSpace(msgText))
		cleanName = strings.ReplaceAll(cleanName, " ", "_")
		if cleanName == "" {
			return true, c.Reply("⚠️ Nama combo tidak boleh kosong.")
		}

		sess.Name = cleanName
		sess.Description = fmt.Sprintf("Smart Combo %s", cleanName)
		sess.Step = StepComboPickProvider
		sess.ProvPage = 0

		return true, w.promptPickProvider(c, sess, 0)

	case StepComboPickProvider, StepComboEditAddTargetPickProv:
		providers, _ := w.db.ListProviders()
		if len(providers) == 0 {
			return false, nil
		}

		var selectedProv *storage.ProviderRecord
		// 1. Try matching by 1-based number index
		if num, err := strconv.Atoi(msgText); err == nil && num >= 1 && num <= len(providers) {
			provCopy := providers[num-1]
			selectedProv = &provCopy
		}
		// 2. Try matching by ID or Name (case-insensitive)
		if selectedProv == nil {
			for _, p := range providers {
				if strings.EqualFold(p.ID, msgText) || strings.EqualFold(p.Name, msgText) {
					provCopy := p
					selectedProv = &provCopy
					break
				}
			}
		}

		if selectedProv != nil {
			sess.SelectedProvider = selectedProv
			sess.ModelPage = 0
			if sess.Step == StepComboPickProvider {
				sess.Step = StepComboPickModel
				return true, w.promptPickModel(c, sess, 0)
			} else {
				sess.Step = StepComboEditAddTargetPickMod
				return true, w.promptEditAddTargetPickMod(c, sess, 0)
			}
		}

		return true, c.Reply(fmt.Sprintf("⚠️ Provider <code>%s</code> tidak ditemukan.\n<i>Silakan ketik nomor (1-%d), ID/nama provider, atau klik tombol di atas.</i>", html.EscapeString(msgText), len(providers)), tele.ModeHTML)

	case StepComboPickModel, StepComboEditAddTargetPickMod:
		p := sess.SelectedProvider
		if p == nil {
			return false, nil
		}
		allModels := w.getModelsForProvider(p)

		var chosenModel string
		// 1. Try matching by 1-based index from model list
		if num, err := strconv.Atoi(msgText); err == nil && num >= 1 && num <= len(allModels) {
			chosenModel = allModels[num-1]
		}
		// 2. Try matching existing models (case-insensitive)
		if chosenModel == "" {
			for _, m := range allModels {
				if strings.EqualFold(m, msgText) {
					chosenModel = m
					break
				}
			}
		}
		// 3. Allow typing custom model name directly
		if chosenModel == "" {
			chosenModel = msgText
		}

		if sess.Step == StepComboPickModel {
			return true, w.applySelectedModel(c, sess, chosenModel)
		} else {
			return true, w.applyEditAddTargetModel(c, sess, chosenModel)
		}

	case StepComboEditDesc:
		combo, err := w.db.GetCombo(sess.EditingComboName)
		if err != nil || combo == nil {
			w.CancelWizard(userID)
			return true, c.Reply("❌ Combo tidak ditemukan.")
		}
		combo.Description = msgText
		_ = w.db.SaveCombo(combo)
		w.providerManager.RegisterCombo(combo)
		sess.Step = StepComboNone
		_ = c.Reply(fmt.Sprintf("✅ Deskripsi combo <b>%s</b> berhasil diubah!", html.EscapeString(combo.Name)), tele.ModeHTML)
		return true, w.RenderComboEditDashboard(c, combo)
	}

	return false, nil
}

const (
	comboProvidersPerPage = 6
	comboModelsPerPage    = 8
)

func (w *ComboWizard) getModelsForProvider(p *storage.ProviderRecord) []string {
	if p == nil {
		return []string{"default"}
	}
	seen := make(map[string]bool)
	var rest []string

	def := strings.TrimSpace(p.DefaultModel)
	if def != "" {
		seen[strings.ToLower(def)] = true
	}

	for _, m := range p.Models {
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
	if len(list) == 0 {
		list = []string{"default"}
	}
	return list
}

func (w *ComboWizard) promptPickProvider(c tele.Context, sess *ComboWizardSession, page int) error {
	if c == nil {
		return nil
	}
	providers, err := w.db.ListProviders()
	if err != nil || len(providers) == 0 {
		return c.Reply("❌ Belum ada provider AI aktif yang terdaftar. Buat provider terlebih dahulu via <code>/wizard</code>.", tele.ModeHTML)
	}

	totalProvs := len(providers)
	totalPages := (totalProvs + comboProvidersPerPage - 1) / comboProvidersPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	sess.ProvPage = page

	startIdx := page * comboProvidersPerPage
	endIdx := startIdx + comboProvidersPerPage
	if endIdx > totalProvs {
		endIdx = totalProvs
	}
	pageProvs := providers[startIdx:endIdx]

	targetNum := len(sess.Targets) + 1
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎯 <b>PILIH TARGET #%d UNTUK COMBO '%s'</b>\n", targetNum, html.EscapeString(sess.Name)))
	if totalPages > 1 {
		sb.WriteString(fmt.Sprintf("Halaman <code>%d/%d</code> (Total: <code>%d provider</code>)\n\n", page+1, totalPages, totalProvs))
	} else {
		sb.WriteString("\n")
	}

	if len(sess.Targets) > 0 {
		sb.WriteString("📋 <b>Rantai Saat Ini:</b>\n")
		for i, t := range sess.Targets {
			sb.WriteString(fmt.Sprintf(" %d. <b>%s</b> ➔ <code>%s</code>\n", i+1, html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Pilih provider yang ingin ditambahkan ke rantai ini:\n")
	for i, p := range pageProvs {
		globalIdx := startIdx + i + 1
		sb.WriteString(fmt.Sprintf("%d. 🤖 <b>%s</b> (<code>%s</code> | %d model)\n", globalIdx, html.EscapeString(p.Name), html.EscapeString(p.Type), len(p.Models)))
	}
	sb.WriteString("\n💡 <i>Klik tombol di bawah atau balas chat dengan nomor/nama provider:</i>\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	var curRow []tele.Btn
	for _, p := range pageProvs {
		btn := menu.Data(fmt.Sprintf("🤖 %s", p.Name), fmt.Sprintf("cwiz_prov_%s", p.ID))
		curRow = append(curRow, btn)
		if len(curRow) == 2 {
			rows = append(rows, menu.Row(curRow...))
			curRow = nil
		}
	}
	if len(curRow) > 0 {
		rows = append(rows, menu.Row(curRow...))
	}

	// Pagination Navigation
	if totalPages > 1 {
		var navRow []tele.Btn
		if page > 0 {
			navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("cwiz_prov_p_%d", page-1)))
		}
		navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "cwiz_noop"))
		if page < totalPages-1 {
			navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("cwiz_prov_p_%d", page+1)))
		}
		rows = append(rows, menu.Row(navRow...))
	}

	if len(sess.Targets) > 0 {
		btnDone := menu.Data("✅ Selesai & Simpan Combo", "cwiz_save")
		rows = append(rows, menu.Row(btnDone))
	}

	btnCancel := menu.Data("❌ Batal", "cwiz_cancel")
	rows = append(rows, menu.Row(btnCancel))

	menu.Inline(rows...)
	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleProviderPage handles pagination for provider selection in combo creation
func (w *ComboWizard) HandleProviderPage(c tele.Context, page int) error {
	if c.Sender() == nil {
		return nil
	}
	w.mu.RLock()
	sess, exists := w.sessions[c.Sender().ID]
	w.mu.RUnlock()
	if !exists {
		return c.Reply("⚠️ Sesi wizard telah berakhir. Ketik <code>/combowizard</code>.", tele.ModeHTML)
	}
	return w.promptPickProvider(c, sess, page)
}

// HandleProviderSelect handles provider choice during creation
func (w *ComboWizard) HandleProviderSelect(c tele.Context, provID string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists {
		return c.Reply("⚠️ Sesi wizard telah berakhir. Ketik <code>/combowizard</code>.", tele.ModeHTML)
	}

	p, err := w.db.GetProvider(provID)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider '%s' tidak ditemukan.", provID))
	}

	sess.SelectedProvider = p
	sess.Step = StepComboPickModel
	sess.ModelPage = 0

	return w.promptPickModel(c, sess, 0)
}

func (w *ComboWizard) promptPickModel(c tele.Context, sess *ComboWizardSession, page int) error {
	if c == nil {
		return nil
	}
	p := sess.SelectedProvider
	if p == nil {
		return w.promptPickProvider(c, sess, 0)
	}

	models := w.getModelsForProvider(p)
	totalModels := len(models)
	totalPages := (totalModels + comboModelsPerPage - 1) / comboModelsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	sess.ModelPage = page

	startIdx := page * comboModelsPerPage
	endIdx := startIdx + comboModelsPerPage
	if endIdx > totalModels {
		endIdx = totalModels
	}
	pageModels := models[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧠 <b>PILIH MODEL DARI: %s</b>\n", html.EscapeString(strings.ToUpper(p.Name))))
	sb.WriteString(fmt.Sprintf("Target #%d untuk combo '%s' | Hal <code>%d/%d</code> (Total: <code>%d model</code>)\n\n",
		len(sess.Targets)+1, html.EscapeString(sess.Name), page+1, totalPages, totalModels))
	sb.WriteString("Daftar model tersedia:\n")

	for i, m := range pageModels {
		globalIdx := startIdx + i + 1
		isDef := strings.EqualFold(m, p.DefaultModel)
		defTag := ""
		if isDef {
			defTag = " ⭐ (Default)"
		}
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>%s\n", globalIdx, html.EscapeString(m), defTag))
	}
	sb.WriteString("\n💡 <i>Klik tombol di bawah, gunakan pagination, atau balas chat dengan <b>nomor</b> atau <b>nama model</b>:</i>\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, m := range pageModels {
		globalIdx := startIdx + i
		isDef := strings.EqualFold(m, p.DefaultModel)
		btnLabel := m
		if len([]rune(btnLabel)) > 26 {
			btnLabel = string([]rune(btnLabel)[:23]) + "..."
		}
		if isDef {
			btnLabel = "⭐ " + btnLabel
		}
		btn := menu.Data(btnLabel, fmt.Sprintf("cwiz_mod_%d", globalIdx))
		rows = append(rows, menu.Row(btn))
	}

	// Pagination Navigation
	if totalPages > 1 {
		var navRow []tele.Btn
		if page > 0 {
			navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("cwiz_mod_p_%d", page-1)))
		}
		navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "cwiz_noop"))
		if page < totalPages-1 {
			navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("cwiz_mod_p_%d", page+1)))
		}
		rows = append(rows, menu.Row(navRow...))
	}

	btnBack := menu.Data("⬅️ Ganti Provider", "cwiz_back_prov")
	btnCancel := menu.Data("❌ Batal", "cwiz_cancel")
	rows = append(rows, menu.Row(btnBack, btnCancel))

	menu.Inline(rows...)
	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleModelPage handles pagination for model selection in combo creation
func (w *ComboWizard) HandleModelPage(c tele.Context, page int) error {
	if c.Sender() == nil {
		return nil
	}
	w.mu.RLock()
	sess, exists := w.sessions[c.Sender().ID]
	w.mu.RUnlock()
	if !exists {
		return c.Reply("⚠️ Sesi wizard telah berakhir. Ketik <code>/combowizard</code>.", tele.ModeHTML)
	}
	return w.promptPickModel(c, sess, page)
}

// HandleModelSelect handles model choice during creation
func (w *ComboWizard) HandleModelSelect(c tele.Context, modelIdx int) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.SelectedProvider == nil {
		return c.Reply("⚠️ Sesi wizard telah berakhir. Ketik <code>/combos</code>.", tele.ModeHTML)
	}

	p := sess.SelectedProvider
	allModels := w.getModelsForProvider(p)
	selectedModel := p.DefaultModel
	if modelIdx >= 0 && modelIdx < len(allModels) {
		selectedModel = allModels[modelIdx]
	}

	return w.applySelectedModel(c, sess, selectedModel)
}

func (w *ComboWizard) applySelectedModel(c tele.Context, sess *ComboWizardSession, selectedModel string) error {
	p := sess.SelectedProvider
	if p == nil {
		return w.promptPickProvider(c, sess, 0)
	}

	if selectedModel == "" {
		selectedModel = p.DefaultModel
	}
	if selectedModel == "" {
		selectedModel = "default"
	}

	priority := len(sess.Targets) + 1
	sess.Targets = append(sess.Targets, storage.ComboTarget{
		ProviderID: p.ID,
		Model:      selectedModel,
		Priority:   priority,
	})

	sess.SelectedProvider = nil
	sess.Step = StepComboPickProvider
	sess.ModelPage = 0

	return w.promptPickProvider(c, sess, sess.ProvPage)
}

// HandleBackToProvider goes back to provider selection preserving targets
func (w *ComboWizard) HandleBackToProvider(c tele.Context) error {
	if c.Sender() == nil {
		return nil
	}
	w.mu.RLock()
	sess, exists := w.sessions[c.Sender().ID]
	w.mu.RUnlock()
	if !exists {
		return w.StartWizard(c)
	}

	sess.SelectedProvider = nil
	if sess.IsEditing {
		sess.Step = StepComboEditAddTargetPickProv
		return w.promptEditAddTargetPickProv(c, sess, sess.ProvPage)
	}

	sess.Step = StepComboPickProvider
	return w.promptPickProvider(c, sess, sess.ProvPage)
}

// HandleSaveCombo finalizes and saves the combo
func (w *ComboWizard) HandleSaveCombo(c tele.Context) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || len(sess.Targets) == 0 {
		return c.Reply("⚠️ Belum ada target yang dipilih untuk combo ini.", tele.ModeHTML)
	}

	comboRec := &storage.ModelComboRecord{
		ID:          uuid.New().String()[:8],
		Name:        sess.Name,
		Description: sess.Description,
		Targets:     sess.Targets,
		Strategy:    sess.Strategy,
		IsActive:    true,
	}

	if err := w.db.SaveCombo(comboRec); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan combo: %v", html.EscapeString(err.Error())))
	}

	w.providerManager.RegisterCombo(comboRec)
	w.CancelWizard(userID)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎉 <b>COMBO '%s' BERHASIL DIBUAT & DIAKTIFKAN!</b>\n\n", html.EscapeString(comboRec.Name)))
	sb.WriteString(fmt.Sprintf("• Strategi: <code>%s</code> (Failsafe Fallback)\n", comboRec.Strategy))
	sb.WriteString("• <b>Rantai Eksekusi:</b>\n")
	for i, t := range comboRec.Targets {
		sb.WriteString(fmt.Sprintf("   %d. Provider: <b>%s</b> ➔ Model: <code>%s</code>\n", i+1, html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
	}
	sb.WriteString("\n💡 <b>Cara Menggunakan Combo Ini Sebagai Model Utama:</b>\n")
	sb.WriteString(fmt.Sprintf("<code>/setlimit global system model combo:%s</code>\n\n", comboRec.Name))
	sb.WriteString("<i>GoAssistant sekarang otomatis memproses pesan menggunakan rantai failsafe ini!</i>")

	menu := &tele.ReplyMarkup{}
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	menu.Inline(menu.Row(btnBack))

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// --- Edit Combo Actions ---

// HandleEditAddTargetStart starts adding a new target to existing combo
func (w *ComboWizard) HandleEditAddTargetStart(c tele.Context, comboName string) error {
	if c.Sender() != nil {
		w.mu.Lock()
		w.sessions[c.Sender().ID] = &ComboWizardSession{
			IsEditing:        true,
			EditingComboName: comboName,
			Step:             StepComboEditAddTargetPickProv,
			ProvPage:         0,
			CreatedAt:        time.Now(),
		}
		w.mu.Unlock()
	}

	w.mu.RLock()
	sess := w.sessions[c.Sender().ID]
	w.mu.RUnlock()

	return w.promptEditAddTargetPickProv(c, sess, 0)
}

func (w *ComboWizard) promptEditAddTargetPickProv(c tele.Context, sess *ComboWizardSession, page int) error {
	providers, err := w.db.ListProviders()
	if err != nil || len(providers) == 0 {
		return c.Reply("❌ Belum ada provider AI yang terdaftar.")
	}

	totalProvs := len(providers)
	totalPages := (totalProvs + comboProvidersPerPage - 1) / comboProvidersPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	sess.ProvPage = page

	startIdx := page * comboProvidersPerPage
	endIdx := startIdx + comboProvidersPerPage
	if endIdx > totalProvs {
		endIdx = totalProvs
	}
	pageProvs := providers[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("➕ <b>TAMBAH TARGET KE COMBO '%s'</b>\n", html.EscapeString(sess.EditingComboName)))
	if totalPages > 1 {
		sb.WriteString(fmt.Sprintf("Halaman <code>%d/%d</code> (Total: <code>%d provider</code>)\n\n", page+1, totalPages, totalProvs))
	} else {
		sb.WriteString("\n")
	}
	sb.WriteString("Pilih provider untuk target baru ini:\n")
	for i, p := range pageProvs {
		globalIdx := startIdx + i + 1
		sb.WriteString(fmt.Sprintf("%d. 🤖 <b>%s</b> (<code>%s</code> | %d model)\n", globalIdx, html.EscapeString(p.Name), html.EscapeString(p.Type), len(p.Models)))
	}
	sb.WriteString("\n💡 <i>Klik tombol di bawah atau balas chat dengan nomor/nama provider:</i>\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	var curRow []tele.Btn
	for _, p := range pageProvs {
		btn := menu.Data(fmt.Sprintf("🤖 %s", p.Name), fmt.Sprintf("cwiz_ed_prov_%s", p.ID))
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
			navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("cwiz_ed_prov_p_%d", page-1)))
		}
		navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "cwiz_noop"))
		if page < totalPages-1 {
			navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("cwiz_ed_prov_p_%d", page+1)))
		}
		rows = append(rows, menu.Row(navRow...))
	}

	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("cwiz_ed_pick_%s", sess.EditingComboName))
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleEditAddTargetProvPage handles pagination for provider selection in edit mode
func (w *ComboWizard) HandleEditAddTargetProvPage(c tele.Context, page int) error {
	if c.Sender() == nil {
		return nil
	}
	w.mu.RLock()
	sess, exists := w.sessions[c.Sender().ID]
	w.mu.RUnlock()
	if !exists {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editcombo</code>.", tele.ModeHTML)
	}
	return w.promptEditAddTargetPickProv(c, sess, page)
}

// HandleEditAddTargetProvSelect handles provider choice when adding target to combo
func (w *ComboWizard) HandleEditAddTargetProvSelect(c tele.Context, provID string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.EditingComboName == "" {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editcombo</code>.", tele.ModeHTML)
	}

	p, err := w.db.GetProvider(provID)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider '%s' tidak ditemukan.", provID))
	}

	sess.SelectedProvider = p
	sess.Step = StepComboEditAddTargetPickMod
	sess.ModelPage = 0

	return w.promptEditAddTargetPickMod(c, sess, 0)
}

func (w *ComboWizard) promptEditAddTargetPickMod(c tele.Context, sess *ComboWizardSession, page int) error {
	p := sess.SelectedProvider
	if p == nil {
		return w.promptEditAddTargetPickProv(c, sess, 0)
	}

	models := w.getModelsForProvider(p)
	totalModels := len(models)
	totalPages := (totalModels + comboModelsPerPage - 1) / comboModelsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	sess.ModelPage = page

	startIdx := page * comboModelsPerPage
	endIdx := startIdx + comboModelsPerPage
	if endIdx > totalModels {
		endIdx = totalModels
	}
	pageModels := models[startIdx:endIdx]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧠 <b>PILIH MODEL DARI: %s</b>\n", html.EscapeString(strings.ToUpper(p.Name))))
	sb.WriteString(fmt.Sprintf("Target baru combo '%s' | Hal <code>%d/%d</code> (Total: <code>%d model</code>)\n\n",
		html.EscapeString(sess.EditingComboName), page+1, totalPages, totalModels))
	sb.WriteString("Daftar model tersedia:\n")

	for i, m := range pageModels {
		globalIdx := startIdx + i + 1
		isDef := strings.EqualFold(m, p.DefaultModel)
		defTag := ""
		if isDef {
			defTag = " ⭐ (Default)"
		}
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>%s\n", globalIdx, html.EscapeString(m), defTag))
	}
	sb.WriteString("\n💡 <i>Klik tombol di bawah, gunakan pagination, atau balas chat dengan <b>nomor</b> atau <b>nama model</b>:</i>\n")

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, m := range pageModels {
		globalIdx := startIdx + i
		isDef := strings.EqualFold(m, p.DefaultModel)
		btnLabel := m
		if len([]rune(btnLabel)) > 26 {
			btnLabel = string([]rune(btnLabel)[:23]) + "..."
		}
		if isDef {
			btnLabel = "⭐ " + btnLabel
		}
		btn := menu.Data(btnLabel, fmt.Sprintf("cwiz_ed_mod_%d", globalIdx))
		rows = append(rows, menu.Row(btn))
	}

	if totalPages > 1 {
		var navRow []tele.Btn
		if page > 0 {
			navRow = append(navRow, menu.Data("⬅️ Prev", fmt.Sprintf("cwiz_ed_mod_p_%d", page-1)))
		}
		navRow = append(navRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "cwiz_noop"))
		if page < totalPages-1 {
			navRow = append(navRow, menu.Data("Next ➡️", fmt.Sprintf("cwiz_ed_mod_p_%d", page+1)))
		}
		rows = append(rows, menu.Row(navRow...))
	}

	btnBack := menu.Data("⬅️ Ganti Provider", "cwiz_ed_add_target")
	btnCancel := menu.Data("❌ Batal", "cwiz_cancel")
	rows = append(rows, menu.Row(btnBack, btnCancel))

	menu.Inline(rows...)
	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleEditAddTargetModPage handles pagination for model selection in edit mode
func (w *ComboWizard) HandleEditAddTargetModPage(c tele.Context, page int) error {
	if c.Sender() == nil {
		return nil
	}
	w.mu.RLock()
	sess, exists := w.sessions[c.Sender().ID]
	w.mu.RUnlock()
	if !exists {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editcombo</code>.", tele.ModeHTML)
	}
	return w.promptEditAddTargetPickMod(c, sess, page)
}

// HandleEditAddTargetModSelect finalizes adding target to existing combo
func (w *ComboWizard) HandleEditAddTargetModSelect(c tele.Context, modelIdx int) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.EditingComboName == "" || sess.SelectedProvider == nil {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editcombo</code>.", tele.ModeHTML)
	}

	p := sess.SelectedProvider
	allModels := w.getModelsForProvider(p)
	selectedModel := p.DefaultModel
	if modelIdx >= 0 && modelIdx < len(allModels) {
		selectedModel = allModels[modelIdx]
	}

	return w.applyEditAddTargetModel(c, sess, selectedModel)
}

func (w *ComboWizard) applyEditAddTargetModel(c tele.Context, sess *ComboWizardSession, selectedModel string) error {
	combo, err := w.db.GetCombo(sess.EditingComboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	p := sess.SelectedProvider
	if p == nil {
		return w.HandleEditAddTargetStart(c, sess.EditingComboName)
	}

	if selectedModel == "" {
		selectedModel = p.DefaultModel
	}
	if selectedModel == "" {
		selectedModel = "default"
	}

	priority := len(combo.Targets) + 1
	combo.Targets = append(combo.Targets, storage.ComboTarget{
		ProviderID: p.ID,
		Model:      selectedModel,
		Priority:   priority,
	})

	_ = w.db.SaveCombo(combo)
	w.providerManager.RegisterCombo(combo)

	w.mu.Lock()
	sess.Step = StepComboNone
	sess.SelectedProvider = nil
	w.mu.Unlock()

	_ = c.Reply(fmt.Sprintf("✅ Target baru (<b>%s</b> ➔ <code>%s</code>) berhasil ditambahkan ke combo <b>%s</b>!",
		html.EscapeString(p.ID), html.EscapeString(selectedModel), html.EscapeString(combo.Name)), tele.ModeHTML)
	return w.RenderComboEditDashboard(c, combo)
}

// HandleEditDelTargetMenu shows buttons to remove individual targets
func (w *ComboWizard) HandleEditDelTargetMenu(c tele.Context, comboName string) error {
	combo, err := w.db.GetCombo(comboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	if len(combo.Targets) == 0 {
		_ = c.Reply("⚠️ Rantai combo ini sudah tidak memiliki target.")
		return w.RenderComboEditDashboard(c, combo)
	}

	text := fmt.Sprintf("➖ <b>HAPUS TARGET DARI COMBO '%s'</b>\n\nKlik target yang ingin dihapus dari rantai eksekusi:", html.EscapeString(combo.Name))

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, t := range combo.Targets {
		idx := i
		btn := menu.Data(fmt.Sprintf("❌ #%d %s ➔ %s", idx+1, t.ProviderID, t.Model), fmt.Sprintf("cwiz_ed_rem_%d", idx))
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("cwiz_ed_pick_%s", combo.Name))
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	if c.Sender() != nil {
		w.mu.Lock()
		w.sessions[c.Sender().ID] = &ComboWizardSession{
			IsEditing:        true,
			EditingComboName: combo.Name,
			CreatedAt:        time.Now(),
		}
		w.mu.Unlock()
	}

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditDelTargetExecute removes a target step at targetIdx
func (w *ComboWizard) HandleEditDelTargetExecute(c tele.Context, targetIdx int) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.EditingComboName == "" {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editcombo</code>.", tele.ModeHTML)
	}

	combo, err := w.db.GetCombo(sess.EditingComboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	if targetIdx < 0 || targetIdx >= len(combo.Targets) {
		return c.Reply("⚠️ Index target tidak valid.")
	}

	var updated []storage.ComboTarget
	for i, t := range combo.Targets {
		if i != targetIdx {
			t.Priority = len(updated) + 1
			updated = append(updated, t)
		}
	}
	combo.Targets = updated

	_ = w.db.SaveCombo(combo)
	w.providerManager.RegisterCombo(combo)

	_ = c.Reply(fmt.Sprintf("✅ Target #%d berhasil dihapus dari combo <b>%s</b>! Sisa: %d target.", targetIdx+1, html.EscapeString(combo.Name), len(combo.Targets)), tele.ModeHTML)
	return w.RenderComboEditDashboard(c, combo)
}

// HandleEditStrategyMenu shows strategy options for combo
func (w *ComboWizard) HandleEditStrategyMenu(c tele.Context, comboName string) error {
	combo, err := w.db.GetCombo(comboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	text := fmt.Sprintf("🔀 <b>STRATEGI COMBO: %s</b>\n\n"+
		"Strategi saat ini: <code>%s</code>\n\n"+
		"• <b>Failsafe (Fallback Rantai):</b> Mencoba target #1. Jika limit/error, otomatis fallback ke target #2, #3, dst.\n"+
		"• <b>Round-Robin:</b> Membagi pesan secara merata bergantian ke setiap target di rantai.\n\n"+
		"Pilih strategi:", html.EscapeString(combo.Name), html.EscapeString(combo.Strategy))

	menu := &tele.ReplyMarkup{}
	btnFS := menu.Data("🛡️ Failsafe (Fallback Rantai)", "cwiz_ed_st_fs")
	btnRR := menu.Data("🔄 Round-Robin", "cwiz_ed_st_rr")
	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("cwiz_ed_pick_%s", combo.Name))

	menu.Inline(
		menu.Row(btnFS, btnRR),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditSetStrategy sets combo strategy
func (w *ComboWizard) HandleEditSetStrategy(c tele.Context, comboName string, strat string) error {
	combo, err := w.db.GetCombo(comboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	combo.Strategy = strat
	_ = w.db.SaveCombo(combo)
	w.providerManager.RegisterCombo(combo)

	_ = c.Reply(fmt.Sprintf("✅ Strategi untuk combo <b>%s</b> diubah ke <code>%s</code>!", html.EscapeString(combo.Name), strat), tele.ModeHTML)
	return w.RenderComboEditDashboard(c, combo)
}

// HandleEditToggleActive toggles combo active status
func (w *ComboWizard) HandleEditToggleActive(c tele.Context, comboName string) error {
	combo, err := w.db.GetCombo(comboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	combo.IsActive = !combo.IsActive
	_ = w.db.SaveCombo(combo)
	if combo.IsActive {
		w.providerManager.RegisterCombo(combo)
	} else {
		w.providerManager.UnregisterCombo(combo.Name)
	}

	statusStr := "Diaktifkan 🟢"
	if !combo.IsActive {
		statusStr = "Dinonaktifkan 🔴"
	}
	_ = c.Reply(fmt.Sprintf("✅ Combo <b>%s</b> berhasil <b>%s</b>!", html.EscapeString(combo.Name), statusStr), tele.ModeHTML)
	return w.RenderComboEditDashboard(c, combo)
}

// HandleEditDeletePrompt asks for delete confirmation
func (w *ComboWizard) HandleEditDeletePrompt(c tele.Context, comboName string) error {
	combo, err := w.db.GetCombo(comboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	text := fmt.Sprintf("⚠️ <b>KONFIRMASI HAPUS COMBO</b>\n\n"+
		"Apakah Anda yakin ingin menghapus Model Combo <b>%s</b>?\n\n"+
		"<i>Tindakan ini permanen. Seluruh channel yang menggunakan combo:%s akan memerlukan penyesuaian model.</i>",
		html.EscapeString(combo.Name), html.EscapeString(combo.Name))

	menu := &tele.ReplyMarkup{}
	btnYes := menu.Data("🗑️ Ya, Hapus Sekarang", fmt.Sprintf("cwiz_ed_del_yes_%s", combo.Name))
	btnNo := menu.Data("❌ Batal", fmt.Sprintf("cwiz_ed_pick_%s", combo.Name))

	menu.Inline(
		menu.Row(btnYes),
		menu.Row(btnNo),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleEditDeleteConfirm deletes the combo
func (w *ComboWizard) HandleEditDeleteConfirm(c tele.Context, comboName string) error {
	_ = w.db.DeleteCombo(comboName)
	w.providerManager.UnregisterCombo(comboName)

	if c.Sender() != nil {
		w.CancelWizard(c.Sender().ID)
	}

	_ = c.Reply(fmt.Sprintf("🗑️ Combo <b>%s</b> berhasil dihapus dari sistem.", html.EscapeString(comboName)), tele.ModeHTML)
	return w.StartEditWizard(c, "")
}

// PromptEditStep sets session step and prompts text input
func (w *ComboWizard) PromptEditStep(c tele.Context, comboName string, step ComboWizardStep, promptMsg string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	w.mu.Lock()
	w.sessions[userID] = &ComboWizardSession{
		IsEditing:        true,
		EditingComboName: comboName,
		Step:             step,
		CreatedAt:        time.Now(),
	}
	w.mu.Unlock()

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", fmt.Sprintf("cwiz_ed_pick_%s", comboName))
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(promptMsg, menu, tele.ModeHTML)
}

// GetSession returns active session
func (w *ComboWizard) GetSession(userID int64) (*ComboWizardSession, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	s, ok := w.sessions[userID]
	return s, ok
}

// HandleEditReorderMenu shows interactive chain reordering menu with Up / Down buttons
func (w *ComboWizard) HandleEditReorderMenu(c tele.Context, comboName string) error {
	combo, err := w.db.GetCombo(comboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	if len(combo.Targets) <= 1 {
		_ = c.Reply("⚠️ Rantai combo memiliki kurang dari 2 target sehingga tidak perlu disusun ulang.")
		return w.RenderComboEditDashboard(c, combo)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔄 <b>SUSUN ULANG CHAIN: %s</b>\n\n", html.EscapeString(combo.Name)))
	sb.WriteString("Gunakan tombol ⬆️ / ⬇️ di samping setiap target untuk mengubah urutan prioritas rantai fallback:\n\n")

	for i, t := range combo.Targets {
		sb.WriteString(fmt.Sprintf("  <b>#%d</b> ➔ <code>%s/%s</code>\n", i+1, html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
	}

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, t := range combo.Targets {
		var btns []tele.Btn
		targetLabel := fmt.Sprintf("#%d %s/%s", i+1, t.ProviderID, t.Model)
		if len(targetLabel) > 20 {
			targetLabel = targetLabel[:18] + ".."
		}
		btnLabel := menu.Data(targetLabel, "mod_noop")
		btns = append(btns, btnLabel)

		if i > 0 {
			btnUp := menu.Data("⬆️", fmt.Sprintf("cwiz_ed_mvup_%d", i))
			btns = append(btns, btnUp)
		}
		if i < len(combo.Targets)-1 {
			btnDown := menu.Data("⬇️", fmt.Sprintf("cwiz_ed_mvdn_%d", i))
			btns = append(btns, btnDown)
		}
		rows = append(rows, menu.Row(btns...))
	}

	btnBack := menu.Data("⬅️ Selesai / Kembali", fmt.Sprintf("cwiz_ed_pick_%s", combo.Name))
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	if c.Sender() != nil {
		w.mu.Lock()
		w.sessions[c.Sender().ID] = &ComboWizardSession{
			IsEditing:        true,
			EditingComboName: combo.Name,
			CreatedAt:        time.Now(),
		}
		w.mu.Unlock()
	}

	return c.EditOrSend(sb.String(), menu, tele.ModeHTML)
}

// HandleEditReorderMove swaps target priority and updates combo
func (w *ComboWizard) HandleEditReorderMove(c tele.Context, targetIdx int, direction string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists || sess.EditingComboName == "" {
		return c.Reply("⚠️ Sesi edit telah berakhir. Gunakan <code>/editcombo</code>.", tele.ModeHTML)
	}

	combo, err := w.db.GetCombo(sess.EditingComboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	swapIdx := targetIdx
	if direction == "up" && targetIdx > 0 {
		swapIdx = targetIdx - 1
	} else if direction == "down" && targetIdx < len(combo.Targets)-1 {
		swapIdx = targetIdx + 1
	} else {
		return c.Respond(&tele.CallbackResponse{})
	}

	// Swap targets
	combo.Targets[targetIdx], combo.Targets[swapIdx] = combo.Targets[swapIdx], combo.Targets[targetIdx]

	// Normalize priorities
	for i := range combo.Targets {
		combo.Targets[i].Priority = i + 1
	}

	_ = w.db.SaveCombo(combo)
	w.providerManager.RegisterCombo(combo)

	return w.HandleEditReorderMenu(c, combo.Name)
}

