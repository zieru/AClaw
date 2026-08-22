package admin

import (
	"fmt"
	"html"
	"log"

	"goassistant/internal/storage"
	"goassistant/internal/tokensaver"
	tele "gopkg.in/telebot.v3"
)

func (a *AdminBot) registerRoutes() {
	// 1. Admin Authorization Middleware
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

	// 2. Main Dashboard & Navigation Commands
	a.bot.Handle("/start", a.handleMenu)
	a.bot.Handle("/menu", a.handleMenu)
	a.bot.Handle("/help", a.handleHelp)
	a.bot.Handle("/status", a.handleStatus)
	a.bot.Handle("/new", a.handleNew)
	a.bot.Handle("/reset", a.handleNew)
	a.bot.Handle("/clear", a.handleNew)
	a.bot.Handle("/stop", a.handleStop)
	a.bot.Handle("/cancel", a.handleStop)

	// Main Menu Inline Callbacks
	a.bot.Handle(&tele.Btn{Unique: "menu_main"}, a.handleMenu)
	a.bot.Handle(&tele.Btn{Unique: "menu_status"}, a.handleStatus)
	a.bot.Handle(&tele.Btn{Unique: "btn_refresh_status"}, func(c tele.Context) error {
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Memperbarui status..."})
		return c.EditOrSend(a.RenderStatusSummary(c), a.StatusKeyboard(), tele.ModeHTML)
	})

	a.bot.Handle(&tele.Btn{Unique: "menu_model"}, func(c tele.Context) error {
		return c.EditOrSend(a.modelUI.RenderModelDashboard(c), a.modelUI.ModelMenuKeyboard(c.Sender().ID), tele.ModeHTML)
	})
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
		return c.EditOrSend(a.auditUI.RenderStatsSummary(), a.auditUI.StatsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_logs"}, a.auditUI.HandleLogs)
	a.bot.Handle(&tele.Btn{Unique: "menu_tools"}, func(c tele.Context) error {
		return c.EditOrSend(a.channelUI.RenderToolsList(), a.channelUI.ChannelsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "menu_tavily"}, a.tavilyUI.HandleMenu)
	a.bot.Handle(&tele.Btn{Unique: "tav_toggle_depth"}, a.tavilyUI.HandleToggleDepth)
	a.bot.Handle(&tele.Btn{Unique: "tav_cycle_max"}, a.tavilyUI.HandleCycleMaxResults)
	a.bot.Handle(&tele.Btn{Unique: "tav_set_key"}, a.tavilyUI.PromptSetKey)
	a.bot.Handle(&tele.Btn{Unique: "tav_test_query"}, a.tavilyUI.PromptTestQuery)
	a.bot.Handle(&tele.Btn{Unique: "tav_cancel"}, func(c tele.Context) error {
		a.tavilyUI.CancelSession(c.Sender().ID)
		txt, kb := a.tavilyUI.RenderTavilyDashboard()
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	})
	a.bot.Handle("/tavily", a.tavilyUI.HandleMenu)
	a.bot.Handle(&tele.Btn{Unique: "menu_backup"}, a.handleBackup)
	a.bot.Handle(&tele.Btn{Unique: "menu_update"}, a.updateUI.HandleCheckUpdate)
	a.bot.Handle(&tele.Btn{Unique: "btn_check_update"}, a.updateUI.HandleCheckUpdate)
	a.bot.Handle(&tele.Btn{Unique: "btn_do_update"}, a.updateUI.HandleDoUpdate)
	a.bot.Handle(&tele.Btn{Unique: "menu_help"}, a.handleHelp)

	// Global Footer Settings Callbacks
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

	// Proxy Pool Commands & Callbacks
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
	a.bot.Handle(&tele.Btn{Unique: "btn_test_proxies"}, a.proxyUI.HandleTestProxies)
	a.bot.Handle(&tele.Btn{Unique: "btn_proxy_groups"}, a.proxyUI.HandleListGroups)
	a.bot.Handle(&tele.Btn{Unique: "btn_toggle_proxy"}, a.proxyUI.HandleToggleProxy)

	// Token Saver Commands & Callbacks
	a.bot.Handle("/tokensaver", a.tokenSaverUI.HandleTokenSaverStatus)
	a.bot.Handle("/tokensaverpreset", a.tokenSaverUI.HandleSetPresetCommand)
	a.bot.Handle("/tokensaverengine", a.tokenSaverUI.HandleSetEngineCommand)
	a.bot.Handle("/tokensaverstyle", a.tokenSaverUI.HandleSetStyleCommand)
	a.bot.Handle("/tokensaverdial", a.tokenSaverUI.HandleSetDialCommand)
	a.bot.Handle("/tokensavermode", a.tokenSaverUI.HandleSetMode)
	a.bot.Handle("/settokensavertarget", a.tokenSaverUI.HandleSetMode)
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_lite"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "lite") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_standard"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "standard") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_aggressive"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "aggressive") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_rtk"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "rtk") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_stacked"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "stacked") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_ultra"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "ultra") })
	a.bot.Handle(&tele.Btn{Unique: "ts_preset_off"}, func(c tele.Context) error { return a.tokenSaverUI.HandlePresetCallback(c, "off") })
	a.bot.Handle(&tele.Btn{Unique: "ts_refresh"}, a.tokenSaverUI.HandleTokenSaverStatus)
	a.bot.Handle(&tele.Btn{Unique: "ts_toggle_dial"}, a.tokenSaverUI.HandleToggleDialCallback)
	a.bot.Handle(&tele.Btn{Unique: "ts_style_terse"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "terse_prose") })
	a.bot.Handle(&tele.Btn{Unique: "ts_style_lesscode"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "less_code") })
	a.bot.Handle(&tele.Btn{Unique: "ts_style_indo"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "id_ringkas") })
	a.bot.Handle(&tele.Btn{Unique: "ts_style_none"}, func(c tele.Context) error { return a.tokenSaverUI.HandleStyleCallback(c, "none") })
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
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_opencode"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "opencode") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_openai"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "openai") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_deepseek"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "deepseek") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_groq"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "groq") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_gemini"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "gemini") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_gemini_web"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "gemini_web") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_anthropic"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "anthropic") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_ollama"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "ollama") })
	a.bot.Handle(&tele.Btn{Unique: "wiz_type_custom"}, func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "custom") })
	a.bot.Handle("/gemini_login", func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "gemini_web") })
	a.bot.Handle("/opencode", func(c tele.Context) error { return a.wizard.HandleTypeSelect(c, "opencode") })

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

	// Model & Combo Selection Commands
	a.bot.Handle("/model", a.modelUI.HandleModelCommand)
	a.bot.Handle("/models", a.modelUI.HandleModelCommand)
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
	a.bot.Handle("/addkey", a.providerUI.HandleAddKey)
	a.bot.Handle("/setkeys", a.providerUI.HandleSetKeys)
	a.bot.Handle("/delkey", a.providerUI.HandleDelKey)
	a.bot.Handle("/keystrategy", a.providerUI.HandleSetKeyStrategy)
	a.bot.Handle("/setmodels", a.providerUI.HandleSetModels)
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

	// Limits Dashboard & Wizard Callbacks
	a.bot.Handle(&tele.Btn{Unique: "lim_wiz_start"}, a.limitsUI.StartLimitsWizard)
	a.bot.Handle(&tele.Btn{Unique: "lim_sc_global"}, func(c tele.Context) error {
		return a.limitsUI.RenderScopeLimitsDashboard(c, "global", "system")
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_sc_chan_menu"}, a.limitsUI.RenderChannelPickMenu)
	a.bot.Handle(&tele.Btn{Unique: "lim_sc_chat_prompt"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepScopeChatID)
		text := "💬 <b>MASUKKAN CHAT / GROUP ID</b>\n\nKirimkan ID chat atau grup Telegram/WhatsApp (contoh: <code>-100123456789</code>):"
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", "lim_wiz_start")
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_back_dash"}, func(c tele.Context) error {
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok && sess.Scope != "" {
			return a.limitsUI.RenderScopeLimitsDashboard(c, sess.Scope, sess.ScopeID)
		}
		return c.EditOrSend(a.limitsUI.RenderLimitsSummary(), a.limitsUI.LimitsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle(&tele.Btn{Unique: "lim_set_tok_max_menu"}, func(c tele.Context) error {
		sess, ok := a.limitsUI.GetSession(c.Sender().ID)
		if !ok || sess.Scope == "" {
			return a.limitsUI.StartLimitsWizard(c)
		}
		text := fmt.Sprintf("📊 <b>ATUR MAKSIMAL TOKEN RESIDUAL (<code>%s:%s</code>)</b>\n\nPilih batas token maksimum:", html.EscapeString(sess.Scope), html.EscapeString(sess.ScopeID))
		menu := &tele.ReplyMarkup{}
		b1k := menu.Data("1,000", "lim_ltok_1000")
		b2k := menu.Data("2,000", "lim_ltok_2000")
		b4k := menu.Data("4,000", "lim_ltok_4000")
		b8k := menu.Data("8,000", "lim_ltok_8000")
		bCust := menu.Data("✏️ Custom", "lim_ltok_cust")
		bBack := menu.Data("⬅️ Kembali", "lim_back_dash")
		menu.Inline(menu.Row(b1k, b2k), menu.Row(b4k, b8k, bCust), menu.Row(bBack))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	})
	for _, tok := range []int{1000, 2000, 4000, 8000} {
		val := tok
		a.bot.Handle(&tele.Btn{Unique: fmt.Sprintf("lim_ltok_%d", val)}, func(c tele.Context) error {
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
	a.bot.Handle(&tele.Btn{Unique: "lim_ltok_cust"}, func(c tele.Context) error {
		a.limitsUI.SetSessionStep(c.Sender().ID, LimitsStepCustomTokens)
		return c.EditOrSend("📊 <b>MASUKKAN MAKSIMAL TOKEN</b>\n\nKirimkan angka batas token (contoh: <code>3500</code>):", tele.ModeHTML)
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

	// Channel & Tool Commands
	a.bot.Handle("/channelwizard", a.channelUI.StartChannelWizard)
	a.bot.Handle("/addchannel", a.channelUI.HandleAddChannel)
	a.bot.Handle("/channels", func(c tele.Context) error {
		return c.Reply(a.channelUI.RenderChannelsList(), a.channelUI.ChannelsKeyboard(), tele.ModeHTML)
	})
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
		return c.Reply(a.auditUI.RenderStatsSummary(), a.auditUI.StatsKeyboard(), tele.ModeHTML)
	})
	a.bot.Handle("/logs", a.auditUI.HandleLogs)
	a.bot.Handle("/viewlog", a.auditUI.HandleViewLog)
	a.bot.Handle("/lastlog", a.auditUI.HandleViewLog)
	a.bot.Handle("/exportlogs", a.auditUI.HandleExportLogs)
	a.bot.Handle("/backup", a.handleBackup)

	// 3. Register Dispatchers
	a.registerDispatcher()

	// 4. Register Autocomplete Commands
	a.registerCommands()
}

func (a *AdminBot) registerCommands() {
	adminCommands := []tele.Command{
		{Text: "menu", Description: "Buka dashboard Control Plane interaktif"},
		{Text: "status", Description: "Cek status server, resource & AI engine"},
		{Text: "new", Description: "Mulai sesi/konteks percakapan baru"},
		{Text: "stop", Description: "Hentikan respon AI atau wizard yang aktif"},
		{Text: "help", Description: "Panduan lengkap perintah bot"},
		{Text: "model", Description: "Ganti model AI / fallback combo"},
		{Text: "providers", Description: "Kelola AI providers & API keys"},
		{Text: "combos", Description: "Kelola model fallback combos"},
		{Text: "proxies", Description: "Kelola proxy upstream pool"},
		{Text: "tokensaver", Description: "Konfigurasi token saver & compression"},
		{Text: "limits", Description: "Kelola batas token, upload & footer"},
		{Text: "channels", Description: "Kelola bot Telegram & WhatsApp"},
		{Text: "tavily", Description: "Konfigurasi Tavily AI search & testing"},
		{Text: "cron", Description: "Jadwal otomatisasi & trigger cron"},
		{Text: "memory", Description: "Lihat memori profil & SOP bot"},
		{Text: "stats", Description: "Statistik token & estimasi biaya"},
		{Text: "logs", Description: "Lihat log aktivitas request & payload"},
		{Text: "backup", Description: "Unduh file backup SQLite & Markdown"},
		{Text: "update", Description: "Cek & pasang update binary dari GitHub"},
	}

	if err := a.bot.SetCommands(adminCommands); err != nil {
		log.Printf("⚠️ [Admin-TG] Gagal mendaftarkan menu autocomplete bot: %v", err)
	} else {
		log.Printf("✅ [Admin-TG] %d perintah bot berhasil didaftarkan ke auto-complete Telegram.", len(adminCommands))
	}
}
