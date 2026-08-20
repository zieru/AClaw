package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SendFileTool struct{}

func (t *SendFileTool) Name() string {
	return "send_file"
}

func (t *SendFileTool) Description() string {
	return "Kirimkan file gambar (PNG, JPG, JPEG, WEBP), dokumen (PDF, CSV, TXT, DOCX), audio, atau arsip dari server lokal langsung sebagai attachment ke chat pengguna Telegram/WhatsApp. Gunakan tool ini setiap kali pengguna meminta file/gambar lokal atau saat kamu selesai membuat grafik/file di server."
}

func (t *SendFileTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"file_path": {
				Type:        "string",
				Description: "Path lengkap atau relatif ke file yang ada di server (contoh: '/home/zieru/AClaw/stuck_order_3bln.png' atau 'report.pdf')",
			},
			"caption": {
				Type:        "string",
				Description: "Keterangan atau teks penjelasan singkat untuk file yang dikirimkan (opsional)",
			},
		},
		Required: []string{"file_path"},
	}
}

func (t *SendFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	filePathVal, ok := args["file_path"]
	if !ok {
		return "", fmt.Errorf("parameter 'file_path' wajib diisi")
	}

	filePath, ok := filePathVal.(string)
	if !ok || strings.TrimSpace(filePath) == "" {
		return "", fmt.Errorf("parameter 'file_path' tidak valid")
	}
	filePath = strings.TrimSpace(filePath)

	caption := ""
	if capVal, ok := args["caption"]; ok {
		if cStr, ok := capVal.(string); ok {
			caption = strings.TrimSpace(cStr)
		}
	}

	// Verify file existence
	info, err := os.Stat(filePath)
	if err != nil {
		// Try checking relative to current working directory
		cwd, _ := os.Getwd()
		altPath := filepath.Join(cwd, filePath)
		info, err = os.Stat(altPath)
		if err != nil {
			return fmt.Sprintf("❌ File tidak ditemukan di server: %s", filePath), nil
		}
		filePath = altPath
	}

	if info.IsDir() {
		return fmt.Sprintf("❌ Path '%s' adalah direktori, bukan file.", filePath), nil
	}

	sizeMB := float64(info.Size()) / (1024 * 1024)

	return fmt.Sprintf("[ATTACH_FILE:%s|CAPTION:%s] ✅ File '%s' (%.2f MB) siap dikirimkan ke chat pengguna.", filePath, caption, filepath.Base(filePath), sizeMB), nil
}
