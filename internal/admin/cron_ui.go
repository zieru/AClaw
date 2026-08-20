package admin

import (
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"goassistant/internal/cron"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type CronStep int

const (
	CronStepNone CronStep = iota
	CronStepID
	CronStepChatID
	CronStepCustomCron
	CronStepPrompt
)

type CronSession struct {
	Step          CronStep
	ID            string
	TargetChannel string
	TargetChatID  string
	CronExpr      string
	Prompt        string
	UpdatedAt     time.Time
}

type CronUI struct {
	db        *storage.DB
	scheduler *cron.Scheduler
	mu        sync.RWMutex
	sessions  map[int64]*CronSession
}

func NewCronUI(db *storage.DB, s *cron.Scheduler) *CronUI {
	return &CronUI{
		db:        db,
		scheduler: s,
		sessions:  make(map[int64]*CronSession),
	}
}

// RenderCronList returns a summary list of all scheduled tasks in HTML format
func (ui *CronUI) RenderCronList() string {
	jobs, err := ui.db.ListCronJobs()
	if err != nil {
		return fmt.Sprintf("❌ Error mengambil data cron: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("⏰ <b>DAFTAR JADWAL TUGAS OTOMATIS (CRON JOBS)</b>\n\n")

	if len(jobs) == 0 {
		sb.WriteString("(Belum ada cron job yang didaftarkan)\n\n")
	} else {
		for i, j := range jobs {
			statusIcon := "🟢 Aktif"
			if !j.IsActive {
				statusIcon = "🔴 Jeda"
			}
			lastRunStr := "Belum pernah"
			if j.LastRun != nil {
				lastRunStr = j.LastRun.Format("02 Jan 15:04:05")
			}

			sb.WriteString(fmt.Sprintf("%d. ⏱️ <b>%s</b> (%s)\n", i+1, html.EscapeString(j.Name), statusIcon))
			sb.WriteString(fmt.Sprintf("   • ID: <code>%s</code>\n", html.EscapeString(j.ID)))
			sb.WriteString(fmt.Sprintf("   • Cron Expr: <code>%s</code>\n", html.EscapeString(j.CronExpr)))
			sb.WriteString(fmt.Sprintf("   • Target: <code>%s</code> (Chat: <code>%s</code>)\n", html.EscapeString(j.TargetChannel), html.EscapeString(j.TargetChatID)))
			sb.WriteString(fmt.Sprintf("   • Prompt: <i>\"%s\"</i>\n", html.EscapeString(j.Prompt)))
			sb.WriteString(fmt.Sprintf("   • Terakhir Dijalankan: <code>%s</code>\n\n", lastRunStr))
		}
	}

	sb.WriteString("📋 <b>Perintah Manajemen Cron:</b>\n")
	sb.WriteString("• <code>/cronwizard</code> - Wizard interaktif buat & atur tugas cron\n")
	sb.WriteString("• <code>/addcron &lt;id&gt; &lt;telegram|whatsapp&gt; &lt;chat_id&gt; \"&lt;cron_expr&gt;\" &lt;prompt&gt;</code>\n")
	sb.WriteString("• <code>/runcron &lt;id&gt;</code> (Jalankan langsung sekarang)\n")
	sb.WriteString("• <code>/delcron &lt;id&gt;</code> (Hapus jadwal)\n")

	return sb.String()
}

// CronKeyboard returns keyboard with wizard and job management buttons
func (ui *CronUI) CronKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	jobs, _ := ui.db.ListCronJobs()

	var rows []tele.Row
	btnWizard := menu.Data("🧙‍♂️ Buat Cron Baru (Wizard)", "cron_wiz_start")
	rows = append(rows, menu.Row(btnWizard))

	if len(jobs) > 0 {
		for _, j := range jobs {
			jCopy := j
			statusEmoji := "🟢"
			if !jCopy.IsActive {
				statusEmoji = "🔴"
			}
			btnRun := menu.Data(fmt.Sprintf("▶️ %s", jCopy.ID), fmt.Sprintf("cron_run_%s", jCopy.ID))
			btnTgl := menu.Data(statusEmoji, fmt.Sprintf("cron_tgl_%s", jCopy.ID))
			btnDel := menu.Data("🗑️", fmt.Sprintf("cron_del_%s", jCopy.ID))
			rows = append(rows, menu.Row(btnRun, btnTgl, btnDel))
		}
	}

	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return menu
}

// StartCronWizard starts the interactive cron setup wizard
func (ui *CronUI) StartCronWizard(c tele.Context) error {
	if c.Sender() == nil {
		return nil
	}
	userID := c.Sender().ID
	ui.mu.Lock()
	ui.sessions[userID] = &CronSession{
		Step:      CronStepID,
		UpdatedAt: time.Now(),
	}
	ui.mu.Unlock()

	text := "⏰ <b>WIZARD JADWAL TUGAS OTOMATIS (CRON JOBS)</b> (Langkah 1/4)\n\n" +
		"Silakan kirimkan <b>ID / Nama Tugas</b> yang diinginkan (satu kata, cth: <code>daily_news</code>, <code>morning_brief</code>):"

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderCronChannelTypePicker prompts for Telegram or WhatsApp channel target
func (ui *CronUI) RenderCronChannelTypePicker(c tele.Context, sess *CronSession) error {
	text := fmt.Sprintf("⏰ <b>PILIH TIPE CHANNEL TARGET (%s)</b> (Langkah 2/4)\n\nPilih media pengiriman hasil prompt:", html.EscapeString(sess.ID))

	menu := &tele.ReplyMarkup{}
	btnTG := menu.Data("✈️ Telegram", "cron_ch_telegram")
	btnWA := menu.Data("🟢 WhatsApp", "cron_ch_whatsapp")
	btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")

	menu.Inline(
		menu.Row(btnTG, btnWA),
		menu.Row(btnCancel),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// RenderCronSchedulePicker prompts for schedule preset or custom cron expression
func (ui *CronUI) RenderCronSchedulePicker(c tele.Context, sess *CronSession) error {
	text := fmt.Sprintf("⏰ <b>PILIH JADWAL FREKUENSI (%s)</b> (Langkah 3/4)\n\nPilih preset frekuensi eksekusi atau pilih custom:", html.EscapeString(sess.ID))

	menu := &tele.ReplyMarkup{}
	btnHourly := menu.Data("⏱️ Setiap 1 Jam (0 * * * *)", "cron_sc_hourly")
	btnMorning := menu.Data("🌅 Setiap Pagi 07:00 (0 7 * * *)", "cron_sc_morning")
	btnEvening := menu.Data("🌇 Setiap Sore 17:00 (0 17 * * *)", "cron_sc_evening")
	btnWeekly := menu.Data("📅 Setiap Senin 08:00 (0 8 * * 1)", "cron_sc_weekly")
	btnCustom := menu.Data("✏️ Ketik Cron Expression Khusus", "cron_sc_custom")
	btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")

	menu.Inline(
		menu.Row(btnHourly),
		menu.Row(btnMorning),
		menu.Row(btnEvening),
		menu.Row(btnWeekly),
		menu.Row(btnCustom),
		menu.Row(btnCancel),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// PromptCronPrompt prompts for prompt text
func (ui *CronUI) PromptCronPrompt(c tele.Context, sess *CronSession) error {
	sess.Step = CronStepPrompt
	text := fmt.Sprintf("📝 <b>MASUKKAN INSTRUKSI AI (%s)</b> (Langkah 4/4)\n\n"+
		"Kirimkan prompt perintah AI yang akan dieksekusi otomatis dan hasilnya dikirim ke chat target.\n\n"+
		"<b>Contoh:</b>\n"+
		"<code>Rangkum berita teknologi terkini hari ini dalam 5 poin penting.</code>\n"+
		"<code>Buat motivasi pagi dan pengingat target harian tim.</code>", html.EscapeString(sess.ID))

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")
	menu.Inline(menu.Row(btnCancel))

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleTextMessage handles chat input during cron setup
func (ui *CronUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}
	userID := c.Sender().ID

	ui.mu.RLock()
	sess, exists := ui.sessions[userID]
	ui.mu.RUnlock()

	if !exists || sess.Step == CronStepNone {
		return false, nil
	}

	msgText := strings.TrimSpace(c.Text())
	if msgText == "/cancel" || strings.EqualFold(msgText, "batal") {
		ui.CancelWizard(userID)
		return true, c.Reply("❌ Wizard cron dibatalkan.")
	}

	switch sess.Step {
	case CronStepID:
		cleanID := strings.ToLower(strings.ReplaceAll(msgText, " ", "_"))
		sess.ID = cleanID
		sess.Step = CronStepNone
		return true, ui.RenderCronChannelTypePicker(c, sess)

	case CronStepChatID:
		sess.TargetChatID = msgText
		sess.Step = CronStepNone
		return true, ui.RenderCronSchedulePicker(c, sess)

	case CronStepCustomCron:
		sess.CronExpr = msgText
		return true, ui.PromptCronPrompt(c, sess)

	case CronStepPrompt:
		sess.Prompt = msgText
		rec := &storage.CronJobRecord{
			ID:            sess.ID,
			Name:          sess.ID,
			CronExpr:      sess.CronExpr,
			TargetChannel: sess.TargetChannel,
			TargetChatID:  sess.TargetChatID,
			Prompt:        sess.Prompt,
			IsActive:      true,
		}

		if err := ui.db.SaveCronJob(rec); err != nil {
			return true, c.Reply(fmt.Sprintf("❌ Gagal menyimpan cron job: %v", html.EscapeString(err.Error())))
		}
		_ = ui.scheduler.Reload()
		ui.CancelWizard(userID)

		text := fmt.Sprintf("🎉 <b>CRON JOB BERHASIL DIJADWALKAN!</b>\n\n"+
			"• ID: <code>%s</code>\n"+
			"• Jadwal: <code>%s</code>\n"+
			"• Target: <b>%s</b> (Chat: <code>%s</code>)\n"+
			"• Prompt: <i>\"%s\"</i>\n\n"+
			"<i>Sistem otomatis menjalankan tugas ini sesuai jadwal yang telah ditentukan!</i>",
			html.EscapeString(rec.ID), html.EscapeString(rec.CronExpr),
			html.EscapeString(rec.TargetChannel), html.EscapeString(rec.TargetChatID),
			html.EscapeString(rec.Prompt))

		menu := &tele.ReplyMarkup{}
		btnRunNow := menu.Data("▶️ Jalankan Detik Ini Juga", fmt.Sprintf("cron_run_%s", rec.ID))
		btnBack := menu.Data("⬅️ Kembali ke Menu Cron", "menu_cron")
		menu.Inline(
			menu.Row(btnRunNow),
			menu.Row(btnBack),
		)

		return true, c.Reply(text, menu, tele.ModeHTML)
	}

	return false, nil
}

// CancelWizard clears user session
func (ui *CronUI) CancelWizard(userID int64) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	delete(ui.sessions, userID)
}

// GetSession returns session
func (ui *CronUI) GetSession(userID int64) (*CronSession, bool) {
	ui.mu.RLock()
	defer ui.mu.RUnlock()
	s, ok := ui.sessions[userID]
	return s, ok
}

// SetSessionStep sets session step
func (ui *CronUI) SetSessionStep(userID int64, step CronStep) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if sess, ok := ui.sessions[userID]; ok {
		sess.Step = step
		sess.UpdatedAt = time.Now()
	}
}

// HandleAddCron processes `/addcron`
func (ui *CronUI) HandleAddCron(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return ui.StartCronWizard(c)
	}
	if len(args) < 5 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/addcron morning_brief telegram -100123456 \"0 7 * * *\" Buat rangkuman berita pagi.</code>", tele.ModeHTML)
	}

	id := args[0]
	targetChan := strings.ToLower(args[1])
	targetChatID := args[2]
	cronExpr := args[3]
	prompt := strings.Join(args[4:], " ")

	rec := &storage.CronJobRecord{
		ID:            id,
		Name:          id,
		CronExpr:      cronExpr,
		TargetChannel: targetChan,
		TargetChatID:  targetChatID,
		Prompt:        prompt,
		IsActive:      true,
	}

	if err := ui.db.SaveCronJob(rec); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan cron job: %v", html.EscapeString(err.Error())))
	}

	_ = ui.scheduler.Reload()
	return c.Reply(fmt.Sprintf("✅ Cron job <b>%s</b> (<code>%s</code>) berhasil dijadwalkan!", html.EscapeString(id), html.EscapeString(cronExpr)), tele.ModeHTML)
}

// HandleRunCron processes `/runcron`
func (ui *CronUI) HandleRunCron(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan ID cron job yang ingin dijalankan.\nContoh: <code>/runcron morning_brief</code>", tele.ModeHTML)
	}

	id := args[0]
	if err := ui.scheduler.RunNow(id); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menjalankan cron job: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("🚀 Cron job <code>%s</code> sedang dieksekusi di background...", html.EscapeString(id)), tele.ModeHTML)
}

// HandleDelCron processes `/delcron`
func (ui *CronUI) HandleDelCron(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan ID cron job yang ingin dihapus.\nContoh: <code>/delcron morning_brief</code>", tele.ModeHTML)
	}

	id := args[0]
	if err := ui.db.DeleteCronJob(id); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menghapus cron job: %v", html.EscapeString(err.Error())))
	}

	_ = ui.scheduler.Reload()
	return c.Reply(fmt.Sprintf("🗑️ Cron job <code>%s</code> berhasil dihapus.", html.EscapeString(id)), tele.ModeHTML)
}

