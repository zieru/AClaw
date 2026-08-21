package admin

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	d1 := 45 * time.Second
	if s := formatDuration(d1); s != "45s" {
		t.Errorf("expected '45s', got '%s'", s)
	}

	d2 := 2*time.Minute + 15*time.Second
	if s := formatDuration(d2); s != "2m 15s" {
		t.Errorf("expected '2m 15s', got '%s'", s)
	}

	d3 := 3*time.Hour + 20*time.Minute + 10*time.Second
	if s := formatDuration(d3); s != "3j 20m 10s" {
		t.Errorf("expected '3j 20m 10s', got '%s'", s)
	}

	d4 := 26*time.Hour + 10*time.Minute + 5*time.Second
	if s := formatDuration(d4); s != "1h 2j 10m 5s" {
		t.Errorf("expected '1h 2j 10m 5s', got '%s'", s)
	}
}
