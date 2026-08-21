package admin

import (
	tele "gopkg.in/telebot.v3"
)

// MainMenuKeyboard builds the primary interactive control dashboard
func MainMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}

	btnStatus := menu.Data("⚡ Status Server", "menu_status")
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
	btnBackup := menu.Data("💾 Backup / Export", "menu_backup")
	btnHelp := menu.Data("❓ Bantuan Command", "menu_help")

	menu.Inline(
		menu.Row(btnStatus, btnProviders),
		menu.Row(btnCombos, btnChannels),
		menu.Row(btnTools, btnProxy),
		menu.Row(btnTokenSaver, btnLimits),
		menu.Row(btnMDFiles, btnCron),
		menu.Row(btnMemory, btnStats),
		menu.Row(btnBackup, btnHelp),
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
