package admin

import (
	tele "gopkg.in/telebot.v3"
)

// MainMenuKeyboard builds the primary interactive control dashboard
func MainMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}

	btnProviders := menu.Data("🤖 Provider AI", "menu_providers")
	btnChannels := menu.Data("📱 Channels (WA/TG)", "menu_channels")
	btnLimits := menu.Data("🛡️ Pembatasan & Limits", "menu_limits")
	btnMDFiles := menu.Data("📝 Manage .MD Bot", "menu_md")
	btnCron := menu.Data("⏰ Cron Scheduler", "menu_cron")
	btnMemory := menu.Data("🧠 Memory & Session", "menu_memory")
	btnStats := menu.Data("📊 Audit Log & Stats", "menu_stats")
	btnTools := menu.Data("🧰 Tool Permissions", "menu_tools")
	btnBackup := menu.Data("💾 Backup / Export", "menu_backup")
	btnHelp := menu.Data("❓ Bantuan Command", "menu_help")

	menu.Inline(
		menu.Row(btnProviders, btnChannels),
		menu.Row(btnLimits, btnTools),
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
