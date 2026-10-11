package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/channel/whatsapp"
	tele "gopkg.in/telebot.v3"
)

func (a *AdminBot) registerDispatcher() {
	// 1. Photo / Image Upload
	a.bot.Handle(tele.OnPhoto, a.handleDirectPhoto)

	// 2. Document / File Upload
	a.bot.Handle(tele.OnDocument, a.handleDirectDocument)

	// 3. Unified Text Interceptor (Wizard active states + Direct Chat)
	a.bot.Handle(tele.OnText, a.handleTextMessage)

	// 4. Unified Dynamic Callback Dispatcher
	a.bot.Handle(tele.OnCallback, a.handleDynamicCallback)
}

func (a *AdminBot) handleTextMessage(c tele.Context) error {
	// 0. Intercept secure password prompt input before anything else (zero-leakage to AI)
	if a.promptManager != nil {
		if handled, err := a.promptManager.HandleTextMessage(c); handled {
			return err
		}
	}

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

	// 7. Check Model Selection dialog (Combos, Providers, Models)
	if handled, err := a.modelUI.HandleTextMessage(c); handled {
		return err
	}

	// 8b. Check Checkin Add User dialog
	if handled, err := a.checkinUI.HandleTextMessage(c); handled {
		return err
	}

	// 8c. Check Topic Wizard dialog (New topic title / Rename)
	if handled, err := a.topicUI.HandleTextMessage(c); handled {
		return err
	}

	// 8d. Check WebAdmin Port dialog
	if a.webAdminUI != nil {
		if handled, err := a.webAdminUI.HandleTextMessage(c); handled {
			return err
		}
	}

	// 8e. Check Web Search Key & Query dialog
	if a.searchUI != nil {
		if handled, err := a.searchUI.HandleTextMessage(c); handled {
			return err
		}
	}

	// 8f. Check Bifrost Proxy Input dialog
	if a.bifrostUI != nil {
		if a.bifrostUI.CheckInput(c) {
			return nil
		}
	}

	// 9. Direct Chat with Assistant from Admin PM
	msg := c.Message().Text
	if msg == "" || msg[0] == '/' {
		return nil
	}

	return a.handleDirectChat(c, msg)
}

func (a *AdminBot) handleDynamicCallback(c tele.Context) error {
	cb := c.Callback()
	if cb == nil {
		return nil
	}
	data := strings.TrimPrefix(cb.Data, "\f")

	// Topic Callbacks
	if strings.HasPrefix(data, "top_") {
		return a.topicUI.HandleCallback(c, data)
	}

	if strings.HasPrefix(data, "cancel_task") {
		_ = c.Respond(&tele.CallbackResponse{Text: "Membatalkan proses AI..."})
		return a.handleStop(c)
	}

	if strings.HasPrefix(data, "retry_admin_task") {
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Mencoba ulang..."})
		lastPrompt := a.orchestrator.GetLastPrompt("admin", fmt.Sprintf("%d", c.Chat().ID), fmt.Sprintf("%d", c.Sender().ID))
		if lastPrompt == "" {
			return c.Send("⚠️ Tidak ada pesan sebelumnya yang dapat dicoba lagi.")
		}
		return a.handleDirectChat(c, lastPrompt)
	}

	// Interactive Option Callbacks
	if strings.HasPrefix(data, "opt_") {
		return a.handleOptionCallback(c, data)
	}

	// Model Switcher Callbacks
	if data == "mod_main" || data == "mod_refresh" {
		return c.EditOrSend(a.modelUI.RenderModelDashboard(c), a.modelUI.ModelMenuKeyboard(c.Sender().ID), tele.ModeHTML)
	}
	if data == "mod_toggle_scope" {
		return a.modelUI.HandleToggleScopeCallback(c)
	}
	if data == "mod_set_default" {
		return a.modelUI.HandleSetDefaultCallback(c)
	}
	if data == "mod_menu_combos" {
		txt, kb := a.modelUI.RenderCombosList(c, 0)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "mod_c_prev_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "mod_c_prev_"), "%d", &page)
		txt, kb := a.modelUI.RenderCombosList(c, page)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "mod_c_next_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "mod_c_next_"), "%d", &page)
		txt, kb := a.modelUI.RenderCombosList(c, page)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if data == "mod_menu_providers" {
		txt, kb := a.modelUI.RenderProvidersList(c)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "mod_set_c_") {
		comboName := strings.TrimPrefix(data, "mod_set_c_")
		return a.modelUI.HandleSetComboCallback(c, comboName)
	}
	if strings.HasPrefix(data, "mod_prov_") {
		provName := strings.TrimPrefix(data, "mod_prov_")
		txt, kb := a.modelUI.RenderProviderModels(c, provName, 0)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "mod_p_prev_") {
		raw := strings.TrimPrefix(data, "mod_p_prev_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			provName := raw[:lastUnderscore]
			var page int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &page)
			txt, kb := a.modelUI.RenderProviderModels(c, provName, page)
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		}
	}
	if strings.HasPrefix(data, "mod_p_next_") {
		raw := strings.TrimPrefix(data, "mod_p_next_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			provName := raw[:lastUnderscore]
			var page int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &page)
			txt, kb := a.modelUI.RenderProviderModels(c, provName, page)
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		}
	}
	if strings.HasPrefix(data, "mod_set_prov_") {
		provName := strings.TrimPrefix(data, "mod_set_prov_")
		return a.modelUI.HandleSetProviderResilientCallback(c, provName)
	}
	if strings.HasPrefix(data, "mod_set_m_") {
		raw := strings.TrimPrefix(data, "mod_set_m_")
		parts := strings.Split(raw, "__")
		if len(parts) == 2 {
			provName := parts[0]
			var modelIdx int
			fmt.Sscanf(parts[1], "%d", &modelIdx)
			return a.modelUI.HandleSetModelCallback(c, provName, modelIdx)
		}
	}
	// Model Fallback (Vision & TTS) Callbacks
	if data == "mod_menu_fallback" {
		txt, kb := a.modelUI.RenderFallbackDashboard(c)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if data == "mod_fb_vis_provs" {
		txt, kb := a.modelUI.RenderFallbackProvidersList(c, "vis")
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if data == "mod_fb_tts_provs" {
		txt, kb := a.modelUI.RenderFallbackProvidersList(c, "tts")
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "mod_fb_prov_") {
		raw := strings.TrimPrefix(data, "mod_fb_prov_")
		parts := strings.SplitN(raw, "_", 2)
		if len(parts) == 2 {
			mode := parts[0]
			provName := parts[1]
			txt, kb := a.modelUI.RenderFallbackProviderModels(c, mode, provName, 0)
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		}
	}
	if strings.HasPrefix(data, "mod_fb_p_prev_") {
		raw := strings.TrimPrefix(data, "mod_fb_p_prev_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			var page int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &page)
			remaining := raw[:lastUnderscore]
			parts := strings.SplitN(remaining, "_", 2)
			if len(parts) == 2 {
				txt, kb := a.modelUI.RenderFallbackProviderModels(c, parts[0], parts[1], page)
				return c.EditOrSend(txt, kb, tele.ModeHTML)
			}
		}
	}
	if strings.HasPrefix(data, "mod_fb_p_next_") {
		raw := strings.TrimPrefix(data, "mod_fb_p_next_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			var page int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &page)
			remaining := raw[:lastUnderscore]
			parts := strings.SplitN(remaining, "_", 2)
			if len(parts) == 2 {
				txt, kb := a.modelUI.RenderFallbackProviderModels(c, parts[0], parts[1], page)
				return c.EditOrSend(txt, kb, tele.ModeHTML)
			}
		}
	}
	if strings.HasPrefix(data, "mod_fb_set_") {
		raw := strings.TrimPrefix(data, "mod_fb_set_")
		splitIdx := strings.Split(raw, "__")
		if len(splitIdx) == 2 {
			var modelIdx int
			fmt.Sscanf(splitIdx[1], "%d", &modelIdx)
			modeAndProv := strings.SplitN(splitIdx[0], "_", 2)
			if len(modeAndProv) == 2 {
				return a.modelUI.HandleSetFallbackModelCallback(c, modeAndProv[0], modeAndProv[1], modelIdx)
			}
		}
	}
	if data == "mod_fb_reset_vis" {
		return a.modelUI.HandleResetFallbackCallback(c, "vis")
	}
	if data == "mod_fb_reset_tts" {
		return a.modelUI.HandleResetFallbackCallback(c, "tts")
	}
	if data == "mod_noop" || data == "cwiz_noop" || data == "wiz_mod_noop" {
		return c.Respond(&tele.CallbackResponse{})
	}

	// Interactive Provider UI Callbacks
	if data == "prov_test_all" {
		return a.providerUI.HandleTestAllProviders(c)
	}
	if strings.HasPrefix(data, "prov_view_") {
		provID := strings.TrimPrefix(data, "prov_view_")
		p, err := a.db.GetProvider(provID)
		if err != nil || p == nil {
			return c.Reply("❌ Provider tidak ditemukan.")
		}
		txt, kb := a.providerUI.RenderProviderDashboard(p)
		return c.EditOrSend(txt, kb, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "prov_test_") {
		provID := strings.TrimPrefix(data, "prov_test_")
		return a.providerUI.HandleTestLatency(c, provID)
	}
	if strings.HasPrefix(data, "prov_tgl_") {
		provID := strings.TrimPrefix(data, "prov_tgl_")
		return a.providerUI.HandleToggleActive(c, provID)
	}

	// Interactive Log UI Callbacks
	if data == "btn_export_logs" {
		return a.auditUI.HandleExportLogs(c)
	}
	if strings.HasPrefix(data, "log_view_") {
		logID := strings.TrimPrefix(data, "log_view_")
		return a.auditUI.HandleViewLogByID(c, logID)
	}
	if strings.HasPrefix(data, "log_p_") {
		raw := strings.TrimPrefix(data, "log_p_")
		var page int
		var errFlag int
		fmt.Sscanf(raw, "%d_%d", &page, &errFlag)
		return a.auditUI.HandleLogsWithPage(c, page, errFlag == 1)
	}
	if strings.HasPrefix(data, "log_toggle_err_") {
		raw := strings.TrimPrefix(data, "log_toggle_err_")
		var page int
		var errFlag int
		if n, _ := fmt.Sscanf(raw, "%d_%d", &page, &errFlag); n == 2 {
			return a.auditUI.HandleLogsWithPage(c, page, errFlag == 1)
		}
		fmt.Sscanf(raw, "%d", &page)
		return a.auditUI.HandleLogsWithPage(c, page, true)
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
	if strings.HasPrefix(data, "wiz_mod_p_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "wiz_mod_p_"), "%d", &page)
		return a.wizard.HandleEditModelsPage(c, page)
	}
	if strings.HasPrefix(data, "wiz_mod_tog_") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(data, "wiz_mod_tog_"), "%d", &idx)
		return a.wizard.HandleEditToggleModel(c, idx)
	}

	// Combo Preferred Option Callbacks
	if data == "cwiz_pref_yes" {
		return a.comboWizard.HandlePreferredOption(c, true)
	}
	if data == "cwiz_pref_no" {
		return a.comboWizard.HandlePreferredOption(c, false)
	}
	if data == "cwiz_pref_back" {
		return a.comboWizard.HandlePreferredBack(c)
	}

	// Combo Edit Callbacks
	if strings.HasPrefix(data, "cwiz_ed_pick_") {
		comboName := strings.TrimPrefix(data, "cwiz_ed_pick_")
		return a.comboWizard.StartEditWizard(c, comboName)
	}
	if strings.HasPrefix(data, "cwiz_prov_p_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_prov_p_"), "%d", &page)
		return a.comboWizard.HandleProviderPage(c, page)
	}
	if strings.HasPrefix(data, "cwiz_prov_") {
		provID := strings.TrimPrefix(data, "cwiz_prov_")
		return a.comboWizard.HandleProviderSelect(c, provID)
	}
	if strings.HasPrefix(data, "cwiz_mod_p_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_mod_p_"), "%d", &page)
		return a.comboWizard.HandleModelPage(c, page)
	}
	if strings.HasPrefix(data, "cwiz_mod_") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_mod_"), "%d", &idx)
		return a.comboWizard.HandleModelSelect(c, idx)
	}
	if strings.HasPrefix(data, "cwiz_ed_prov_p_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_ed_prov_p_"), "%d", &page)
		return a.comboWizard.HandleEditAddTargetProvPage(c, page)
	}
	if strings.HasPrefix(data, "cwiz_ed_prov_") {
		provID := strings.TrimPrefix(data, "cwiz_ed_prov_")
		return a.comboWizard.HandleEditAddTargetProvSelect(c, provID)
	}
	if strings.HasPrefix(data, "cwiz_ed_mod_p_") {
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_ed_mod_p_"), "%d", &page)
		return a.comboWizard.HandleEditAddTargetModPage(c, page)
	}
	if strings.HasPrefix(data, "cwiz_ed_mod_") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_ed_mod_"), "%d", &idx)
		return a.comboWizard.HandleEditAddTargetModSelect(c, idx)
	}
	if strings.HasPrefix(data, "cwiz_ed_rem_") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_ed_rem_"), "%d", &idx)
		return a.comboWizard.HandleEditDelTargetExecute(c, idx)
	}
	if strings.HasPrefix(data, "cwiz_ed_mvup_") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_ed_mvup_"), "%d", &idx)
		return a.comboWizard.HandleEditReorderMove(c, idx, "up")
	}
	if strings.HasPrefix(data, "cwiz_ed_mvdn_") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(data, "cwiz_ed_mvdn_"), "%d", &idx)
		return a.comboWizard.HandleEditReorderMove(c, idx, "down")
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
	if data == "lim_set_footer_menu" {
		return a.limitsUI.RenderFooterMenu(c)
	}
	if data == "lim_set_stream_menu" {
		return a.limitsUI.RenderStreamMenu(c)
	}
	if data == "lim_set_upload_menu" {
		return a.limitsUI.RenderUploadMenu(c)
	}
	if data == "lim_set_tokens_menu" {
		return a.limitsUI.RenderTokensMenu(c)
	}
	if data == "lim_set_history_menu" {
		return a.limitsUI.RenderHistoryMenu(c)
	}
	if data == "lim_set_compact_menu" {
		return a.limitsUI.RenderCompactionMenu(c)
	}
	if data == "lim_set_model_menu" {
		return a.limitsUI.RenderModelMenu(c)
	}
	if data == "lim_set_timeout_api_menu" {
		return a.limitsUI.RenderTimeoutAPIMenu(c)
	}
	if data == "lim_set_timeout_handler_menu" {
		return a.limitsUI.RenderTimeoutHandlerMenu(c)
	}
	if data == "lim_set_audit_max_menu" {
		return a.limitsUI.RenderAuditMaxMenu(c)
	}
	if data == "lim_set_budget_menu" {
		return a.limitsUI.RenderBudgetMenu(c)
	}
	if data == "lim_do_rotate_audit" {
		return a.limitsUI.HandleRotateAudit(c)
	}
	if strings.HasPrefix(data, "lim_set_val_") {
		raw := strings.TrimPrefix(data, "lim_set_val_")
		firstUnderscore := strings.Index(raw, "_")
		if firstUnderscore != -1 {
			param := raw[:firstUnderscore]
			val := raw[firstUnderscore+1:]
			return a.limitsUI.HandleSetVal(c, param, val)
		}
	}
	if strings.HasPrefix(data, "lim_input_") {
		param := strings.TrimPrefix(data, "lim_input_")
		var step LimitsStep
		var prompt string
		switch param {
		case "upload":
			step = LimitsStepCustomUpload
			prompt = "📁 <b>MASUKKAN BATAS MAX UPLOAD (MB)</b>\n\nKirimkan angka MB positif (contoh: <code>25</code>):"
		case "tokens":
			step = LimitsStepCustomTokens
			prompt = "🪙 <b>MASUKKAN BATAS MAX OUTPUT TOKENS</b>\n\nKirimkan angka token positif (contoh: <code>4096</code>):"
		case "history":
			step = LimitsStepCustomHistory
			prompt = "💬 <b>MASUKKAN BATAS MAX HISTORY TURNS</b>\n\nKirimkan angka turn positif (contoh: <code>30</code>):"
		case "threshold":
			step = LimitsStepCustomThreshold
			prompt = "🗜️ <b>MASUKKAN AMBANG BATAS AUTO-COMPACTION</b>\n\nKirimkan angka turn threshold positif (contoh: <code>15</code>):"
		case "model":
			step = LimitsStepCustomModel
			prompt = "🤖 <b>MASUKKAN MODEL / COMBO OVERRIDE</b>\n\nKirimkan nama model atau combo (contoh: <code>gemini-2.5-flash</code> atau <code>combo_smart</code>):"
		case "timeapi":
			step = LimitsStepCustomTimeoutAPI
			prompt = "⏱️ <b>MASUKKAN TIMEOUT API CALL (DETIK)</b>\n\nKirimkan angka detik positif (contoh: <code>90</code>):"
		case "timehand":
			step = LimitsStepCustomTimeoutHandler
			prompt = "⏳ <b>MASUKKAN TIMEOUT HANDLER (DETIK)</b>\n\nKirimkan angka detik positif (contoh: <code>120</code>):"
		case "auditmax":
			step = LimitsStepCustomMaxAudit
			prompt = "📜 <b>MASUKKAN BATAS ROTASI AUDIT LOG</b>\n\nKirimkan angka baris log positif (contoh: <code>5000</code>):"
		case "budget":
			step = LimitsStepCustomBudget
			prompt = "💰 <b>MASUKKAN TOKEN BUDGET</b>\n\nKirimkan angka token budget positif (contoh: <code>10000</code>):"
		}
		if step != LimitsStepNone {
			a.limitsUI.SetSessionStep(c.Sender().ID, step)
			menu := &tele.ReplyMarkup{}
			btnCancel := menu.Data("❌ Batal", "lim_back_dash")
			menu.Inline(menu.Row(btnCancel))
			return c.EditOrSend(prompt, menu, tele.ModeHTML)
		}
	}
	if data == "lim_mod_menu" {
		return a.limitsUI.RenderModelMenu(c)
	}
	if data == "lim_mod_combos" {
		return a.limitsUI.RenderLimitCombosPicker(c)
	}
	if data == "lim_mod_provs" {
		return a.limitsUI.RenderLimitProvidersPicker(c)
	}
	if strings.HasPrefix(data, "lim_mod_prov_") {
		raw := strings.TrimPrefix(data, "lim_mod_prov_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			provName := raw[:lastUnderscore]
			var page int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &page)
			return a.limitsUI.RenderLimitProviderModelsPicker(c, provName, page)
		}
	}
	if strings.HasPrefix(data, "lim_mod_pick_") {
		raw := strings.TrimPrefix(data, "lim_mod_pick_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			provName := raw[:lastUnderscore]
			var modelIdx int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &modelIdx)
			return a.limitsUI.HandlePickProviderModel(c, provName, modelIdx)
		}
	}
	if strings.HasPrefix(data, "lim_mod_set_") {
		modVal := strings.TrimPrefix(data, "lim_mod_set_")
		if sess, ok := a.limitsUI.GetSession(c.Sender().ID); ok {
			pol := a.db.GetOrCreatePolicy(sess.Scope, sess.ScopeID)
			pol.ModelOverride = modVal
			_ = a.db.SavePolicy(pol)
			_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🔀 Combo '%s' aktif!", modVal)})
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
	if data == "chan_wiz_grp_y" || data == "chan_wiz_grp_n" {
		sess, ok := a.channelUI.GetSession(c.Sender().ID)
		if !ok || sess.Step != ChannelStepGrapariChoice {
			return a.channelUI.StartChannelWizard(c)
		}
		grapariOnly := (data == "chan_wiz_grp_y")
		return a.channelUI.FinalizeCreateChannel(c, sess, grapariOnly)
	}
	if strings.HasPrefix(data, "chan_tgl_grp_") {
		chID := strings.TrimPrefix(data, "chan_tgl_grp_")
		ch, err := a.db.GetChannel(chID)
		if err != nil || ch == nil {
			return c.Reply("❌ Channel tidak ditemukan.")
		}
		newVal := !ch.IsGrapariOnly()
		ch.SetGrapariOnly(newVal)
		_ = a.db.SaveChannel(ch)

		if newVal {
			for _, t := range a.toolRegistry.ListAll() {
				allowed := (t.Name() == "g3a_search_grapari_knowledge" || t.Name() == "search_telkomsel_web")
				_ = a.db.SetChannelToolPerm(ch.ID, t.Name(), allowed)
			}
		}

		statusMsg := "Mode Khusus GraPARI diaktifkan!"
		if !newVal {
			statusMsg = "Mode Khusus GraPARI dinonaktifkan."
		}
		_ = c.Respond(&tele.CallbackResponse{Text: statusMsg})
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
		text := fmt.Sprintf("🔑 <b>GANTI TOKEN (%s)</b>\n\nKirimkan Bot Token Telegram baru:", html.EscapeString(ch.Name))
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", fmt.Sprintf("chan_ed_pick_%s", ch.ID))
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "chan_del_") {
		chID := strings.TrimPrefix(data, "chan_del_")
		if mgr := whatsapp.GetManager(); mgr != nil {
			_ = mgr.DeleteAdapter(chID)
		}
		_ = a.db.DeleteChannel(chID)
		return a.channelUI.StartChannelWizard(c)
	}

	// WhatsApp Specific Callbacks
	if strings.HasPrefix(data, "chan_wa_qr_") {
		chID := strings.TrimPrefix(data, "chan_wa_qr_")
		return a.channelUI.GetWhatsAppUI().SendQRCodePhoto(c, chID)
	}
	if strings.HasPrefix(data, "chan_wa_paircode_prompt_") {
		chID := strings.TrimPrefix(data, "chan_wa_paircode_prompt_")
		return a.channelUI.GetWhatsAppUI().PromptPairCode(c, chID)
	}
	if strings.HasPrefix(data, "chan_wa_dm_menu_") {
		chID := strings.TrimPrefix(data, "chan_wa_dm_menu_")
		return a.channelUI.GetWhatsAppUI().RenderDMPolicyMenu(c, chID)
	}
	if strings.HasPrefix(data, "chan_wa_set_dm_") {
		raw := strings.TrimPrefix(data, "chan_wa_set_dm_")
		chID, policy := parseChannelPolicyCallback(raw)
		if chID != "" && policy != "" {
			return a.channelUI.GetWhatsAppUI().HandleSetDMPolicy(c, chID, policy)
		}
	}
	if strings.HasPrefix(data, "chan_wa_grp_menu_") {
		chID := strings.TrimPrefix(data, "chan_wa_grp_menu_")
		return a.channelUI.GetWhatsAppUI().RenderGroupPolicyMenu(c, chID)
	}
	if strings.HasPrefix(data, "chan_wa_set_grp_") {
		raw := strings.TrimPrefix(data, "chan_wa_set_grp_")
		chID, policy := parseChannelPolicyCallback(raw)
		if chID != "" && policy != "" {
			return a.channelUI.GetWhatsAppUI().HandleSetGroupPolicy(c, chID, policy)
		}
	}
	if strings.HasPrefix(data, "chan_wa_men_menu_") {
		chID := strings.TrimPrefix(data, "chan_wa_men_menu_")
		return a.channelUI.GetWhatsAppUI().RenderMentionPolicyMenu(c, chID)
	}
	if strings.HasPrefix(data, "chan_wa_set_men_") {
		raw := strings.TrimPrefix(data, "chan_wa_set_men_")
		chID, policy := parseChannelPolicyCallback(raw)
		if chID != "" && policy != "" {
			return a.channelUI.GetWhatsAppUI().HandleSetMentionPolicy(c, chID, policy)
		}
	}
	if strings.HasPrefix(data, "chan_wa_list_menu_") {
		chID := strings.TrimPrefix(data, "chan_wa_list_menu_")
		return a.channelUI.GetWhatsAppUI().RenderWhitelistManagerMenu(c, chID)
	}
	if strings.HasPrefix(data, "chan_wa_input_trust_") {
		chID := strings.TrimPrefix(data, "chan_wa_input_trust_")
		ch, err := a.db.GetChannel(chID)
		if err != nil || ch == nil {
			return c.Reply("❌ Channel tidak ditemukan.")
		}
		a.channelUI.SetSessionStep(c.Sender().ID, chID, ChannelStepAddTrustedNumbers)
		text := fmt.Sprintf("➕ <b>TAMBAH NOMOR TRUSTED (%s)</b>\n\nKirimkan nomor telepon WhatsApp yang ingin diizinkan (bisa lebih dari satu, pisahkan dengan koma atau baris baru):\n\n<b>Contoh:</b>\n<code>628123456789, 628987654321</code>", html.EscapeString(ch.Name))
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", fmt.Sprintf("chan_wa_list_menu_%s", ch.ID))
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "chan_wa_input_grp_") {
		chID := strings.TrimPrefix(data, "chan_wa_input_grp_")
		ch, err := a.db.GetChannel(chID)
		if err != nil || ch == nil {
			return c.Reply("❌ Channel tidak ditemukan.")
		}
		a.channelUI.SetSessionStep(c.Sender().ID, chID, ChannelStepAddAllowedGroups)
		text := fmt.Sprintf("➕ <b>TAMBAH ID GRUP WHITELIST (%s)</b>\n\nKirimkan Group JID WhatsApp yang ingin diizinkan (bisa lebih dari satu, pisahkan dengan koma atau baris baru):\n\n<b>Contoh:</b>\n<code>1203630248234@g.us, 120363999999@g.us</code>", html.EscapeString(ch.Name))
		menu := &tele.ReplyMarkup{}
		btnCancel := menu.Data("❌ Batal", fmt.Sprintf("chan_wa_list_menu_%s", ch.ID))
		menu.Inline(menu.Row(btnCancel))
		return c.EditOrSend(text, menu, tele.ModeHTML)
	}
	if strings.HasPrefix(data, "chan_wa_gwiz_") {
		raw := strings.TrimPrefix(data, "chan_wa_gwiz_")
		lastUnderscore := strings.LastIndex(raw, "_")
		if lastUnderscore != -1 {
			chID := raw[:lastUnderscore]
			var page int
			fmt.Sscanf(raw[lastUnderscore+1:], "%d", &page)
			return a.channelUI.GetWhatsAppUI().RenderGroupWhitelistWizard(c, chID, page)
		}
	}
	if strings.HasPrefix(data, "chan_wa_gtgl_") {
		raw := strings.TrimPrefix(data, "chan_wa_gtgl_")
		parts := strings.Split(raw, "_")
		if len(parts) >= 3 {
			chID := parts[0]
			var page, globalIdx int
			fmt.Sscanf(parts[1], "%d", &page)
			fmt.Sscanf(parts[2], "%d", &globalIdx)
			return a.channelUI.GetWhatsAppUI().HandleToggleGroupWhitelist(c, chID, page, globalIdx)
		}
	}
	if strings.HasPrefix(data, "chan_wa_clr_trust_") {
		chID := strings.TrimPrefix(data, "chan_wa_clr_trust_")
		return a.channelUI.GetWhatsAppUI().HandleClearLists(c, chID, "trust")
	}
	if strings.HasPrefix(data, "chan_wa_clr_grp_") {
		chID := strings.TrimPrefix(data, "chan_wa_clr_grp_")
		return a.channelUI.GetWhatsAppUI().HandleClearLists(c, chID, "grp")
	}

	// Policy Wizard Shortcut
	if strings.HasPrefix(data, "pol_wiz_ch_") {
		chID := strings.TrimPrefix(data, "pol_wiz_ch_")
		return a.limitsUI.RenderScopeLimitsDashboard(c, "channel", chID)
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

	// Checkin Callbacks
	if strings.HasPrefix(data, "checkin_btn_del_") {
		idxStr := strings.TrimPrefix(data, "checkin_btn_del_")
		return a.checkinUI.HandleDeleteUserCallback(c, idxStr)
	}

	// MD Multi-Channel Callbacks
	if data == "md_scope_custom" {
		return a.mdUI.PromptCustomChannelID(c)
	}
	if strings.HasPrefix(data, "md_scope:") {
		channelID := strings.TrimPrefix(data, "md_scope:")
		return a.mdUI.RenderChannelDashboard(c, channelID)
	}
	if strings.HasPrefix(data, "md_newfile:") {
		channelID := strings.TrimPrefix(data, "md_newfile:")
		return a.mdUI.PromptNewFileName(c, channelID)
	}
	if strings.HasPrefix(data, "md_f:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "md_f:"), ":", 2)
		if len(parts) == 2 {
			return a.mdUI.RenderMDFileDashboard(c, parts[0], parts[1])
		}
	}
	if strings.HasPrefix(data, "md_v:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "md_v:"), ":", 2)
		if len(parts) == 2 {
			return a.mdUI.RenderFullFile(c, parts[0], parts[1])
		}
	}
	if strings.HasPrefix(data, "md_e:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "md_e:"), ":", 2)
		if len(parts) == 2 {
			return a.mdUI.PromptEditContent(c, parts[0], parts[1])
		}
	}
	if strings.HasPrefix(data, "md_a:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "md_a:"), ":", 2)
		if len(parts) == 2 {
			return a.mdUI.PromptAppendContent(c, parts[0], parts[1])
		}
	}
	if strings.HasPrefix(data, "md_r:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "md_r:"), ":", 2)
		if len(parts) == 2 {
			return a.mdUI.HandleResetChannelFile(c, parts[0], parts[1])
		}
	}

	// Legacy MD Fallback Callbacks
	if strings.HasPrefix(data, "md_pick_") {
		fname := strings.TrimPrefix(data, "md_pick_")
		return a.mdUI.RenderMDFileDashboard(c, "global", fname)
	}
	if strings.HasPrefix(data, "md_view_full_") {
		fname := strings.TrimPrefix(data, "md_view_full_")
		return a.mdUI.RenderFullFile(c, "global", fname)
	}
	if strings.HasPrefix(data, "md_edit_prompt_") {
		fname := strings.TrimPrefix(data, "md_edit_prompt_")
		return a.mdUI.PromptEditContent(c, "global", fname)
	}
	if strings.HasPrefix(data, "md_app_prompt_") {
		fname := strings.TrimPrefix(data, "md_app_prompt_")
		return a.mdUI.PromptAppendContent(c, "global", fname)
	}

	// Memory Engine Callbacks
	if strings.HasPrefix(data, "mem_") {
		return a.memoryUI.HandleCallback(c, data)
	}

	// Bifrost AI Gateway Callbacks
	if strings.HasPrefix(data, "bf_") && a.bifrostUI != nil {
		switch data {
		case "bf_menu_workers":
			txt, kb := a.bifrostUI.RenderWorkersMenu()
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		case "bf_menu_net":
			txt, kb := a.bifrostUI.RenderNetworkMenu()
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		case "bf_menu_stream":
			txt, kb := a.bifrostUI.RenderStreamMenu()
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		case "bf_menu_cache":
			txt, kb := a.bifrostUI.RenderCacheMenu()
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		case "bf_menu_proxy":
			txt, kb := a.bifrostUI.RenderProxyMenu()
			return c.EditOrSend(txt, kb, tele.ModeHTML)
		case "bf_reload_all":
			return a.bifrostUI.HandleReloadAll(c)
		case "bf_set_conc_4":
			return a.bifrostUI.HandleConcurrencySet(c, 4)
		case "bf_set_conc_8":
			return a.bifrostUI.HandleConcurrencySet(c, 8)
		case "bf_set_conc_16":
			return a.bifrostUI.HandleConcurrencySet(c, 16)
		case "bf_set_conc_32":
			return a.bifrostUI.HandleConcurrencySet(c, 32)
		case "bf_set_buf_32":
			return a.bifrostUI.HandleBufferSizeSet(c, 32)
		case "bf_set_buf_64":
			return a.bifrostUI.HandleBufferSizeSet(c, 64)
		case "bf_set_buf_128":
			return a.bifrostUI.HandleBufferSizeSet(c, 128)
		case "bf_set_to_30":
			return a.bifrostUI.HandleTimeoutSet(c, 30)
		case "bf_set_to_60":
			return a.bifrostUI.HandleTimeoutSet(c, 60)
		case "bf_set_to_90":
			return a.bifrostUI.HandleTimeoutSet(c, 90)
		case "bf_set_to_120":
			return a.bifrostUI.HandleTimeoutSet(c, 120)
		case "bf_set_retry_0":
			return a.bifrostUI.HandleRetrySet(c, 0)
		case "bf_set_retry_1":
			return a.bifrostUI.HandleRetrySet(c, 1)
		case "bf_set_retry_2":
			return a.bifrostUI.HandleRetrySet(c, 2)
		case "bf_set_retry_3":
			return a.bifrostUI.HandleRetrySet(c, 3)
		case "bf_tgl_private":
			return a.bifrostUI.HandleTogglePrivate(c)
		case "bf_tgl_tls":
			return a.bifrostUI.HandleToggleTLS(c)
		case "bf_set_st_15":
			return a.bifrostUI.HandleStreamTimeoutSet(c, 15)
		case "bf_set_st_30":
			return a.bifrostUI.HandleStreamTimeoutSet(c, 30)
		case "bf_set_st_60":
			return a.bifrostUI.HandleStreamTimeoutSet(c, 60)
		case "bf_set_st_120":
			return a.bifrostUI.HandleStreamTimeoutSet(c, 120)
		case "bf_tgl_usage":
			return a.bifrostUI.HandleToggleUsage(c)
		case "bf_tgl_done":
			return a.bifrostUI.HandleToggleDone(c)
		case "bf_tgl_cache":
			return a.bifrostUI.HandleToggleCache(c)
		case "bf_prompt_proxy":
			return a.bifrostUI.HandlePromptProxy(c)
		case "bf_clear_proxy":
			return a.bifrostUI.HandleClearProxy(c)
		}
	}

	return nil
}

func parseChannelPolicyCallback(raw string) (string, string) {
	if strings.Contains(raw, "__") {
		parts := strings.SplitN(raw, "__", 2)
		return parts[0], parts[1]
	}
	lastUnderscore := strings.LastIndex(raw, "_")
	if lastUnderscore != -1 {
		return raw[:lastUnderscore], raw[lastUnderscore+1:]
	}
	return "", ""
}
