package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/cron"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type CronUI struct {
	db        *storage.DB
	scheduler *cron.Scheduler
}

func NewCronUI(db *storage.DB, s *cron.Scheduler) *CronUI {
	return &CronUI{db: db, scheduler: s}
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
	sb.WriteString("• <code>/addcron &lt;id&gt; &lt;telegram|whatsapp&gt; &lt;chat_id&gt; \"&lt;cron_expr&gt;\" &lt;prompt&gt;</code>\n")
	sb.WriteString("• <code>/runcron &lt;id&gt;</code> (Jalankan langsung sekarang)\n")
	sb.WriteString("• <code>/delcron &lt;id&gt;</code> (Hapus jadwal)\n\n")
	sb.WriteString("<b>Contoh Menambah Tugas Terjadwal:</b>\n")
	sb.WriteString("<code>/addcron morning_brief telegram -100123456789 \"0 7 * * *\" Rangkumlah berita teknologi dan cuaca hari ini.</code>\n")

	return sb.String()
}

// HandleAddCron processes `/addcron`
func (ui *CronUI) HandleAddCron(c tele.Context) error {
	args := c.Args()
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

	// Reload scheduler
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
