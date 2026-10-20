package admin

import (
	"fmt"
	"html"
	"log"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/storage"

	tele "gopkg.in/telebot.v3"
)

// GrapariApprovalManager coordinates authorization between GraPARI WA channels and Telegram Awesome Leap Bot
type GrapariApprovalManager struct {
	bot        *tele.Bot
	db         *storage.DB
	cfg        *config.AppConfig
	mu         sync.RWMutex
	waNotifier func(channelID, targetJID, message string) error
}

var (
	globalGrapariApprovalMgr  *GrapariApprovalManager
	globalGrapariApprovalOnce sync.Once
)

// InitGrapariApprovalManager initializes the singleton manager
func InitGrapariApprovalManager(bot *tele.Bot, db *storage.DB, cfg *config.AppConfig) *GrapariApprovalManager {
	globalGrapariApprovalOnce.Do(func() {
		globalGrapariApprovalMgr = &GrapariApprovalManager{
			bot: bot,
			db:  db,
			cfg: cfg,
		}
	})
	return globalGrapariApprovalMgr
}

// GetGrapariApprovalManager returns the initialized manager
func GetGrapariApprovalManager() *GrapariApprovalManager {
	return globalGrapariApprovalMgr
}

// SetWANotifier registers callback to send WA message back to users
func (m *GrapariApprovalManager) SetWANotifier(notifier func(channelID, targetJID, message string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waNotifier = notifier
}

// NotifyNewRequest sends an authorization card to the Telegram approval group
func (m *GrapariApprovalManager) NotifyNewRequest(channelID, channelName string, user *storage.GrapariUser) error {
	if m == nil || m.bot == nil {
		return fmt.Errorf("grapari approval manager / bot not initialized")
	}

	dataDir := "./data"
	if m.cfg != nil && m.cfg.Server.DataDir != "" {
		dataDir = m.cfg.Server.DataDir
	}

	gdb, err := storage.OpenGrapariDB(dataDir, channelID, channelName)
	if err != nil {
		return fmt.Errorf("failed to open grapari db: %w", err)
	}

	targetGroupID, err := gdb.GetApprovalGroup()
	if err != nil || targetGroupID == 0 {
		log.Printf("⚠️ [GraPARI-Auth] Channel '%s' belum memiliki grup Telegram approval. Gunakan /setapprovalgroup di grup target.", channelName)
		return nil
	}

	text := fmt.Sprintf("🔔 <b>PERMOHONAN OTORISASI OPERASIONAL GRAPARI</b>\n\n"+
		"• <b>Nama Petugas:</b> %s\n"+
		"• <b>NIK / SIAD:</b> <code>%s</code>\n"+
		"• <b>Role:</b> <b>%s</b>\n"+
		"• <b>No. WhatsApp:</b> <code>+%s</code>\n"+
		"• <b>Channel:</b> %s (<code>%s</code>)\n"+
		"• <b>Waktu:</b> %s WIB\n\n"+
		"<i>Silakan tentukan keputusan otorisasi di bawah ini:</i>",
		html.EscapeString(user.Name),
		html.EscapeString(user.NIKSIAD),
		html.EscapeString(user.Role),
		html.EscapeString(user.PhoneNumber),
		html.EscapeString(channelName),
		html.EscapeString(channelID),
		time.Now().Format("02/01/2006 15:04"),
	)

	menu := &tele.ReplyMarkup{}
	btnAuth := menu.Data("✅ Izinkan (Authorize)", fmt.Sprintf("grp_auth_%s__%s", channelID, user.UserJID))
	btnReject := menu.Data("❌ Tolak (Reject)", fmt.Sprintf("grp_rej_%s__%s", channelID, user.UserJID))

	menu.Inline(menu.Row(btnAuth, btnReject))

	targetChat := &tele.Chat{ID: targetGroupID}
	_, sendErr := m.bot.Send(targetChat, text, menu, tele.ModeHTML)
	if sendErr != nil {
		log.Printf("❌ [GraPARI-Auth] Gagal mengirim pesan ke grup Telegram %d: %v", targetGroupID, sendErr)
	}
	return sendErr
}

// HandleSetApprovalGroup handles /setapprovalgroup command inside Telegram group
func (m *GrapariApprovalManager) HandleSetApprovalGroup(c tele.Context) error {
	if c.Chat() == nil || (c.Chat().Type != tele.ChatGroup && c.Chat().Type != tele.ChatSuperGroup) {
		return c.Reply("⚠️ Perintah <code>/setapprovalgroup</code> harus dijalankan di dalam <b>Grup Telegram</b> (tempat bot diundang).", tele.ModeHTML)
	}

	// Verify if sender is admin
	if c.Sender() != nil && m.cfg != nil && !m.cfg.IsAdminUser(c.Sender().ID) {
		return c.Reply("⛔ <b>Akses Ditolak:</b> Hanya administrator yang dapat menyetel grup otorisasi.", tele.ModeHTML)
	}

	groupID := c.Chat().ID
	groupTitle := c.Chat().Title

	// Find active GraPARI channels
	channels, err := m.db.ListChannels()
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membaca channel: %v", err))
	}

	dataDir := "./data"
	if m.cfg != nil && m.cfg.Server.DataDir != "" {
		dataDir = m.cfg.Server.DataDir
	}

	grapariCount := 0
	var boundChannels []string
	for _, ch := range channels {
		if ch.IsGrapariOnly() {
			gdb, err := storage.OpenGrapariDB(dataDir, ch.ID, ch.Name)
			if err == nil {
				_ = gdb.SetApprovalGroup(groupID)
				boundChannels = append(boundChannels, fmt.Sprintf("%s (<code>%s</code>)", ch.Name, ch.ID))
				grapariCount++
			}
		}
	}

	if grapariCount == 0 {
		return c.Reply("ℹ️ Belum ada channel yang diaktifkan sebagai <b>Mode Khusus GraPARI</b>.\nSilakan aktifkan toggle GraPARI di <code>/channelwizard</code> terlebih dahulu.", tele.ModeHTML)
	}

	reply := fmt.Sprintf("✅ <b>GRUP OTORISASI GRAPARI BERHASIL DISETEL!</b>\n\n"+
		"• <b>Grup:</b> %s (<code>%d</code>)\n"+
		"• <b>Channel Terhubung:</b>\n- %s\n\n"+
		"Mulai sekarang, setiap petugas yang melakukan registrasi di WhatsApp akan otomatis mengirimkan kartu otorisasi ke grup ini.",
		html.EscapeString(groupTitle), groupID, strings.Join(boundChannels, "\n- "))

	return c.Reply(reply, tele.ModeHTML)
}

// HandleApprovalCallback processes the [Authorize] or [Reject] button clicks
func (m *GrapariApprovalManager) HandleApprovalCallback(c tele.Context, action, channelID, userJID string) error {
	sender := c.Sender()
	approverName := "Admin"
	if sender != nil {
		if sender.Username != "" {
			approverName = "@" + sender.Username
		} else {
			approverName = sender.FirstName
		}
	}

	dataDir := "./data"
	if m.cfg != nil && m.cfg.Server.DataDir != "" {
		dataDir = m.cfg.Server.DataDir
	}

	ch, _ := m.db.GetChannel(channelID)
	channelName := channelID
	if ch != nil {
		channelName = ch.Name
	}

	gdb, err := storage.OpenGrapariDB(dataDir, channelID, channelName)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Gagal membuka database channel: " + err.Error(), ShowAlert: true})
	}

	targetUser, err := gdb.GetUser(userJID)
	if err != nil || targetUser == nil {
		return c.Respond(&tele.CallbackResponse{Text: "User tidak ditemukan di database.", ShowAlert: true})
	}

	if targetUser.Status == storage.GrapariStatusAuthorized {
		return c.Respond(&tele.CallbackResponse{Text: "Permohonan ini sudah disetujui sebelumnya.", ShowAlert: true})
	}
	if targetUser.Status == storage.GrapariStatusRejected {
		return c.Respond(&tele.CallbackResponse{Text: "Permohonan ini sudah ditolak sebelumnya.", ShowAlert: true})
	}

	if action == "auth" {
		_ = gdb.SetUserStatus(userJID, storage.GrapariStatusAuthorized, approverName)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Berhasil disetujui!"})

		// Edit original Telegram message
		updatedText := fmt.Sprintf("✅ <b>OTORISASI DISETUJUI</b>\n\n"+
			"• <b>Petugas:</b> %s\n"+
			"• <b>NIK / SIAD:</b> <code>%s</code>\n"+
			"• <b>Role:</b> <b>%s</b>\n"+
			"• <b>WhatsApp:</b> <code>+%s</code>\n"+
			"• <b>Disetujui Oleh:</b> %s pada %s WIB",
			html.EscapeString(targetUser.Name),
			html.EscapeString(targetUser.NIKSIAD),
			html.EscapeString(targetUser.Role),
			html.EscapeString(targetUser.PhoneNumber),
			html.EscapeString(approverName),
			time.Now().Format("02/01/2006 15:04"),
		)
		_ = c.Edit(updatedText, tele.ModeHTML)

		// Send WA confirmation to user
		m.notifyWAUser(channelID, userJID, fmt.Sprintf("🎉 *AKUN TELAH DISETUJUI*\n\n"+
			"Halo *%s*, permohonan akses Anda sebagai *%s* (NIK: `%s`) telah disetujui oleh %s.\n\n"+
			"Sekarang Anda dapat bertanya mengenai SOP, flowchart, panduan kerja, dan operasional GraPARI. Selamat bertugas!",
			targetUser.Name, targetUser.Role, targetUser.NIKSIAD, approverName))

	} else if action == "rej" {
		_ = gdb.SetUserStatus(userJID, storage.GrapariStatusRejected, approverName)
		_ = c.Respond(&tele.CallbackResponse{Text: "❌ Permohonan ditolak."})

		// Edit original Telegram message
		updatedText := fmt.Sprintf("❌ <b>OTORISASI DITOLAK</b>\n\n"+
			"• <b>Petugas:</b> %s\n"+
			"• <b>NIK / SIAD:</b> <code>%s</code>\n"+
			"• <b>Role:</b> <b>%s</b>\n"+
			"• <b>WhatsApp:</b> <code>+%s</code>\n"+
			"• <b>Ditolak Oleh:</b> %s pada %s WIB",
			html.EscapeString(targetUser.Name),
			html.EscapeString(targetUser.NIKSIAD),
			html.EscapeString(targetUser.Role),
			html.EscapeString(targetUser.PhoneNumber),
			html.EscapeString(approverName),
			time.Now().Format("02/01/2006 15:04"),
		)
		_ = c.Edit(updatedText, tele.ModeHTML)

		// Send WA rejection notice to user
		m.notifyWAUser(channelID, userJID, fmt.Sprintf("⛔ *PERMOHONAN AKSES DITOLAK*\n\n"+
			"Halo *%s*, mohon maaf permohonan akses Anda sebagai *%s* (NIK: `%s`) belum disetujui oleh %s.\n"+
			"Silakan konfirmasi ke Administrator jika ada kesalahan.",
			targetUser.Name, targetUser.Role, targetUser.NIKSIAD, approverName))
	}

	return nil
}

func (m *GrapariApprovalManager) notifyWAUser(channelID, targetJID, message string) {
	m.mu.RLock()
	notifier := m.waNotifier
	m.mu.RUnlock()

	if notifier != nil {
		go func() {
			if err := notifier(channelID, targetJID, message); err != nil {
				log.Printf("⚠️ [GraPARI-Auth] Gagal mengirim pesan konfirmasi ke WA %s: %v", targetJID, err)
			}
		}()
	}
}
