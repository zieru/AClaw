package webadmin

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed ui/*
var uiFS embed.FS

// GetUIFileSystem returns the HTTP filesystem for embedded static UI assets
func GetUIFileSystem() (http.FileSystem, error) {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}
