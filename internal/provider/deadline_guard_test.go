package provider

import (
	"context"
	"testing"
	"time"
)

func TestGetRemainingContextTime(t *testing.T) {
	// No deadline
	_, has := GetRemainingContextTime(context.Background())
	if has {
		t.Errorf("expected no deadline for context.Background(), got true")
	}

	// Active deadline
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rem, has := GetRemainingContextTime(ctx)
	if !has {
		t.Fatalf("expected active deadline, got false")
	}
	if rem < 18*time.Second || rem > 21*time.Second {
		t.Errorf("expected remaining time ~20s, got %v", rem)
	}

	// Expired deadline
	expCtx, expCancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer expCancel()

	remExp, hasExp := GetRemainingContextTime(expCtx)
	if !hasExp {
		t.Fatalf("expected has deadline for expired context, got false")
	}
	if remExp != 0 {
		t.Errorf("expected 0 for expired context, got %v", remExp)
	}
}

func TestCalculateAttemptTimeout(t *testing.T) {
	// 1. No deadline -> returns maxPerAttempt
	to1 := CalculateAttemptTimeout(context.Background(), 50*time.Second, 15*time.Second)
	if to1 != 50*time.Second {
		t.Errorf("expected 50s, got %v", to1)
	}

	// 2. Abundant time (120s remaining), max 50s, reserve 15s -> returns 50s
	ctxLong, cancelLong := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancelLong()

	to2 := CalculateAttemptTimeout(ctxLong, 50*time.Second, 15*time.Second)
	if to2 != 50*time.Second {
		t.Errorf("expected 50s, got %v", to2)
	}

	// 3. Constrained time (40s remaining), max 50s, reserve 15s -> usable is ~25s
	ctxMedium, cancelMedium := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancelMedium()

	to3 := CalculateAttemptTimeout(ctxMedium, 50*time.Second, 15*time.Second)
	if to3 < 24*time.Second || to3 > 26*time.Second {
		t.Errorf("expected ~25s usable, got %v", to3)
	}

	// 4. Critical time (10s remaining), reserve 15s -> should provide remaining minus safety margin (~9.5s)
	ctxShort, cancelShort := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShort()

	to4 := CalculateAttemptTimeout(ctxShort, 50*time.Second, 15*time.Second)
	if to4 < 8*time.Second || to4 > 10*time.Second {
		t.Errorf("expected ~9.5s for critical time, got %v", to4)
	}
}

func TestCanAffordWait(t *testing.T) {
	// No deadline can always afford wait
	if !CanAffordWait(context.Background(), 30*time.Second, 2500*time.Millisecond) {
		t.Errorf("expected true for context without deadline")
	}

	// Context with 30s remaining:
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait 10s + reserve 2.5s = 12.5s (< 30s) -> should be true
	if !CanAffordWait(ctx, 10*time.Second, 2500*time.Millisecond) {
		t.Errorf("expected true when remaining time (30s) > wait (10s) + reserve (2.5s)")
	}

	// Wait 28s + reserve 2.5s = 30.5s (> 30s) -> should be false
	if CanAffordWait(ctx, 28*time.Second, 2500*time.Millisecond) {
		t.Errorf("expected false when remaining time (30s) < wait (28s) + reserve (2.5s)")
	}
}
