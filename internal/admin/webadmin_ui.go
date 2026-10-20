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
	waitingKey  sync.Map
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

	currentKey := "sk-..."
	if u.server != nil && u.server.AuthManager() != nil {
		currentKey = u.server.AuthManager().GetAPIKey()
	}

	text := fmt.Sprintf("🌐 <b>GOASSISTANT WEB ADMIN & AI API</b>\n\n"+
		"Status: 🟢 <b>Aktif & Terlindungi</b>\n"+
		"Port Aktif: <code>%d</code>\n"+
		"URL Web Admin: <code>%s</code>\n"+
		"🔑 <b>AI API Bearer Token:</b> <code>%s</code>\n"+
		"🤖 <b>OpenAI API Endpoint:</b> <code>http://%s:%d/v1</code>\n\n"+
		"<b>Fitur Unggulan:</b>\n"+
		"• 🔐 <b>Login OTP Telegram:</b> Kode 6-digit dikirimkan langsung ke Telegram Admin ini.\n"+
		"• 🤖 <b>OpenAI Compatible API:</b> <code>/v1/chat/completions</code> & <code>/v1/models</code> (dukung streaming & Python SDK).\n"+
		"• 📊 <b>Monitoring & Audit Log:</b> Pantau seluruh aktivitas prompt, respon, token & biaya.\n"+
		"• ⚙️ <b>Ganti Port & Key Dinamis:</b> Ubah port atau API key kapan saja tanpa restart daemon.\n\n"+
		"💡 <i>Gunakan perintah <code>/setwebport &lt;port&gt;</code>, <code>/setapikey &lt;key&gt;</code>, <code>/genapikey</code> atau klik tombol di bawah:</i>",
		port, url, currentKey, hostIP, port)

	menu := &tele.ReplyMarkup{}
	btnSetPort := menu.Data("⚙️ Ubah Port", "btn_webadmin_setport")
	btnSetKey := menu.Data("🔑 Set API Key", "btn_webadmin_setkey")
	btnGenKey := menu.Data("🎲 Gen Key Baru", "btn_webadmin_genkey")
	btnRestart := menu.Data("🔄 Restart Web", "btn_webadmin_restart")
	btnBack := menu.Data("⬅️ Kembali", "menu_main")

	menu.Inline(
		menu.Row(btnSetPort, btnSetKey),
		menu.Row(btnGenKey, btnRestart),
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

// HandleTextMessage intercepts text input if user is in waitingPort or waitingKey state
func (u *WebAdminUI) HandleTextMessage(c tele.Context) (bool, error) {
	if _, ok := u.waitingKey.Load(c.Sender().ID); ok {
		text := strings.TrimSpace(c.Text())
		u.waitingKey.Delete(c.Sender().ID)
		if text == "/cancel" || text == "/stop" {
			return true, c.Send("🛑 Pengaturan API Key dibatalkan.", tele.ModeHTML)
		}
		return true, u.applyAPIKeyChange(c, text)
	}

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

// HandleSetAPIKeyCommand handles /setapikey <key>
func (u *WebAdminUI) HandleSetAPIKeyCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		u.waitingKey.Store(c.Sender().ID, true)
		return c.Send("🔑 <b>PENGATURAN AI API BEARER TOKEN</b>\n\n"+
			"Silakan kirimkan API Key baru (standar berawalan <code>sk-...</code>):\n"+
			"<i>Contoh: sk-mysecretkey2026</i>\n\n"+
			"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
	}

	return u.applyAPIKeyChange(c, args[0])
}

// HandleGenAPIKeyCommand handles /genapikey
func (u *WebAdminUI) HandleGenAPIKeyCommand(c tele.Context) error {
	if u.server == nil || u.server.AuthManager() == nil {
		return c.Send("⚠️ Server Web Admin tidak aktif.", tele.ModeHTML)
	}

	newKey, err := u.server.AuthManager().GenerateAPIKey()
	if err != nil {
		return c.Send(fmt.Sprintf("❌ Gagal membuat API Key baru: %v", err), tele.ModeHTML)
	}

	port := u.server.GetPort()
	hostIP := getLocalOutboundIP()

	return c.Send(fmt.Sprintf("🎲 <b>API KEY BARU BERHASIL DIBUAT!</b>\n\n"+
		"Kunci Aktif: <code>%s</code>\n"+
		"Prefix: <code>sk-</code>\n\n"+
		"📌 <b>Header HTTP:</b>\n"+
		"<code>Authorization: Bearer %s</code>\n\n"+
		"🤖 <b>Endpoint AI:</b>\n"+
		"<code>http://%s:%d/v1/chat/completions</code>",
		newKey, newKey, hostIP, port), tele.ModeHTML)
}

func (u *WebAdminUI) applyAPIKeyChange(c tele.Context, keyStr string) error {
	if u.server == nil || u.server.AuthManager() == nil {
		return c.Send("⚠️ Server Web Admin tidak aktif.", tele.ModeHTML)
	}

	newKey, err := u.server.AuthManager().SetAPIKey(keyStr)
	if err != nil {
		return c.Send(fmt.Sprintf("❌ Gagal menyetel API Key: %v", err), tele.ModeHTML)
	}

	port := u.server.GetPort()
	hostIP := getLocalOutboundIP()

	return c.Send(fmt.Sprintf("✅ <b>API KEY BERHASIL DIPERBARUI!</b>\n\n"+
		"Kunci Aktif: <code>%s</code>\n"+
		"Prefix: <code>sk-</code>\n\n"+
		"📌 <b>Header HTTP:</b>\n"+
		"<code>Authorization: Bearer %s</code>\n\n"+
		"🤖 <b>Endpoint AI:</b>\n"+
		"<code>http://%s:%d/v1/chat/completions</code>",
		newKey, newKey, hostIP, port), tele.ModeHTML)
}

// HandleSetKeyCallback handles button "🔑 Set API Key"
func (u *WebAdminUI) HandleSetKeyCallback(c tele.Context) error {
	_ = c.Respond()
	u.waitingKey.Store(c.Sender().ID, true)
	return c.Send("🔑 <b>PENGATURAN AI API BEARER TOKEN</b>\n\n"+
		"Silakan kirimkan API Key baru berformat <code>sk-...</code>:\n"+
		"<i>Contoh: sk-mysecrettoken</i>\n\n"+
		"💡 <i>Kirim /cancel untuk membatalkan.</i>", tele.ModeHTML)
}

// HandleGenKeyCallback handles button "🎲 Gen Key Baru"
func (u *WebAdminUI) HandleGenKeyCallback(c tele.Context) error {
	_ = c.Respond(&tele.CallbackResponse{Text: "🎲 Menghasilkan key acak baru..."})
	return u.HandleGenAPIKeyCommand(c)
}

// HandleRestartCallback handles button "🔄 Restart Web"
func (u *WebAdminUI) HandleRestartCallback(c tele.Context) error {
	_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Merestart Web Admin..."})
	if u.server == nil {
		return c.Send("⚠️ Server Web Admin tidak aktif.", tele.ModeHTML)
	}

	currentPort := u.server.GetPort()
	if err := u.server.RestartPort(currentPort); err != nil {
		return c.Send(fmt.Sprintf("❌ Gagal merestart Web Admin: %v", err), tele.ModeHTML)
	}

	return u.HandleWebAdminDashboard(c)
}

// HandleSetBindCommand handles /setwebbind <address>
func (u *WebAdminUI) HandleSetBindCommand(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("📝 <b>PENGATURAN BINDING ADDRESS WEB ADMIN</b>\n\n"+
			"Format: <code>/setwebbind &lt;address&gt;</code>\n"+
			"<i>Contoh:</i>\n"+
			"• <code>/setwebbind 0.0.0.0</code> (Dapat diakses dari seluruh jaringan)\n"+
			"• <code>/setwebbind 127.0.0.1</code> (Hanya akses lokal server/localhost)\n\n"+
			fmt.Sprintf("Binding aktif saat ini: <code>%s</code>", u.server.GetBindAddress()), tele.ModeHTML)
	}

	newBind := strings.TrimSpace(args[0])
	if u.server == nil {
		return c.Send("⚠️ Layanan Web Admin belum diaktifkan.", tele.ModeHTML)
	}

	currentPort := u.server.GetPort()
	if err := u.server.Restart(newBind, currentPort); err != nil {
		return c.Send(fmt.Sprintf("❌ <b>Gagal mengatur binding address:</b> %v", err), tele.ModeHTML)
	}

	return c.Send(fmt.Sprintf("✅ <b>BINDING ADDRESS BERHASIL DIUBAH!</b>\n\n"+
		"🌐 <b>Host:</b> <code>%s</code>\n"+
		"🔌 <b>Port:</b> <code>%d</code>\n"+
		"🔗 <b>URL:</b> <code>%s</code>", newBind, currentPort, u.server.GetURL()), tele.ModeHTML)
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

	if err := u.server.RestartPort(port); err != nil {
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
