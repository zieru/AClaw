package admin

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"goassistant/internal/storage"
	"goassistant/internal/webadmin"
	tele "gopkg.in/telebot.v3"
)

type WebAdminUI struct {
	server      *webadmin.Server
	db          *storage.DB
	bot         *tele.Bot
	waitingPort sync.Map
}

func NewWebAdminUI(server *webadmin.Server, db *storage.DB, bot *tele.Bot) *WebAdminUI {
	return &WebAdminUI{
		server: server,
		db:     db,
		bot:    bot,
	}
}

// HandleWebAdminDashboard renders the Telegram UI for Web Admin configuration
func (u *WebAdminUI) HandleWebAdminDashboard(c tele.Context) error {
	port := 12111
	if u.server != nil {
		port = u.server.GetPort()
	}

	hostIP := getLocalOutboundIP()
	url := fmt.Sprintf("http://%s:%d/admin", hostIP, port)

	text := fmt.Sprintf("🌐 <b>GOASSISTANT WEB ADMIN CONTROL</b>\n\n"+
		"Status: 🟢 <b>Aktif & Terlindungi (2FA OTP)</b>\n"+
		"Port Aktif: <code>%d</code>\n"+
		"URL Akses: <code>%s</code>\n\n"+
		"<b>Fitur Unggulan Web Admin:</b>\n"+
		"• 🔐 <b>Login OTP Telegram:</b> Kode 6-digit dikirimkan langsung ke Telegram Admin ini.\n"+
		"• 📊 <b>Monitoring & Audit Log:</b> Pantau seluruh aktivitas prompt, respon, token & biaya.\n"+
		"• 💬 <b>Web Chat AI:</b> Antarmuka obrolan asisten dengan streaming respon & proses berpikir.\n"+
		"• ⚙️ <b>Ganti Port Dinamis:</b> Ubah port kapan saja tanpa restart daemon.\n\n"+
		"💡 <i>Gunakan perintah <code>/setwebport &lt;port&gt;</code> atau klik tombol di bawah:</i>",
		port, url)

	menu := &tele.ReplyMarkup{}
	btnSetPort := menu.Data("⚙️ Ubah Port", "btn_webadmin_setport")
	btnRestart := menu.Data("🔄 Restart Web", "btn_webadmin_restart")
	btnBack := menu.Data("⬅️ Kembali", "menu_main")

	menu.Inline(
		menu.Row(btnSetPort, btnRestart),
		menu.Row(btnBack),
	)

	return c.EditOrSend(text, menu, tele.ModeHTML)
}

// HandleSetPortCommand handles /setwebport <port>
func (u *WebAdminUI) HandleSetPortCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		u.waitingPort.Store(c.Sender().ID, true)
		return c.Send("📝 <b>PENGATURAN PORT WEB ADMIN</b>\n\n"+
			"Silakan kirimkan nomor port baru yang ingin digunakan (antara <code>1024</code> sampai <code>65535</code>):\n"+
			"<i>Contoh: 8088 atau 12111</i>\n\n"+
			"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
	}

	portStr := args[0]
	return u.applyPortChange(c, portStr)
}

// HandleTextMessage intercepts text input if user is in waitingPort state
func (u *WebAdminUI) HandleTextMessage(c tele.Context) (bool, error) {
	if _, ok := u.waitingPort.Load(c.Sender().ID); !ok {
		return false, nil
	}

	text := strings.TrimSpace(c.Text())
	if text == "/cancel" || text == "/stop" {
		u.waitingPort.Delete(c.Sender().ID)
		return true, c.Send("🛑 Pengaturan port dibatalkan.", tele.ModeHTML)
	}

	u.waitingPort.Delete(c.Sender().ID)
	return true, u.applyPortChange(c, text)
}

// HandleSetPortCallback handles button "⚙️ Ubah Port"
func (u *WebAdminUI) HandleSetPortCallback(c tele.Context) error {
	_ = c.Respond()
	u.waitingPort.Store(c.Sender().ID, true)
	return c.Send("📝 <b>PENGATURAN PORT WEB ADMIN</b>\n\n"+
		"Silakan kirimkan nomor port baru yang diinginkan (antara <code>1024</code> sampai <code>65535</code>):\n"+
		"<i>Contoh: 8088</i>\n\n"+
		"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

// HandleRestartCallback handles button "🔄 Restart Web"
func (u *WebAdminUI) HandleRestartCallback(c tele.Context) error {
	_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Merestart Web Admin..."})
	if u.server == nil {
		return c.Send("⚠️ Server Web Admin tidak aktif.", tele.ModeHTML)
	}

	currentPort := u.server.GetPort()
	if err := u.server.Restart(currentPort); err != nil {
		return c.Send(fmt.Sprintf("❌ Gagal merestart Web Admin: %v", err), tele.ModeHTML)
	}

	return u.HandleWebAdminDashboard(c)
}

func (u *WebAdminUI) applyPortChange(c tele.Context, portStr string) error {
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port < 1024 || port > 65535 {
		return c.Send("⚠️ <b>Port tidak valid.</b> Masukkan angka port antara <code>1024</code> sampai <code>65535</code>.", tele.ModeHTML)
	}

	if u.server == nil {
		return c.Send("⚠️ Layanan Web Admin belum diaktifkan.", tele.ModeHTML)
	}

	oldPort := u.server.GetPort()
	if oldPort == port {
		return c.Send(fmt.Sprintf("ℹ️ Web Admin sudah berjalan di port <code>%d</code>.", port), tele.ModeHTML)
	}

	msg, _ := u.bot.Send(c.Chat(), fmt.Sprintf("⏳ <i>Sedang memindahkan listener Web Admin dari port %d ke %d...</i>", oldPort, port), tele.ModeHTML)

	if err := u.server.Restart(port); err != nil {
		if msg != nil {
			_, _ = u.bot.Edit(msg, fmt.Sprintf("❌ <b>Gagal berpindah port:</b> %v", err), tele.ModeHTML)
		}
		return err
	}

	hostIP := getLocalOutboundIP()
	newURL := fmt.Sprintf("http://%s:%d/admin", hostIP, port)

	successText := fmt.Sprintf("✅ <b>PORT WEB ADMIN BERHASIL DIUBAH!</b>\n\n"+
		"🌐 <b>Port Baru:</b> <code>%d</code>\n"+
		"🔗 <b>URL Akses:</b> <code>%s</code>\n\n"+
		"Sistem listener Web Admin telah aktif di port baru dan siap digunakan.", port, newURL)

	if msg != nil {
		_, _ = u.bot.Edit(msg, successText, tele.ModeHTML)
	} else {
		_ = c.Send(successText, tele.ModeHTML)
	}

	return nil
}

func (u *WebAdminUI) CancelSession(userID int64) {
	u.waitingPort.Delete(userID)
}

func getLocalOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
