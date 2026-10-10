package admin

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// handleRestartG3A updates and restarts the g3a process on the host server
func (a *AdminBot) handleRestartG3A(c tele.Context) error {
	msg, _ := a.bot.Send(c.Chat(), "🔄 <b>Memeriksa update & menyiapkan restart service g3a...</b>", tele.ModeHTML)

	go func() {
		updated, newTag, err := executeG3AUpdateAndRestart()
		time.Sleep(1 * time.Second)

		if err != nil {
			log.Printf("⚠️ [G3A] Gagal update/restart g3a: %v", err)
			_, _ = a.bot.Edit(msg, fmt.Sprintf(
				"❌ <b>GAGAL UPDATE & RESTART G3A:</b>\n\n<code>%s</code>",
				html.EscapeString(err.Error()),
			), tele.ModeHTML)
			return
		}

		statusText := "Instance g3a MCP telah dimuat ulang (versi saat ini)"
		if updated {
			statusText = fmt.Sprintf("Binary g3a berhasil di-update ke <code>%s</code> dan dimuat ulang!", html.EscapeString(newTag))
		}

		_, _ = a.bot.Edit(msg, fmt.Sprintf(
			"✅ <b>G3A BERHASIL DI-UPDATE & DI-RESTART!</b>\n\n"+
				"• <b>Status:</b> %s\n"+
				"• <b>Knowledge Base:</b> SOP GraPARI & DuckDB analytics siap digunakan.\n\n"+
				"💡 <i>Gunakan <code>/tools</code> untuk melihat daftar tool aktif.</i>",
			statusText,
		), tele.ModeHTML)
	}()

	return nil
}

func executeG3AUpdateAndRestart() (bool, string, error) {
	// 1. Locate g3a binary path
	g3aPath := "/home/zieru/bin/g3a"
	binDir := "/home/zieru/bin"
	if runtime.GOOS == "windows" {
		g3aPath = "g3a.exe"
		binDir = "."
	} else if _, err := os.Stat(g3aPath); err != nil {
		if path, err := exec.LookPath("g3a"); err == nil {
			g3aPath = path
			binDir = filepath.Dir(path)
		} else {
			home, _ := os.UserHomeDir()
			candidate := filepath.Join(home, "bin", "g3a")
			if _, err := os.Stat(candidate); err == nil {
				g3aPath = candidate
				binDir = filepath.Dir(candidate)
			}
		}
	}

	updated := false
	latestTag := ""

	// 2. If Linux, check & download latest release from GitHub
	if runtime.GOOS == "linux" {
		tag, downloadURL, err := getLatestG3ARelease()
		if err == nil && downloadURL != "" {
			latestTag = tag
			targetFile := filepath.Join(binDir, "g3a-linux-amd64")
			tempFile := targetFile + ".new"

			if err := downloadFile(downloadURL, tempFile); err == nil {
				_ = os.Chmod(tempFile, 0755)
				_ = os.Rename(tempFile, targetFile)
				_ = os.Remove(g3aPath)
				_ = os.Symlink(targetFile, g3aPath)
				updated = true
				log.Printf("🚀 [G3A] Berhasil mengunduh update g3a (%s) ke %s", tag, targetFile)
			} else {
				log.Printf("⚠️ [G3A] Gagal download update g3a: %v (melanjutkan restart binary saat ini)", err)
			}
		}
	}

	// 3. Kill existing running g3a mcp process
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

	// 4. Trigger restart via --restart flag
	cmd := exec.Command(g3aPath, "--restart")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[G3A] restart command output: %s (err: %v)", string(out), err)
	}

	return updated, latestTag, nil
}

func getLatestG3ARelease() (string, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/zieru/a7g3/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "GoAssistant-Admin")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status github: %s", resp.Status)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", err
	}

	for _, a := range rel.Assets {
		if a.Name == "g3a-linux-amd64" {
			return rel.TagName, a.BrowserDownloadURL, nil
		}
	}

	return rel.TagName, "", fmt.Errorf("asset g3a-linux-amd64 tidak ditemukan di release %s", rel.TagName)
}

func downloadFile(url, dstPath string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
