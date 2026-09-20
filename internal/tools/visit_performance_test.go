package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestVisitPerformanceTool_Schema(t *testing.T) {
	tool := &VisitPerformanceTool{}

	if tool.Name() != "capture_visit_performance" {
		t.Fatalf("expected tool name 'capture_visit_performance', got '%s'", tool.Name())
	}

	params := tool.Parameters()
	if params.Type != "object" {
		t.Errorf("expected params type 'object', got '%s'", params.Type)
	}

	if _, ok := params.Properties["month"]; !ok {
		t.Errorf("expected property 'month' to be defined")
	}

	if _, ok := params.Properties["flag"]; !ok {
		t.Errorf("expected property 'flag' to be defined")
	}

	if _, ok := params.Properties["subjek_serving"]; !ok {
		t.Errorf("expected property 'subjek_serving' to be defined")
	}

	if _, ok := params.Properties["capture_screenshot"]; !ok {
		t.Errorf("expected property 'capture_screenshot' to be defined")
	}

	if _, ok := params.Properties["section"]; !ok {
		t.Errorf("expected property 'section' to be defined")
	}
}

func TestVisitPerformanceTool_Helpers(t *testing.T) {
	// Test getPrevMonth
	if prev := getPrevMonth("2026-03"); prev != "2026-02" {
		t.Errorf("expected '2026-02', got '%s'", prev)
	}
	if prev := getPrevMonth("2026-01"); prev != "2025-12" {
		t.Errorf("expected '2025-12', got '%s'", prev)
	}
	if prev := getPrevMonth("invalid"); prev != "" {
		t.Errorf("expected empty string for invalid month, got '%s'", prev)
	}

	// Test formatNumber
	if formatted := formatNumber(1488520); formatted != "1.488.520" {
		t.Errorf("expected '1.488.520', got '%s'", formatted)
	}
	if formatted := formatNumber(500); formatted != "500" {
		t.Errorf("expected '500', got '%s'", formatted)
	}

	// Test buildVisitWhereQuery
	where := buildVisitWhereQuery("2026-03", "Dilayani", "> 1 Menit")
	if where == "" {
		t.Errorf("expected where query to not be empty")
	}
}

func TestVisitPerformanceTool_Execute_Mock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "performance-total"):
			w.Write([]byte(`{"status":"success","output":{"rows":[{"total_visit":1000,"avg_waiting_minutes":4.5,"avg_serving_minutes":12.3}]}}`))
		case strings.Contains(r.URL.Path, "performance-regional"):
			w.Write([]byte(`{"status":"success","output":{"rows":[{"regional":"Sumbagut","total_visit":600,"avg_waiting_minutes":5.0,"avg_serving_minutes":13.0},{"regional":"Sumbagsel","total_visit":400,"avg_waiting_minutes":3.8,"avg_serving_minutes":11.2}]}}`))
		case strings.Contains(r.URL.Path, "performance-territory"):
			w.Write([]byte(`{"status":"success","output":{"rows":[{"territory":"MEDAN","regional":"Sumbagut","total_visit":600,"avg_waiting_minutes":7.2,"avg_serving_minutes":14.0}]}}`))
		default:
			w.Write([]byte(`{"status":"success","output":{"rows":[]}}`))
		}
	}))
	defer server.Close()

	os.Setenv("GOASSIST_API_URL", server.URL)
	defer os.Unsetenv("GOASSIST_API_URL")

	tool := &VisitPerformanceTool{}
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"month":              "2026-03",
		"capture_screenshot": false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(res, "TOTAL AREA SUMATERA") && !strings.Contains(res, "Total Area Sumatera") {
		t.Errorf("expected output to contain Total Area Sumatera, got: %s", res)
	}
	if !strings.Contains(res, "1.000") {
		t.Errorf("expected output to contain formatted total visit '1.000', got: %s", res)
	}
	if !strings.Contains(res, "Sumbagut") {
		t.Errorf("expected output to contain 'Sumbagut', got: %s", res)
	}
	if !strings.Contains(res, "MEDAN") {
		t.Errorf("expected output to contain 'MEDAN', got: %s", res)
	}
}

func TestVisitPerformanceTool_Execute_Live(t *testing.T) {
	tool := &VisitPerformanceTool{}
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"month":              "2026-03",
		"capture_screenshot": false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("Live tool result:\n%s", res)
	if !strings.Contains(res, "DATA METRIK VISIT PERFORMANCE") {
		t.Errorf("expected output to contain data metric header, got: %s", res)
	}
}

