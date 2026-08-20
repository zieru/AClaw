package admin

import (
	"fmt"
	"html"
	"strings"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type ComboUI struct {
	db              *storage.DB
	providerManager *provider.Manager
}

func NewComboUI(db *storage.DB, pm *provider.Manager) *ComboUI {
	return &ComboUI{db: db, providerManager: pm}
}

// RenderCombosList returns HTML summary of configured combos
func (ui *ComboUI) RenderCombosList() string {
	combos, err := ui.db.ListCombos()
	if err != nil {
		return fmt.Sprintf("❌ Error mengambil data combo: %v", html.EscapeString(err.Error()))
	}

	var sb strings.Builder
	sb.WriteString("🔀 <b>9ROUTER MODEL COMBOS & SMART CHAINS</b>\n\n")

	if len(combos) == 0 {
		sb.WriteString("(Belum ada Combo yang dibuat)\n\n")
	} else {
		for i, c := range combos {
			statusIcon := "🟢"
			if !c.IsActive {
				statusIcon = "🔴"
			}

			var targetsStr []string
			for _, t := range c.Targets {
				targetsStr = append(targetsStr, fmt.Sprintf("<code>%s/%s</code>", html.EscapeString(t.ProviderID), html.EscapeString(t.Model)))
			}

			sb.WriteString(fmt.Sprintf("%d. %s <b>%s</b> (Strategi: <code>%s</code>)\n", i+1, statusIcon, html.EscapeString(c.Name), html.EscapeString(c.Strategy)))
			if c.Description != "" {
				sb.WriteString(fmt.Sprintf("   • Info: <i>%s</i>\n", html.EscapeString(c.Description)))
			}
			sb.WriteString(fmt.Sprintf("   • Chain: %s\n\n", strings.Join(targetsStr, " ➔ ")))
		}
	}

	sb.WriteString("📋 <b>Perintah Combo Chains:</b>\n")
	sb.WriteString("• <code>/addcombo &lt;name&gt; &lt;prov1:model1,prov2:model2,...&gt; [deskripsi]</code>\n")
	sb.WriteString("• <code>/delcombo &lt;name&gt;</code>\n\n")
	sb.WriteString("💡 <b>Contoh Penggunaan:</b>\n")
	sb.WriteString("<code>/addcombo smart openai:gpt-4o,anthropic:claude-3-5-sonnet,gemini:gemini-2.0-flash \"Smart Models Failsafe\"</code>\n")
	sb.WriteString("<code>/addcombo fast groq:llama-3.3-70b-versatile,9router:gpt-4o-mini \"Fast & Cheap\"</code>\n")
	sb.WriteString("<i>Setelah dibuat, Anda dapat mengatur model chat ke <code>combo:smart</code> atau <code>smart</code>!</i>")

	return sb.String()
}

// HandleCombos processes `/combos`
func (ui *ComboUI) HandleCombos(c tele.Context) error {
	return c.Send(ui.RenderCombosList(), ui.CombosKeyboard(), tele.ModeHTML)
}

func (ui *ComboUI) CombosKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnWizard := menu.Data("🧙‍♂️ Buat Combo Baru (Wizard)", "cwiz_start")
	btnBack := menu.Data("⬅️ Kembali ke Menu Utama", "menu_main")
	menu.Inline(
		menu.Row(btnWizard),
		menu.Row(btnBack),
	)
	return menu
}

// HandleAddCombo processes `/addcombo <name> <prov1:model1,prov2:model2,...> [desc]`
func (ui *ComboUI) HandleAddCombo(c tele.Context) error {
	args := c.Args()
	if len(args) < 2 {
		return c.Reply("⚠️ Format salah!\nContoh:\n<code>/addcombo smart openai:gpt-4o,anthropic:claude-3-5-sonnet,gemini:gemini-2.0-flash</code>", tele.ModeHTML)
	}

	name := strings.ToLower(strings.TrimSpace(args[0]))
	rawTargets := strings.Split(args[1], ",")
	desc := ""
	if len(args) >= 3 {
		desc = strings.Join(args[2:], " ")
	}

	var targets []storage.ComboTarget
	for i, rt := range rawTargets {
		parts := strings.SplitN(strings.TrimSpace(rt), ":", 2)
		if len(parts) != 2 {
			return c.Reply(fmt.Sprintf("⚠️ Target '%s' tidak valid. Format harus <code>provider_id:model_name</code>", html.EscapeString(rt)), tele.ModeHTML)
		}
		targets = append(targets, storage.ComboTarget{
			ProviderID: strings.TrimSpace(parts[0]),
			Model:      strings.TrimSpace(parts[1]),
			Priority:   i + 1,
		})
	}

	if len(targets) == 0 {
		return c.Reply("⚠️ Minimal harus ada 1 target dalam combo.")
	}

	record := &storage.ModelComboRecord{
		Name:        name,
		Description: desc,
		Targets:     targets,
		Strategy:    "failsafe",
		IsActive:    true,
	}

	if err := ui.db.SaveCombo(record); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan combo: %v", html.EscapeString(err.Error())))
	}

	ui.providerManager.RegisterCombo(record)
	return c.Reply(fmt.Sprintf("✅ Combo <b>%s</b> berhasil dibuat dengan %d fallback target!\nGunakan model <code>combo:%s</code> pada channel/chat.", html.EscapeString(name), len(targets), html.EscapeString(name)), tele.ModeHTML)
}

// HandleDelCombo processes `/delcombo <name>`
func (ui *ComboUI) HandleDelCombo(c tele.Context) error {
	args := c.Args()
	if len(args) < 1 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/delcombo smart</code>", tele.ModeHTML)
	}

	name := strings.ToLower(strings.TrimSpace(args[0]))
	if err := ui.db.DeleteCombo(name); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menghapus combo: %v", html.EscapeString(err.Error())))
	}

	ui.providerManager.UnregisterCombo(name)
	return c.Reply(fmt.Sprintf("🗑️ Combo <b>%s</b> berhasil dihapus!", html.EscapeString(name)), tele.ModeHTML)
}
