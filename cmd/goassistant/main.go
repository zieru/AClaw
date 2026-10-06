package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"goassistant/internal/admin"
	"goassistant/internal/agent"
	bf "goassistant/internal/bifrost"
	"goassistant/internal/channel"
	tgchannel "goassistant/internal/channel/telegram"
	wachannel "goassistant/internal/channel/whatsapp"
	"goassistant/internal/checkin"
	"goassistant/internal/config"
	"goassistant/internal/cron"
	"goassistant/internal/goassisthttp"
	"goassistant/internal/instance"
	"goassistant/internal/memory"
	"goassistant/internal/omniroute"
	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/search"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
	"goassistant/internal/updater"
	"goassistant/internal/version"
	"goassistant/internal/webadmin"

	tele "gopkg.in/telebot.v3"
)

// handleCLIUpdate implements `goassistant --update` / `--check`. It queries
// GitHub Releases, optionally applies the new binary in-place and restarts via
// the daemon manager (supervise-daemon on OpenRC respawns automatically).
func handleCLIUpdate(apply bool, cfg *config.AppConfig) {
	repo := cfg.Updater.GitHubRepo
	if repo == "" {
		repo = version.DefaultRepo
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Printf("🔍 Memeriksa update dari %s ...\n", repo)
	rel, asset, hasUpdate, err := updater.CheckForUpdate(ctx, repo, version.Version)
	if err != nil {
		fmt.Printf("❌ Gagal memeriksa update: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("• Versi saat ini : v%s (%s)\n", version.Version, version.BuildDate)
	fmt.Printf("• Release terbaru: %s (tanggal %s)\n", rel.TagName, rel.PublishedAt.Format("02 Jan 2006 15:04"))
	if rel.Body != "" {
		fmt.Printf("• Changelog:\n%s\n", rel.Body)
	}

	if asset == nil {
		fmt.Printf("⚠️ Tidak ada binary yang cocok untuk %s/%s pada release %s.\n", runtime.GOOS, runtime.GOARCH, rel.TagName)
		os.Exit(1)
	}
	fmt.Printf("• Binary asset   : %s (%.2f MB)\n", asset.Name, float64(asset.Size)/(1024*1024))

	if !apply {
		if hasUpdate {
			fmt.Printf("✨ Update tersedia. Jalankan 'goassistant --update' untuk memasang.\n")
		} else {
			fmt.Printf("✅ GoAssistant sudah menggunakan versi terbaru.\n")
		}
		return
	}

	if !hasUpdate {
		fmt.Printf("✅ Versi terbaru sudah terpasang. Tidak perlu update.\n")
		return
	}

	fmt.Printf("⬇️ Mengunduh %s ...\n", asset.Name)
	if err := updater.ApplyUpdate(ctx, asset.BrowserDownloadURL, func(downloaded, total int64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("\r⏳ %.1f%% (%d/%d MB)", pct, downloaded/(1024*1024), total/(1024*1024))
		}
	}); err != nil {
		fmt.Printf("\n❌ Gagal memasang update: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Update berhasil dipasang! Memulai restart otomatis...\n")
	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if arg == "--update" || arg == "--check" {
			continue
		}
		args = append(args, arg)
	}
	if err := updater.RestartSelfWithArgs(args); err != nil {
		fmt.Printf("⚠️ Gagal restart otomatis: %v. Silakan restart service/daemon manual.\n", err)
	}
}

func main() {
	configPath := flag.String("config", "configs/default_config.yaml", "Path ke file konfigurasi YAML")
	showVersion := flag.Bool("version", false, "Tampilkan versi aplikasi")
	doUpdate := flag.Bool("update", false, "Cek & pasang update binary dari GitHub Releases lalu restart")
	checkUpdate := flag.Bool("check", false, "Cek versi terbaru dari GitHub Releases tanpa memasang")
	flag.Parse()

	if *showVersion {
		fmt.Printf("GoAssistant Daemon v%s (Built: %s) [Zero-CGO Static Binary]\n", version.Version, version.BuildDate)
		return
	}

	log.Println("==========================================================")
	log.Printf("🚀 Memulai GoAssistant Core Daemon v%s...", version.Version)
	log.Println("⚡ Kompatibilitas: Pure Go (Zero-CGO) - Ready for manylinux_2_28")
	log.Println("==========================================================")

	// 1. Load Configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("❌ Gagal memuat konfigurasi: %v", err)
	}
	log.Printf("⚙️ Konfigurasi berhasil dimuat dari: %s", *configPath)

	// 1b. Self-update via CLI: goassistant --update / --check
	if *doUpdate || *checkUpdate {
		handleCLIUpdate(*doUpdate, cfg)
		return
	}

	// Environment variable overrides for OmniRoute
	if envPass := os.Getenv("OMNIROUTE_PASSWORD"); envPass != "" {
		cfg.OmniRoute.Password = envPass
	}
	if envBase := os.Getenv("OMNIROUTE_BASE_URL"); envBase != "" {
		cfg.OmniRoute.BaseURL = envBase
	}
	if envKey := os.Getenv("OMNIROUTE_API_KEY"); envKey != "" {
		cfg.OmniRoute.APIKey = envKey
	}

	// Initialize OmniRoute Gateway Collaboration Client
	var omniClient *omniroute.Client
	if cfg.OmniRoute.Enabled {
		omniClient = omniroute.InitClient(cfg.OmniRoute)
		log.Printf("🚀 OmniRoute Gateway Collaboration aktif di: %s", omniClient.BaseURL())
	}

	// Single-Instance Takeover Lock (Hentikan instance lama jika ada)
	releaseLock, err := instance.EnsureSingleInstance(cfg.Server.DataDir)
	if err != nil {
		log.Printf("⚠️ Peringatan instance lock: %v", err)
	} else {
		defer releaseLock()
	}

	// Check environment variable override for admin bot token
	if envToken := os.Getenv("GOASSISTANT_TELEGRAM_TOKEN"); envToken != "" {
		cfg.AdminTelegram.BotToken = envToken
	}

	// 2. Open Pure Go SQLite Database
	db, err := storage.Open(cfg.Server.DBPath)
	if err != nil {
		log.Fatalf("❌ Gagal membuka database SQLite: %v", err)
	}
	defer db.Close()
	log.Printf("📦 Database SQLite berhasil dimuat: %s", cfg.Server.DBPath)

	// 3. Initialize Core Managers
	search.InitGlobalEngine(cfg.Search)
	log.Printf("🔍 Web Search Engine aktif (Provider: %s, Fallback: %t)", cfg.Search.Provider, cfg.Search.FallbackEnabled)
	toolReg := tools.GetRegistry()
	provMgr := provider.GetManager()
	embedder := memory.NewEmbedder(cfg.Memory.GetEmbeddingConfig(), db)
	memMgr := memory.NewManager(db, cfg.Memory, embedder, provMgr)
	memMgr.StartAutoCompactor(context.Background())
	log.Printf("🧠 Memory Engine Standalone aktif (Strategi: %s, MaxTokens: %d, Retensi: %d hari, Auto-Compaction: %t)",
		memMgr.GetStrategy(), memMgr.GetMaxTokens(), memMgr.GetRetentionDays(), cfg.Memory.AutoCompaction)
	toolReg.Register(tools.NewUserMemoryTool(memMgr))
	sessMgr := memory.NewSessionManager(db)
	mdLoader := agent.NewMDLoader(cfg.Server.MDDir)
	promptBld := agent.NewPromptBuilder(mdLoader)

	// Seed initial memories from CSV if available (idempotent INSERT OR IGNORE)
	if cfg.Memory.SeedCSVPath != "" {
		if _, statErr := os.Stat(cfg.Memory.SeedCSVPath); statErr == nil {
			seedCtx, seedCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, _ = memory.SeedFromCSV(seedCtx, db, cfg.Memory.SeedCSVPath, "399999658", embedder)
			seedCancel()
		}
	}

	// Initialize External MCP Servers (e.g. a7g3 DuckDB analytics engine)
	mcpManager := tools.NewMCPManager(cfg.MCPServers)
	if err := mcpManager.StartAndRegister(context.Background(), toolReg); err != nil {
		log.Printf("⚠️ Gagal inisialisasi MCP servers: %v", err)
	}
	defer mcpManager.Close()

	// Initialize Proxy Pool (Built-in 9Router Engine)
	initialPoolEnabled := cfg.ProxyPool.Enabled
	if globPol, err := db.GetPolicy("global", "system"); err == nil && globPol != nil {
		initialPoolEnabled = globPol.ProxyPoolEnabled
	}
	proxyPool := proxy.NewPool(db, initialPoolEnabled, cfg.ProxyPool.Strategy)
	for _, rawProxy := range cfg.ProxyPool.InitialProxies {
		_, _ = proxyPool.AddNode(rawProxy, "")
	}

	// Initialize Webshare.io Proxy Pool Provider if configured
	if cfg.Webshare.APIKey != "" {
		wsClient := proxy.NewWebshareClient(cfg.Webshare.APIKey)
		group := cfg.Webshare.GroupName
		if group == "" {
			group = "webshare"
		}
		if cfg.Webshare.AutoSync {
			interval := time.Duration(cfg.Webshare.SyncIntervalMinutes) * time.Minute
			wsClient.StartAutoSync(context.Background(), proxyPool, interval, group, cfg.Webshare.Protocol, cfg.Webshare.Mode, cfg.Webshare.Countries)
			log.Printf("🌐 [Webshare.io] Background auto-sync proxy pool diaktifkan (Setiap %v, Group: '%s')", interval, group)
		} else {
			// One-time initial sync in background on startup
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				if count, err := wsClient.SyncToPool(ctx, proxyPool, group, cfg.Webshare.Protocol, cfg.Webshare.Mode, cfg.Webshare.Countries, false); err != nil {
					log.Printf("⚠️ [Webshare.io] Gagal sinkronisasi awal proxy pool: %v", err)
				} else {
					log.Printf("🌐 [Webshare.io] Sinkronisasi awal berhasil: %d proxy diimpor ke group '%s'", count, group)
				}
			}()
		}
	}

	provMgr.SetDefaultHTTPClient(proxyPool.NewHTTPClient(
		time.Duration(config.Get().Timeouts.APICallSeconds) * time.Second))
	log.Printf("🌐 9Router Proxy Pool diinisialisasi (Strategi: %s, Status: %v)", cfg.ProxyPool.Strategy, cfg.ProxyPool.Enabled)

	// 4. Seed Default Global Policy if not exists
	if globPol, _ := db.GetPolicy("global", "system"); globPol == nil {
		_ = db.SavePolicy(&storage.PolicyRecord{
			Scope:               "global",
			ScopeID:             "system",
			MaxUploadFileMB:     10,
			MaxTokens:           cfg.Defaults.MaxTokens,
			MaxHistoryTurns:     cfg.Defaults.MaxContextTurns,
			AutoCompaction:      true,
			CompactionThreshold: 15,
			TokenSaverMode:      cfg.TokenSaver.DefaultMode,
			ProxyPoolEnabled:    cfg.ProxyPool.Enabled,
		})
	}

	// 5. Seed Providers from DB or Default 9Router
	dbProviders, _ := db.ListProviders()
	if len(dbProviders) == 0 {
		default9Router := &storage.ProviderRecord{
			ID:           "9router",
			Name:         "9Router Gateway",
			Type:         "9router",
			BaseURL:      "https://api.9router.com/v1",
			APIKey:       os.Getenv("NINEROUTER_API_KEY"),
			APIKeys:      []string{os.Getenv("NINEROUTER_API_KEY")},
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini", "gpt-4o", "deepseek-chat", "claude-3-5-sonnet"},
			KeyStrategy:  "round-robin",
			Strategy:     "failsafe",
			IsActive:     true,
			Priority:     1,
		}
		_ = db.SaveProvider(default9Router)
		dbProviders, _ = db.ListProviders()
		log.Printf("🤖 Provider default 9Router didaftarkan (%s)", default9Router.DefaultModel)
	}

	// 5a. Initialize Bifrost AI Gateway. When enabled it takes over provider
	// routing, fallback chains, load balancing and key management, replacing the
	// legacy multi-provider router (per substitution rule).
	if cfg.Bifrost.Enabled {
		bifrostClient, err := bf.Init(db, cfg.Bifrost)
		if err != nil {
			log.Fatalf("❌ Gagal menginisialisasi Bifrost AI Gateway: %v", err)
		}
		defer bifrostClient.Shutdown()
		provMgr.SetRouter(bifrostClient)
		for i := range dbProviders {
			p := &dbProviders[i]
			if p.IsActive {
				provMgr.RegisterWithID(p.ID, bf.NewGatewayProvider(bifrostClient, p), p.Priority)
			}
		}
		log.Printf("🚀 Bifrost AI Gateway aktif (routing, fallback & load-balance via Bifrost)")
	} else {
		// 5b. Legacy provider registration (only when Bifrost is disabled)
		for _, p := range dbProviders {
			if !p.IsActive {
				continue
			}
			keys := p.APIKeys
			if len(keys) == 0 && p.APIKey != "" {
				keys = []string{p.APIKey}
			}
			models := p.EnabledModels()
			if len(models) == 0 && p.DefaultModel != "" {
				models = []string{p.DefaultModel}
			}

			var inst provider.Provider
			switch p.Type {
			case "gemini_web", "gemini_scrape":
				authData := p.APIKey
				if len(keys) > 0 {
					authData = strings.Join(keys, "; ")
				}
				webInst := provider.NewGeminiWebProvider(p.Name, authData, p.DefaultModel, models)
				webInst.SetOnCookieUpdate(func(provName, newCookies string, cookieMap map[string]string) {
					pRec, err := db.GetProvider(provName)
					if err == nil && pRec != nil {
						pRec.APIKey = newCookies
						pRec.APIKeys = []string{newCookies}
						_ = db.SaveProvider(pRec)
						log.Printf("🔄 [GeminiWeb] Cookie sesi Google (%s) berhasil diperbarui dan disimpan secara otomatis", provName)
					}
				})
				inst = webInst
			case "gemini":
				inst = provider.NewGeminiProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
			case "anthropic":
				inst = provider.NewAnthropicProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
			case "free_router", "free_openai", "free_gemini", "opencodefree", "free":
				inst = provider.NewFreeOpenAIProviderWithKeys(p.Name, p.Type, p.BaseURL, keys, p.KeyStrategy, p.DefaultModel, models)
			default:
				inst = provider.NewOpenAIProviderWithKeys(p.Name, p.Type, p.BaseURL, keys, p.KeyStrategy, p.DefaultModel, models)
			}
			provMgr.RegisterWithID(p.ID, inst, p.Priority)
			log.Printf("🤖 Provider aktif: %s (Tipe: %s, Default Model: %s, Keys: %d)", p.Name, p.Type, p.DefaultModel, len(keys))
		}

		// 5c. Load Registered Combos (legacy path)
		dbCombos, _ := db.ListCombos()
		for _, c := range dbCombos {
			if c.IsActive {
				comboCopy := c
				provMgr.RegisterCombo(&comboCopy)
				log.Printf("🔀 Combo aktif dimuat: %s (%d targets)", c.Name, len(c.Targets))
			}
		}
	}

	// 6. Initialize Multi-Agent Delegation Tool & Orchestrator
	subagentTool := agent.NewSubagentTool(promptBld, toolReg, provMgr)
	toolReg.Register(subagentTool)
	log.Printf("🤖 Multi-Agent Delegation Tool ('%s') didaftarkan (Max Parallel: %d, Auto-Delegate: %v)",
		subagentTool.Name(), cfg.SubAgent.MaxParallel, cfg.SubAgent.AutoDelegate)

	if cfg.Streaming.Enabled {
		log.Printf("📡 Streaming diaktifkan (Thinking: %v, Display: %s, ChunkDelay: %dms)",
			cfg.Streaming.ThinkingEnabled, cfg.Streaming.ThinkingDisplay, cfg.Streaming.ChunkDelayMs)
	}

	orchestrator := agent.NewOrchestrator(db, sessMgr, memMgr, promptBld, toolReg, provMgr)

	// 7. Initialize WhatsApp Manager (Multi-Device Store)
	waMgr, err := wachannel.InitManager(cfg.Server.DataDir, orchestrator, db)
	if err != nil {
		log.Printf("⚠️ Gagal inisialisasi WhatsApp Native Manager: %v", err)
	} else {
		defer waMgr.Close()
		log.Println("📱 WhatsApp Native Multi-Device Manager berhasil diinisialisasi.")
	}

	// 7b. Load and Initialize Active Channels
	activeChannels := make(map[string]channel.Channel)
	dbChannels, _ := db.ListChannels()
	for _, ch := range dbChannels {
		if !ch.IsActive {
			continue
		}
		if ch.Type == "telegram" {
			if ch.Identifier == cfg.AdminTelegram.BotToken {
				log.Printf("ℹ️ Channel Telegram '%s' menggunakan token yang sama dengan Admin Control Plane (dikelola langsung oleh Admin Bot).", ch.Name)
				continue
			}
			adapter, err := tgchannel.NewBotAdapter(ch.ID, ch.Name, ch.Identifier, orchestrator, db)
			if err == nil {
				activeChannels[ch.ID] = adapter
				_ = adapter.Start(context.Background())
			} else {
				log.Printf("⚠️ Gagal menjalankan channel Telegram '%s': %v", ch.Name, err)
			}
		} else if ch.Type == "whatsapp" {
			if waMgr != nil {
				chCopy := ch
				adapter, err := waMgr.CreateOrGetAdapter(&chCopy)
				if err == nil {
					activeChannels[ch.ID] = adapter
					_ = adapter.Start(context.Background())
				} else {
					log.Printf("⚠️ Gagal menjalankan channel WhatsApp '%s': %v", ch.Name, err)
				}
			}
		}
	}

	// 8. Setup Message Dispatcher for Cron Scheduler
	messageSender := func(channelType, targetID, text string) error {
		if channelType == "whatsapp" && waMgr != nil {
			for _, ad := range waMgr.ListAdapters() {
				if ad.IsConnected() && ad.IsLoggedIn() {
					return ad.SendMessage(targetID, text)
				}
			}
		}
		for _, ch := range activeChannels {
			if ch.Type() == channelType {
				return ch.SendMessage(targetID, text)
			}
		}
		return fmt.Errorf("tidak ada channel aktif bertipe %s", channelType)
	}

	// 9. Start Cron Scheduler
	scheduler := cron.NewScheduler(db, orchestrator, messageSender)
	if err := scheduler.Start(); err != nil {
		log.Printf("⚠️ Gagal memulai scheduler: %v", err)
	} else {
		log.Println("⏰ Cron Scheduler aktif.")
	}
	defer scheduler.Stop()

	// 9b. Start HCNSEC Auto Checkin Service
	checkinSvc := checkin.NewService(db, nil)
	checkinSvc.StartBackgroundScheduler(context.Background())
	defer checkinSvc.Stop()

	// 10. Start Admin Control Plane Telegram Bot
	var adminBot *admin.AdminBot
	if cfg.AdminTelegram.BotToken != "" {
		var err error
		adminBot, err = admin.NewAdminBot(
			cfg.AdminTelegram.BotToken,
			cfg,
			db,
			mdLoader,
			orchestrator,
			scheduler,
			toolReg,
			provMgr,
			memMgr,
			sessMgr,
			proxyPool,
			checkinSvc,
		)
		if err != nil {
			log.Printf("❌ Gagal memulai Telegram Admin Bot: %v", err)
		} else {
			// Set notify callback to send checkin report to admin
			checkinSvc.SetNotifyCallback(func(report string) {
				for _, uid := range cfg.AdminTelegram.AllowedUserIDs {
					_, _ = adminBot.Bot().Send(&tele.User{ID: uid}, report, tele.ModeHTML)
				}
			})
			go adminBot.Start()
			defer adminBot.Stop()
		}
	} else {
		log.Println("⚠️ Admin Telegram Bot Token belum diset di default_config.yaml atau GOASSISTANT_TELEGRAM_TOKEN.")
		log.Println("💡 Anda dapat menyetel token bot Telegram di configs/default_config.yaml lalu jalankan kembali daemon.")
	}

	// 11. Start GoAssist HTTP API Server
	var httpServer *goassisthttp.Server
	if cfg.HTTPServer.Enabled {
		readTimeout := time.Duration(cfg.HTTPServer.ReadTimeoutSeconds) * time.Second
		writeTimeout := time.Duration(cfg.HTTPServer.WriteTimeoutSeconds) * time.Second
		httpServer = goassisthttp.NewServer(cfg.HTTPServer.Port, cfg.HTTPServer.EndpointsFile, readTimeout, writeTimeout)
		httpServer.Start()
	}

	// 11b. Start GoAssistant Web Admin Control Plane (Telegram OTP Protected & Dynamic Port)
	var webAdminServer *webadmin.Server
	if cfg.WebAdmin.Enabled {
		var tgSender webadmin.TelegramSender
		if adminBot != nil {
			tgSender = func(userID int64, text string) error {
				_, sendErr := adminBot.Bot().Send(&tele.User{ID: userID}, text, tele.ModeHTML)
				return sendErr
			}
		}
		webAdminServer = webadmin.NewServer(cfg, db, orchestrator, tgSender)
		if err := webAdminServer.Start(); err != nil {
			log.Printf("⚠️ Gagal memulai Web Admin Server: %v", err)
		} else {
			if adminBot != nil {
				adminBot.SetWebAdminServer(webAdminServer)
			}
		}
	}

	log.Println("✅ GoAssistant Core siap melayani. Tekan Ctrl+C untuk berhenti.")

	// Wait for OS Interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\n🛑 Menghentikan GoAssistant secara aman (Graceful Shutdown)...")

	if webAdminServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := webAdminServer.Stop(shutdownCtx); err != nil {
			log.Printf("⚠️ Error saat mematikan Web Admin Server: %v", err)
		}
	}

	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("⚠️ Error saat mematikan HTTP Server: %v", err)
		}
	}
}
