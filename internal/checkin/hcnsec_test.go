package checkin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckinUserWithCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stringsContains := r.URL.Path == "/api/user/login"; stringsContains {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["username"] == "admin@example.com" && body["password"] == "secret123" {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid_session_token"})
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(LoginResponse{
					Success: true,
					Data: UserData{
						ID:       41863,
						Username: "admin_user",
					},
				})
				return
			}
			http.Error(w, `{"success":false,"message":"invalid credentials"}`, http.StatusOK)
			return
		}

		if r.URL.Path == "/api/user/checkin" {
			newUser := r.Header.Get("New-Api-User")
			if newUser != "41863" {
				http.Error(w, `{"success":false,"message":"unauthorized user"}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(CheckinResponse{
				Success: true,
				Message: "签到成功",
				Data:    json.RawMessage(`{"quota_awarded":500000}`),
			})
			return
		}

		if r.URL.Path == "/api/user/self" {
			newUser := r.Header.Get("New-Api-User")
			if newUser != "41863" {
				http.Error(w, `{"success":false,"message":"unauthorized user"}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(UserInfoResponse{
				Success: true,
				Message: "",
				Data: UserData{
					ID:          41863,
					Username:    "admin_user",
					DisplayName: "Admin User",
					Quota:       1000000,
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	svc := &Service{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	res, err := svc.CheckinUser(context.Background(), "admin@example.com:secret123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.Success {
		t.Errorf("expected success true, got false (error=%s, msg=%s)", res.Error, res.Message)
	}

	if res.UserID != "41863" {
		t.Errorf("expected userID 41863, got %s", res.UserID)
	}

	if res.BalanceAfter != 2.0 {
		t.Errorf("expected balance $2.0, got %f", res.BalanceAfter)
	}

	if res.AwardedDollar != 1.0 {
		t.Errorf("expected awarded $1.0, got %f", res.AwardedDollar)
	}
}

func TestQuotaToDollar(t *testing.T) {
	cases := []struct {
		quota    int64
		expected float64
	}{
		{500000, 1.0},
		{250000, 0.5},
		{1000000, 2.0},
		{0, 0.0},
	}

	for _, tc := range cases {
		got := QuotaToDollar(tc.quota)
		if got != tc.expected {
			t.Errorf("QuotaToDollar(%d) = %f, expected %f", tc.quota, got, tc.expected)
		}
	}
}
