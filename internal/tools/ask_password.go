package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type AskPasswordTool struct{}

func NewAskPasswordTool() *AskPasswordTool {
	return &AskPasswordTool{}
}

func (t *AskPasswordTool) Name() string {
	return "ask_password"
}

func (t *AskPasswordTool) Description() string {
	return "Minta input password atau kredensial rahasia secara interaktif dan aman dari pengguna melalui dialog custom Telegram (ForceReply). Password tidak akan pernah terlihat oleh AI (zero-leakage) dan pesan dialog akan otomatis dihapus setelah selesai."
}

func (t *AskPasswordTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"title": {
				Type:        "string",
				Description: "Judul permintaan password/kredensial (contoh: 'Password Database PostgreSQL', 'SSH Key Passphrase', 'Password Sudo').",
			},
			"description": {
				Type:        "string",
				Description: "Penjelasan mengapa password dibutuhkan dan tujuan eksekusi perintah.",
			},
			"target": {
				Type:        "string",
				Description: "Target penggunaan password (opsional, contoh: 'sudo' untuk otomatis menyimpannya ke sesi sudo server).",
			},
		},
		Required: []string{"title", "description"},
	}
}

func (t *AskPasswordTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Input Password Aman"
	}

	description, _ := args["description"].(string)
	description = strings.TrimSpace(description)
	if description == "" {
		description = "Sistem memerlukan input password untuk melanjutkan tugas."
	}

	target, _ := args["target"].(string)
	target = strings.ToLower(strings.TrimSpace(target))

	prompter := GetPasswordPrompter(ctx)
	if prompter == nil {
		return "", errors.New("dialog input password tidak didukung pada konteks atau channel saat ini")
	}

	pass, finish, err := prompter.PromptPassword(ctx, title, description)
	if err != nil {
		return fmt.Sprintf("Gagal meminta password: %v", err), nil
	}
	if finish != nil {
		defer finish()
	}

	if pass == "" {
		return "Pengguna membatalkan input password atau password kosong.", nil
	}

	// Resolve session key for sudo or credential cache
	var sessionKey string
	if chatID, ok := ctx.Value("chat_id").(string); ok && chatID != "" {
		sessionKey = chatID
	} else if userID, ok := ctx.Value("user_id").(string); ok && userID != "" {
		sessionKey = userID
	}

	if target == "sudo" && sessionKey != "" {
		SetSudoSession(sessionKey, pass)
	}

	return fmt.Sprintf("[SUCCESS] Password untuk '%s' berhasil dimasukkan oleh pengguna via dialog aman dan tersimpan di memori sesi aktif. Password TIDAK ditampilkan kepada Anda demi privasi dan keamanan. Anda kini dapat melanjutkan eksekusi perintah berikutnya.", title), nil
}
