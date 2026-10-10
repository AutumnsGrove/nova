package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProvider_PRV08_ClassifyRetryable(t *testing.T) {
	for _, status := range []int{408, 409, 425, 429, 500, 502, 503, 504} {
		if retryable, _ := Classify(status, ""); !retryable {
			t.Errorf("Classify(%d) = false, want retryable", status)
		}
	}
}

func TestProvider_PRV09_ClassifyNonRetryable(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422} {
		if retryable, _ := Classify(status, ""); retryable {
			t.Errorf("Classify(%d) = true, want not retryable", status)
		}
	}
}

func TestProvider_PRV10_RetryAfterSeconds(t *testing.T) {
	if _, after := Classify(429, "3"); after != 3*time.Second {
		t.Errorf("Retry-After 3 = %v, want 3s", after)
	}
}

func TestProvider_PRV11_RetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	header := now.Add(10 * time.Second).Format(http.TimeFormat)
	_, after := classifyAt(now, 429, header)
	if after < 9*time.Second || after > 11*time.Second {
		t.Errorf("Retry-After date = %v, want about 10s", after)
	}
}

func TestProvider_PRV12_RetryAfterGarbage(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, header := range []string{"abc", "-5"} {
		if _, after := classifyAt(now, 429, header); after != 0 {
			t.Errorf("classifyAt(%q) = %v, want 0", header, after)
		}
	}
	if _, after := classifyAt(now, 429, "99999"); after != 5*time.Minute {
		t.Errorf("classifyAt(99999) = %v, want 5m", after)
	}
}

func TestProvider_PRV15_ErrorUnwraps(t *testing.T) {
	cause := errors.New("connection reset")
	err := &Error{Status: 502, Code: "bad_gateway", Message: "upstream failed", Retryable: true, Err: cause}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}
	text := err.Error()
	for _, want := range []string{"502", "bad_gateway", "upstream failed", "connection reset"} {
		if !strings.Contains(text, want) {
			t.Errorf("Error() = %q, should contain %q", text, want)
		}
	}
}

func FuzzClassify(f *testing.F) {
	f.Add(429, "3")
	f.Add(500, "")
	f.Add(0, "garbage")
	f.Add(503, "99999")
	f.Add(503, "9227000000") // must be capped, not overflowed
	f.Fuzz(func(t *testing.T, status int, header string) {
		_, after := Classify(status, header)
		if after < 0 || after > 5*time.Minute {
			t.Fatalf("Classify(%d, %q) retry-after = %v, want within [0, 5m]", status, header, after)
		}
	})
}
