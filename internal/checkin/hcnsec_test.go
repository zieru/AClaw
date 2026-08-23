package checkin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckinUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newUser := r.Header.Get("New-Api-User")
		if newUser != "1001" {
			http.Error(w, `{"success":false,"message":"unauthorized user"}`, http.StatusUnauthorized)
			return
		}

		if r.URL.Path == "/api/user/checkin" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(CheckinResponse{
				Success: true,
				Message: "签到成功，获得 1000 额度",
			})
			return
		}

		if r.URL.Path == "/api/user/self" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(UserInfoResponse{
				Success: true,
				Message: "",
				Data: UserData{
					ID:          1001,
					Username:    "testuser",
					DisplayName: "Test User",
					Quota:       500000,
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

	res, err := svc.CheckinUser(context.Background(), "1001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.Success {
		t.Errorf("expected success true, got false")
	}

	if res.QuotaAfter != 500000 {
		t.Errorf("expected quota 500000, got %d", res.QuotaAfter)
	}

	if res.BalanceDollar != 1.0 {
		t.Errorf("expected balance $1.0, got %f", res.BalanceDollar)
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
