package provider

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxRetryAfter caps how long a provider may ask the caller to wait.
const maxRetryAfter = 5 * time.Minute

// Error is what a provider returns for a failed request. Retryable and
// RetryAfter tell the agent loop whether and when to try again.
type Error struct {
	Status     int
	Code       string
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

// Error renders the failure for logs and error chains.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("provider: request failed")
	if e.Status != 0 {
		fmt.Fprintf(&b, " (status %d)", e.Status)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Retryable {
		b.WriteString(" (retryable)")
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

// Unwrap exposes the underlying cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// retryableStatus is the set of HTTP statuses a retry may help with.
var retryableStatus = map[int]bool{
	408: true, 409: true, 425: true, 429: true,
	500: true, 502: true, 503: true, 504: true,
}

// Classify reports whether a status is worth retrying and how long to wait,
// parsed from a Retry-After header. It reads the wall clock; tests use
// classifyAt for a fixed now.
func Classify(status int, retryAfterHeader string) (bool, time.Duration) {
	return classifyAt(time.Now(), status, retryAfterHeader)
}

func classifyAt(now time.Time, status int, header string) (bool, time.Duration) {
	return retryableStatus[status], parseRetryAfter(now, header)
}

// parseRetryAfter accepts delta-seconds or an HTTP date. Anything unparseable
// or already in the past is zero, and the result never exceeds maxRetryAfter.
func parseRetryAfter(now time.Time, header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		// Cap before multiplying so a huge number cannot overflow Duration.
		if secs >= int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := when.Sub(now); d > 0 {
			return capRetryAfter(d)
		}
	}
	return 0
}

func capRetryAfter(d time.Duration) time.Duration {
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}
