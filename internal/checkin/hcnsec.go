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
	"net/http/cookiejar"
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
	QuotaPerUnit       = 500000.0                  // 500000 quota = $1.0
)

// LoginResponse represents API response from /api/user/login
type LoginResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Data    UserData `json:"data"`
}

// CheckinResponse represents the API response from /api/user/checkin
type CheckinResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type CheckinAwardData struct {
	QuotaAwarded int64 `json:"quota_awarded"`
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
	Account       string    `json:"account"` // email or username or masked auth
	UserID        string    `json:"user_id"`
	Username      string    `json:"username"`
	Success       bool      `json:"success"`
	AlreadyDone   bool      `json:"already_done"`
	Message       string    `json:"message"`
	QuotaBefore   int64     `json:"quota_before,omitempty"`
	QuotaAfter    int64     `json:"quota_after,omitempty"`
	BalanceBefore float64   `json:"balance_before,omitempty"`
	BalanceAfter  float64   `json:"balance_after,omitempty"`
	AwardedDollar float64   `json:"awarded_dollar,omitempty"`
	AwardedQuota  int64     `json:"awarded_quota,omitempty"`
	ExecutedAt    time.Time `json:"executed_at"`
	Error         string    `json:"error,omitempty"`
}

// QuotaToDollar converts OneAPI/NewAPI integer quota to dollar representation ($1 = 500000 quota)
func QuotaToDollar(quota int64) float64 {
	return float64(quota) / QuotaPerUnit
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
	jar, _ := cookiejar.New(nil)
	return &Service{
		db: db,
		httpClient: &http.Client{
			Jar:     jar,
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

// ListUserIDs returns the configured accounts / tokens / user IDs
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

// SaveUserIDs persists the list of accounts / tokens
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

// AddUserID adds a new account (email:password or session cookie)
func (s *Service) AddUserID(account string) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return errors.New("input akun tidak boleh kosong")
	}
	users := s.ListUserIDs()
	for _, u := range users {
		if u == account {
			return fmt.Errorf("akun '%s' sudah terdaftar", maskSecret(account))
		}
	}
	users = append(users, account)
	return s.SaveUserIDs(users)
}

// RemoveUserID removes an account by exact string or index
func (s *Service) RemoveUserID(account string) error {
	account = strings.TrimSpace(account)
	users := s.ListUserIDs()
	var updated []string
	var found bool
	for _, u := range users {
		if u == account || maskSecret(u) == account {
			found = true
			continue
		}
		updated = append(updated, u)
	}
	if !found {
		return fmt.Errorf("akun '%s' tidak ditemukan dalam daftar", account)
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

// Login performs authentication on HCNSEC and returns session cookie jar and user data
func (s *Service) Login(ctx context.Context, client *http.Client, username, password string) (*UserData, string, error) {
	loginURL := fmt.Sprintf("%s/api/user/login?turnstile=", s.baseURL)
	payload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", loginURL, bytes.NewReader(payload))
	if err != nil {
		return nil, "", fmt.Errorf("gagal membuat request login: %w", err)
	}

	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Origin", s.baseURL)
	req.Header.Set("Referer", fmt.Sprintf("%s/login", s.baseURL))

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("gagal request login: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("gagal membaca response login: %w", err)
	}

	var loginResp LoginResponse
	if err := json.Unmarshal(bodyBytes, &loginResp); err != nil {
		return nil, "", fmt.Errorf("HTTP %d: parse error login (%s)", resp.StatusCode, string(bodyBytes))
	}

	if !loginResp.Success {
		return nil, "", errors.New(loginResp.Message)
	}

	// Capture cookies string
	var cookieParts []string
	for _, c := range resp.Cookies() {
		cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	cookieStr := strings.Join(cookieParts, "; ")

	return &loginResp.Data, cookieStr, nil
}

// CheckinUser executes full login -> get info before -> checkin -> get info after
func (s *Service) CheckinUser(ctx context.Context, rawInput string) (*CheckinResult, error) {
	rawInput = strings.TrimSpace(rawInput)
	if rawInput == "" {
		return nil, errors.New("input akun tidak boleh kosong")
	}

	result := &CheckinResult{
		Account:    maskSecret(rawInput),
		ExecutedAt: time.Now(),
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	var userID string
	var username string
	var sessionCookies string

	// Case 1: credentials format "username:password" or "email:password"
	if colonIdx := strings.Index(rawInput, ":"); colonIdx > 0 && !strings.HasPrefix(rawInput, "session=") {
		u := rawInput[:colonIdx]
		p := rawInput[colonIdx+1:]

		userData, cookies, err := s.Login(ctx, client, u, p)
		if err != nil {
			result.Error = fmt.Sprintf("Login gagal: %v", err)
			return result, err
		}

		userID = fmt.Sprintf("%d", userData.ID)
		username = userData.Username
		sessionCookies = cookies
		result.UserID = userID
		result.Username = username
		result.Account = u
	} else {
		// Case 2: raw session cookie (e.g. "session=...") or "id|session=..."
		if strings.Contains(rawInput, "|") {
			parts := strings.SplitN(rawInput, "|", 2)
			userID = strings.TrimSpace(parts[0])
			sessionCookies = strings.TrimSpace(parts[1])
		} else {
			sessionCookies = rawInput
		}
	}

	// 1. Get info before checkin
	infoBefore, _ := s.GetUserInfoWithClient(ctx, client, userID, sessionCookies)
	if infoBefore != nil {
		result.QuotaBefore = infoBefore.Quota
		result.BalanceBefore = QuotaToDollar(infoBefore.Quota)
		if result.Username == "" {
			result.Username = infoBefore.Username
		}
		if result.UserID == "" && infoBefore.ID != 0 {
			result.UserID = fmt.Sprintf("%d", infoBefore.ID)
		}
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
	req.Header.Set("Referer", fmt.Sprintf("%s/", s.baseURL))
	if userID != "" {
		req.Header.Set("New-Api-User", userID)
	}
	if sessionCookies != "" {
		req.Header.Set("Cookie", sessionCookies)
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("koneksi checkin gagal: %v", err)
		return result, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("gagal membaca response checkin: %v", err)
		return result, err
	}

	var checkinResp CheckinResponse
	if err := json.Unmarshal(bodyBytes, &checkinResp); err != nil {
		result.Error = fmt.Sprintf("HTTP %d: response invalid (%s)", resp.StatusCode, string(bodyBytes))
		return result, errors.New(result.Error)
	}

	result.Success = checkinResp.Success
	result.Message = checkinResp.Message

	if strings.Contains(result.Message, "已签到") || strings.Contains(result.Message, "重复签到") || strings.Contains(result.Message, "今天已签到") {
		result.AlreadyDone = true
	}

	if checkinResp.Success && len(checkinResp.Data) > 0 {
		var award CheckinAwardData
		if jsonErr := json.Unmarshal(checkinResp.Data, &award); jsonErr == nil && award.QuotaAwarded > 0 {
			result.AwardedQuota = award.QuotaAwarded
			result.AwardedDollar = QuotaToDollar(award.QuotaAwarded)
		}
	}

	// 3. Get info after checkin
	infoAfter, err := s.GetUserInfoWithClient(ctx, client, userID, sessionCookies)
	if err == nil && infoAfter != nil {
		result.QuotaAfter = infoAfter.Quota
		result.BalanceAfter = QuotaToDollar(infoAfter.Quota)
		if result.Username == "" {
			result.Username = infoAfter.Username
		}
		if result.UserID == "" && infoAfter.ID != 0 {
			result.UserID = fmt.Sprintf("%d", infoAfter.ID)
		}
		if result.Success && result.AwardedDollar == 0 && result.QuotaBefore > 0 {
			diff := result.QuotaAfter - result.QuotaBefore
			if diff > 0 {
				result.AwardedQuota = diff
				result.AwardedDollar = QuotaToDollar(diff)
			}
		}
	}

	return result, nil
}

// GetUserInfoWithClient fetches current user info with custom client & cookie
func (s *Service) GetUserInfoWithClient(ctx context.Context, client *http.Client, userID string, cookies string) (*UserData, error) {
	infoURL := fmt.Sprintf("%s/api/user/self", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", infoURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Origin", s.baseURL)
	req.Header.Set("Referer", fmt.Sprintf("%s/", s.baseURL))
	if userID != "" {
		req.Header.Set("New-Api-User", userID)
	}
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}

	resp, err := client.Do(req)
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

// RunAll executes checkin for all configured accounts and returns structured results and HTML report
func (s *Service) RunAll(ctx context.Context) ([]*CheckinResult, string) {
	accounts := s.ListUserIDs()
	if len(accounts) == 0 {
		return nil, "⚠️ Tidak ada akun yang terdaftar. Tambahkan akun via <code>/checkin_add username:password</code>"
	}

	var results []*CheckinResult
	var reportBuilder strings.Builder

	locWIB, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		locWIB = time.FixedZone("WIB", 7*3600)
	}
	nowWIB := time.Now().In(locWIB)

	reportBuilder.WriteString("🎁 <b>iamhc / HCNSEC 签到通知 (Check-in Report)</b>\n")
	reportBuilder.WriteString(fmt.Sprintf("⏰ <i>Waktu: %s</i>\n\n", nowWIB.Format("2006-01-02 15:04:05 WIB")))

	for i, acc := range accounts {
		res, _ := s.CheckinUser(ctx, acc)
		results = append(results, res)

		accountName := res.Username
		if accountName == "" {
			accountName = res.Account
		}
		if res.UserID != "" {
			accountName = fmt.Sprintf("%s (ID: %s)", accountName, res.UserID)
		}

		reportBuilder.WriteString(fmt.Sprintf("<b>#%d 👤 Akun:</b> <code>%s</code>\n", i+1, accountName))

		if res.Success {
			reportBuilder.WriteString(fmt.Sprintf("• Status: 🟢 <b>Check-in Berhasil!</b> (+$%.2f / +%d quota)\n", res.AwardedDollar, res.AwardedQuota))
			reportBuilder.WriteString(fmt.Sprintf("• Saldo Awal: <code>$%.2f</code> (%d quota)\n", res.BalanceBefore, res.QuotaBefore))
			reportBuilder.WriteString(fmt.Sprintf("• Saldo Akhir: <code>$%.2f</code> (%d quota)\n", res.BalanceAfter, res.QuotaAfter))
		} else if res.AlreadyDone {
			reportBuilder.WriteString("• Status: 🟡 <b>Sudah Check-in Hari Ini</b>\n")
			reportBuilder.WriteString(fmt.Sprintf("• Saldo Saat Ini: <code>$%.2f</code> (%d quota)\n", res.BalanceAfter, res.QuotaAfter))
		} else {
			errMsg := res.Message
			if errMsg == "" && res.Error != "" {
				errMsg = res.Error
			}
			reportBuilder.WriteString(fmt.Sprintf("• Status: 🔴 <b>Gagal:</b> %s\n", errMsg))
		}
		reportBuilder.WriteString("\n")

		log.Printf("🎁 [Checkin] %s: Success=%v, Done=%v, Award=+$%.2f, Quota=%d ($%.2f)",
			res.Account, res.Success, res.AlreadyDone, res.AwardedDollar, res.QuotaAfter, res.BalanceAfter)
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
						log.Printf("🚀 [Checkin] Memulai auto checkin harian untuk %d akun...", len(users))
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

func maskSecret(raw string) string {
	if colonIdx := strings.Index(raw, ":"); colonIdx > 0 {
		user := raw[:colonIdx]
		return fmt.Sprintf("%s:******", user)
	}
	if len(raw) > 12 {
		return fmt.Sprintf("%s...%s", raw[:4], raw[len(raw)-4:])
	}
	return raw
}
