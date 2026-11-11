package provider

import (
	"context"
	"time"
)

// DefaultAttemptTimeout defines the balanced attempt timeout (Option A: 50 seconds)
const DefaultAttemptTimeout = 50 * time.Second

// GetRemainingContextTime returns the remaining duration before the context reaches its deadline
func GetRemainingContextTime(ctx context.Context) (time.Duration, bool) {
	if ctx == nil {
		return 0, false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0, true
	}
	return remaining, true
}

// CalculateAttemptTimeout calculates a safe timeout for a single LLM attempt.
// It ensures that one slow/hanging provider call does not starve the parent context,
// leaving enough reserve time (minReserve) for retries, tool execution, or fallback.
func CalculateAttemptTimeout(ctx context.Context, maxPerAttempt time.Duration, minReserve time.Duration) time.Duration {
	if maxPerAttempt <= 0 {
		maxPerAttempt = DefaultAttemptTimeout
	}
	if minReserve <= 0 {
		minReserve = 15 * time.Second
	}

	remaining, hasDeadline := GetRemainingContextTime(ctx)
	if !hasDeadline {
		return maxPerAttempt
	}

	// If remaining context time is less than minReserve, give whatever is left (minimum 2s)
	if remaining <= minReserve {
		if remaining > 2*time.Second {
			return remaining - 500*time.Millisecond
		}
		return remaining
	}

	// Usable time for this attempt without encroaching on minReserve
	usableTime := remaining - minReserve
	if usableTime < maxPerAttempt {
		if usableTime < 2*time.Second {
			return 2 * time.Second
		}
		return usableTime
	}

	return maxPerAttempt
}

// CanAffordWait checks whether the context has enough remaining lifetime to safely sleep for waitDur
// while still retaining a buffer (minReserve) to complete execution.
func CanAffordWait(ctx context.Context, waitDur time.Duration, minReserve time.Duration) bool {
	if minReserve <= 0 {
		minReserve = 2500 * time.Millisecond
	}
	remaining, hasDeadline := GetRemainingContextTime(ctx)
	if !hasDeadline {
		return true // No deadline on context, can always wait
	}

	return remaining > (waitDur + minReserve)
}
