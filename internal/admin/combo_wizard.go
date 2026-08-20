package admin

import (
	"fmt"
	"html"
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

	if sess.Step == StepComboEnterName {
		cleanName := strings.ToLower(strings.TrimSpace(msgText))
		cleanName = strings.ReplaceAll(cleanName, " ", "_")
		if cleanName == "" {
			return true, c.Reply("⚠️ Nama combo tidak boleh kosong.")
		}

		sess.Name = cleanName
		sess.Description = fmt.Sprintf("Smart Combo %s", cleanName)
		sess.Step = StepComboPickProvider

		return true, w.promptPickProvider(c, sess)
	}

	if sess.Step == StepComboEditDesc {
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

func (w *ComboWizard) promptPickProvider(c tele.Context, sess *ComboWizardSession) error {
	providers, err := w.db.ListProviders()
	if err != nil || len(providers) == 0 {
		return c.Reply("❌ Belum ada provider AI aktif yang terdaftar. Buat provider terlebih dahulu via <code>/wizard</code>.", tele.ModeHTML)
	}

	targetNum := len(sess.Targets) + 1
	text := fmt.Sprintf("🎯 <b>PILIH TARGET #%d UNTUK COMBO '%s'</b>\n\n", targetNum, html.EscapeString(sess.Name))

	if len(sess.Targets) > 0 {
		text += "📋 <b>Rantai Saat Ini:</b>\n"
		for i, t := range sess.Targets {
			text += fmt.Sprintf(" %d. <b>%s</b> ➔ <code>%s</code>\n", i+1, html.EscapeString(t.ProviderID), html.EscapeString(t.Model))
		}
		text += "\n"
	}

	text += "Pilih provider yang ingin ditambahkan ke rantai ini:"

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, p := range providers {
		provCopy := p
		btn := menu.Data(fmt.Sprintf("🤖 %s (%s)", provCopy.Name, provCopy.Type), fmt.Sprintf("cwiz_prov_%s", provCopy.ID))
		rows = append(rows, menu.Row(btn))
	}

	if len(sess.Targets) > 0 {
		btnDone := menu.Data("✅ Selesai & Simpan Combo", "cwiz_save")
		rows = append(rows, menu.Row(btnDone))
	}

	btnCancel := menu.Data("❌ Batal", "cwiz_cancel")
	rows = append(rows, menu.Row(btnCancel))

	menu.Inline(rows...)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleProviderSelect handles provider choice during creation / reordering
func (w *ComboWizard) HandleProviderSelect(c tele.Context, provID string) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID

	w.mu.RLock()
	sess, exists := w.sessions[userID]
	w.mu.RUnlock()

	if !exists {
		return c.Reply("⚠️ Sesi wizard telah berakhir. Ketik <code>/combos</code> atau <code>/wizard</code> untuk memulai baru.", tele.ModeHTML)
	}

	p, err := w.db.GetProvider(provID)
	if err != nil || p == nil {
		return c.Reply(fmt.Sprintf("❌ Provider '%s' tidak ditemukan.", provID))
	}

	sess.SelectedProvider = p
	sess.Step = StepComboPickModel

	return w.promptPickModel(c, sess)
}

func (w *ComboWizard) promptPickModel(c tele.Context, sess *ComboWizardSession) error {
	p := sess.SelectedProvider
	if p == nil {
		return w.promptPickProvider(c, sess)
	}

	models := p.Models
	if len(models) == 0 && p.DefaultModel != "" {
		models = []string{p.DefaultModel}
	}
	if len(models) == 0 {
		models = []string{"default"}
	}

	text := fmt.Sprintf("🧠 <b>PILIH MODEL DARI PROVIDER: %s</b>\n\n"+
		"Pilih model yang akan digunakan dalam rantai target ini:", html.EscapeString(p.Name))

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, m := range models {
		if i >= 15 {
			break
		}
		btn := menu.Data(fmt.Sprintf("⚡ %s", m), fmt.Sprintf("cwiz_mod_%d", i))
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ Ganti Provider", "cwiz_back_prov")
	btnCancel := menu.Data("❌ Batal", "cwiz_cancel")
	rows = append(rows, menu.Row(btnBack, btnCancel))

	menu.Inline(rows...)
	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleModelSelect handles model choice during creation / reordering
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
	models := p.Models
	if len(models) == 0 && p.DefaultModel != "" {
		models = []string{p.DefaultModel}
	}

	selectedModel := p.DefaultModel
	if modelIdx >= 0 && modelIdx < len(models) {
		selectedModel = models[modelIdx]
	}

	priority := len(sess.Targets) + 1
	sess.Targets = append(sess.Targets, storage.ComboTarget{
		ProviderID: p.ID,
		Model:      selectedModel,
		Priority:   priority,
	})

	sess.SelectedProvider = nil
	sess.Step = StepComboPickProvider

	return w.promptPickProvider(c, sess)
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
	providers, err := w.db.ListProviders()
	if err != nil || len(providers) == 0 {
		return c.Reply("❌ Belum ada provider AI yang terdaftar.")
	}

	text := fmt.Sprintf("➕ <b>TAMBAH TARGET KE COMBO '%s'</b>\n\nPilih provider untuk target baru ini:", html.EscapeString(comboName))

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for _, p := range providers {
		provCopy := p
		btn := menu.Data(fmt.Sprintf("🤖 %s (%s)", provCopy.Name, provCopy.Type), fmt.Sprintf("cwiz_ed_prov_%s", provCopy.ID))
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ Batal / Kembali", fmt.Sprintf("cwiz_ed_pick_%s", comboName))
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	if c.Sender() != nil {
		w.mu.Lock()
		w.sessions[c.Sender().ID] = &ComboWizardSession{
			IsEditing:        true,
			EditingComboName: comboName,
			Step:             StepComboEditAddTargetPickProv,
			CreatedAt:        time.Now(),
		}
		w.mu.Unlock()
	}

	return c.EditOrSend(text, menu, tele.ModeHTML)
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

	models := p.Models
	if len(models) == 0 && p.DefaultModel != "" {
		models = []string{p.DefaultModel}
	}
	if len(models) == 0 {
		models = []string{"default"}
	}

	text := fmt.Sprintf("🧠 <b>PILIH MODEL DARI %s UNTUK COMBO '%s'</b>\n\nPilih model:", html.EscapeString(p.Name), html.EscapeString(sess.EditingComboName))

	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, m := range models {
		if i >= 15 {
			break
		}
		btn := menu.Data(fmt.Sprintf("⚡ %s", m), fmt.Sprintf("cwiz_ed_mod_%d", i))
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ Ganti Provider", "cwiz_ed_add_target")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return c.EditOrSend(text, menu, tele.ModeHTML)
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

	combo, err := w.db.GetCombo(sess.EditingComboName)
	if err != nil || combo == nil {
		return c.Reply("❌ Combo tidak ditemukan.")
	}

	p := sess.SelectedProvider
	models := p.Models
	if len(models) == 0 && p.DefaultModel != "" {
		models = []string{p.DefaultModel}
	}

	selectedModel := p.DefaultModel
	if modelIdx >= 0 && modelIdx < len(models) {
		selectedModel = models[modelIdx]
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

	_ = c.Reply(fmt.Sprintf("✅ Target baru (<b>%s</b> ➔ <code>%s</code>) berhasil ditambahkan ke combo <b>%s</b>!", html.EscapeString(p.ID), html.EscapeString(selectedModel), html.EscapeString(combo.Name)), tele.ModeHTML)
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

