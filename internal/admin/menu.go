package admin

import (
	tele "gopkg.in/telebot.v3"
)

// MainMenuKeyboard builds the primary interactive control dashboard
func MainMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}

	btnStatus := menu.Data("⚡ Status Server", "menu_status")
	btnModel := menu.Data("🎛️ Switch Model", "menu_model")
	btnProviders := menu.Data("🤖 Provider AI", "menu_providers")
	btnCombos := menu.Data("🔀 Model Combos", "menu_combos")
	btnChannels := menu.Data("📱 Channels (WA/TG)", "menu_channels")
	btnProxy := menu.Data("🌐 Proxy Pool (9Router)", "menu_proxy")
	btnTokenSaver := menu.Data("🌿 Token Saver (RTK)", "menu_tokensaver")
	btnLimits := menu.Data("🛡️ Limits & Footer", "menu_limits")
	btnMDFiles := menu.Data("📝 Manage .MD Bot", "menu_md")
	btnCron := menu.Data("⏰ Cron Scheduler", "menu_cron")
	btnMemory := menu.Data("🧠 Memory & Session", "menu_memory")
	btnStats := menu.Data("📊 Audit Log & Stats", "menu_stats")
	btnTools := menu.Data("🧰 Tool Permissions", "menu_tools")
	btnTavily := menu.Data("🌐 Tavily AI Search", "menu_tavily")
	btnCheckin := menu.Data("🎁 Auto Check-in", "menu_checkin")
	btnBackup := menu.Data("💾 Backup / Export", "menu_backup")
	btnUpdate := menu.Data("🚀 System Update", "menu_update")
	btnHelp := menu.Data("❓ Bantuan Command", "menu_help")

	menu.Inline(
		menu.Row(btnStatus, btnModel),
		menu.Row(btnProviders, btnCombos),
		menu.Row(btnChannels, btnTools),
		menu.Row(btnProxy, btnTokenSaver),
		menu.Row(btnTavily, btnLimits),
		menu.Row(btnMDFiles, btnCron),
		menu.Row(btnMemory, btnStats),
		menu.Row(btnCheckin, btnBackup),
		menu.Row(btnUpdate, btnHelp),
	)

	return menu
}

// BackToMenuKeyboard returns a single button returning to main menu
func BackToMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	menu.Inline(menu.Row(btnBack))
	return menu
}
