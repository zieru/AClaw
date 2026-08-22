package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

func (a *AdminBot) registerDispatcher() {
	// 1. File Upload (Markdown documents / backups)
	a.bot.Handle(tele.OnDocument, a.mdUI.HandleDocumentUpload)

	// 2. Unified Text Interceptor (Wizard active states + Direct Chat)
	a.bot.Handle(tele.OnText, a.handleTextMessage)

	// 3. Unified Dynamic Callback Dispatcher
	a.bot.Handle(tele.OnCallback, a.handleDynamicCallback)
}

func (a *AdminBot) handleTextMessage(c tele.Context) error {
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

	// 7. Check Tavily Config dialog
	if handled, err := a.tavilyUI.HandleTextMessage(c); handled {
		return err
	}

	// 8. Direct Chat with Assistant from Admin PM
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

	if strings.HasPrefix(data, "cancel_task") {
		_ = c.Respond(&tele.CallbackResponse{Text: "Membatalkan proses AI..."})
		return a.handleStop(c)
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
		txt, kb := a.modelUI.RenderCombosList(c)
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
	if data == "mod_noop" {
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

	return nil
}
