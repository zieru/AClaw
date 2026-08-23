package admin

import (
	"context"
	"fmt"

	tele "gopkg.in/telebot.v3"
)

func (a *AdminBot) handleMenu(c tele.Context) error {
	text := "👋 <b>SELAMAT DATANG DI GOASSISTANT CONTROL PLANE</b>\n\n" +
		"Sistem AI Assistant berbasis <b>Pure Golang</b> tanpa web UI. Anda dapat mengatur seluruh komponen sistem langsung melalui tombol interaktif di bawah ini:"
	return c.Send(text, MainMenuKeyboard(), tele.ModeHTML)
}

func (a *AdminBot) handleStatus(c tele.Context) error {
	return c.Send(a.RenderStatusSummary(c), a.StatusKeyboard(), tele.ModeHTML)
}

func (a *AdminBot) handleNew(c tele.Context) error {
	// 1. Cancel running tasks if any
	if cancelVal, loaded := a.activeTasks.LoadAndDelete(c.Chat().ID); loaded {
		if cancel, ok := cancelVal.(context.CancelFunc); ok {
			cancel()
		}
	}

	// 2. Clear any active wizard state
	userID := c.Sender().ID
	a.wizard.CancelWizard(userID)
	a.comboWizard.CancelWizard(userID)
	a.limitsUI.CancelWizard(userID)
	a.channelUI.CancelWizard(userID)
	a.cronUI.CancelWizard(userID)
	a.mdUI.CancelWizard(userID)
	a.tavilyUI.CancelSession(userID)
	a.modelUI.CancelSession(userID)

	// 3. Reset database session history for this chat
	chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
	_ = a.sessManager.ResetChatSessions(chatIDStr)

	text := "✨ <b>SESI PERCAKAPAN BARU DIMULAI</b>\n\n" +
		"Konteks percakapan dan riwayat pesan sebelumnya telah dibersihkan.\n" +
		"Anda sekarang berada di sesi percakapan baru yang segar.\n\n" +
		"💡 <i>Kirim pesan atau pertanyaan apa saja untuk mulai berinteraksi dengan AI Assistant.</i>"

	return c.Send(text, tele.ModeHTML)
}

func (a *AdminBot) handleStop(c tele.Context) error {
	stoppedTask := false
	if cancelVal, loaded := a.activeTasks.LoadAndDelete(c.Chat().ID); loaded {
		if cancel, ok := cancelVal.(context.CancelFunc); ok {
			cancel()
			stoppedTask = true
		}
	}

	userID := c.Sender().ID
	a.wizard.CancelWizard(userID)
	a.comboWizard.CancelWizard(userID)
	a.limitsUI.CancelWizard(userID)
	a.channelUI.CancelWizard(userID)
	a.cronUI.CancelWizard(userID)
	a.mdUI.CancelWizard(userID)
	a.tavilyUI.CancelSession(userID)
	a.modelUI.CancelSession(userID)

	var text string
	if stoppedTask {
		text = "🛑 <b>PROSES DIHENTIKAN</b>\n\n" +
			"Generasi respon AI dan eksekusi tool yang sedang berjalan berhasil dibatalkan."
	} else {
		text = "🛑 <b>PROSES DIHENTIKAN</b>\n\n" +
			"Seluruh wizard input interaktif dan antrean aktif telah dibatalkan. Sistem siap menerima perintah baru."
	}

	return c.Send(text, tele.ModeHTML)
}

func (a *AdminBot) handleHelp(c tele.Context) error {
	text := "📖 <b>PANDUAN LENGKAP COMMAND GOASSISTANT</b>\n\n" +
		"🎛️ <b>Navigasi, Konteks & Status:</b>\n" +
		"• <code>/menu</code> - Buka dashboard tombol interaktif utama\n" +
		"• <code>/status</code> - Cek status operasional, resource & runtime AI\n" +
		"• <code>/new</code> (atau <code>/reset</code>) - Mulai sesi baru & reset riwayat konteks\n" +
		"• <code>/stop</code> (atau <code>/cancel</code>) - Hentikan respon AI atau batalkan wizard\n" +
		"• <code>/model</code> - Ganti model AI, pilih provider/model atau aktifkan combo\n" +
		"• <code>/help</code> - Tampilkan panduan ini\n\n" +
		"🤖 <b>Provider AI (9Router Multi-Key & Router):</b>\n" +
		"• <code>/wizard</code> atau <code>/setup</code> - Wizard interaktif tambah provider & deteksi model otomatis\n" +
		"• <code>/editprovider [id]</code> - Wizard interaktif edit konfigurasi provider\n" +
		"• <code>/fetchmodels &lt;provider_id&gt;</code> - Deteksi & segarkan model otomatis dari <code>/models</code>\n" +
		"• <code>/providers</code> - Lihat daftar provider & model\n" +
		"• <code>/addprovider &lt;id&gt; &lt;name&gt; &lt;type&gt; [base_url] [model]</code>\n" +
		"• <code>/addkey &lt;provider_id&gt; &lt;api_key&gt;</code> - Tambah API key ke pool\n" +
		"• <code>/setkeys &lt;provider_id&gt; &lt;key1,key2,...&gt;</code> - Set multiple keys\n" +
		"• <code>/delkey &lt;provider_id&gt; &lt;index|key&gt;</code> - Hapus key\n" +
		"• <code>/keystrategy &lt;provider_id&gt; &lt;round-robin|failover|random&gt;</code>\n" +
		"• <code>/setmodels &lt;provider_id&gt; &lt;m1,m2,...&gt;</code> - Daftarkan model yang didukung\n" +
		"• <code>/addmodel &lt;provider_id&gt; &lt;model_name&gt;</code>\n" +
		"• <code>/setmodel &lt;provider_id&gt; &lt;default_model&gt;</code>\n" +
		"• <code>/delprovider &lt;provider_id&gt;</code>\n\n" +
		"🔀 <b>Model Combos & Fallback Chains:</b>\n" +
		"• <code>/combos</code> - Lihat seluruh combo chains\n" +
		"• <code>/combowizard</code> - Wizard interaktif buat combo baru\n" +
		"• <code>/editcombo [name]</code> - Wizard interaktif edit targets & strategi combo\n" +
		"• <code>/addcombo &lt;name&gt; &lt;prov1:model1,prov2:model2,...&gt;</code>\n" +
		"• <code>/delcombo &lt;name&gt;</code>\n\n" +
		"🌐 <b>Proxy Pool (9Router Engine):</b>\n" +
		"• <code>/proxies</code> - Lihat daftar proxy upstream, latensi & kesehatan\n" +
		"• <code>/addproxy &lt;url&gt; [label]</code> - Tambah proxy (HTTP/HTTPS/SOCKS5)\n" +
		"• <code>/delproxy &lt;id&gt;</code> - Hapus node proxy\n" +
		"• <code>/testproxies</code> - Uji koneksi semua proxy secara paralel\n" +
		"• <code>/toggleproxy</code> - Aktifkan / nonaktifkan proxy pool\n" +
		"• <code>/proxystrategy &lt;round-robin|least-errors|best-latency|random&gt;</code>\n\n" +
		"🌿 <b>Token Saver (RTK & Caveman Mode):</b>\n" +
		"• <code>/tokensaver</code> - Lihat total token yang berhasil dihemat\n" +
		"• <code>/settokensavertarget &lt;auto|aggressive|caveman|off&gt;</code> - Ganti mode penghemat token\n\n" +
		"🛡️ <b>Pembatasan & Footer (Governance):</b>\n" +
		"• <code>/limits</code> - Lihat ringkasan batas upload, token & footer\n" +
		"• <code>/setfooter &lt;global|channel|chat&gt; &lt;id&gt; &lt;off|tokens|full&gt;</code> - Atur tampilan footer\n" +
		"• <code>/setlimit &lt;global|channel|chat&gt; &lt;id&gt; &lt;param&gt; &lt;value&gt;</code>\n\n" +
		"📱 <b>Channel & Tools:</b>\n" +
		"• <code>/channels</code> - Kelola bot Telegram & WhatsApp\n" +
		"• <code>/addchannel &lt;id&gt; &lt;type&gt; &lt;name&gt; &lt;token/endpoint&gt;</code>\n" +
		"• <code>/tools</code> - Lihat seluruh tool AI yang tersedia\n" +
		"• <code>/toolperms &lt;channel_id&gt; &lt;tool_name&gt; &lt;allow|deny&gt;</code>\n\n" +
		"📝 <b>File Markdown Bot:</b>\n" +
		"• <code>/md</code> - Lihat daftar file (.md)\n" +
		"• <code>/viewmd &lt;file&gt;</code> - Baca isi file\n" +
		"• <code>/editmd &lt;file&gt; &lt;konten&gt;</code> - Ubah isi file\n" +
		"• <i>Kirim file .md langsung ke chat untuk auto-update.</i>\n\n" +
		"⏰ <b>Tugas Otomatis (Cron Scheduler):</b>\n" +
		"• <code>/cron</code> - Lihat daftar jadwal cron aktif\n" +
		"• <code>/addcron &lt;id&gt; &lt;tg|wa&gt; &lt;chat_id&gt; \"&lt;cron_expr&gt;\" &lt;prompt&gt;</code>\n" +
		"• <code>/runcron &lt;id&gt;</code> - Jalankan jadwal detik ini juga\n\n" +
		"🧠 <b>Memori & Sesi:</b>\n" +
		"• <code>/memory</code> - Lihat memori profil & SOP\n" +
		"• <code>/savefact &lt;scope&gt; &lt;id&gt; &lt;tag&gt; &lt;content&gt;</code> - Simpan fakta permanen\n" +
		"• <code>/resetsession &lt;chat_id&gt;</code> - Bersihkan riwayat percakapan\n\n" +
		"📊 <b>Audit & Observability:</b>\n" +
		"• <code>/stats</code> - Ringkasan konsumsi token, penghematan & biaya\n" +
		"• <code>/logs</code> - 10 aktivitas request terakhir\n" +
		"• <code>/exportlogs</code> - Unduh laporan audit .csv\n" +
		"• <code>/backup</code> - Unduh backup database & file markdown"
	return c.Send(text, tele.ModeHTML)
}
