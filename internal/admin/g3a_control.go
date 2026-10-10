package admin

import (
	"fmt"
	"html"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

// handleRestartG3A restarts the g3a process on the host server
func (a *AdminBot) handleRestartG3A(c tele.Context) error {
	msg, _ := a.bot.Send(c.Chat(), "🔄 <b>Mengirim sinyal restart ke service g3a...</b>", tele.ModeHTML)

	go func() {
		err := executeG3ARestart()
		time.Sleep(1 * time.Second)

		if err != nil {
			log.Printf("⚠️ [G3A] Gagal restart g3a: %v", err)
			_, _ = a.bot.Edit(msg, fmt.Sprintf(
				"❌ <b>GAGAL MERESTART G3A:</b>\n\n<code>%s</code>",
				html.EscapeString(err.Error()),
			), tele.ModeHTML)
			return
		}

		_, _ = a.bot.Edit(msg,
			"✅ <b>G3A BERHASIL DI-RESTART!</b>\n\n"+
				"• <b>Status:</b> Instance g3a MCP telah dimuat ulang\n"+
				"• <b>Knowledge Base:</b> SOP GraPARI & DuckDB analytics siap digunakan kembali.\n\n"+
				"💡 <i>Gunakan <code>/tools</code> untuk melihat daftar tool aktif.</i>",
			tele.ModeHTML,
		)
	}()

	return nil
}

func executeG3ARestart() error {
	// 1. Locate g3a binary
	g3aPath := "/home/zieru/bin/g3a"
	if runtime.GOOS == "windows" {
		g3aPath = "g3a.exe"
	} else if _, err := os.Stat(g3aPath); err != nil {
		if path, err := exec.LookPath("g3a"); err == nil {
			g3aPath = path
		} else {
			home, _ := os.UserHomeDir()
			candidate := filepath.Join(home, "bin", "g3a")
			if _, err := os.Stat(candidate); err == nil {
				g3aPath = candidate
			}
		}
	}

	// 2. Kill existing running g3a mcp process
	if runtime.GOOS != "windows" {
		out, _ := exec.Command("pgrep", "-f", "g3a mcp").Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
				proc, err := os.FindProcess(pid)
				if err == nil {
					_ = proc.Kill()
				}
			}
		}
	}

	// 3. Trigger restart via --restart flag
	cmd := exec.Command(g3aPath, "--restart")
	if out, err := cmd.CombinedOutput(); err != nil {
		// If command ran or supervisor respawns it, ignore exit error
		log.Printf("[G3A] restart command output: %s (err: %v)", string(out), err)
	}

	return nil
}
