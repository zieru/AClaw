package admin

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"goassistant/internal/config"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"

	tele "gopkg.in/telebot.v3"
)

type ProxyUIHandler struct {
	db        *storage.DB
	proxyPool *proxy.Pool
}

func NewProxyUIHandler(db *storage.DB, pool *proxy.Pool) *ProxyUIHandler {
	return &ProxyUIHandler{
		db:        db,
		proxyPool: pool,
	}
}

// HandleListProxies displays all proxies and their health metrics
func (h *ProxyUIHandler) HandleListProxies(c tele.Context) error {
	nodes := h.proxyPool.ListNodes()
	var sb strings.Builder

	statusEmoji := "🟢"
	if !h.proxyPool.IsEnabled() {
		statusEmoji = "🔴"
	}

	groups := h.proxyPool.ListGroups()

	sb.WriteString(fmt.Sprintf("🌐 <b>Daftar Proxy Pool (9Router Engine)</b> %s\n", statusEmoji))
	sb.WriteString(fmt.Sprintf("Status Global: <b>%s</b> | Strategi: <code>%s</code> | Groups: <b>%d</b>\n\n",
		map[bool]string{true: "Aktif", false: "Nonaktif"}[h.proxyPool.IsEnabled()],
		h.proxyPool.GetStrategy(),
		len(groups),
	))

	if len(nodes) == 0 {
		sb.WriteString("<i>Belum ada proxy upstream terdaftar. Koneksi menggunakan direct internet.</i>\n\n")
		sb.WriteString("💡 <b>Perintah Manajemen Proxy:</b>\n")
		sb.WriteString("• <code>/syncwebshare [api_token]</code> - Tarik proxy otomatis dari Webshare.io API\n")
		sb.WriteString("• <code>/webshare</code> - Lihat profil & kuota akun Webshare.io\n")
		sb.WriteString("• <code>/addproxies &lt;group&gt; &lt;paste 50+ proxies...&gt;</code> - Impor batch proxy massal\n")
		sb.WriteString("• <code>/addproxy &lt;url&gt; [group] [label]</code> - Tambah 1 proxy\n")
		sb.WriteString("• <code>/proxygroups</code> - Lihat & kelola group proxy\n")
		sb.WriteString("• <code>/setproviderproxy &lt;prov_id&gt; &lt;group|off&gt;</code> - Pasang proxy ke provider tertentu\n")
		return c.Reply(sb.String(), h.ProxyMenuKeyboard(), tele.ModeHTML)
	}

	// Group nodes by group
	byGroup := make(map[string][]*storage.ProxyNodeRecord)
	for _, n := range nodes {
		grp := n.GroupName
		if grp == "" {
			grp = "default"
		}
		byGroup[grp] = append(byGroup[grp], n)
	}

	for grp, gNodes := range byGroup {
		activeCount := 0
		for _, gn := range gNodes {
			if gn.IsActive {
				activeCount++
			}
		}

		sb.WriteString(fmt.Sprintf("📂 <b>Group: %s</b> (%d/%d Aktif)\n", html.EscapeString(grp), activeCount, len(gNodes)))
		for i, n := range gNodes {
			if i >= 10 && len(gNodes) > 12 {
				sb.WriteString(fmt.Sprintf("   <i>... dan %d proxy lainnya di group ini</i>\n", len(gNodes)-10))
				break
			}

			nodeStatus := "🟢"
			if !n.IsActive {
				nodeStatus = "⚪ (Off)"
			} else if n.FailCount > 3 && n.FailCount > n.SuccessCount {
				nodeStatus = "🔴 (Dead)"
			} else if n.AvgLatencyMs > 1000 {
				nodeStatus = "🟡 (Slow)"
			}

			latStr := "-"
			if n.AvgLatencyMs > 0 {
				latStr = fmt.Sprintf("%dms", n.AvgLatencyMs)
			}

			sb.WriteString(fmt.Sprintf("   • <b>%s</b> [%s] %s (<code>%s</code>): ⚡ <b>%s</b> | 🔗 <code>%s</code>\n",
				n.Label, strings.ToUpper(n.Protocol), nodeStatus, n.ID, latStr, maskProxyURL(n.URL)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("💡 <b>Perintah:</b>\n")
	sb.WriteString("• <code>/addproxies &lt;group&gt; &lt;url1\nurl2\n...&gt;</code> - Impor 50+ proxy sekaligus\n")
	sb.WriteString("• <code>/proxygroups</code> - Ringkasan per-grup proxy\n")
	sb.WriteString("• <code>/togglegroup &lt;group&gt;</code> - Nyalakan/matikan 1 group\n")
	sb.WriteString("• <code>/testgroup &lt;group&gt;</code> - Uji latensi group tertentu\n")
	sb.WriteString("• <code>/testproxies</code> - Uji seluruh proxy paralel\n")
	sb.WriteString("• <code>/prunedead [group]</code> - Hapus proxy yang mati/error\n")
	sb.WriteString("• <code>/setproviderproxy &lt;prov_id&gt; &lt;group|off&gt;</code>\n")

	return c.Reply(sb.String(), h.ProxyMenuKeyboard(), tele.ModeHTML)
}

// HandleListGroups lists distinct proxy groups and summaries
func (h *ProxyUIHandler) HandleListGroups(c tele.Context) error {
	groups := h.proxyPool.ListGroups()
	var sb strings.Builder

	sb.WriteString("📂 <b>DAFTAR GROUP PROXY POOL</b>\n\n")

	for i, grp := range groups {
		nodes := h.proxyPool.ListNodesByGroup(grp)
		activeCount := 0
		totalLat := 0
		countWithLat := 0
		for _, n := range nodes {
			if n.IsActive {
				activeCount++
			}
			if n.AvgLatencyMs > 0 {
				totalLat += n.AvgLatencyMs
				countWithLat++
			}
		}

		avgLatStr := "-"
		if countWithLat > 0 {
			avgLatStr = fmt.Sprintf("%dms", totalLat/countWithLat)
		}

		sb.WriteString(fmt.Sprintf("<b>%d. 📁 Group: %s</b>\n", i+1, html.EscapeString(grp)))
		sb.WriteString(fmt.Sprintf("   • Node: <b>%d total</b> (🟢 %d Aktif)\n", len(nodes), activeCount))
		sb.WriteString(fmt.Sprintf("   • Rata-rata Latensi: ⚡ <b>%s</b>\n", avgLatStr))
		sb.WriteString(fmt.Sprintf("   • Aksi: <code>/togglegroup %s</code> | <code>/testgroup %s</code> | <code>/delgroup %s</code>\n\n", grp, grp, grp))
	}

	sb.WriteString("💡 <i>Untuk mengaitkan group ke provider AI:</i>\n")
	sb.WriteString("<code>/setproviderproxy &lt;provider_id&gt; &lt;group_name&gt;</code>\n\n")
	sb.WriteString("💡 <i>Untuk menambah 50+ proxy baru ke group:</i>\n")
	sb.WriteString("<code>/addproxies &lt;group_name&gt; &lt;paste proxies...&gt;</code>")

	return c.Reply(sb.String(), h.ProxyMenuKeyboard(), tele.ModeHTML)
}

// HandleAddProxies handles batch proxy import (e.g. 50+ proxies pasted in 1 message with commas or newlines)
func (h *ProxyUIHandler) HandleAddProxies(c tele.Context) error {
	fullText := c.Text()

	cmdPrefix := ""
	if strings.HasPrefix(fullText, "/") {
		parts := strings.Fields(fullText)
		if len(parts) > 0 {
			cmdPrefix = parts[0]
		}
	}
	bodyText := strings.TrimSpace(strings.TrimPrefix(fullText, cmdPrefix))
	if bodyText == "" {
		text := "📥 <b>IMPORT PROXY BATCH (MASSAL)</b>\n\n" +
			"Kirimkan format:\n" +
			"<code>/addproxies &lt;group_name&gt; &lt;url1,url2,url3...&gt;</code>\n\n" +
			"Atau format baris baru:\n" +
			"<code>/addproxies webshare.io</code>\n" +
			"<code>http://user:pass@ip1:port</code>\n" +
			"<code>http://user:pass@ip2:port</code>\n" +
			"<code>socks5://ip3:port</code>\n\n" +
			"💡 <i>Mendukung pemisah koma (,), titik koma (;), spasi, maupun baris baru!</i>"
		return c.Reply(text, tele.ModeHTML)
	}

	group := "default"

	// Split by newline, comma, semicolon, or whitespace
	tokens := strings.FieldsFunc(bodyText, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})

	var proxyList []string
	for i, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" || strings.HasPrefix(tok, "#") {
			continue
		}

		// If first token is not a proxy URL (does not contain :// or :port or @), treat as group name!
		if i == 0 && !strings.Contains(tok, "://") && !strings.Contains(tok, "@") && !strings.Contains(tok, ":") {
			group = tok
			continue
		}

		proxyList = append(proxyList, tok)
	}

	if len(proxyList) == 0 {
		return c.Reply("⚠️ Tidak ditemukan daftar URL proxy di pesan Anda.", tele.ModeHTML)
	}

	count, err := h.proxyPool.AddBatch(proxyList, group)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mengimpor proxy: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("✅ Berhasil mengimpor <b>%d proxy node</b> ke dalam Group <code>%s</code>!\n\nKetik <code>/testgroup %s</code> untuk memeriksa kesehatan seluruh proxy yang baru ditambahkan.", count, html.EscapeString(group), html.EscapeString(group)), tele.ModeHTML)
}

// HandleAddProxy adds a single proxy node
func (h *ProxyUIHandler) HandleAddProxy(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format salah!\nContoh:\n• <code>/addproxy http://user:pass@127.0.0.1:8080 default \"Proxy SG 1\"</code>\n• <code>/addproxy socks5://127.0.0.1:1080 dahl_proxies \"Socks US\"</code>", tele.ModeHTML)
	}

	rawURL := args[0]
	group := "default"
	label := ""

	if len(args) > 1 {
		group = args[1]
	}
	if len(args) > 2 {
		label = strings.Join(args[2:], " ")
		label = strings.Trim(label, "\"")
	}

	node, err := h.proxyPool.AddNodeWithGroup(rawURL, label, group)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menambahkan proxy: %v", err), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Proxy <b>%s</b> (<code>%s</code>) berhasil didaftarkan di Group <code>%s</code>!\n\nKetik <code>/testproxies</code> untuk memeriksa koneksi.", node.Label, node.ID, node.GroupName), tele.ModeHTML)
}

// HandleDeleteProxy removes a proxy node
func (h *ProxyUIHandler) HandleDeleteProxy(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/delproxy &lt;id&gt;</code>", tele.ModeHTML)
	}

	id := args[0]
	if err := h.proxyPool.DeleteNode(id); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menghapus proxy: %v", err), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Proxy dengan ID <code>%s</code> berhasil dihapus.", id), tele.ModeHTML)
}

// HandleToggleGroup toggles a proxy group active/inactive
func (h *ProxyUIHandler) HandleToggleGroup(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/togglegroup &lt;group_name&gt;</code>", tele.ModeHTML)
	}

	group := args[0]
	nodes := h.proxyPool.ListNodesByGroup(group)
	if len(nodes) == 0 {
		return c.Reply(fmt.Sprintf("❌ Group proxy '%s' tidak ditemukan atau kosong.", html.EscapeString(group)))
	}

	// Toggle based on first node state
	newState := !nodes[0].IsActive
	if err := h.proxyPool.ToggleGroup(group, newState); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mengubah status group: %v", err))
	}

	stateStr := "DIAKTIFKAN 🟢"
	if !newState {
		stateStr = "DINONAKTIFKAN 🔴"
	}

	return c.Reply(fmt.Sprintf("⚙️ Seluruh proxy di Group <b>%s</b> berhasil <b>%s</b>.", html.EscapeString(group), stateStr), tele.ModeHTML)
}

// HandleDeleteGroup deletes all proxies in a group
func (h *ProxyUIHandler) HandleDeleteGroup(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/delgroup &lt;group_name&gt;</code>", tele.ModeHTML)
	}

	group := args[0]
	if err := h.proxyPool.DeleteGroup(group); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menghapus group: %v", err))
	}

	return c.Reply(fmt.Sprintf("🗑️ Seluruh proxy di Group <b>%s</b> berhasil dihapus!", html.EscapeString(group)), tele.ModeHTML)
}

// HandleTestGroup tests proxies in a specific group
func (h *ProxyUIHandler) HandleTestGroup(c tele.Context) error {
	group := ""
	if len(c.Args()) > 0 {
		group = c.Args()[0]
	}

	nodes := h.proxyPool.ListNodesByGroup(group)
	if len(nodes) == 0 {
		return c.Reply(fmt.Sprintf("⚠️ Tidak ada proxy di group '%s' untuk dites.", html.EscapeString(group)), tele.ModeHTML)
	}

	_ = c.Reply(fmt.Sprintf("🔄 <i>Sedang menguji konektivitas %d proxy di group '%s' secara paralel...</i>", len(nodes), html.EscapeString(group)), tele.ModeHTML)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results := h.proxyPool.CheckGroupHealth(ctx, group)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>Hasil Uji Kesehatan Proxy (Group: %s):</b>\n\n", html.EscapeString(group)))

	successCount := 0
	for _, n := range nodes {
		lat, ok := results[n.ID]
		if !ok || lat < 0 {
			sb.WriteString(fmt.Sprintf("❌ <b>%s</b> (<code>%s</code>): Timeout / Error\n", n.Label, n.ID))
		} else {
			successCount++
			badge := "🟢"
			if lat > 1000 {
				badge = "🟡"
			}
			sb.WriteString(fmt.Sprintf("%s <b>%s</b> (<code>%s</code>): Latensi <b>%dms</b>\n", badge, n.Label, n.ID, lat))
		}
	}

	sb.WriteString(fmt.Sprintf("\n📈 <b>Ringkasan:</b> %d/%d Berhasil | %d Gagal\n", successCount, len(nodes), len(nodes)-successCount))
	if len(nodes)-successCount > 0 {
		sb.WriteString("💡 <i>Ketik <code>/prunedead " + html.EscapeString(group) + "</code> untuk menghapus node yang mati secara otomatis.</i>")
	}

	return c.Reply(sb.String(), tele.ModeHTML)
}

// HandleTestProxies tests all proxies in parallel
func (h *ProxyUIHandler) HandleTestProxies(c tele.Context) error {
	return h.HandleTestGroup(c)
}

// HandlePruneDead removes or disables dead proxies
func (h *ProxyUIHandler) HandlePruneDead(c tele.Context) error {
	group := ""
	if len(c.Args()) > 0 {
		group = c.Args()[0]
	}

	nodes := h.proxyPool.ListNodesByGroup(group)
	pruned := 0
	for _, n := range nodes {
		if n.FailCount >= 3 && n.FailCount > n.SuccessCount {
			_ = h.proxyPool.DeleteNode(n.ID)
			pruned++
		}
	}

	return c.Reply(fmt.Sprintf("🧹 Berhasil membersihkan <b>%d proxy node</b> yang tidak merespon/mati!", pruned), tele.ModeHTML)
}

// HandleToggleProxy enables or disables global proxy pool
func (h *ProxyUIHandler) HandleToggleProxy(c tele.Context) error {
	newState := !h.proxyPool.IsEnabled()
	h.proxyPool.SetEnabled(newState)

	if h.db != nil {
		if pol, err := h.db.GetPolicy("global", "system"); err == nil && pol != nil {
			pol.ProxyPoolEnabled = newState
			_ = h.db.SavePolicy(pol)
		}
	}

	stateStr := "diaktifkan 🟢"
	if !newState {
		stateStr = "dinonaktifkan 🔴 (Direct Connection)"
	}

	return c.Reply(fmt.Sprintf("⚙️ Proxy Pool Global berhasil <b>%s</b>.", stateStr), tele.ModeHTML)
}

// HandleSetStrategy configures proxy selection algorithm
func (h *ProxyUIHandler) HandleSetStrategy(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Format salah!\nPilihan: <code>/proxystrategy round-robin</code>, <code>least-errors</code>, <code>best-latency</code>, <code>random</code>", tele.ModeHTML)
	}

	strategy := strings.ToLower(args[0])
	switch strategy {
	case "round-robin", "least-errors", "best-latency", "random":
		h.proxyPool.SetStrategy(strategy)
		return c.Reply(fmt.Sprintf("✅ Strategi pemilihan proxy pool diubah ke: <code>%s</code>", strategy), tele.ModeHTML)
	default:
		return c.Reply("❌ Strategi tidak valid. Gunakan salah satu dari: <code>round-robin</code>, <code>least-errors</code>, <code>best-latency</code>, <code>random</code>", tele.ModeHTML)
	}
}

// HandleSyncWebshare synchronizes proxies directly from Webshare.io API
func (h *ProxyUIHandler) HandleSyncWebshare(c tele.Context) error {
	args := c.Args()
	cfg := config.Get()

	apiKey := ""
	group := "webshare"
	mode := "direct"
	protocol := "http"
	var countries []string

	if cfg != nil {
		if cfg.Webshare.APIKey != "" {
			apiKey = cfg.Webshare.APIKey
		}
		if cfg.Webshare.GroupName != "" {
			group = cfg.Webshare.GroupName
		}
		if cfg.Webshare.Mode != "" {
			mode = cfg.Webshare.Mode
		}
		if cfg.Webshare.Protocol != "" {
			protocol = cfg.Webshare.Protocol
		}
		countries = cfg.Webshare.Countries
	}

	// Override from arguments if provided: /syncwebshare [api_key] [group_name] [mode]
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		apiKey = strings.TrimSpace(args[0])
	}
	if len(args) > 1 && strings.TrimSpace(args[1]) != "" {
		group = strings.TrimSpace(args[1])
	}
	if len(args) > 2 && strings.TrimSpace(args[2]) != "" {
		mode = strings.ToLower(strings.TrimSpace(args[2]))
	}

	if apiKey == "" {
		text := "⚠️ <b>API Token Webshare.io belum ditemukan!</b>\n\n" +
			"Gunakan salah satu cara berikut:\n" +
			"1. Jalankan dengan token langsung:\n" +
			"   <code>/syncwebshare &lt;your_webshare_api_token&gt; [group_name] [mode]</code>\n\n" +
			"2. Masukkan token di <code>configs/default_config.yaml</code> pada section <code>webshare.api_key</code>\n" +
			"3. Atau set environment variable <code>WEBSHARE_API_KEY</code>\n\n" +
			"💡 <i>Dapatkan API Token Webshare Anda di https://dashboard.webshare.io/user/token</i>"
		return c.Reply(text, tele.ModeHTML)
	}

	_ = c.Reply("🔄 <i>Menghubungi Webshare.io API (https://apidocs.webshare.io/)...</i>", tele.ModeHTML)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsClient := proxy.NewWebshareClient(apiKey)
	count, err := wsClient.SyncToPool(ctx, h.proxyPool, group, protocol, mode, countries, true)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal sinkronisasi proxy dari Webshare: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	sb := strings.Builder{}
	sb.WriteString("✅ <b>SINKRONISASI WEBSHARE BERHASIL!</b> 🌐\n\n")
	sb.WriteString(fmt.Sprintf("• Total Proxy Diimpor: <b>%d node</b>\n", count))
	sb.WriteString(fmt.Sprintf("• Group Target: <code>%s</code>\n", html.EscapeString(group)))
	sb.WriteString(fmt.Sprintf("• Mode: <code>%s</code> | Protokol: <code>%s</code>\n\n", mode, strings.ToUpper(protocol)))
	sb.WriteString(fmt.Sprintf("💡 <i>Ketik <code>/testgroup %s</code> untuk memeriksa latensi koneksi proxy Webshare.</i>\n", html.EscapeString(group)))
	sb.WriteString(fmt.Sprintf("💡 <i>Ketik <code>/setproviderproxy &lt;provider_id&gt; %s</code> untuk menghubungkan ke model AI.</i>", html.EscapeString(group)))

	return c.Reply(sb.String(), h.ProxyMenuKeyboard(), tele.ModeHTML)
}

// HandleWebshareInfo displays account and subscription details from Webshare.io
func (h *ProxyUIHandler) HandleWebshareInfo(c tele.Context) error {
	cfg := config.Get()
	apiKey := ""
	if cfg != nil && cfg.Webshare.APIKey != "" {
		apiKey = cfg.Webshare.APIKey
	}
	if len(c.Args()) > 0 {
		apiKey = strings.TrimSpace(c.Args()[0])
	}

	if apiKey == "" {
		return c.Reply("⚠️ API Token Webshare belum diset. Gunakan <code>/webshare &lt;api_token&gt;</code> atau set di config.", tele.ModeHTML)
	}

	_ = c.Reply("🔄 <i>Mengambil data akun dari Webshare.io API...</i>", tele.ModeHTML)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsClient := proxy.NewWebshareClient(apiKey)
	prof, err := wsClient.GetProfile(ctx)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mengambil profil Webshare: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	group := "webshare"
	if cfg != nil && cfg.Webshare.GroupName != "" {
		group = cfg.Webshare.GroupName
	}
	activeNodes := h.proxyPool.ListNodesByGroup(group)

	sb := strings.Builder{}
	sb.WriteString("🌐 <b>INFORMASI AKUN WEBSHARE.IO</b>\n\n")
	sb.WriteString(fmt.Sprintf("• Email Akun: <code>%s</code>\n", html.EscapeString(prof.Email)))
	sb.WriteString(fmt.Sprintf("• Status Berlangganan: <b>%s</b>\n", map[bool]string{true: "🟢 Aktif", false: "⚪ Free / Inactive"}[prof.Subscribed]))
	sb.WriteString(fmt.Sprintf("• Total Kuota Proxy: <b>%d proxy</b>\n", prof.ProxyCount))
	sb.WriteString(fmt.Sprintf("• Node di Database GoAssistant: <b>%d terdaftar</b> (Group: <code>%s</code>)\n\n", len(activeNodes), html.EscapeString(group)))
	sb.WriteString("💡 <b>Aksi Cepat:</b>\n")
	sb.WriteString(fmt.Sprintf("• Sinkronisasi Ulang: <code>/syncwebshare</code>\n"))
	sb.WriteString(fmt.Sprintf("• Uji Koneksi: <code>/testgroup %s</code>\n", html.EscapeString(group)))

	return c.Reply(sb.String(), h.ProxyMenuKeyboard(), tele.ModeHTML)
}

func (h *ProxyUIHandler) ProxyMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnTest := menu.Data("⚡ Test Semua Proxy", "btn_test_proxies")
	btnGroups := menu.Data("📁 Kelola Group Proxy", "btn_proxy_groups")
	btnSyncWS := menu.Data("🌐 Sync Webshare.io", "btn_sync_webshare")
	btnWSInfo := menu.Data("ℹ️ Akun Webshare", "btn_webshare_info")
	btnToggle := menu.Data("🔘 Toggle Proxy Pool", "btn_toggle_proxy")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")

	menu.Inline(
		menu.Row(btnTest, btnGroups),
		menu.Row(btnSyncWS, btnWSInfo),
		menu.Row(btnToggle, btnBack),
	)
	return menu
}

func maskProxyURL(raw string) string {
	if len(raw) < 12 {
		return raw
	}
	parts := strings.Split(raw, "@")
	if len(parts) > 1 {
		// Has user:pass, mask credentials
		return parts[0][:4] + ":***@" + parts[1]
	}
	return raw
}
