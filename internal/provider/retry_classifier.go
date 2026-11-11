package provider

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrorCategory categorizes errors returned by LLM providers to dictate recovery actions
type ErrorCategory string

const (
	CategoryRateLimit        ErrorCategory = "rate_limit"        // HTTP 429, RPM/TPM limit, ResourceExhausted, Quota
	CategoryServerOverloaded ErrorCategory = "server_overload"   // HTTP 503, 529, Provider Overloaded, Server Error
	CategoryContextExceeded  ErrorCategory = "context_exceeded"  // Token context overflow, context_length_exceeded
	CategoryStreamStalled    ErrorCategory = "stream_stalled"    // Stream stalled, unexpected EOF, connection reset
	CategoryAuthFatal        ErrorCategory = "auth_fatal"        // HTTP 401, 403, invalid api key
	CategoryUnknown          ErrorCategory = "unknown"
)

var retryAfterRegex = regexp.MustCompile(`(?i)(?:try again in|retry after|wait|reset in|retry in)\s*([\d\.]+)\s*(ms|msec|millisecond|milliseconds|s|sec|second|seconds|m|min|minute|minutes)?`)

// ClassifyError categorizes an error based on its text and known provider error signatures
func ClassifyError(err error) ErrorCategory {
	if err == nil {
		return CategoryUnknown
	}
	errStr := strings.ToLower(err.Error())

	// 1. Context Length Exceeded (MUST NOT be confused with rate limit)
	if strings.Contains(errStr, "context_length_exceeded") ||
		strings.Contains(errStr, "maximum context length") ||
		(strings.Contains(errStr, "token limit") && !strings.Contains(errStr, "rate limit") && !strings.Contains(errStr, "quota")) ||
		strings.Contains(errStr, "prompt is too long") ||
		strings.Contains(errStr, "exceeds maximum input tokens") {
		return CategoryContextExceeded
	}

	// 2. Rate Limits (RPM / TPM / Quota / 429)
	if strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "ratelimit") ||
		strings.Contains(errStr, "status 429") ||
		strings.Contains(errStr, "code 429") ||
		strings.Contains(errStr, "429 too many requests") ||
		strings.Contains(errStr, "resource has been exhausted") ||
		strings.Contains(errStr, "resource_exhausted") ||
		strings.Contains(errStr, "quota exceeded") ||
		strings.Contains(errStr, "quota_exceeded") ||
		strings.Contains(errStr, "exceeded your current quota") ||
		strings.Contains(errStr, "provider_rate_limited") ||
		strings.Contains(errStr, "rate_limit_error") ||
		strings.Contains(errStr, "provider_credentials_exhausted") ||
		strings.Contains(errStr, "tpm limit") ||
		strings.Contains(errStr, "rpm limit") {
		return CategoryRateLimit
	}

	// 3. Provider Overload / Server Unavailable (503 / 529 / 502 / 504)
	if strings.Contains(errStr, "status 503") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "provider_overloaded") ||
		strings.Contains(errStr, "status 529") ||
		strings.Contains(errStr, "site overloaded") ||
		strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "bad gateway") ||
		strings.Contains(errStr, "gateway timeout") ||
		strings.Contains(errStr, "status 502") ||
		strings.Contains(errStr, "status 504") ||
		strings.Contains(errStr, "internal server error") ||
		strings.Contains(errStr, "status 500") {
		return CategoryServerOverloaded
	}

	// 4. Stream Stalled & Transient Network Issues
	if strings.Contains(errStr, "stream stalled") ||
		strings.Contains(errStr, "stream read error") ||
		strings.Contains(errStr, "unexpected eof") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "dial tcp") {
		return CategoryStreamStalled
	}

	// 5. Auth Fatal (401 / 403 / Invalid Key)
	if strings.Contains(errStr, "status 401") ||
		strings.Contains(errStr, "status 403") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "invalid api key") ||
		strings.Contains(errStr, "invalid_api_key") ||
		strings.Contains(errStr, "authentication_error") ||
		strings.Contains(errStr, "provider_auth_failed") {
		return CategoryAuthFatal
	}

	return CategoryUnknown
}

// IsRateLimitOrOverload returns true if the error is due to RPM/quota limits or server overload
func IsRateLimitOrOverload(err error) bool {
	cat := ClassifyError(err)
	return cat == CategoryRateLimit || cat == CategoryServerOverloaded
}

// IsContextLengthExceeded returns true if the error is strictly a context/token length overflow
func IsContextLengthExceeded(err error) bool {
	return ClassifyError(err) == CategoryContextExceeded
}

// IsStreamStalledOrNetwork returns true if error is a transient stream interruption or network blip
func IsStreamStalledOrNetwork(err error) bool {
	return ClassifyError(err) == CategoryStreamStalled
}

// IsAuthFatal returns true if the error is due to an invalid or revoked API key
func IsAuthFatal(err error) bool {
	return ClassifyError(err) == CategoryAuthFatal
}

// ParseRetryAfterDuration extracts wait time from error message or calculates adaptive exponential backoff
func ParseRetryAfterDuration(errStr string, attempt int, baseDelay time.Duration, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}
	if maxDelay <= 0 {
		maxDelay = 45 * time.Second
	}

	// 1. Try to extract explicit retry time from error text
	matches := retryAfterRegex.FindStringSubmatch(errStr)
	if len(matches) >= 2 {
		valStr := matches[1]
		val, err := strconv.ParseFloat(valStr, 64)
		if err == nil && val > 0 {
			unit := "s"
			if len(matches) >= 3 && matches[2] != "" {
				unit = strings.ToLower(matches[2])
			}

			var d time.Duration
			switch {
			case strings.HasPrefix(unit, "ms") || strings.HasPrefix(unit, "milli"):
				d = time.Duration(val * float64(time.Millisecond))
			case strings.HasPrefix(unit, "m"):
				d = time.Duration(val * float64(time.Minute))
			default:
				d = time.Duration(val * float64(time.Second))
			}

			// Ensure minimum 1.5 second and cap at maxDelay
			if d < 1500*time.Millisecond {
				d = 1500 * time.Millisecond
			}
			if d > maxDelay {
				d = maxDelay
			}
			return d
		}
	}

	// 2. If no explicit duration found, compute exponential backoff: baseDelay * 2^attempt
	// attempt 0 -> baseDelay (e.g. 5s)
	// attempt 1 -> baseDelay * 2 (e.g. 10s)
	// attempt 2 -> baseDelay * 4 (e.g. 20s)
	// attempt 3 -> baseDelay * 6 (capped at maxDelay)
	multiplier := math.Pow(2, float64(attempt))
	backoff := time.Duration(float64(baseDelay) * multiplier)
	if backoff > maxDelay {
		backoff = maxDelay
	}
	return backoff
}
