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
	return "Eksekusi perintah terminal/shell di server lokal (misal: menjalankan query analitik g3a, script visualisasi chart, export data, atau utilitas sistem). Jika perintah memerlukan hak akses administrator/root (sudo/doas), sertakan parameter sudo_password yang telah dikonfirmasi dan diberikan oleh pengguna."
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
				Description: "Password administrator (sudo/root) pengguna jika perintah membutuhkan hak akses root/administrator (opsional).",
			},
		},
		Required: []string{"command"},
	}
}

type PrivilegeType string

const (
	PrivilegeNone PrivilegeType = ""
	PrivilegeSudo PrivilegeType = "sudo"
	PrivilegeDoas PrivilegeType = "doas"
)

func detectPrivilege(cmd string) PrivilegeType {
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
			return PrivilegeSudo
		}
		if part == "doas" {
			return PrivilegeDoas
		}
	}
	return PrivilegeNone
}

func containsSudo(cmd string) bool {
	return detectPrivilege(cmd) == PrivilegeSudo
}

func containsDoas(cmd string) bool {
	return detectPrivilege(cmd) == PrivilegeDoas
}

func containsPrivilege(cmd string) bool {
	return detectPrivilege(cmd) != PrivilegeNone
}

func injectDoasNonInteractive(cmd string) string {
	if strings.Contains(cmd, "doas -n") {
		return cmd
	}
	var lines []string
	for _, line := range strings.Split(cmd, "\n") {
		tokens := strings.Fields(line)
		var newTokens []string
		for _, tok := range tokens {
			if tok == "doas" {
				newTokens = append(newTokens, "doas", "-n")
			} else {
				newTokens = append(newTokens, tok)
			}
		}
		lines = append(lines, strings.Join(newTokens, " "))
	}
	return strings.Join(lines, "\n")
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

	privTool := detectPrivilege(cmdStr)
	isPrivileged := runtime.GOOS != "windows" && privTool != PrivilegeNone

	// If command requires sudo and no password is in active session, prompt interactively via Telegram prompter
	var finishPrompt func()
	if privTool == PrivilegeSudo && sudoPass == "" {
		if prompter := GetPasswordPrompter(ctx); prompter != nil {
			pass, finish, err := prompter.PromptPassword(ctx, "Konfirmasi Password Administrator (sudo)", fmt.Sprintf("Perintah yang akan dieksekusi:\n<code>%s</code>", html.EscapeString(cmdStr)))
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

	if privTool == PrivilegeSudo {
		if sudoPass != "" {
			finalCmdStr = injectSudoStdinFlag(cmdStr)
			stdinInput = sudoPass + "\n"
		} else {
			// Anti-freeze: enforce non-interactive so sudo fails immediately if password is required
			finalCmdStr = injectSudoNonInteractive(cmdStr)
		}
	} else if privTool == PrivilegeDoas {
		// Anti-freeze: enforce non-interactive so doas fails immediately if password/authorization is required
		finalCmdStr = injectDoasNonInteractive(cmdStr)
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

	// Check if tool is missing entirely
	if strings.Contains(errStr, "sudo: not found") || strings.Contains(errStr, "sudo: command not found") {
		return fmt.Sprintf("[TOOL_NOT_FOUND]\nPerintah gagal dieksekusi karena binary 'sudo' tidak terpasang di host server ini.\n\nINSTRUKSI UNTUK AI ASSISTANT:\n1. JANGAN mengulangi perintah dengan 'sudo'.\n2. Periksa bagian 'Host System Environment' di sistem prompt (misalnya: jika server adalah Alpine Linux/BSD, gunakan 'doas', bukan 'sudo').\n3. Informasikan kepada pengguna bahwa sistem ini menggunakan 'doas' (atau tool privilese lain) dan jalankan perintah dengan tool yang sesuai."), nil
	}
	if strings.Contains(errStr, "doas: not found") || strings.Contains(errStr, "doas: command not found") {
		return fmt.Sprintf("[TOOL_NOT_FOUND]\nPerintah gagal dieksekusi karena binary 'doas' tidak terpasang di host server ini.\n\nINSTRUKSI UNTUK AI ASSISTANT:\n1. JANGAN mengulangi perintah dengan 'doas'.\n2. Periksa bagian 'Host System Environment' di sistem prompt (misalnya gunakan 'sudo').\n3. Jalankan perintah dengan tool privilese yang sesuai."), nil
	}

	// Check if privilege escalation failed because authorization / password required
	if isPrivileged && (strings.Contains(errStr, "a password is required") || strings.Contains(outStr, "a password is required") || strings.Contains(errStr, "password is required") || strings.Contains(errStr, "Operation not permitted") || strings.Contains(errStr, "Authorization failed") || strings.Contains(errStr, "a tty is required") || strings.Contains(errStr, "doas: a tty is required")) {
		if privTool == PrivilegeDoas {
			return fmt.Sprintf("[PRIVILEGE_REQUIRED]\nPerintah '%s' membutuhkan hak akses administrator (doas) dan belum diizinkan secara non-interaktif.\n\nINSTRUKSI WAJIB UNTUK AI ASSISTANT:\n1. JANGAN mencoba mengeksekusi perintah ini lagi sekarang.\n2. DILARANG meminta pengguna mengetikkan password di chat percakapan biasa.\n3. Jelaskan kepada pengguna bahwa perintah <code>%s</code> membutuhkan hak akses root/administrator via doas.\n4. Beritahukan pengguna untuk mengizinkan user menjalankan perintah doas (misalnya menambahkan rule 'permit nopass' untuk user bot di /etc/doas.d/).", cmdStr, cmdStr), nil
		}
		return fmt.Sprintf("[SUDO_PASSWORD_REQUIRED]\nPerintah '%s' membutuhkan hak akses administrator (sudo) dan memerlukan password server.\n\nINSTRUKSI WAJIB UNTUK AI ASSISTANT:\n1. JANGAN mencoba mengeksekusi perintah ini lagi sekarang.\n2. DILARANG meminta pengguna mengetikkan password di chat percakapan biasa.\n3. Jelaskan kepada pengguna bahwa perintah <code>%s</code> membutuhkan hak akses root/sudo.\n4. Beritahukan pengguna untuk mengatur password sudo via perintah /setsudo di Telegram atau ulangi perintah agar sistem memicu dialog aman Telegram.", cmdStr, cmdStr), nil
	}

	// Check if sudo failed because password was incorrect
	if privTool == PrivilegeSudo && sudoPass != "" && (strings.Contains(errStr, "incorrect password") || strings.Contains(errStr, "a password is required")) {
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
