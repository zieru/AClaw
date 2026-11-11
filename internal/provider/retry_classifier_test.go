package provider

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		err      error
		expected ErrorCategory
	}{
		{
			err:      errors.New("bifrost: status 429"),
			expected: CategoryRateLimit,
		},
		{
			err:      errors.New("Resource has been exhausted (e.g. check quota)"),
			expected: CategoryRateLimit,
		},
		{
			err:      errors.New("Rate limit reached for requests per minute (RPM). Please try again in 14.5s"),
			expected: CategoryRateLimit,
		},
		{
			err:      errors.New("bifrost: provider_credentials_exhausted"),
			expected: CategoryRateLimit,
		},
		{
			err:      errors.New("bifrost: status 503"),
			expected: CategoryServerOverloaded,
		},
		{
			err:      errors.New("bifrost: status 529 site overloaded"),
			expected: CategoryServerOverloaded,
		},
		{
			err:      errors.New("context_length_exceeded: maximum context length is 8192 tokens"),
			expected: CategoryContextExceeded,
		},
		{
			err:      errors.New("prompt is too long for model"),
			expected: CategoryContextExceeded,
		},
		{
			err:      errors.New("stream stalled: no chunks for 15s"),
			expected: CategoryStreamStalled,
		},
		{
			err:      errors.New("unexpected EOF"),
			expected: CategoryStreamStalled,
		},
		{
			err:      errors.New("bifrost: status 401: invalid api key"),
			expected: CategoryAuthFatal,
		},
		{
			err:      errors.New("provider_auth_failed: authentication_error"),
			expected: CategoryAuthFatal,
		},
	}

	for _, tc := range tests {
		actual := ClassifyError(tc.err)
		if actual != tc.expected {
			t.Errorf("ClassifyError(%q) = %s; want %s", tc.err.Error(), actual, tc.expected)
		}
	}
}

func TestParseRetryAfterDuration(t *testing.T) {
	// Test explicit second parsing
	d1 := ParseRetryAfterDuration("Rate limit reached. Please try again in 14.5s", 0, 5*time.Second, 60*time.Second)
	if d1 != 14500*time.Millisecond {
		t.Errorf("expected 14.5s, got %v", d1)
	}

	// Test explicit seconds with word
	d2 := ParseRetryAfterDuration("quota exceeded, retry after 20 seconds", 0, 5*time.Second, 60*time.Second)
	if d2 != 20*time.Second {
		t.Errorf("expected 20s, got %v", d2)
	}

	// Test minimum clamp for small ms
	d3 := ParseRetryAfterDuration("please try again in 200ms", 0, 5*time.Second, 60*time.Second)
	if d3 < 1500*time.Millisecond {
		t.Errorf("expected at least 1.5s clamp, got %v", d3)
	}

	// Test max cap
	d4 := ParseRetryAfterDuration("wait 5m", 0, 5*time.Second, 30*time.Second)
	if d4 != 30*time.Second {
		t.Errorf("expected 30s cap, got %v", d4)
	}

	// Test exponential backoff when no duration in string
	base := 5 * time.Second
	max := 45 * time.Second
	att0 := ParseRetryAfterDuration("generic rate limit error without time", 0, base, max)
	if att0 != 5*time.Second {
		t.Errorf("attempt 0 expected 5s, got %v", att0)
	}
	att1 := ParseRetryAfterDuration("generic rate limit error without time", 1, base, max)
	if att1 != 10*time.Second {
		t.Errorf("attempt 1 expected 10s, got %v", att1)
	}
	att2 := ParseRetryAfterDuration("generic rate limit error without time", 2, base, max)
	if att2 != 20*time.Second {
		t.Errorf("attempt 2 expected 20s, got %v", att2)
	}
}
