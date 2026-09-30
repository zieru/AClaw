package tools

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type BashTool struct{}

func (t *BashTool) Name() string {
	return "bash_exec"
}

func (t *BashTool) Description() string {
	return "Eksekusi perintah terminal/shell di server lokal (misal: menjalankan query analitik g3a, script visualisasi chart, export data, atau utilitas sistem). Jika perintah memerlukan hak akses administrator/root (sudo), sertakan parameter sudo_password yang telah dikonfirmasi dan diberikan oleh pengguna."
}

func (t *BashTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"command": {
				Type:        "string",
				Description: "Perintah shell/terminal yang akan dieksekusi.",
			},
			"sudo_password": {
				Type:        "string",
				Description: "Password sudo pengguna jika perintah membutuhkan hak akses root/administrator (opsional).",
			},
		},
		Required: []string{"command"},
	}
}

func containsSudo(cmd string) bool {
	normalized := strings.ReplaceAll(cmd, "|", " ")
	normalized = strings.ReplaceAll(normalized, ";", " ")
	normalized = strings.ReplaceAll(normalized, "&", " ")
	normalized = strings.ReplaceAll(normalized, "'", " ")
	normalized = strings.ReplaceAll(normalized, "\"", " ")
	normalized = strings.ReplaceAll(normalized, "`", " ")
	normalized = strings.ReplaceAll(normalized, "(", " ")
	normalized = strings.ReplaceAll(normalized, ")", " ")
	for _, part := range strings.Fields(normalized) {
		if part == "sudo" {
			return true
		}
	}
	return false
}

func injectSudoStdinFlag(cmd string) string {
	if strings.Contains(cmd, "sudo -S") {
		return cmd
	}
	var lines []string
	for _, line := range strings.Split(cmd, "\n") {
		tokens := strings.Fields(line)
		var newTokens []string
		for _, tok := range tokens {
			if tok == "sudo" {
				newTokens = append(newTokens, "sudo", "-S", "-p", "''")
			} else {
				newTokens = append(newTokens, tok)
			}
		}
		lines = append(lines, strings.Join(newTokens, " "))
	}
	return strings.Join(lines, "\n")
}

func injectSudoNonInteractive(cmd string) string {
	if strings.Contains(cmd, "sudo -n") {
		return cmd
	}
	tokens := strings.Fields(cmd)
	var newTokens []string
	for _, tok := range tokens {
		if tok == "sudo" {
			newTokens = append(newTokens, "sudo", "-n")
		} else {
			newTokens = append(newTokens, tok)
		}
	}
	return strings.Join(newTokens, " ")
}

func (t *BashTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdStr, ok := args["command"].(string)
	if !ok || strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("parameter 'command' wajib diisi")
	}

	// Resolve session key for sudo cache
	var sessionKey string
	if chatID, ok := ctx.Value("chat_id").(string); ok && chatID != "" {
		sessionKey = chatID
	} else if userID, ok := ctx.Value("user_id").(string); ok && userID != "" {
		sessionKey = userID
	}

	sudoPass, _ := args["sudo_password"].(string)
	sudoPass = strings.TrimSpace(sudoPass)

	// If sudo_password is provided, update or refresh session
	if sudoPass != "" && sessionKey != "" {
		SetSudoSession(sessionKey, sudoPass)
	} else if sudoPass == "" && sessionKey != "" {
		// Use active cached sudo session if available
		sudoPass = GetSudoSession(sessionKey)
	}

	isSudo := runtime.GOOS != "windows" && containsSudo(cmdStr)

	// If command requires sudo and no password is in active session, prompt interactively via Telegram prompter
	var finishPrompt func()
	if isSudo && sudoPass == "" {
		if prompter := GetPasswordPrompter(ctx); prompter != nil {
			pass, finish, err := prompter.PromptPassword(ctx, "Konfirmasi Password Administrator (Sudo)", fmt.Sprintf("Perintah yang akan dieksekusi:\n<code>%s</code>", html.EscapeString(cmdStr)))
			if err == nil && pass != "" {
				sudoPass = pass
				finishPrompt = finish
				if sessionKey != "" {
					SetSudoSession(sessionKey, sudoPass)
				}
			}
		}
	}
	if finishPrompt != nil {
		defer finishPrompt()
	}

	// Prepare actual command string and stdin
	finalCmdStr := cmdStr
	var stdinInput string

	if isSudo {
		if sudoPass != "" {
			finalCmdStr = injectSudoStdinFlag(cmdStr)
			stdinInput = sudoPass + "\n"
		} else {
			// Anti-freeze: enforce non-interactive so sudo fails immediately if password is required
			finalCmdStr = injectSudoNonInteractive(cmdStr)
		}
	}

	// Set timeout of 30 seconds for command execution
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", finalCmdStr)
	}

	if stdinInput != "" {
		cmd.Stdin = strings.NewReader(stdinInput)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	outStr := stdout.String()
	errStr := stderr.String()

	// Redact password from output if present
	if sudoPass != "" {
		outStr = strings.ReplaceAll(outStr, sudoPass, "[REDACTED_PASSWORD]")
		errStr = strings.ReplaceAll(errStr, sudoPass, "[REDACTED_PASSWORD]")
	}

	// Check if sudo failed because password was missing/required
	if isSudo && (strings.Contains(errStr, "a password is required") || strings.Contains(outStr, "a password is required") || strings.Contains(errStr, "password is required")) {
		return fmt.Sprintf("[SUDO_PASSWORD_REQUIRED]\nPerintah '%s' membutuhkan hak akses administrator (sudo) dan memerlukan password server.\n\nINSTRUKSI WAJIB UNTUK AI ASSISTANT:\n1. JANGAN mencoba mengeksekusi perintah ini lagi sekarang.\n2. DILARANG meminta pengguna mengetikkan password di chat percakapan biasa.\n3. Jelaskan kepada pengguna bahwa perintah <code>%s</code> membutuhkan hak akses root/sudo.\n4. Beritahukan pengguna untuk mengatur password sudo via perintah /setsudo di Telegram atau ulangi perintah agar sistem memicu dialog aman Telegram.", cmdStr, cmdStr), nil
	}

	// Check if sudo failed because password was incorrect
	if isSudo && sudoPass != "" && (strings.Contains(errStr, "incorrect password") || strings.Contains(errStr, "a password is required")) {
		if sessionKey != "" {
			ClearSudoSession(sessionKey) // Invalidate incorrect cached session
		}
		return fmt.Sprintf("[SUDO_AUTH_FAILED]\nPassword sudo yang dimasukkan tidak valid / salah.\n\nINSTRUKSI UNTUK AI ASSISTANT:\nBeritahukan kepada pengguna bahwa password sudo salah dan minta pengguna memasukkan password yang benar."), nil
	}

	var sb strings.Builder
	if len(outStr) > 0 {
		sb.WriteString("STDOUT:\n")
		if len(outStr) > 4000 {
			sb.WriteString(outStr[:4000] + "\n...[truncated]")
		} else {
			sb.WriteString(outStr)
		}
	}
	if len(errStr) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("STDERR:\n")
		if len(errStr) > 2000 {
			sb.WriteString(errStr[:2000] + "\n...[truncated]")
		} else {
			sb.WriteString(errStr)
		}
	}

	if err != nil {
		if sb.Len() == 0 {
			return fmt.Sprintf("Error eksekusi: %v", err), nil
		}
		sb.WriteString(fmt.Sprintf("\n(Exit Code Error: %v)", err))
	}

	if sb.Len() == 0 {
		return "(Command selesai tanpa output)", nil
	}

	return sb.String(), nil
}
