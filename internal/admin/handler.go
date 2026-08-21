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
	"sync"
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
	activeTasks  sync.Map

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
	updateUI     *UpdateUI
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
		wizard:       NewProviderWizard(db, pm, pool, bot),
		comboUI:      NewComboUI(db, pm),
		comboWizard:  NewComboWizard(db, pm, bot),
		channelUI:    NewChannelUI(db, tr),
		proxyUI:      NewProxyUIHandler(db, pool),
		tokenSaverUI: NewTokenSaverUIHandler(db),
		mdUI:         NewMDUI(loader, bot),
		cronUI:       NewCronUI(db, sched),
		memoryUI:     NewMemoryUI(db, mm, sm),
		auditUI:      NewAuditUI(db),
		updateUI:     NewUpdateUI(cfg, bot),
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
	a.bot.Handle("/status", a.handleStatus)
	a.bot.Handle("/new", a.handleNew)
	a.bot.Handle("/reset", a.handleNew)
	a.bot.Handle("/clear", a.handleNew)
	a.bot.Handle("/stop", a.handleStop)
	a.bot.Handle("/cancel", a.handleStop)
	a.bot.Handle(&tele.Btn{Unique: "menu_main"}, a.handleMenu)
	a.bot.Handle(&tele.Btn{Unique: "menu_status"}, a.handleStatus)

	// Interactive Menu Callbacks
	a.bot.Handle(&tele.Btn{Unique: "menu_providers"}, func(c tele.Context) error {
		return c.EditOrSend(a.providerUI.RenderProvidersList(), a.providerUI.ProviderMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_combos"}, func(c tele.Context) error {
		return c.EditOrSend(a.comboUI.RenderCombosList(), a.comboUI.CombosKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_channels"}, func(c tele.Context) error {
		return c.EditOrSend(a.channelUI.RenderChannelsList(), a.channelUI.ChannelsKeyboard(), tele.ModeHTML)
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
		return c.EditOrSend(a.mdUI.RenderMDList(), a.mdUI.MDMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_cron"}, func(c tele.Context) error {
		return c.EditOrSend(a.cronUI.RenderCronList(), a.cronUI.CronKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_memory"}, func(c tele.Context) error {
		return c.EditOrSend(a.memoryUI.RenderMemorySummary(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_stats"}, func(c tele.Context) error {
		return c.EditOrSend(a.auditUI.RenderStatsSummary(), BackToMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_tools"}, func(c tele.Context) error {
		return c.EditOrSend(a.channelUI.RenderToolsList(), a.channelUI.ChannelsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_backup"}, a.handleBackup)
	a.bot.Handle(&tele.Btn{Unique: "menu_update"}, a.updateUI.HandleCheckUpdate)
	a.bot.Handle(&tele.Btn{Unique: "btn_check_update"}, a.updateUI.HandleCheckUpdate)
	a.bot.Handle(&tele.Btn{Unique: "btn_do_update"}, a.updateUI.HandleDoUpdate)
	a.bot.Handle(&tele.Btn{Unique: "menu_help"}, a.handleHelp)

	// Update Commands
	a.bot.Handle("/update", a.updateUI.HandleCheckUpdate)
	a.bot.Handle("/checkupdate", a.updateUI.HandleCheckUpdate)

	// Governance & Limits Commands
	a.bot.Handle("/limits", func(c tele.Context) error {
		return c.Reply(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle("/limitswizard", a.limitsUI.StartLimitsWizard)
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
	a.bot.Handle("/editprovider", func(c tele.Context) error {
		target := ""
		if len(c.Args()) > 0 {
			target = c.Args()[0]
		}
		return a.wizard.StartEditWizard(c, target)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_start"}, a.wizard.StartWizard)
	a.bot.Handle(&tele.Btn{Unique: "wiz_edit_start"}, func(c tele.Context) error {
		return a.wizard.StartEditWizard(c, "")
	})
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

	// Edit Provider Callbacks
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_detect"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditAutoDetect(c, sess.EditingProviderID)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_defmod"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditPickDefaultModel(c, sess.EditingProviderID)
	})
	for i := 0; i <= 15; i++ {
		idx := i
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("wiz_edm_%d", idx)}, func(c tele.Context) error {
			return a.wizard.HandleEditSetDefaultModel(c, idx)
		})
	}
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_keys_rep"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.PromptEditStep(c, sess.EditingProviderID, StepEditReplaceKeys,
			"🔑 <b>GANTI SEMUA API KEY</b>\n\nSilakan kirimkan API Key baru untuk provider ini (bisa lebih dari 1 key, pisahkan dengan koma/baris baru):")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_keys_add"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.PromptEditStep(c, sess.EditingProviderID, StepEditAddKeys,
			"➕ <b>TAMBAH API KEY KE POOL</b>\n\nSilakan kirimkan API Key tambahan yang ingin dimasukkan ke rotasi pool:")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_keystrat"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditKeyStrategyMenu(c, sess.EditingProviderID)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_strat_rr"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditSetKeyStrategy(c, sess.EditingProviderID, "round-robin")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_strat_fo"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditSetKeyStrategy(c, sess.EditingProviderID, "failover")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_strat_rd"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditSetKeyStrategy(c, sess.EditingProviderID, "random")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_baseurl"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.PromptEditStep(c, sess.EditingProviderID, StepEditBaseURL,
			"🌐 <b>UBAH BASE URL</b>\n\nKirimkan endpoint Base URL baru (contoh: <code>https://api.openai.com/v1</code>):")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_proxy"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditProxyMenu(c, sess.EditingProviderID)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_px_off"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		p, err := a.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			return c.Reply("❌ Provider tidak ditemukan.")
		}
		p.ProxyEnabled = false
		p.ProxyGroup = ""
		_ = a.db.SaveProvider(p)
		return a.wizard.RenderProviderEditDashboard(c, p)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_px_def"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		p, err := a.db.GetProvider(sess.EditingProviderID)
		if err != nil || p == nil {
			return c.Reply("❌ Provider tidak ditemukan.")
		}
		p.ProxyEnabled = true
		p.ProxyGroup = "default"
		_ = a.db.SaveProvider(p)
		return a.wizard.RenderProviderEditDashboard(c, p)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_px_cust"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.PromptEditStep(c, sess.EditingProviderID, StepEditProxyGroup,
			"🛡️ <b>PROXY GROUP KHUSUS</b>\n\nKetik nama proxy group yang ingin dipasangkan ke provider ini:")
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_toggle"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditToggleActive(c, sess.EditingProviderID)
	})
	a.bot.Handle(&tele.Btn{Unique: "wiz_ed_del"}, func(c tele.Context) error {
		sess, ok := a.wizard.GetSession(c.Sender().ID)
		if !ok || sess.EditingProviderID == "" {
			return a.wizard.StartEditWizard(c, "")
		}
		return a.wizard.HandleEditDeletePrompt(c, sess.EditingProviderID)
	})

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
	a.bot.Handle("/editcombo", func(c tele.Context) error {
		target := ""
		if len(c.Args()) > 0 {
			target = c.Args()[0]
		}
		return a.comboWizard.StartEditWizard(c, target)
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_edit_start"}, func(c tele.Context) error {
		return a.comboWizard.StartEditWizard(c, "")
	})
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

	// Combo Edit Callbacks
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_add_target"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditAddTargetStart(c, sess.EditingComboName)
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_del_target"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditDelTargetMenu(c, sess.EditingComboName)
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_reorder"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		cName := sess.EditingComboName
		a.comboWizard.CancelWizard(c.Sender().ID)
		a.comboWizard.mu.Lock()
		a.comboWizard.sessions[c.Sender().ID] = &ComboWizardSession{
			IsEditing:        true,
			EditingComboName: cName,
			Name:             cName,
			Step:             StepComboPickProvider,
			CreatedAt:        time.Now(),
		}
		a.comboWizard.mu.Unlock()
		return a.comboWizard.promptPickProvider(c, a.comboWizard.sessions[c.Sender().ID])
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_strat"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditStrategyMenu(c, sess.EditingComboName)
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_st_fs"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditSetStrategy(c, sess.EditingComboName, "failsafe")
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_st_rr"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditSetStrategy(c, sess.EditingComboName, "round-robin")
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_desc"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.PromptEditStep(c, sess.EditingComboName, StepComboEditDesc,
			"📝 <b>UBAH DESKRIPSI COMBO</b>\n\nKirimkan deskripsi baru untuk combo ini:")
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_toggle"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditToggleActive(c, sess.EditingComboName)
	})
	a.bot.Handle(&tele.Btn{Unique: "cwiz_ed_del"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		sess, ok := a.comboWizard.sessions[c.Sender().ID]
		if !ok || sess.EditingComboName == "" {
			return a.comboWizard.StartEditWizard(c, "")
		}
		return a.comboWizard.HandleEditDeletePrompt(c, sess.EditingComboName)
	})

	// Model index buttons for Combo Wizard (0..15)
	for i := 0; i <= 15; i++ {
		idx := i
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("cwiz_mod_%d", idx)}, func(c tele.Context) error {
			return a.comboWizard.HandleModelSelect(c, idx)
		})
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("cwiz_ed_mod_%d", idx)}, func(c tele.Context) error {
			return a.comboWizard.HandleEditAddTargetModSelect(c, idx)
		})
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("cwiz_ed_rem_%d", idx)}, func(c tele.Context) error {
			return a.comboWizard.HandleEditDelTargetExecute(c, idx)
		})
	}

	// Limits Wizard Callbacks
	a.bot.Handle(&tele.Btn{Unique: "lim_wiz_start"}, a.limitsUI.StartLimitsWizard)
	a.bot.Handle(&tele.Btn{Unique: "lim_sc_global"}, func(c tele.Context) error {
		return a.limitsUI.RenderScopeLimitsDashboard(c, "global", "system")
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_sc_chan_menu"}, a.limitsUI.RenderChannelPickMenu)
	a.bot.Handle(&tele.Btn{Unique: "lim_sc_chat_prompt"}, func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepScopeChatID)
		text := "💬 <b>MASUKKAN CHAT / GROUP ID</b>\n\nKirimkan Chat ID (contoh: <code>-100123456789</code> atau ID user Telegram):"
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", "lim_wiz_start")
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_footer_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		text := fmt.Sprintf("📊 <b>PILIH TAMPILAN FOOTER (<code>%s:%s</code>)</b>\n\n"+
			"• <b>Off:</b> Tanpa footer di akhir jawaban bot.\n"+
			"• <b>Tokens:</b> Menampilkan penggunaan token & model.\n"+
			"• <b>Full:</b> Menampilkan latency (ms), token, context turns, & model.", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		btnOff := menu.Data("❌ Off", "lim_ft_off")
		btnTok := menu.Data("🪙 Tokens Only", "lim_ft_tok")
		btnFull := menu.Data("📊 Full (Lengkap)", "lim_ft_full")
		btnBack := menu.Data("⬅️ Kembali", fmt.Sprintf("lim_back_dash"))
		menu.Inline(menu.Row(btnOff, btnTok, btnFull), menu.Row(btnBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_ft_off"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
			if pol == nil {
				pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
			}
			pol.FooterMode = "off"
			_ = a.db.SavePolicy(pol)
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_ft_tok"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
			if pol == nil {
				pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
			}
			pol.FooterMode = "tokens"
			_ = a.db.SavePolicy(pol)
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_ft_full"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
			if pol == nil {
				pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
			}
			pol.FooterMode = "full"
			_ = a.db.SavePolicy(pol)
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_back_dash"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok && sess.Scope != "" {
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_upload_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		text := fmt.Sprintf("📁 <b>ATUR MAX UPLOAD FILE (<code>%s:%s</code>)</b>\n\nPilih batas ukuran upload dokumen / gambar:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		b5 := menu.Data("5 MB", "lim_up_5")
		b10 := menu.Data("10 MB", "lim_up_10")
		b25 := menu.Data("25 MB", "lim_up_25")
		b50 := menu.Data("50 MB", "lim_up_50")
		b100 := menu.Data("100 MB", "lim_up_100")
		bCust := menu.Data("✏️ Angka Custom", "lim_up_cust")
		bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
		menu.Inline(menu.Row(b5, b10, b25), menu.Row(b50, b100, bCust), menu.Row(bBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	for _, upMB := range []int{5, 10, 25, 50, 100} {
		val := upMB
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("lim_up_%d", val)}, func(c tele.Context) error {
			if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
				pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
				if pol == nil {
					pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
				}
				pol.MaxUploadFileMB = val
				_ = a.db.SavePolicy(pol)
				return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
			}
			return a.limitsUI.StartLimitsWizard(c)
		})
	}
	a.bot.Handle(&tele.Btn{Unique: "lim_up_cust"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepCustomUpload)
		return c.EditOrSend("📁 <b>MASUKKAN MAX UPLOAD (MB)</b>\n\nKirimkan angka ukuran maksimum dalam MB (contoh: <code>15</code> atau <code>30</code>):", tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_tokens_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		text := fmt.Sprintf("🪙 <b>ATUR MAX OUTPUT TOKENS (<code>%s:%s</code>)</b>\n\nPilih batas maksimum token output AI per respons:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		b1k := menu.Data("1024", "lim_tk_1024")
		b2k := menu.Data("2048", "lim_tk_2048")
		b4k := menu.Data("4096", "lim_tk_4096")
		b8k := menu.Data("8192", "lim_tk_8192")
		b16k := menu.Data("16384", "lim_tk_16384")
		bCust := menu.Data("✏️ Custom", "lim_tk_cust")
		bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
		menu.Inline(menu.Row(b1k, b2k, b4k), menu.Row(b8k, b16k, bCust), menu.Row(bBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	for _, tk := range []int{1024, 2048, 4096, 8192, 16384} {
		val := tk
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("lim_tk_%d", val)}, func(c tele.Context) error {
			if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
				pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
				if pol == nil {
					pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
				}
				pol.MaxTokens = val
				_ = a.db.SavePolicy(pol)
				return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
			}
			return a.limitsUI.StartLimitsWizard(c)
		})
	}
	a.bot.Handle(&tele.Btn{Unique: "lim_tk_cust"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepCustomTokens)
		return c.EditOrSend("🪙 <b>MASUKKAN MAX OUTPUT TOKENS</b>\n\nKirimkan angka token maksimum (contoh: <code>6000</code>):", tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_history_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		text := fmt.Sprintf("💬 <b>ATUR MAX HISTORY TURNS (<code>%s:%s</code>)</b>\n\nPilih batas turn konteks percakapan yang disimpan:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		b10 := menu.Data("10 turns", "lim_hi_10")
		b15 := menu.Data("15 turns", "lim_hi_15")
		b20 := menu.Data("20 turns", "lim_hi_20")
		b30 := menu.Data("30 turns", "lim_hi_30")
		b50 := menu.Data("50 turns", "lim_hi_50")
		bCust := menu.Data("✏️ Custom", "lim_hi_cust")
		bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
		menu.Inline(menu.Row(b10, b15, b20), menu.Row(b30, b50, bCust), menu.Row(bBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	for _, hi := range []int{10, 15, 20, 30, 50} {
		val := hi
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("lim_hi_%d", val)}, func(c tele.Context) error {
			if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
				pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
				if pol == nil {
					pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
				}
				pol.MaxHistoryTurns = val
				_ = a.db.SavePolicy(pol)
				return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
			}
			return a.limitsUI.StartLimitsWizard(c)
		})
	}
	a.bot.Handle(&tele.Btn{Unique: "lim_hi_cust"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepCustomHistory)
		return c.EditOrSend("💬 <b>MASUKKAN MAX HISTORY TURNS</b>\n\nKirimkan angka jumlah turn history percakapan (contoh: <code>40</code>):", tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_compact_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		text := fmt.Sprintf("🗜️ <b>ATUR AUTO-COMPACTION (<code>%s:%s</code>)</b>\n\nKompresi otomatis ringkasan percakapan saat turn melebihi ambang batas:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		bOn := menu.Data("🟢 Aktifkan", "lim_cp_on")
		bOff := menu.Data("🔴 Nonaktifkan", "lim_cp_off")
		t10 := menu.Data("Ambang 10", "lim_cp_th_10")
		t15 := menu.Data("Ambang 15", "lim_cp_th_15")
		t20 := menu.Data("Ambang 20", "lim_cp_th_20")
		tCust := menu.Data("✏️ Ambang Custom", "lim_cp_th_cust")
		bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
		menu.Inline(menu.Row(bOn, bOff), menu.Row(t10, t15, t20), menu.Row(tCust), menu.Row(bBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_cp_on"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
			if pol == nil {
				pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
			}
			pol.AutoCompaction = true
			_ = a.db.SavePolicy(pol)
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_cp_off"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
			if pol == nil {
				pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
			}
			pol.AutoCompaction = false
			_ = a.db.SavePolicy(pol)
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	for _, th := range []int{10, 15, 20} {
		val := th
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("lim_cp_th_%d", val)}, func(c tele.Context) error {
			if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
				pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
				if pol == nil {
					pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
				}
				pol.CompactionThreshold = val
				_ = a.db.SavePolicy(pol)
				return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
			}
			return a.limitsUI.StartLimitsWizard(c)
		})
	}
	a.bot.Handle(&tele.Btn{Unique: "lim_cp_th_cust"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepCustomThreshold)
		return c.EditOrSend("🗜️ <b>MASUKKAN AMBANG COMPACTION (TURNS)</b>\n\nKirimkan jumlah turn minimal untuk memicu auto-compaction (contoh: <code>12</code>):", tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_model_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		combos, _ := a.db.ListCombos()
		text := fmt.Sprintf("🤖 <b>ATUR MODEL OVERRIDE (<code>%s:%s</code>)</b>\n\nPilih model default atau combo chain yang ingin dipaksakan:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		var rows []tele.Row
		btnReset := menu.Data("🔄 Reset ke Default Provider", "lim_mod_reset")
		rows = append(rows, menu.Row(btnReset))
		for _, cm := range combos {
			cmCopy := cm
			btn := menu.Data(fmt.Sprintf("🔀 Combo: %s", cmCopy.Name), fmt.Sprintf("lim_mod_set_combo:%s", cmCopy.Name))
			rows = append(rows, menu.Row(btn))
		}
		btnCust := menu.Data("✏️ Ketik Nama Model Khusus", "lim_mod_cust")
		btnBack := menu.Data("⬅️ Kembali", "lim_back_dash")
		rows = append(rows, menu.Row(btnCust), menu.Row(btnBack))
		menu.Inline(rows...)
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_mod_reset"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
			if pol == nil {
				pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
			}
			pol.ModelOverride = ""
			_ = a.db.SavePolicy(pol)
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return a.limitsUI.StartLimitsWizard(c)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_mod_cust"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepCustomModel)
		return c.EditOrSend("🤖 <b>MASUKKAN MODEL OVERRIDE</b>\n\nKetik nama model (contoh: <code>gpt-4o-mini</code>, <code>claude-3-5-sonnet-20241022</code>, atau <code>combo:smart_chain</code>):", tele.ModeHTML)
	})

	// Channel Wizard Callbacks
	a.bot.Handle(&tele.Btn{Unique: "chan_wiz_start"}, a.channelUI.StartChannelWizard)
	a.bot.Handle(&tele.Btn{Unique: "chan_wiz_add_type"}, a.channelUI.RenderAddChannelTypeMenu)
	a.bot.Handle(&tele.Btn{Unique: "chan_type_telegram"}, func(c tele.Context) error {
		return a.channelUI.PromptChannelIDAndName(c, "telegram")
	})
	a.bot.Handle(&tele.Btn{Unique: "chan_type_whatsapp"}, func(c tele.Context) error {
		return a.channelUI.PromptChannelIDAndName(c, "whatsapp")
	})
	a.bot.Handle(&tele.Btn{Unique: "tool_wiz_start"}, func(c tele.Context) error {
		return a.channelUI.StartToolWizard(c, "")
	})

	// Cron Wizard Callbacks
	a.bot.Handle(&tele.Btn{Unique: "cron_wiz_start"}, a.cronUI.StartCronWizard)
	a.bot.Handle(&tele.Btn{Unique: "cron_wiz_cancel"}, func(c tele.Context) error {
		a.cronUI.CancelWizard(c.Sender().ID)
		return c.EditOrSend("❌ Cron wizard dibatalkan.", a.cronUI.CronKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_ch_telegram"}, func(c tele.Context) error {
		sess, ok := a.cronUI.GetSession(c.Sender().ID)
		if !ok {
			return a.cronUI.StartCronWizard(c)
		}
		sess.TargetChannel = "telegram"
		a.cronUI.SetSessionStep(c.Sender().ID, CronStepChatID)
		text := "💬 <b>MASUKKAN CHAT / GROUP ID TARGET TELEGRAM</b>\n\nKirimkan ID chat/grup tujuan pengiriman pesan cron (contoh: <code>-100123456789</code>):"
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_ch_whatsapp"}, func(c tele.Context) error {
		sess, ok := a.cronUI.GetSession(c.Sender().ID)
		if !ok {
			return a.cronUI.StartCronWizard(c)
		}
		sess.TargetChannel = "whatsapp"
		a.cronUI.SetSessionStep(c.Sender().ID, CronStepChatID)
		text := "💬 <b>MASUKKAN NOMOR / GROUP JID TARGET WHATSAPP</b>\n\nKirimkan nomor WA atau JID tujuan (contoh: <code>628123456789@s.whatsapp.net</code>):"
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_sc_hourly"}, func(c tele.Context) error {
		sess, ok := a.cronUI.GetSession(c.Sender().ID)
		if !ok {
			return a.cronUI.StartCronWizard(c)
		}
		sess.CronExpr = "0 * * * *"
		return a.cronUI.PromptCronPrompt(c, sess)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_sc_morning"}, func(c tele.Context) error {
		sess, ok := a.cronUI.GetSession(c.Sender().ID)
		if !ok {
			return a.cronUI.StartCronWizard(c)
		}
		sess.CronExpr = "0 7 * * *"
		return a.cronUI.PromptCronPrompt(c, sess)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_sc_evening"}, func(c tele.Context) error {
		sess, ok := a.cronUI.GetSession(c.Sender().ID)
		if !ok {
			return a.cronUI.StartCronWizard(c)
		}
		sess.CronExpr = "0 17 * * *"
		return a.cronUI.PromptCronPrompt(c, sess)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_sc_weekly"}, func(c tele.Context) error {
		sess, ok := a.cronUI.GetSession(c.Sender().ID)
		if !ok {
			return a.cronUI.StartCronWizard(c)
		}
		sess.CronExpr = "0 8 * * 1"
		return a.cronUI.PromptCronPrompt(c, sess)
	})
	a.bot.Handle(&tele.Btn{Unique: "cron_sc_custom"}, func(c tele.Context) error {
		if _, ok := a.cronUI.GetSession(c.Sender().ID); !ok {
			return a.cronUI.StartCronWizard(c)
		}
		a.cronUI.SetSessionStep(c.Sender().ID, CronStepCustomCron)
		text := "✏️ <b>MASUKKAN CRON EXPRESSION KHUSUS</b>\n\nKirimkan ekspresi cron standar 5 kolom (contoh: <code>*/30 * * * *</code> atau <code>0 9 * * 1-5</code>):"
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", "cron_wiz_cancel")
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})

	// MD Wizard Callbacks
	a.bot.Handle(&tele.Btn{Unique: "md_wiz_start"}, a.mdUI.StartMDWizard)
	a.bot.Handle(&tele.Btn{Unique: "md_reload_all"}, func(c tele.Context) error {
		a.mdLoader.Reload()
		return c.Reply("🔄 Seluruh cache file <code>.md</code> berhasil dimuat ulang!", tele.ModeHTML)
	})

	// Dynamic Callback Dispatcher for Custom Prefix Buttons
	a.bot.Handle(tele.OnCallback, func(c tele.Context) error {
		cb := c.Callback()
		if cb == nil {
			return nil
		}
		data := strings.TrimPrefix(cb.Data, "\f")

		if strings.HasPrefix(data, "cancel_task") {
			_ = c.Respond(&tele.CallbackResponse{Text: "Membatalkan proses AI..."})
			return a.handleStop(c)
		}

		// Provider Edit Callbacks
		if strings.HasPrefix(data, "wiz_ed_pick_") {
			provID := strings.TrimPrefix(data, "wiz_ed_pick_")
			return a.wizard.StartEditWizard(c, provID)
		}
		if strings.HasPrefix(data, "wiz_ed_del_yes_") {
			provID := strings.TrimPrefix(data, "wiz_ed_del_yes_")
			return a.wizard.HandleEditDeleteConfirm(c, provID)
		}

		// Combo Edit Callbacks
		if strings.HasPrefix(data, "cwiz_ed_pick_") {
			comboName := strings.TrimPrefix(data, "cwiz_ed_pick_")
			return a.comboWizard.StartEditWizard(c, comboName)
		}
		if strings.HasPrefix(data, "cwiz_prov_") {
			provID := strings.TrimPrefix(data, "cwiz_prov_")
			return a.comboWizard.HandleProviderSelect(c, provID)
		}
		if strings.HasPrefix(data, "cwiz_ed_prov_") {
			provID := strings.TrimPrefix(data, "cwiz_ed_prov_")
			return a.comboWizard.HandleEditAddTargetProvSelect(c, provID)
		}
		if strings.HasPrefix(data, "cwiz_ed_del_yes_") {
			comboName := strings.TrimPrefix(data, "cwiz_ed_del_yes_")
			return a.comboWizard.HandleEditDeleteConfirm(c, comboName)
		}

		// Limits Callbacks
		if strings.HasPrefix(data, "lim_sc_ch_") {
			chID := strings.TrimPrefix(data, "lim_sc_ch_")
			return a.limitsUI.RenderScopeLimitsDashboard(c, "channel", chID)
		}
		if strings.HasPrefix(data, "lim_mod_set_") {
			modVal := strings.TrimPrefix(data, "lim_mod_set_")
			if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
				pol, _ := a.db.GetPolicy(sess.Scope, sess.ScopeID)
				if pol == nil {
					pol = &storage.PolicyRecord{Scope: sess.Scope, ScopeID: sess.ScopeID}
				}
				pol.ModelOverride = modVal
				_ = a.db.SavePolicy(pol)
				return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
			}
		}

		// Channel Callbacks
		if strings.HasPrefix(data, "chan_ed_pick_") {
			chID := strings.TrimPrefix(data, "chan_ed_pick_")
			ch, err := a.db.GetChannel(chID)
			if err != nil || ch == nil {
				return c.Reply("❌ Channel tidak ditemukan.")
			}
			return a.channelUI.RenderChannelDashboard(c, ch)
		}
		if strings.HasPrefix(data, "chan_tgl_") {
			chID := strings.TrimPrefix(data, "chan_tgl_")
			ch, err := a.db.GetChannel(chID)
			if err != nil || ch == nil {
				return c.Reply("❌ Channel tidak ditemukan.")
			}
			ch.IsActive = !ch.IsActive
			_ = a.db.SaveChannel(ch)
			return a.channelUI.RenderChannelDashboard(c, ch)
		}
		if strings.HasPrefix(data, "chan_ed_tok_") {
			chID := strings.TrimPrefix(data, "chan_ed_tok_")
			ch, err := a.db.GetChannel(chID)
			if err != nil || ch == nil {
				return c.Reply("❌ Channel tidak ditemukan.")
			}
			a.channelUI.SetSessionStep(c.Sender().ID, chID, ChannelStepEditIdentifier)
			text := fmt.Sprintf("🔑 <b>GANTI TOKEN / ENDPOINT (%s)</b>\n\nKirimkan Token Telegram baru atau Webhook URL WhatsApp baru:", html.EscapeString(ch.Name))
			menu := &tele.ReplyMarkup{}
			btnCancel := menu.Data("❌ Batal", fmt.Sprintf("chan_ed_pick_%s", ch.ID))
			menu.Inline(menu.Row(btnCancel))
			return c.EditOrSend(text, menu, tele.ModeHTML)
		}
		if strings.HasPrefix(data, "chan_del_") {
			chID := strings.TrimPrefix(data, "chan_del_")
			_ = a.db.DeleteChannel(chID)
			return a.channelUI.StartChannelWizard(c)
		}

		// Tool Perms Matrix Callbacks
		if strings.HasPrefix(data, "tool_wiz_ch_") {
			chID := strings.TrimPrefix(data, "tool_wiz_ch_")
			return a.channelUI.StartToolWizard(c, chID)
		}
		if strings.HasPrefix(data, "tperm_") {
			raw := strings.TrimPrefix(data, "tperm_")
			parts := strings.SplitN(raw, "_", 2)
			if len(parts) == 2 {
				return a.channelUI.HandleToggleToolPerm(c, parts[0], parts[1])
			}
		}

		// Cron Callbacks
		if strings.HasPrefix(data, "cron_run_") {
			cronID := strings.TrimPrefix(data, "cron_run_")
			if err := a.scheduler.RunNow(cronID); err != nil {
				return c.Reply(fmt.Sprintf("❌ Gagal: %v", err))
			}
			return c.Reply(fmt.Sprintf("🚀 Cron job <code>%s</code> sedang dieksekusi sekarang!", html.EscapeString(cronID)), tele.ModeHTML)
		}
		if strings.HasPrefix(data, "cron_tgl_") {
			cronID := strings.TrimPrefix(data, "cron_tgl_")
			jobs, _ := a.db.ListCronJobs()
			for _, j := range jobs {
				if j.ID == cronID {
					jCopy := j
					jCopy.IsActive = !jCopy.IsActive
					_ = a.db.SaveCronJob(&jCopy)
					_ = a.scheduler.Reload()
					break
				}
			}
			return c.EditOrSend(a.cronUI.RenderCronList(), a.cronUI.CronKeyboard(), tele.ModeHTML)
		}
		if strings.HasPrefix(data, "cron_del_") {
			cronID := strings.TrimPrefix(data, "cron_del_")
			_ = a.db.DeleteCronJob(cronID)
			_ = a.scheduler.Reload()
			return c.EditOrSend(a.cronUI.RenderCronList(), a.cronUI.CronKeyboard(), tele.ModeHTML)
		}

		// MD Callbacks
		if strings.HasPrefix(data, "md_pick_") {
			fname := strings.TrimPrefix(data, "md_pick_")
			return a.mdUI.RenderMDFileDashboard(c, fname)
		}
		if strings.HasPrefix(data, "md_view_full_") {
			fname := strings.TrimPrefix(data, "md_view_full_")
			return a.mdUI.RenderFullFile(c, fname)
		}
		if strings.HasPrefix(data, "md_edit_prompt_") {
			fname := strings.TrimPrefix(data, "md_edit_prompt_")
			return a.mdUI.PromptEditContent(c, fname)
		}
		if strings.HasPrefix(data, "md_app_prompt_") {
			fname := strings.TrimPrefix(data, "md_app_prompt_")
			return a.mdUI.PromptAppendContent(c, fname)
		}

		return nil
	})

	// Channel & Tool Commands
	a.bot.Handle("/channelwizard", a.channelUI.StartChannelWizard)
	a.bot.Handle("/addchannel", a.channelUI.HandleAddChannel)
	a.bot.Handle("/tools", func(c tele.Context) error {
		return c.Reply(a.channelUI.RenderToolsList(), tele.ModeHTML)
	})
	a.bot.Handle("/toolwizard", func(c tele.Context) error {
		return a.channelUI.StartToolWizard(c, "")
	})
	a.bot.Handle("/toolperms", a.channelUI.HandleToolPerms)

	// MD Commands
	a.bot.Handle("/md", func(c tele.Context) error {
		return c.Reply(a.mdUI.RenderMDList(), a.mdUI.MDMenuKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle("/mdwizard", a.mdUI.StartMDWizard)
	a.bot.Handle("/viewmd", a.mdUI.HandleViewMD)
	a.bot.Handle("/editmd", a.mdUI.HandleEditMD)
	a.bot.Handle("/reloadmd", func(c tele.Context) error {
		a.mdLoader.Reload()
		return c.Reply("🔄 Seluruh cache file <code>.md</code> berhasil dimuat ulang!", tele.ModeHTML)
	})

	// Cron Commands
	a.bot.Handle("/cron", func(c tele.Context) error {
		return c.Reply(a.cronUI.RenderCronList(), a.cronUI.CronKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle("/cronwizard", a.cronUI.StartCronWizard)
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

	// Unified Text Message Handler (Wizard State Interceptors + Direct Chat)
	a.bot.Handle(tele.OnText, func(c tele.Context) error {
		// 1. Check Provider Wizard dialog
		if handled, err := a.wizard.HandleTextMessage(c); handled {
			return err
		}

		// 2. Check Combo Wizard dialog
		if handled, err := a.comboWizard.HandleTextMessage(c); handled {
			return err
		}

		// 3. Check Limits Wizard dialog
		if handled, err := a.limitsUI.HandleTextMessage(c); handled {
			return err
		}

		// 4. Check Channel Wizard dialog
		if handled, err := a.channelUI.HandleTextMessage(c); handled {
			return err
		}

		// 5. Check Cron Wizard dialog
		if handled, err := a.cronUI.HandleTextMessage(c); handled {
			return err
		}

		// 6. Check MD Wizard dialog
		if handled, err := a.mdUI.HandleTextMessage(c); handled {
			return err
		}

		// 7. Direct Chat with Assistant from Admin PM
		msg := c.Message().Text
		if msg == "" || msg[0] == '/' {
			return nil
		}

		_ = c.Notify(tele.Typing)
		cancelMenu := &tele.ReplyMarkup{}
		cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
		cancelMenu.Inline(cancelMenu.Row(cancelBtn))

		thinkingMsg, _ := a.bot.Reply(c.Message(), "🤔 <i>Sedang berpikir...</i>", tele.ModeHTML, cancelMenu)

		stopUpdater, onProgressStatus := startAdminProgressiveThinking(a.bot, thinkingMsg)
		defer stopUpdater()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		a.activeTasks.Store(c.Chat().ID, cancel)
		defer func() {
			a.activeTasks.Delete(c.Chat().ID)
			cancel()
		}()

		resp, err := a.orchestrator.ProcessMessage(ctx, agent.UserRequest{
			ChannelType: "telegram_admin",
			ChannelID:   "admin",
			ChannelName: "Telegram Admin PM",
			ChatID:      fmt.Sprintf("%d", c.Chat().ID),
			UserID:      fmt.Sprintf("%d", c.Sender().ID),
			UserName:    c.Sender().Username,
			UserPrompt:  msg,
			OnProgress: func(status string) {
				onProgressStatus(status)
			},
		})

		stopUpdater()

		if err != nil {
			if ctx.Err() == context.Canceled {
				text := "🛑 <b>PROSES DIBATALKAN</b>\n\nRespon AI berhasil dihentikan atas permintaan pengguna."
				if thinkingMsg != nil {
					_, _ = a.bot.Edit(thinkingMsg, text, tele.ModeHTML)
					return nil
				}
				return c.Reply(text, tele.ModeHTML)
			}

			friendlyErr := agent.FormatUserFriendlyError(err)
			if thinkingMsg != nil {
				_, _ = a.bot.Edit(thinkingMsg, friendlyErr, tele.ModeHTML)
				return nil
			}
			return c.Reply(friendlyErr, tele.ModeHTML)
		}

		return sendOrEditSplitMessage(c, thinkingMsg, resp.Text, resp.MediaFiles...)
	})

	// Register bot command autocomplete in Telegram UI
	a.registerCommands()
}

func (a *AdminBot) registerCommands() {
	adminCommands := []tele.Command{
		{Text: "menu", Description: "Buka dashboard Control Plane interaktif"},
		{Text: "status", Description: "Cek status server, resource & AI engine"},
		{Text: "new", Description: "Mulai sesi/konteks percakapan baru"},
		{Text: "stop", Description: "Hentikan respon AI atau wizard yang aktif"},
		{Text: "help", Description: "Panduan lengkap perintah bot"},
		{Text: "providers", Description: "Kelola AI providers & API keys"},
		{Text: "combos", Description: "Kelola model fallback combos"},
		{Text: "proxies", Description: "Kelola proxy upstream pool"},
		{Text: "tokensaver", Description: "Konfigurasi token saver & compression"},
		{Text: "limits", Description: "Kelola batas token, upload & footer"},
		{Text: "channels", Description: "Kelola bot Telegram & WhatsApp"},
		{Text: "tools", Description: "Daftar tool AI & perizinan channel"},
		{Text: "cron", Description: "Jadwal otomatisasi & trigger cron"},
		{Text: "memory", Description: "Lihat memori profil & SOP bot"},
		{Text: "stats", Description: "Statistik token & estimasi biaya"},
		{Text: "logs", Description: "Lihat 10 aktivitas request terakhir"},
		{Text: "backup", Description: "Unduh file backup SQLite & Markdown"},
		{Text: "update", Description: "Cek & pasang update binary dari GitHub"},
	}

	if err := a.bot.SetCommands(adminCommands); err != nil {
		log.Printf("⚠️ [Admin-TG] Gagal mendaftarkan menu autocomplete bot: %v", err)
	} else {
		log.Printf("✅ [Admin-TG] %d perintah bot berhasil didaftarkan ke auto-complete Telegram.", len(adminCommands))
	}
}

func (a *AdminBot) handleStatus(c tele.Context) error {
	return c.Send(a.RenderStatusSummary(c), BackToMenuKeyboard(), tele.ModeHTML)
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

func (a *AdminBot) handleMenu(c tele.Context) error {
	text := "👋 <b>SELAMAT DATANG DI GOASSISTANT CONTROL PLANE</b>\n\n" +
		"Sistem AI Assistant berbasis <b>Pure Golang</b> tanpa web UI. Anda dapat mengatur seluruh komponen sistem langsung melalui tombol interaktif di bawah ini:"
	return c.Send(text, MainMenuKeyboard(), tele.ModeHTML)
}

func sendSplitMessage(c tele.Context, text string) error {
	return sendOrEditSplitMessage(c, nil, text)
}

func sendOrEditSplitMessage(c tele.Context, thinkingMsg *tele.Message, text string, mediaFiles ...agent.MediaAttachment) error {
	if strings.TrimSpace(text) == "" {
		text = "(Tidak ada respon dari model)"
	}

	chunks := splitText(text, 4000)
	if len(chunks) > 0 {
		if thinkingMsg != nil {
			_, err := c.Bot().Edit(thinkingMsg, chunks[0])
			if err != nil {
				_ = c.Reply(chunks[0])
			}
			for _, chunk := range chunks[1:] {
				if err := c.Reply(chunk); err != nil {
					_ = err
				}
			}
		} else {
			for _, chunk := range chunks {
				if err := c.Reply(chunk); err != nil {
					_ = err
				}
			}
		}
	}

	// Dispatch media attachments
	for _, mf := range mediaFiles {
		ext := strings.ToLower(filepath.Ext(mf.FilePath))
		isPhoto := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"

		if isPhoto {
			photo := &tele.Photo{
				File:    tele.FromDisk(mf.FilePath),
				Caption: mf.Caption,
			}
			_ = c.Send(photo)
		} else {
			doc := &tele.Document{
				File:     tele.FromDisk(mf.FilePath),
				Caption:  mf.Caption,
				FileName: filepath.Base(mf.FilePath),
			}
			_ = c.Send(doc)
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
		"🎛️ <b>Navigasi, Konteks & Status:</b>\n" +
		"• <code>/menu</code> - Buka dashboard tombol interaktif utama\n" +
		"• <code>/status</code> - Cek status operasional, resource & runtime AI\n" +
		"• <code>/new</code> (atau <code>/reset</code>) - Mulai sesi baru & reset riwayat konteks\n" +
		"• <code>/stop</code> (atau <code>/cancel</code>) - Hentikan respon AI atau batalkan wizard\n" +
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

// startAdminProgressiveThinking periodically updates the thinking indicator with elapsed time & dynamic messages
func startAdminProgressiveThinking(bot *tele.Bot, targetMsg *tele.Message) (stopFunc func(), updateStatus func(string)) {
	if targetMsg == nil {
		return func() {}, func(string) {}
	}

	cancelMenu := &tele.ReplyMarkup{}
	cancelBtn := cancelMenu.Data("🛑 Batalkan", "cancel_task")
	cancelMenu.Inline(cancelMenu.Row(cancelBtn))

	var mu sync.Mutex
	customStatus := ""
	stopped := false
	doneChan := make(chan struct{})
	startTime := time.Now()

	updateStatus = func(status string) {
		mu.Lock()
		customStatus = status
		mu.Unlock()
		if targetMsg != nil {
			_, _ = bot.Edit(targetMsg, status, tele.ModeHTML, cancelMenu)
		}
	}

	stopFunc = func() {
		mu.Lock()
		if stopped {
			mu.Unlock()
			return
		}
		stopped = true
		mu.Unlock()
		close(doneChan)
	}

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-doneChan:
				return
			case <-ticker.C:
				mu.Lock()
				if stopped {
					mu.Unlock()
					return
				}
				elapsedSec := int(time.Since(startTime).Seconds())
				var text string
				if customStatus != "" {
					text = fmt.Sprintf("%s <i>(%dd)</i>", customStatus, elapsedSec)
				} else {
					switch {
					case elapsedSec < 6:
						text = fmt.Sprintf("⚡ <i>Masih menganalisis pertanyaan & konteks... (%dd)</i>", elapsedSec)
					case elapsedSec < 22:
						text = fmt.Sprintf("🔍 <i>Sedang memproses instruksi secara mendalam... (%dd)</i>", elapsedSec)
					default:
						text = fmt.Sprintf("⏳ <i>Hampir selesai, memvalidasi & menyusun format output... (%dd)</i>", elapsedSec)

					}
				}
				mu.Unlock()

				if targetMsg != nil {
					_, _ = bot.Edit(targetMsg, text, tele.ModeHTML, cancelMenu)
				}
			}
		}
	}()

	return stopFunc, updateStatus
}
