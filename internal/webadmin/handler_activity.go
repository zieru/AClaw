package webadmin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/storage"
)

type ActivityListResponse struct {
	Items []storage.AuditLogRecord `json:"items"`
	Total int                      `json:"total"`
	Page  int                      `json:"page"`
	Limit int                      `json:"limit"`
}

type SystemStatsResponse struct {
	UptimeSeconds   int64             `json:"uptime_seconds"`
	UptimeFormatted string            `json:"uptime_formatted"`
	MemoryAllocMB   float64           `json:"memory_alloc_mb"`
	MemorySysMB     float64           `json:"memory_sys_mb"`
	Goroutines      int               `json:"goroutines"`
	GoVersion       string            `json:"go_version"`
	TotalAuditLogs  int               `json:"total_audit_logs"`
	CurrentWebPort  int               `json:"current_web_port"`
	ActiveProviders []string          `json:"active_providers"`
	ChannelStats    map[string]int    `json:"channel_stats"`
	ServerTime      string            `json:"server_time"`
}

func (s *Server) handleListActivities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}

	channel := strings.TrimSpace(q.Get("channel"))
	status := strings.TrimSpace(q.Get("status"))
	search := strings.TrimSpace(q.Get("search"))

	offset := (page - 1) * limit

	items, total, err := s.db.ListAuditLogsFiltered(channel, status, search, limit, offset)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if items == nil {
		items = []storage.AuditLogRecord{}
	}

	resp := ActivityListResponse{
		Items: items,
		Total: total,
		Page:  page,
		Limit: limit,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGetActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		// Attempt to extract from path: /api/activities/detail?id=... or /api/activities/<id>
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 {
			id = parts[len(parts)-1]
		}
	}

	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Parameter id diperlukan"})
		return
	}

	logRec, err := s.db.GetAuditLogByID(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Log tidak ditemukan"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"item": logRec})
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptime := time.Since(s.startTime)
	hours := int(uptime.Hours())
	mins := int(uptime.Minutes()) % 60
	secs := int(uptime.Seconds()) % 60
	uptimeStr := strings.TrimSpace(strings.ReplaceAll(
		strings.TrimSpace(
			strings.Join([]string{
				strconv.Itoa(hours) + "j",
				strconv.Itoa(mins) + "m",
				strconv.Itoa(secs) + "d",
			}, " "),
		), "0j ", ""))

	totalLogs, _ := s.db.CountAuditLogs()

	activeProvNames := []string{}
	if s.orchestrator != nil {
		// Check db providers
		if provs, err := s.db.ListProviders(); err == nil {
			for _, p := range provs {
				if p.IsActive {
					activeProvNames = append(activeProvNames, p.Name)
				}
			}
		}
	}

	channelStats := make(map[string]int)
	if channels, err := s.db.ListChannels(); err == nil {
		for _, ch := range channels {
			if ch.IsActive {
				channelStats[ch.Type]++
			}
		}
	}

	resp := SystemStatsResponse{
		UptimeSeconds:   int64(uptime.Seconds()),
		UptimeFormatted: uptimeStr,
		MemoryAllocMB:   float64(m.Alloc) / 1024 / 1024,
		MemorySysMB:     float64(m.Sys) / 1024 / 1024,
		Goroutines:      runtime.NumGoroutine(),
		GoVersion:       runtime.Version(),
		TotalAuditLogs:  totalLogs,
		CurrentWebPort:  s.GetPort(),
		ActiveProviders: activeProvNames,
		ChannelStats:    channelStats,
		ServerTime:      time.Now().Format("2006-01-02 15:04:05"),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
