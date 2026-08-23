package admin

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"goassistant/internal/checkin"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type CheckinSessionStep int

const (
	CheckinStepIdle CheckinSessionStep = iota
	CheckinStepInputUserID
)

type CheckinSession struct {
	Step      CheckinSessionStep
	CreatedAt time.Time
}

type CheckinUI struct {
	db       *storage.DB
	svc      *checkin.Service
	mu       sync.Mutex
	sessions map[int64]*CheckinSession
}

func NewCheckinUI(db *storage.DB, svc *checkin.Service) *CheckinUI {
	return &CheckinUI{
		db:       db,
		svc:      svc,
		sessions: make(map[int64]*CheckinSession),
	}
}

// RenderCheckinDashboard renders the main dashboard for HCNSEC checkin management
func (ui *CheckinUI) RenderCheckinDashboard() (string, *tele.ReplyMarkup) {
	users := ui.svc.ListUserIDs()
	isEnabled := ui.svc.IsEnabled()
	lastRun, _ := ui.svc.GetLastRun()

	statusBadge := "🟢 Aktif (Berjalan setiap hari pukul 00:05 WIB)"
	if !isEnabled {
		statusBadge = "🔴 Nonaktif"
	}

	lastRunText := "<i>Belum pernah dijalankan</i>"
	if !lastRun.IsZero() {
		locWIB, err := time.LoadLocation("Asia/Jakarta")
		if err != nil {
			locWIB = time.FixedZone("WIB", 7*3600)
		}
		lastRunText = fmt.Sprintf("<code>%s</code>", lastRun.In(locWIB).Format("2006-01-02 15:04:05 WIB"))
	}

	var sb strings.Builder
	sb.WriteString("🎁 <b>AUTO CHECK-IN HCNSEC (NEW API)</b>\n\n")
	sb.WriteString("Fitur ini melakukan absensi harian otomatis ke <code>https://api.hcnsec.cn/api/user/checkin</code> untuk klaim kuota saldo gratis setiap hari.\n\n")
	sb.WriteString(fmt.Sprintf("• <b>Status Otomatis:</b> %s\n", statusBadge))
	sb.WriteString(fmt.Sprintf("• <b>Terakhir Dijalankan:</b> %s\n", lastRunText))
	sb.WriteString(fmt.Sprintf("• <b>Total User Terdaftar:</b> <code>%d user</code>\n\n", len(users)))

	if len(users) > 0 {
		sb.WriteString("📋 <b>Daftar New-Api-User ID:</b>\n")
		for i, u := range users {
			sb.WriteString(fmt.Sprintf("  %d. <code>%s</code>\n", i+1, html.EscapeString(u)))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("⚠️ <i>Belum ada User ID yang didaftarkan. Klik 'Tambah User ID' untuk mendaftarkan akun.</i>\n\n")
	}

	sb.WriteString("💡 <i>Pilih aksi di bawah:</i>")

	menu := &tele.ReplyMarkup{}
	btnRun := menu.Data("🚀 Check-in Sekarang", "checkin_btn_run")
	btnAdd := menu.Data("➕ Tambah User ID", "checkin_btn_add")

	toggleLabel := "⏸️ Nonaktifkan Auto"
	if !isEnabled {
		toggleLabel = "▶️ Aktifkan Auto"
	}
	btnToggle := menu.Data(toggleLabel, "checkin_btn_toggle")
	btnRefresh := menu.Data("🔄 Refresh", "checkin_btn_refresh")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	var rows []tele.Row
	rows = append(rows, menu.Row(btnRun, btnAdd))
	rows = append(rows, menu.Row(btnToggle, btnRefresh))

	// If there are users, add quick delete buttons
	if len(users) > 0 && len(users) <= 8 {
		var delButtons []tele.Btn
		for i, u := range users {
			delButtons = append(delButtons, menu.Data(fmt.Sprintf("🗑 Hapus #%d (%s)", i+1, u), fmt.Sprintf("checkin_btn_del_%d", i)))
		}
		// Group in pairs of 2
		for i := 0; i < len(delButtons); i += 2 {
			if i+1 < len(delButtons) {
				rows = append(rows, menu.Row(delButtons[i], delButtons[i+1]))
			} else {
				rows = append(rows, menu.Row(delButtons[i]))
			}
		}
	}

	rows = append(rows, menu.Row(btnBack))
	menu.Inline(rows...)

	return sb.String(), menu
}

func (ui *CheckinUI) HandleMenu(c tele.Context) error {
	txt, kb := ui.RenderCheckinDashboard()
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *CheckinUI) HandleRunNow(c tele.Context) error {
	_ = c.Respond(&tele.CallbackResponse{Text: "🚀 Menjalankan check-in..."})
	_ = c.EditOrSend("⏳ <i>Sedang menghubungi https://api.hcnsec.cn untuk eksekusi check-in...</i>", tele.ModeHTML)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		_, report := ui.svc.RunAll(ctx)
		txt, kb := ui.RenderCheckinDashboard()

		fullResp := report + "\n\n" + txt
		_ = c.Send(fullResp, kb, tele.ModeHTML)
	}()

	return nil
}

func (ui *CheckinUI) HandleToggle(c tele.Context) error {
	current := ui.svc.IsEnabled()
	_ = ui.svc.SetEnabled(!current)
	txt, kb := ui.RenderCheckinDashboard()
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *CheckinUI) PromptAddUser(c tele.Context) error {
	ui.mu.Lock()
	ui.sessions[c.Sender().ID] = &CheckinSession{
		Step:      CheckinStepInputUserID,
		CreatedAt: time.Now(),
	}
	ui.mu.Unlock()

	menu := &tele.ReplyMarkup{}
	btnCancel := menu.Data("❌ Batal", "checkin_btn_cancel")
	menu.Inline(menu.Row(btnCancel))

	msg := "➕ <b>TAMBAH NEW-API-USER ID</b>\n\n" +
		"Silakan kirimkan nilai <b>New-Api-User</b> (User ID HCNSEC Anda berupa angka/ID).\n\n" +
		"<i>Contoh:</i> <code>10294</code> atau beberapa user ID dipisahkan koma: <code>10294, 20381</code>"

	return c.EditOrSend(msg, menu, tele.ModeHTML)
}

func (ui *CheckinUI) HandleDeleteUserCallback(c tele.Context, idxStr string) error {
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Index invalid"})
	}

	users := ui.svc.ListUserIDs()
	if idx < 0 || idx >= len(users) {
		return c.Respond(&tele.CallbackResponse{Text: "User ID sudah tidak ada"})
	}

	target := users[idx]
	_ = ui.svc.RemoveUserID(target)
	_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("User %s berhasil dihapus", target)})

	txt, kb := ui.RenderCheckinDashboard()
	return c.EditOrSend(txt, kb, tele.ModeHTML)
}

func (ui *CheckinUI) CancelSession(senderID int64) {
	ui.mu.Lock()
	delete(ui.sessions, senderID)
	ui.mu.Unlock()
}

func (ui *CheckinUI) HandleTextMessage(c tele.Context) (bool, error) {
	if c.Sender() == nil {
		return false, nil
	}

	ui.mu.Lock()
	sess, exists := ui.sessions[c.Sender().ID]
	if !exists {
		ui.mu.Unlock()
		return false, nil
	}

	if time.Since(sess.CreatedAt) > 5*time.Minute {
		delete(ui.sessions, c.Sender().ID)
		ui.mu.Unlock()
		return false, nil
	}

	step := sess.Step
	delete(ui.sessions, c.Sender().ID)
	ui.mu.Unlock()

	if step == CheckinStepInputUserID {
		input := strings.TrimSpace(c.Text())
		if input == "" {
			return true, c.Reply("❌ User ID tidak boleh kosong.")
		}

		tokens := strings.FieldsFunc(input, func(r rune) bool {
			return r == ',' || r == '\n' || r == ' '
		})

		var added []string
		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if tok != "" {
				if err := ui.svc.AddUserID(tok); err == nil {
					added = append(added, tok)
				}
			}
		}

		if len(added) == 0 {
			return true, c.Reply("⚠️ Tidak ada User ID baru yang ditambahkan (mungkin sudah terdaftar).")
		}

		_ = c.Reply(fmt.Sprintf("✅ Berhasil menambahkan %d User ID: <code>%s</code>", len(added), strings.Join(added, ", ")), tele.ModeHTML)
		txt, kb := ui.RenderCheckinDashboard()
		return true, c.Send(txt, kb, tele.ModeHTML)
	}

	return false, nil
}
