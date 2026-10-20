package admin

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tele "gopkg.in/telebot.v3"
)

func (a *AdminBot) handleBackup(c tele.Context) error {
	_ = c.Notify(tele.Typing)

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	// 1. Archive SQLite DB file
	dbPath := a.cfg.Server.DBPath
	if dbData, err := os.ReadFile(dbPath); err == nil {
		w, _ := zipWriter.Create("goassistant.db")
		_, _ = w.Write(dbData)
	}

	// 2. Archive all MD files
	mdFiles, _ := a.mdLoader.ListFiles()
	for _, fname := range mdFiles {
		if content, err := a.mdLoader.GetFile(fname); err == nil {
			w, _ := zipWriter.Create(filepath.Join("md", fname))
			_, _ = w.Write([]byte(content))
		}
	}

	_ = zipWriter.Close()

	doc := &tele.Document{
		File:     tele.FromReader(&buf),
		FileName: fmt.Sprintf("goassistant_backup_%s.zip", time.Now().Format("20060102_150405")),
		Caption:  "💾 Full Backup GoAssistant (SQLite Database + Markdown Configs)",
	}

	return c.Send(doc)
}
