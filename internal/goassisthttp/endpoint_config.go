package goassisthttp

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PaginationConfig menyimpan konfigurasi pagination untuk endpoint
type PaginationConfig struct {
	DefaultPage  int    `yaml:"default_page"`  // Default page (misal: 1)
	DefaultLimit int    `yaml:"default_limit"` // Default limit (misal: 10)
	MaxLimit     int    `yaml:"max_limit"`     // Batas maksimal limit (misal: 100)
	PassAs       string `yaml:"pass_as"`       // "page" (--page & --limit) atau "offset" (--offset & --limit)
}

// EndpointItem mendefinisikan konfigurasi 1 rute API
type EndpointItem struct {
	Path           string            `yaml:"path"`            // URL path, misal: "/api/datafunneling/funneling"
	Method         string            `yaml:"method"`          // HTTP Method: GET, POST, dll (default GET)
	Binary         string            `yaml:"binary"`          // Nama executable binary (default: "g3a")
	Command        string            `yaml:"command"`         // Argumen perintah dasar CLI (misal: "/datafunneling/funneling")
	Type           string            `yaml:"type"`            // "pagination" atau "regular"
	TimeoutSeconds int               `yaml:"timeout_seconds"` // Timeout eksekusi binary dalam detik
	Defaults       map[string]string `yaml:"defaults"`        // Default flags CLI jika query param tidak diberikan
	Pagination     PaginationConfig  `yaml:"pagination"`      // Konfigurasi khusus pagination
}

// EndpointsConfig struktur utama file configs/endpoints.yaml
type EndpointsConfig struct {
	Endpoints []EndpointItem `yaml:"endpoints"`
}

// LoadEndpointsConfig membaca dan mem-parsing file konfigurasi endpoints.yaml
func LoadEndpointsConfig(filePath string) (*EndpointsConfig, error) {
	cfg := &EndpointsConfig{}
	if filePath == "" {
		filePath = "configs/endpoints.yaml"
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca endpoints config %s: %w", filePath, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("gagal parsing yaml endpoints %s: %w", filePath, err)
	}

	// Terapkan default fallback untuk setiap endpoint
	for i := range cfg.Endpoints {
		ep := &cfg.Endpoints[i]
		if ep.Method == "" {
			ep.Method = "GET"
		}
		if ep.Binary == "" {
			ep.Binary = "g3a"
		}
		if ep.Type == "" {
			ep.Type = "regular"
		}
		if ep.TimeoutSeconds <= 0 {
			ep.TimeoutSeconds = 30
		}
		if ep.Defaults == nil {
			ep.Defaults = make(map[string]string)
		}

		if ep.Type == "pagination" {
			if ep.Pagination.DefaultPage <= 0 {
				ep.Pagination.DefaultPage = 1
			}
			if ep.Pagination.DefaultLimit <= 0 {
				ep.Pagination.DefaultLimit = 10
			}
			if ep.Pagination.MaxLimit <= 0 {
				ep.Pagination.MaxLimit = 100
			}
			if ep.Pagination.PassAs == "" {
				ep.Pagination.PassAs = "page"
			}
		}
	}

	return cfg, nil
}
