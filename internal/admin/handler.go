package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/cron"
	"goassistant/internal/memory"
	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"
	"goassistant/internal/tokensaver"
	"goassistant/internal/tools"
	tele "gopkg.in/telebot.v3"
)

type AdminBot struct {
	bot          *tele.Bot
	db           *storage.DB
	cfg          *config.AppConfig
	mdLoader     *agent.MDLoader
	orchestrator *agent.Orchestrator
	scheduler    *cron.Scheduler
	toolRegistry *tools.Registry
	provManager  *provider.Manager
	memManager   *memory.Manager
	sessManager  *memory.SessionManager
	proxyPool    *proxy.Pool

	limitsUI     *LimitsUI
	providerUI   *ProviderUI
	wizard       *ProviderWizard
	comboUI      *ComboUI
	comboWizard  *ComboWizard
	channelUI    *ChannelUI
	proxyUI      *ProxyUIHandler
	tokenSaverUI *TokenSaverUIHandler
	mdUI         *MDUI
	cronUI       *CronUI
	memoryUI     *MemoryUI
	auditUI      *AuditUI
}

func NewAdminBot(
	token string,
	cfg *config.AppConfig,
	db *storage.DB,
	loader *agent.MDLoader,
	orch *agent.Orchestrator,
	sched *cron.Scheduler,
	tr *tools.Registry,
	pm *provider.Manager,
	mm *memory.Manager,
	sm *memory.SessionManager,
	pool *proxy.Pool,
) (*AdminBot, error) {
	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: time.Duration(cfg.AdminTelegram.PollTimeout) * time.Second},
	}

	bot, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi admin telebot: %w", err)
	}

	a := &AdminBot{
		bot:          bot,
		db:           db,
		cfg:          cfg,
		mdLoader:     loader,
		orchestrator: orch,
		scheduler:    sched,
		toolRegistry: tr,
		provManager:  pm,
		memManager:   mm,
		sessManager:  sm,
		proxyPool:    pool,

		limitsUI:     NewLimitsUI(db),
		providerUI:   NewProviderUI(db, pm, pool),
		wizard:       NewProviderWizard(db, pm, bot),
		comboUI:      NewComboUI(db, pm),
		comboWizard:  NewComboWizard(db, pm, bot),
		channelUI:    NewChannelUI(db, tr),
		proxyUI:      NewProxyUIHandler(db, pool),
		tokenSaverUI: NewTokenSaverUIHandler(db),
		mdUI:         NewMDUI(loader, bot),
		cronUI:       NewCronUI(db, sched),
		memoryUI:     NewMemoryUI(db, mm, sm),
		auditUI:      NewAuditUI(db),
	}

	a.registerRoutes()
	return a, nil
}

func (a *AdminBot) Start() {
	log.Printf("[Admin-TG] Control Plane Bot aktif (@%s). Menunggu perintah admin...", a.bot.Me.Username)
	a.bot.Start()
}

func (a *AdminBot) Stop() {
	a.bot.Stop()
}

// Bot returns the telebot instance
func (a *AdminBot) Bot() *tele.Bot {
	return a.bot
}

func (a *AdminBot) registerRoutes() {
	// Admin Authorization Middleware
	a.bot.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			if c.Sender() == nil {
				return nil
			}
			senderID := c.Sender().ID
			if !a.cfg.IsAdminUser(senderID) {
				log.Printf("⛔ Akses ditolak dari Telegram User ID: %d (@%s)", senderID, c.Sender().Username)
				return c.Reply("⛔ <b>Akses Ditolak:</b> Anda tidak memiliki hak akses administrator GoAssistant.", tele.ModeHTML)
			}
			return next(c)
		}
	})

	// Main Dashboard & Navigation
	a.bot.Handle("/start", a.handleMenu)
	a.bot.Handle("/menu", a.handleMenu)
	a.bot.Handle("/help", a.handleHelp)
	a.bot.Handle(&tele.Btn{Unique: "menu_main"}, a.handleMenu)

	// Interactive Menu Callbacks
	a.bot.Handle(&tele.Btn{Unique: "menu_providers"}, func(c tele.Context) error {
		return c.EditOrSend(a.providerUI.RenderProvidersList(), a.providerUI.ProviderMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_combos"}, func(c tele.Context) error {
		return c.EditOrSend(a.comboUI.RenderCombosList(), a.comboUI.CombosKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_channels"}, func(c tele.Context) error {
		return c.EditOrSend(a.channelUI.RenderChannelsList(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_proxy"}, func(c tele.Context) error {
		return a.proxyUI.HandleListProxies(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_tokensaver"}, func(c tele.Context) error {
		return a.tokenSaverUI.HandleTokenSaverStatus(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_limits"}, func(c tele.Context) error {
		return c.EditOrSend(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "set_footer_global_off"}, func(c tele.Context) error {
		pol, _ := a.db.GetPolicy("global", "system")
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
		}
		pol.FooterMode = "off"
		_ = a.db.SavePolicy(pol)
		return c.EditOrSend(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "set_footer_global_tokens"}, func(c tele.Context) error {
		pol, _ := a.db.GetPolicy("global", "system")
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
		}
		pol.FooterMode = "tokens"
		_ = a.db.SavePolicy(pol)
		return c.EditOrSend(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "set_footer_global_full"}, func(c tele.Context) error {
		pol, _ := a.db.GetPolicy("global", "system")
		if pol == nil {
			pol = &storage.PolicyRecord{Scope: "global", ScopeID: "system"}
		}
		pol.FooterMode = "full"
		_ = a.db.SavePolicy(pol)
		return c.EditOrSend(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_md"}, func(c tele.Context) error {
		return c.EditOrSend(a.mdUI.RenderMDList(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_cron"}, func(c tele.Context) error {
		return c.EditOrSend(a.cronUI.RenderCronList(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_memory"}, func(c tele.Context) error {
		return c.EditOrSend(a.memoryUI.RenderMemorySummary(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_stats"}, func(c tele.Context) error {
		return c.EditOrSend(a.auditUI.RenderStatsSummary(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_tools"}, func(c tele.Context) error {
		return c.EditOrSend(a.channelUI.RenderToolsList(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_backup"}, a.handleBackup)
	a.bot.Handle(&tele.Btn{Unique: "menu_help"}, a.handleHelp)

	// Governance & Limits Commands
	a.bot.Handle("/limits", func(c tele.Context) error {
		return c.Reply(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle("/setlimit", a.limitsUI.HandleSetLimit)
	a.bot.Handle("/setfooter", a.limitsUI.HandleSetFooter)
	a.bot.Handle("/footer", a.limitsUI.HandleSetFooter)

	// Proxy Pool Commands (9Router Engine & Groups)
	a.bot.Handle("/proxies", a.proxyUI.HandleListProxies)
	a.bot.Handle("/proxy", a.proxyUI.HandleListProxies)
	a.bot.Handle("/proxygroups", a.proxyUI.HandleListGroups)
	a.bot.Handle("/groups", a.proxyUI.HandleListGroups)
	a.bot.Handle("/addproxies", a.proxyUI.HandleAddProxies)
	a.bot.Handle("/addproxy", a.proxyUI.HandleAddProxy)
	a.bot.Handle("/delproxy", a.proxyUI.HandleDeleteProxy)
	a.bot.Handle("/togglegroup", a.proxyUI.HandleToggleGroup)
	a.bot.Handle("/delgroup", a.proxyUI.HandleDeleteGroup)
	a.bot.Handle("/testgroup", a.proxyUI.HandleTestGroup)
	a.bot.Handle("/testproxies", a.proxyUI.HandleTestProxies)
	a.bot.Handle("/prunedead", a.proxyUI.HandlePruneDead)
	a.bot.Handle("/toggleproxy", a.proxyUI.HandleToggleProxy)
	a.bot.Handle("/proxystrategy", a.proxyUI.HandleSetStrategy)

	// Proxy Button Callbacks
	a.bot.Handle(&tele.Btn{Unique: "btn_test_proxies"}, a.proxyUI.HandleTestProxies)
	a.bot.Handle(&tele.Btn{Unique: "btn_proxy_groups"}, a.proxyUI.HandleListGroups)
	a.bot.Handle(&tele.Btn{Unique: "btn_toggle_proxy"}, a.proxyUI.HandleToggleProxy)

	// Token Saver Commands & 12-Engine Stack Callbacks
	a.bot.Handle("/tokensaver", a.tokenSaverUI.HandleTokenSaverStatus)
	a.bot.Handle("/tokensaverpreset", a.tokenSaverUI.HandleSetPresetCommand)
	a.bot.Handle("/tokensaverengine", a.tokenSaverUI.HandleSetEngineCommand)
	a.bot.Handle("/tokensaverstyle", a.tokenSaverUI.HandleSetStyleCommand)
	a.bot.Handle("/tokensaverdial", a.tokenSaverUI.HandleSetDialCommand)
	a.bot.Handle("/tokensavermode", a.tokenSaverUI.HandleSetMode)
	a.bot.Handle("/settokensavertarget", a.tokenSaverUI.HandleSetMode)

	// Token Saver Preset Callbacks
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_lite"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "lite") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_standard"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "standard") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_aggressive"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "aggressive") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_rtk"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "rtk") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_stacked"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "stacked") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_ultra"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "ultra") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_off"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "off") })
	a.bot.Handle(&tele.Btn{Unique: "ts_refresh"}, a.tokenSaverUI.HandleTokenSaverStatus)
	a.bot.Handle(&tele.Btn{Unique: "ts_toggle_dial"}, a.tokenSaverUI.HandleToggleDialCallback)

	// Token Saver Output Style Callbacks
	a.bot.Handle(&tele.Btn{Unique: "ts_style_terse"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "terse_prose") })
	a.bot.Handle(&tele.Btn{Unique: "ts_style_lesscode"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "less_code") })
	a.bot.Handle(&tele.Btn{Unique: "ts_style_indo"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "id_ringkas") })
	a.bot.Handle(&tele.Btn{Unique: "ts_style_none"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "none") })

	// Token Saver 12-Engine Individual Toggle Callbacks
	for _, engineID := range tokensaver.CanonicalEngines {
		eid := engineID
		a.bot.Handle(&tele.Btn{Unique: "ts_tgl_" + eid}, func(c tele.Context) error {
			return a.tokenSaverUI.HandleToggleEngineCallback(c, eid)
		})
	}

	// Provider Setup Wizard & Commands
	a.bot.Handle("/wizard", a.wizard.StartWizard)
	a.bot.Handle("/setup", a.wizard.StartWizard)
	a.bot.Handle(&tele.Btn{Unique: "wiz_start"}, a.wizard.StartWizard)
	a.bot.Handle(&tele.Btn{Unique: "wiz_cancel"}, func(c tele.Context) error {
		a.wizard.CancelWizard(c.Sender().ID)
		return c.EditOrSend("❌ Setup wizard dibatalkan.", a.providerUI.ProviderMenuKeyboard(), tele.ModeHTML)
	})

	// Wizard Type Selection Callbacks
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_9router"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "9router") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_openai"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "openai") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_deepseek"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "deepseek") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_groq"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "groq") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_gemini"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "gemini") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_anthropic"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "anthropic") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_ollama"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "ollama") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_custom"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "custom") })

	// Wizard Model Choice Callbacks (0..15)
	for i := 0; i <= 15; i++ {
		idx := i
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("wiz_mod_%d", idx)}, func(c tele.Context) error {
			return a.wizard.HandleModelSelect(c, idx)
		})
	}

	a.bot.Handle("/providers", func(c tele.Context) error {
		return c.Reply(a.providerUI.RenderProvidersList(), a.providerUI.ProviderMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle("/fetchmodels", a.providerUI.HandleFetchModels)
	a.bot.Handle("/setproviderproxy", a.providerUI.HandleSetProviderProxy)
	a.bot.Handle("/addprovider", func(c tele.Context) error {
		if len(c.Args()) == 0 {
			return a.wizard.StartWizard(c)
		}
		return a.providerUI.HandleAddProvider(c)
	})
	a.bot.Handle("/setkey", a.providerUI.HandleSetKey)
	a.bot.Handle("/addkey", a.providerUI.HandleAddKey)
	a.bot.Handle("/setkeys", a.providerUI.HandleSetKeys)
	a.bot.Handle("/delkey", a.providerUI.HandleDelKey)
	a.bot.Handle("/keystrategy", a.providerUI.HandleSetKeyStrategy)
	a.bot.Handle("/setmodels", a.providerUI.HandleSetModels)
	a.bot.Handle("/addmodel", a.providerUI.HandleAddModel)
	a.bot.Handle("/setmodel", a.providerUI.HandleSetModel)
	a.bot.Handle("/delprovider", a.providerUI.HandleDelProvider)

	// Combo Commands & Wizard
	a.bot.Handle("/combos", a.comboUI.HandleCombos)
	a.bot.Handle("/combowizard", a.comboWizard.StartWizard)
	a.bot.Handle("/addcombo", func(c tele.Context) error {
		if len(c.Args()) == 0 {
			return a.comboWizard.StartWizard(c)
		}
		return a.comboUI.HandleAddCombo(c)
	})
	a.bot.Handle("/delcombo", a.comboUI.HandleDelCombo)

	// Combo Wizard Callbacks
	a.bot.Handle(&tele.Btn{Unique: "cwiz_start"}, a.comboWizard.StartWizard)
	a.bot.Handle(&tele.Btn{Unique: "cwiz_save"}, a.comboWizard.HandleSaveCombo)
	a.bot.Handle(&tele.Btn{Unique: "cwiz_cancel"}, func(c tele.Context) error {
		a.comboWizard.CancelWizard(c.Sender().ID)
		return c.EditOrSend("❌ Combo wizard dibatalkan.", a.comboUI.CombosKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_back_prov"}, func(c tele.Context) error {
		return a.comboWizard.StartWizard(c)
	})

	// Dynamic Provider buttons for Combo Wizard
	dbProviders, _ := a.db.ListProviders()
	for _, p := range dbProviders {
		pID := p.ID
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("cwiz_prov_%s", pID)}, func(c tele.Context) error {
			return a.comboWizard.HandleProviderSelect(c, pID)
		})
	}

	// Model index buttons for Combo Wizard (0..15)
	for i := 0; i <= 15; i++ {
		idx := i
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("cwiz_mod_%d", idx)}, func(c tele.Context) error {
			return a.comboWizard.HandleModelSelect(c, idx)
		})
	}
	a.bot.Handle("/addchannel", a.channelUI.HandleAddChannel)
	a.bot.Handle("/tools", func(c tele.Context) error {
		return c.Reply(a.channelUI.RenderToolsList(), tele.ModeHTML)
	})
	a.bot.Handle("/toolperms", a.channelUI.HandleToolPerms)

	// MD Commands
	a.bot.Handle("/md", func(c tele.Context) error {
		return c.Reply(a.mdUI.RenderMDList(), tele.ModeHTML)
	})
	a.bot.Handle("/viewmd", a.mdUI.HandleViewMD)
	a.bot.Handle("/editmd", a.mdUI.HandleEditMD)
	a.bot.Handle("/reloadmd", func(c tele.Context) error {
		a.mdLoader.Reload()
		return c.Reply("🔄 Seluruh cache file <code>.md</code> berhasil dimuat ulang!", tele.ModeHTML)
	})

	// Cron Commands
	a.bot.Handle("/cron", func(c tele.Context) error {
		return c.Reply(a.cronUI.RenderCronList(), tele.ModeHTML)
	})
	a.bot.Handle("/addcron", a.cronUI.HandleAddCron)
	a.bot.Handle("/runcron", a.cronUI.HandleRunCron)
	a.bot.Handle("/delcron", a.cronUI.HandleDelCron)

	// Memory Commands
	a.bot.Handle("/memory", func(c tele.Context) error {
		return c.Reply(a.memoryUI.RenderMemorySummary(), tele.ModeHTML)
	})
	a.bot.Handle("/savefact", a.memoryUI.HandleSaveFact)
	a.bot.Handle("/resetsession", a.memoryUI.HandleResetSession)

	// Stats & Audit Commands
	a.bot.Handle("/stats", func(c tele.Context) error {
		return c.Reply(a.auditUI.RenderStatsSummary(), tele.ModeHTML)
	})
	a.bot.Handle("/logs", a.auditUI.HandleLogs)
	a.bot.Handle("/viewlog", a.auditUI.HandleViewLog)
	a.bot.Handle("/lastlog", a.auditUI.HandleViewLog)
	a.bot.Handle("/exportlogs", a.auditUI.HandleExportLogs)
	a.bot.Handle("/backup", a.handleBackup)

	// File Upload Handler (Document .md / backup restore)
	a.bot.Handle(tele.OnDocument, a.mdUI.HandleDocumentUpload)

	// Unified Text Message Handler (Wizard State Interceptor + Direct Chat)
	a.bot.Handle(tele.OnText, func(c tele.Context) error {
		// 1. Check and intercept if user is in Provider Wizard dialog
		if handled, err := a.wizard.HandleTextMessage(c); handled {
			return err
		}

		// 2. Check and intercept if user is in Combo Wizard dialog
		if handled, err := a.comboWizard.HandleTextMessage(c); handled {
			return err
		}

		// 3. Direct Chat with Assistant from Admin PM
		msg := c.Message().Text
		if msg == "" || msg[0] == '/' {
			return nil
		}

		_ = c.Notify(tele.Typing)
		thinkingMsg, _ := a.bot.Reply(c.Message(), "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		resp, err := a.orchestrator.ProcessMessage(ctx, agent.UserRequest{
			ChannelType: "telegram_admin",
			ChannelID:   "admin",
			ChannelName: "Telegram Admin PM",
			ChatID:      fmt.Sprintf("%d", c.Chat().ID),
			UserID:      fmt.Sprintf("%d", c.Sender().ID),
			UserName:    c.Sender().Username,
			UserPrompt:  msg,
			OnProgress: func(status string) {
				if thinkingMsg != nil {
					_, _ = a.bot.Edit(thinkingMsg, status, tele.ModeHTML)
				}
			},
		})

		if err != nil {
			if thinkingMsg != nil {
				_, _ = a.bot.Edit(thinkingMsg, fmt.Sprintf("❌ Error: %v", html.EscapeString(err.Error())), tele.ModeHTML)
				return nil
			}
			return c.Reply(fmt.Sprintf("❌ Error: %v", html.EscapeString(err.Error())), tele.ModeHTML)
		}

		return sendOrEditSplitMessage(c, thinkingMsg, resp.Text)
	})
}

func (a *AdminBot) handleMenu(c tele.Context) error {
	text := "👋 <b>SELAMAT DATANG DI GOASSISTANT CONTROL PLANE</b>\n\n" +
		"Sistem AI Assistant berbasis <b>Pure Golang</b> tanpa web UI. Anda dapat mengatur seluruh komponen sistem langsung melalui tombol interaktif di bawah ini:"
	return c.Send(text, MainMenuKeyboard(), tele.ModeHTML)
}

func sendSplitMessage(c tele.Context, text string) error {
	return sendOrEditSplitMessage(c, nil, text)
}

func sendOrEditSplitMessage(c tele.Context, thinkingMsg *tele.Message, text string) error {
	if strings.TrimSpace(text) == "" {
		text = "(Tidak ada respon dari model)"
	}

	chunks := splitText(text, 4000)
	if len(chunks) == 0 {
		return nil
	}

	if thinkingMsg != nil {
		_, err := c.Bot().Edit(thinkingMsg, chunks[0])
		if err != nil {
			_ = c.Reply(chunks[0])
		}
		for _, chunk := range chunks[1:] {
			if err := c.Reply(chunk); err != nil {
				return err
			}
		}
		return nil
	}

	for _, chunk := range chunks {
		if err := c.Reply(chunk); err != nil {
			return err
		}
	}
	return nil
}

func splitText(text string, maxLen int) []string {
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		chunkSize := maxLen
		lastNL := strings.LastIndex(text[:chunkSize], "\n")
		if lastNL > maxLen/2 {
			chunkSize = lastNL
		}
		chunks = append(chunks, text[:chunkSize])
		text = strings.TrimPrefix(text[chunkSize:], "\n")
	}
	return chunks
}

func (a *AdminBot) handleHelp(c tele.Context) error {
	text := "📖 <b>PANDUAN LENGKAP COMMAND GOASSISTANT</b>\n\n" +
		"🎛️ <b>Navigasi & Menu:</b>\n" +
		"• <code>/menu</code> - Tampilkan dashboard tombol interaktif\n\n" +
		"🤖 <b>Provider AI (9Router Multi-Key & Router):</b>\n" +
		"• <code>/wizard</code> atau <code>/setup</code> - Wizard interaktif tambah provider & deteksi model otomatis\n" +
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

func (a *AdminBot) handleBackup(c tele.Context) error {
	_ = c.Notify(tele.Typing)

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	// 1. Archive SQLite DB file
	dbPath := a.cfg.Server.DBPath
	if dbData, err := os.ReadFile(dbPath); err == nil {
		w, _ := zipWriter.Create("goassistant.db")
		_, _ = w.Write(dbData)
	}

	// 2. Archive all MD files
	mdFiles, _ := a.mdLoader.ListFiles()
	for _, fname := range mdFiles {
		if content, err := a.mdLoader.GetFile(fname); err == nil {
			w, _ := zipWriter.Create(filepath.Join("md", fname))
			_, _ = w.Write([]byte(content))
		}
	}

	_ = zipWriter.Close()

	doc := &tele.Document{
		File:     tele.FromReader(&buf),
		FileName: fmt.Sprintf("goassistant_backup_%s.zip", time.Now().Format("20060102_150405")),
		Caption:  "💾 Full Backup GoAssistant (SQLite Database + Markdown Configs)",
	}

	return c.Send(doc)
}
