package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"goassistant/internal/admin"
	"goassistant/internal/agent"
	"goassistant/internal/channel"
	tgchannel "goassistant/internal/channel/telegram"
	wachannel "goassistant/internal/channel/whatsapp"
	"goassistant/internal/config"
	"goassistant/internal/cron"
	"goassistant/internal/memory"
	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"
	"goassistant/internal/tools"
	"time"
)

var (
	version   = "1.1.0"
	buildDate = "2026-08-20"
)

func main() {
	configPath := flag.String("config", "configs/default_config.yaml", "Path ke file konfigurasi YAML")
	showVersion := flag.Bool("version", false, "Tampilkan versi aplikasi")
	flag.Parse()

	if *showVersion {
		fmt.Printf("GoAssistant Daemon v%s (Built: %s) [Zero-CGO Static Binary]\n", version, buildDate)
		return
	}

	log.Println("==========================================================")
	log.Printf("🚀 Memulai GoAssistant Core Daemon v%s...", version)
	log.Println("⚡ Kompatibilitas: Pure Go (Zero-CGO) - Ready for manylinux_2_28")
	log.Println("==========================================================")

	// 1. Load Configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("❌ Gagal memuat konfigurasi: %v", err)
	}
	log.Printf("⚙️ Konfigurasi berhasil dimuat dari: %s", *configPath)

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
	sessMgr := memory.NewSessionManager(db)
	mdLoader := agent.NewMDLoader(cfg.Server.MDDir)
	promptBld := agent.NewPromptBuilder(mdLoader)

	// Initialize Proxy Pool (Built-in 9Router Engine)
	proxyPool := proxy.NewPool(db, cfg.ProxyPool.Enabled, cfg.ProxyPool.Strategy)
	for _, rawProxy := range cfg.ProxyPool.InitialProxies {
		_, _ = proxyPool.AddNode(rawProxy, "")
	}
	provMgr.SetDefaultHTTPClient(proxyPool.NewHTTPClient(90 * time.Second))
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
			case "gemini":
				inst = provider.NewGeminiProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
			case "anthropic":
				inst = provider.NewAnthropicProviderWithKeys(p.Name, keys, p.KeyStrategy, p.DefaultModel, models)
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

	// 6. Initialize Orchestrator
	orchestrator := agent.NewOrchestrator(db, sessMgr, memMgr, promptBld, toolReg, provMgr)

	// 7. Load and Initialize Active Channels
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
			adapter := wachannel.NewBridgeAdapter(ch.ID, ch.Name, ch.Identifier, "", orchestrator, db)
			activeChannels[ch.ID] = adapter
			_ = adapter.Start(context.Background())
		}
	}

	// 8. Setup Message Dispatcher for Cron Scheduler
	messageSender := func(channelType, targetID, text string) error {
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
		)
		if err != nil {
			log.Printf("❌ Gagal memulai Telegram Admin Bot: %v", err)
		} else {
			go adminBot.Start()
			defer adminBot.Stop()
		}
	} else {
		log.Println("⚠️ Admin Telegram Bot Token belum diset di default_config.yaml atau GOASSISTANT_TELEGRAM_TOKEN.")
		log.Println("💡 Anda dapat menyetel token bot Telegram di configs/default_config.yaml lalu jalankan kembali daemon.")
	}

	log.Println("✅ GoAssistant Core siap melayani. Tekan Ctrl+C untuk berhenti.")

	// Wait for OS Interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\n🛑 Menghentikan GoAssistant secara aman (Graceful Shutdown)...")
}
