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
)

type ComboWizardSession struct {
	Step             ComboWizardStep
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
		w.CancelWizard(userID)
		return true, c.Reply("❌ Setup combo wizard dibatalkan.")
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

// HandleProviderSelect handles provider choice
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

// HandleModelSelect handles model choice
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
