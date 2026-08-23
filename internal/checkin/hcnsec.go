package checkin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"goassistant/internal/storage"
)

const (
	DefaultBaseURL     = "https://api.hcnsec.cn"
	SettingUsersKey    = "checkin_hcnsec_users"
	SettingEnabledKey  = "checkin_hcnsec_enabled"
	SettingLastRunKey  = "checkin_hcnsec_last_run"
	SettingScheduleKey = "checkin_hcnsec_schedule" // e.g. "00:05"
)

// CheckinResponse represents the API response from /api/user/checkin
type CheckinResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// UserInfoResponse represents the API response from /api/user/self
type UserInfoResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Data    UserData `json:"data"`
}

// UserData holds quota and user details
type UserData struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	Quota       int64   `json:"quota"`
	UsedQuota   int64   `json:"used_quota"`
	RequestCnt  int64   `json:"request_count"`
	Group       string  `json:"group"`
	Role        int     `json:"role"`
	Status      int     `json:"status"`
}

// CheckinResult holds the aggregated result for a user check-in run
type CheckinResult struct {
	UserID        string    `json:"user_id"`
	Success       bool      `json:"success"`
	Message       string    `json:"message"`
	QuotaBefore   int64     `json:"quota_before,omitempty"`
	QuotaAfter    int64     `json:"quota_after,omitempty"`
	BalanceDollar float64   `json:"balance_dollar,omitempty"`
	Username      string    `json:"username,omitempty"`
	ExecutedAt    time.Time `json:"executed_at"`
	Error         string    `json:"error,omitempty"`
}

// QuotaToDollar converts OneAPI/NewAPI integer quota to dollar representation ($1 = 500000 quota)
func QuotaToDollar(quota int64) float64 {
	return float64(quota) / 500000.0
}

// Service manages daily automatic and on-demand check-in for HCNSEC / New API
type Service struct {
	mu         sync.RWMutex
	db         *storage.DB
	httpClient *http.Client
	baseURL    string
	stopChan   chan struct{}
	notifyCb   func(report string)
}

// NewService creates a new Checkin Service instance
func NewService(db *storage.DB, notifyCb func(report string)) *Service {
	return &Service{
		db: db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:  DefaultBaseURL,
		stopChan: make(chan struct{}),
		notifyCb: notifyCb,
	}
}

// SetNotifyCallback updates the notification callback
func (s *Service) SetNotifyCallback(cb func(report string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifyCb = cb
}

// ListUserIDs returns the configured New-Api-User IDs
func (s *Service) ListUserIDs() []string {
	if s.db == nil {
		return nil
	}
	val, err := s.db.GetSetting(SettingUsersKey, "")
	if err != nil || strings.TrimSpace(val) == "" {
		return nil
	}

	var users []string
	if strings.HasPrefix(strings.TrimSpace(val), "[") {
		_ = json.Unmarshal([]byte(val), &users)
	} else {
		for _, u := range strings.Split(val, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				users = append(users, u)
			}
		}
	}
	return users
}

// SaveUserIDs persists the list of New-Api-User IDs
func (s *Service) SaveUserIDs(users []string) error {
	if s.db == nil {
		return errors.New("database belum terhubung")
	}
	var clean []string
	seen := make(map[string]bool)
	for _, u := range users {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			clean = append(clean, u)
			seen[u] = true
		}
	}
	data, _ := json.Marshal(clean)
	return s.db.SetSetting(SettingUsersKey, string(data))
}

// AddUserID adds a new New-Api-User ID
func (s *Service) AddUserID(userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("user ID tidak boleh kosong")
	}
	users := s.ListUserIDs()
	for _, u := range users {
		if u == userID {
			return fmt.Errorf("user ID %s sudah terdaftar", userID)
		}
	}
	users = append(users, userID)
	return s.SaveUserIDs(users)
}

// RemoveUserID removes a New-Api-User ID
func (s *Service) RemoveUserID(userID string) error {
	userID = strings.TrimSpace(userID)
	users := s.ListUserIDs()
	var updated []string
	var found bool
	for _, u := range users {
		if u == userID {
			found = true
			continue
		}
		updated = append(updated, u)
	}
	if !found {
		return fmt.Errorf("user ID %s tidak ditemukan dalam daftar", userID)
	}
	return s.SaveUserIDs(updated)
}

// IsEnabled checks if checkin service is enabled
func (s *Service) IsEnabled() bool {
	if s.db == nil {
		return false
	}
	val, err := s.db.GetSetting(SettingEnabledKey, "true")
	if err != nil {
		return true
	}
	return val != "false" && val != "0"
}

// SetEnabled enables or disables auto checkin
func (s *Service) SetEnabled(enabled bool) error {
	if s.db == nil {
		return errors.New("database belum terhubung")
	}
	val := "true"
	if !enabled {
		val = "false"
	}
	return s.db.SetSetting(SettingEnabledKey, val)
}

// GetLastRun returns the timestamp of the last checkin run
func (s *Service) GetLastRun() (time.Time, error) {
	if s.db == nil {
		return time.Time{}, errors.New("database belum terhubung")
	}
	val, err := s.db.GetSetting(SettingLastRunKey, "")
	if err != nil || val == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, val)
}

// CheckinUser executes check-in for a single user ID
func (s *Service) CheckinUser(ctx context.Context, userID string) (*CheckinResult, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("user_id tidak boleh kosong")
	}

	result := &CheckinResult{
		UserID:     userID,
		ExecutedAt: time.Now(),
	}

	// 1. Optional: Get user info before checkin
	infoBefore, _ := s.GetUserInfo(ctx, userID)
	if infoBefore != nil {
		result.QuotaBefore = infoBefore.Quota
		result.Username = infoBefore.Username
	}

	// 2. Perform Checkin Request
	checkinURL := fmt.Sprintf("%s/api/user/checkin", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", checkinURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Origin", s.baseURL)
	req.Header.Set("Referer", s.baseURL+"/")
	req.Header.Set("New-Api-User", userID)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("koneksi gagal: %v", err)
		return result, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("gagal membaca response: %v", err)
		return result, err
	}

	var checkinResp CheckinResponse
	if err := json.Unmarshal(bodyBytes, &checkinResp); err != nil {
		result.Error = fmt.Sprintf("HTTP %d: format response invalid (%s)", resp.StatusCode, string(bodyBytes))
		return result, errors.New(result.Error)
	}

	result.Success = checkinResp.Success
	result.Message = checkinResp.Message

	// 3. Get updated user info after checkin
	infoAfter, err := s.GetUserInfo(ctx, userID)
	if err == nil && infoAfter != nil {
		result.QuotaAfter = infoAfter.Quota
		result.BalanceDollar = QuotaToDollar(infoAfter.Quota)
		if result.Username == "" {
			result.Username = infoAfter.Username
		}
	}

	return result, nil
}

// GetUserInfo fetches current user info & quota balance
func (s *Service) GetUserInfo(ctx context.Context, userID string) (*UserData, error) {
	infoURL := fmt.Sprintf("%s/api/user/self", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", infoURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Origin", s.baseURL)
	req.Header.Set("Referer", s.baseURL+"/")
	req.Header.Set("New-Api-User", userID)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var userResp UserInfoResponse
	if err := json.Unmarshal(bodyBytes, &userResp); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	if !userResp.Success {
		return nil, errors.New(userResp.Message)
	}

	return &userResp.Data, nil
}

// RunAll executes checkin for all configured users and formats a summary report
func (s *Service) RunAll(ctx context.Context) ([]*CheckinResult, string) {
	users := s.ListUserIDs()
	if len(users) == 0 {
		return nil, "⚠️ Tidak ada New-Api-User ID yang terdaftar. Tambahkan user ID via /checkin_add <id>"
	}

	var results []*CheckinResult
	var reportBuilder strings.Builder

	locWIB, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		locWIB = time.FixedZone("WIB", 7*3600)
	}
	nowWIB := time.Now().In(locWIB)

	reportBuilder.WriteString("📋 <b>REKAP AUTO CHECK-IN HCNSEC</b>\n")
	reportBuilder.WriteString(fmt.Sprintf("⏰ <i>Waktu: %s</i>\n\n", nowWIB.Format("2006-01-02 15:04:05 WIB")))

	for i, userID := range users {
		res, _ := s.CheckinUser(ctx, userID)
		results = append(results, res)

		statusIcon := "🟢"
		if !res.Success {
			statusIcon = "🔴"
		}

		userLabel := userID
		if res.Username != "" {
			userLabel = fmt.Sprintf("%s (@%s)", userID, res.Username)
		}

		msg := res.Message
		if msg == "" && res.Error != "" {
			msg = res.Error
		}

		reportBuilder.WriteString(fmt.Sprintf("<b>#%d User:</b> <code>%s</code>\n", i+1, userLabel))
		reportBuilder.WriteString(fmt.Sprintf("• Status: %s %s\n", statusIcon, msg))
		if res.QuotaAfter > 0 {
			reportBuilder.WriteString(fmt.Sprintf("• Saldo Kuota: <code>%d</code> (~$%.4f)\n", res.QuotaAfter, res.BalanceDollar))
		}
		reportBuilder.WriteString("\n")

		log.Printf("🎁 [Checkin] User %s: Success=%v, Msg=%s, Quota=%d ($%.4f)",
			userID, res.Success, msg, res.QuotaAfter, res.BalanceDollar)
	}

	// Update last run time in DB
	if s.db != nil {
		_ = s.db.SetSetting(SettingLastRunKey, time.Now().Format(time.RFC3339))
	}

	report := strings.TrimSpace(reportBuilder.String())
	return results, report
}

// StartBackgroundScheduler starts a daily ticker routine to execute checkin once per day
func (s *Service) StartBackgroundScheduler(ctx context.Context) {
	go func() {
		log.Println("⏰ [Checkin] Background Daily Check-in Scheduler aktif.")

		// Check every 1 minute to trigger at targeted time (default: 00:05 WIB)
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		var lastTriggerDate string

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopChan:
				return
			case t := <-ticker.C:
				if !s.IsEnabled() {
					continue
				}

				locWIB, err := time.LoadLocation("Asia/Jakarta")
				if err != nil {
					locWIB = time.FixedZone("WIB", 7*3600)
				}
				nowWIB := t.In(locWIB)
				currentDate := nowWIB.Format("2006-01-02")

				// Run once per day around 00:05 WIB (or anytime after 00:05 if not yet run today)
				if nowWIB.Hour() == 0 && nowWIB.Minute() >= 5 && lastTriggerDate != currentDate {
					users := s.ListUserIDs()
					if len(users) > 0 {
						log.Printf("🚀 [Checkin] Memulai auto checkin harian untuk %d user...", len(users))
						runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
						_, report := s.RunAll(runCtx)
						cancel()

						lastTriggerDate = currentDate

						s.mu.RLock()
						cb := s.notifyCb
						s.mu.RUnlock()

						if cb != nil && report != "" {
							cb(report)
						}
					}
				}
			}
		}
	}()
}

// Stop stops the background scheduler
func (s *Service) Stop() {
	select {
	case <-s.stopChan:
	default:
		close(s.stopChan)
	}
}
