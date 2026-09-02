package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"goassistant/internal/admin"
	"goassistant/internal/agent"
	"goassistant/internal/channel"
	tgchannel "goassistant/internal/channel/telegram"
	wachannel "goassistant/internal/channel/whatsapp"
	"goassistant/internal/checkin"
	"goassistant/internal/config"
	"goassistant/internal/cron"
	"goassistant/internal/goassisthttp"
	"goassistant/internal/instance"
	"goassistant/internal/memory"
	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
	"goassistant/internal/version"

	tele "gopkg.in/telebot.v3"
)

func main() {
	configPath := flag.String("config", "configs/default_config.yaml", "Path ke file konfigurasi YAML")
	showVersion := flag.Bool("version", false, "Tampilkan versi aplikasi")
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
	toolReg := tools.GetRegistry()
	provMgr := provider.GetManager()
	memMgr := memory.NewManager(db)
	toolReg.Register(tools.NewUserMemoryTool(memMgr))
	sessMgr := memory.NewSessionManager(db)
	mdLoader := agent.NewMDLoader(cfg.Server.MDDir)
	promptBld := agent.NewPromptBuilder(mdLoader)

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
		provMgr.Register(provider.NewOpenAIProviderWithKeys(default9Router.Name, default9Router.Type, default9Router.BaseURL, default9Router.APIKeys, default9Router.KeyStrategy, default9Router.DefaultModel, default9Router.Models), 1)
		log.Printf("🤖 Provider default 9Router didaftarkan (%s)", default9Router.DefaultModel)
	} else {
		for _, p := range dbProviders {
			if !p.IsActive {
				continue
			}
			keys := p.APIKeys
			if len(keys) == 0 && p.APIKey != "" {
				keys = []string{p.APIKey}
			}
			models := p.Models
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
			provMgr.Register(inst, p.Priority)
			log.Printf("🤖 Provider aktif: %s (Tipe: %s, Default Model: %s, Keys: %d)", p.Name, p.Type, p.DefaultModel, len(keys))
		}
	}

	// 5b. Load Registered Combos
	dbCombos, _ := db.ListCombos()
	for _, c := range dbCombos {
		if c.IsActive {
			comboCopy := c
			provMgr.RegisterCombo(&comboCopy)
			log.Printf("🔀 Combo aktif dimuat: %s (%d targets)", c.Name, len(c.Targets))
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
	if cfg.AdminTelegram.BotToken != "" {
		adminBot, err := admin.NewAdminBot(
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

	log.Println("✅ GoAssistant Core siap melayani. Tekan Ctrl+C untuk berhenti.")

	// Wait for OS Interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\n🛑 Menghentikan GoAssistant secara aman (Graceful Shutdown)...")

	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("⚠️ Error saat mematikan HTTP Server: %v", err)
		}
	}
}

