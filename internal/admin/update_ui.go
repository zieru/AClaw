package admin

import (
	"context"
	"fmt"
	"html"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/updater"
	"goassistant/internal/version"

	tele "gopkg.in/telebot.v3"
)

type UpdateUI struct {
	cfg        *config.AppConfig
	bot        *tele.Bot
	updatingMu sync.Mutex
	isUpdating bool
}

func NewUpdateUI(cfg *config.AppConfig, bot *tele.Bot) *UpdateUI {
	return &UpdateUI{
		cfg: cfg,
		bot: bot,
	}
}

// RenderUpdateDashboard checks GitHub releases and generates the status dashboard
func (u *UpdateUI) RenderUpdateDashboard(ctx context.Context) (string, *tele.ReplyMarkup) {
	menu := &tele.ReplyMarkup{}

	repo := u.cfg.Updater.GitHubRepo
	if repo == "" {
		repo = version.DefaultRepo
	}

	rel, asset, hasUpdate, err := updater.CheckForUpdate(ctx, repo, version.Version)

	var sb strings.Builder
	sb.WriteString("🚀 <b>GOASSISTANT SYSTEM AUTO-UPDATE</b>\n\n")
	sb.WriteString(fmt.Sprintf("• Versi Saat Ini: <code>v%s</code> (%s)\n", version.Version, version.BuildDate))
	sb.WriteString(fmt.Sprintf("• Target Platform: <code>Linux x64 (amd64)</code> [Static Binary]\n"))
	sb.WriteString(fmt.Sprintf("• Target Repository: <a href=\"https://github.com/%s\">%s</a>\n\n", html.EscapeString(repo), html.EscapeString(repo)))

	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ <b>Gagal memeriksa update:</b>\n<code>%s</code>\n\n", html.EscapeString(err.Error())))
		btnCheck := menu.Data("🔄 Cek Ulang", "btn_check_update")
		btnBack := menu.Data("⬅️ Kembali", "menu_main")
		menu.Inline(menu.Row(btnCheck, btnBack))
		return sb.String(), menu
	}

	sb.WriteString(fmt.Sprintf("🏷️ <b>Release Terbaru di GitHub:</b> <code>%s</code>\n", html.EscapeString(rel.TagName)))
	sb.WriteString(fmt.Sprintf("📅 Tanggal Rilis: <code>%s</code>\n", rel.PublishedAt.Format("02 Jan 2006 15:04 MST")))

	if asset != nil {
		sizeMB := float64(asset.Size) / (1024 * 1024)
		sb.WriteString(fmt.Sprintf("📦 Binary Asset: <code>%s</code> (<code>%.2f MB</code>)\n", html.EscapeString(asset.Name), sizeMB))
	} else {
		sb.WriteString(fmt.Sprintf("⚠️ Binary Asset: <i>Tidak ada binary yang cocok untuk %s/%s</i>\n", runtime.GOOS, runtime.GOARCH))
	}

	if rel.Body != "" {
		notes := strings.TrimSpace(rel.Body)
		if len(notes) > 400 {
			notes = notes[:400] + "..."
		}
		sb.WriteString(fmt.Sprintf("\n📋 <b>Changelog:</b>\n<i>%s</i>\n", html.EscapeString(notes)))
	}
	sb.WriteString("\n")

	btnCheck := menu.Data("🔄 Cek Ulang", "btn_check_update")
	btnBack := menu.Data("⬅️ Kembali", "menu_main")

	if asset != nil {
		if hasUpdate {
			sb.WriteString("✨ <b>Versi baru tersedia untuk diunduh dan dipasang!</b>\nTekan tombol di bawah untuk langsung mengupdate daemon.")
			btnApply := menu.Data(fmt.Sprintf("🚀 Update ke %s", rel.TagName), "btn_do_update")
			menu.Inline(
				menu.Row(btnApply),
				menu.Row(btnCheck, btnBack),
			)
		} else {
			sb.WriteString("✅ <b>GoAssistant Anda sudah menggunakan versi paling baru!</b>")
			btnReinstall := menu.Data("⚡ Install Ulang Binary", "btn_do_update")
			menu.Inline(
				menu.Row(btnReinstall),
				menu.Row(btnCheck, btnBack),
			)
		}
	} else {
		menu.Inline(menu.Row(btnCheck, btnBack))
	}

	return sb.String(), menu
}

// HandleCheckUpdate handles /update or check button
func (u *UpdateUI) HandleCheckUpdate(c tele.Context) error {
	_ = c.Respond(&tele.CallbackResponse{Text: "🔍 Memeriksa update GitHub..."})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	text, keyboard := u.RenderUpdateDashboard(ctx)
	return c.EditOrSend(text, keyboard, tele.ModeHTML)
}

// HandleDoUpdate downloads, installs, and restarts the daemon
func (u *UpdateUI) HandleDoUpdate(c tele.Context) error {
	u.updatingMu.Lock()
	if u.isUpdating {
		u.updatingMu.Unlock()
		return c.Reply("⚠️ Proses update sedang berjalan. Mohon tunggu...", tele.ModeHTML)
	}
	u.isUpdating = true
	u.updatingMu.Unlock()

	_ = c.Respond(&tele.CallbackResponse{Text: "🚀 Memulai proses update..."})

	go func() {
		defer func() {
			u.updatingMu.Lock()
			u.isUpdating = false
			u.updatingMu.Unlock()
		}()

		repo := u.cfg.Updater.GitHubRepo
		if repo == "" {
			repo = version.DefaultRepo
		}

		statusMsg, err := u.bot.Send(c.Chat(), "⏳ <b>MEMULAI AUTO-UPDATE</b>\n\n🔍 Menghubungi GitHub Releases API...", tele.ModeHTML)
		if err != nil {
			log.Printf("❌ [Updater] Gagal mengirim pesan awal Telegram: %v", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		rel, asset, _, err := updater.CheckForUpdate(ctx, repo, version.Version)
		if err != nil {
			_, _ = u.bot.Edit(statusMsg, fmt.Sprintf("❌ <b>Gagal memeriksa release:</b>\n<code>%s</code>", html.EscapeString(err.Error())), tele.ModeHTML)
			return
		}

		if asset == nil {
			_, _ = u.bot.Edit(statusMsg, fmt.Sprintf("❌ <b>Tidak ditemukan binary untuk %s/%s pada release %s.</b>", runtime.GOOS, runtime.GOARCH, rel.TagName), tele.ModeHTML)
			return
		}

		sizeMB := float64(asset.Size) / (1024 * 1024)
		_, _ = u.bot.Edit(statusMsg, fmt.Sprintf(
			"⬇️ <b>MENGUNDUH BINARY BARU...</b>\n\n"+
				"• Versi Target: <code>%s</code>\n"+
				"• File: <code>%s</code> (<code>%.2f MB</code>)\n"+
				"• Download URL: <code>%s</code>\n\n"+
				"⏳ Mohon tunggu sampai unduhan dan pemasangan selesai...",
			html.EscapeString(rel.TagName),
			html.EscapeString(asset.Name),
			sizeMB,
			html.EscapeString(asset.BrowserDownloadURL),
		), tele.ModeHTML)

		// Download and install
		lastProgressUpdate := time.Now()
		downloadErr := updater.ApplyUpdate(ctx, asset.BrowserDownloadURL, func(downloaded, total int64) {
			if time.Since(lastProgressUpdate) > 3*time.Second && total > 0 {
				lastProgressUpdate = time.Now()
				pct := (float64(downloaded) / float64(total)) * 100
				dlMB := float64(downloaded) / (1024 * 1024)
				totMB := float64(total) / (1024 * 1024)
				_, _ = u.bot.Edit(statusMsg, fmt.Sprintf(
					"⬇️ <b>MENGUNDUH BINARY BARU... (%.1f%%)</b>\n\n"+
						"• Versi Target: <code>%s</code>\n"+
						"• Progress: <code>%.2f MB / %.2f MB</code>\n\n"+
						"⏳ Sedang mengunduh file...",
					pct,
					html.EscapeString(rel.TagName),
					dlMB,
					totMB,
				), tele.ModeHTML)
			}
		})

		if downloadErr != nil {
			log.Printf("❌ [Updater] Gagal mengunduh atau memasang update: %v", downloadErr)
			_, _ = u.bot.Edit(statusMsg, fmt.Sprintf(
				"❌ <b>GAGAL MEMASANG UPDATE:</b>\n\n<code>%s</code>\n\nDaemon tetap berjalan pada versi <code>v%s</code>.",
				html.EscapeString(downloadErr.Error()),
				version.Version,
			), tele.ModeHTML)
			return
		}

		_, _ = u.bot.Edit(statusMsg, fmt.Sprintf(
			"✅ <b>UPDATE BERHASIL DIPASANG!</b>\n\n"+
				"• Versi Lama: <code>v%s</code>\n"+
				"• Versi Baru: <code>%s</code>\n\n"+
				"🔄 <b>Memulai restart otomatis daemon sekarang...</b>\n"+
				"GoAssistant akan kembali online dalam hitungan detik. Silakan ketik <code>/status</code> sebentar lagi untuk memverifikasi.",
			version.Version,
			html.EscapeString(rel.TagName),
		), tele.ModeHTML)

		log.Printf("🚀 [Updater] Update ke %s berhasil dipasang! Memulai restart self...", rel.TagName)
		time.Sleep(1 * time.Second)

		if err := updater.RestartSelf(); err != nil {
			log.Printf("⚠️ [Updater] Gagal restart otomatis: %v. Admin harus me-restart manual.", err)
			_, _ = u.bot.Send(c.Chat(), fmt.Sprintf("⚠️ <b>Peringatan:</b> Binary baru sudah terpasang, namun proses restart otomatis gagal (<code>%s</code>). Silakan restart service/daemon secara manual.", html.EscapeString(err.Error())), tele.ModeHTML)
		}
	}()

	return nil
}
