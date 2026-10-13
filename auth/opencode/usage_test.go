package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// statusJSON builds a /api/go/status payload.
func statusJSON(meters map[string]map[string]any, endsAt string) []byte {
	access := map[string]any{"meters": meters}
	if endsAt != "" {
		access["endsAt"] = endsAt
	}
	data, _ := json.Marshal(map[string]any{"access": access})
	return data
}

func TestUsage_USG01_ParseAll(t *testing.T) {
	payload := statusJSON(map[string]map[string]any{
		"fiveHour": {"usedMicroCents": 1_200_000_000, "limitMicroCents": 1_920_000_000, "resetsAt": "2026-10-13T14:00:00Z"},
		"week":     {"usedMicroCents": 3_010_000_000, "limitMicroCents": 9_600_000_000, "resetsAt": "2026-10-17T00:00:00Z"},
		"month":    {"usedMicroCents": 4_230_000_000, "limitMicroCents": 9_600_000_000},
	}, "2026-10-25T00:00:00Z")
	meters := ParseStatus(payload)
	if len(meters) != 3 {
		t.Fatalf("meters = %d, want 3", len(meters))
	}
	if meters[0].Kind != MeterFiveHour || meters[0].Label != "Rolling 5h" || meters[0].Short != "5h" {
		t.Fatalf("fiveHour = %+v", meters[0])
	}
	if meters[0].UsedUSD != 12.0 || meters[0].LimitUSD != 19.2 {
		t.Fatalf("fiveHour money = %v/%v", meters[0].UsedUSD, meters[0].LimitUSD)
	}
	if meters[0].Percent != 62.5 {
		t.Fatalf("fiveHour percent = %v, want 62.5", meters[0].Percent)
	}
	if meters[2].ResetsAt != "2026-10-25T00:00:00Z" {
		t.Fatalf("month resets = %q", meters[2].ResetsAt)
	}
}

func TestUsage_USG02_PercentClamped(t *testing.T) {
	payload := statusJSON(map[string]map[string]any{
		"fiveHour": {"usedMicroCents": 200_00_000_00, "limitMicroCents": 100_00_000_00},
	}, "")
	meters := ParseStatus(payload)
	if len(meters) != 1 || meters[0].Percent != 100 {
		t.Fatalf("meters = %+v", meters)
	}
}

func TestUsage_USG03_MonthResetFallback(t *testing.T) {
	payload := statusJSON(map[string]map[string]any{
		"month": {"usedMicroCents": 1_00_000_00, "limitMicroCents": 10_00_000_00},
	}, "2026-11-01T00:00:00Z")
	meters := ParseStatus(payload)
	if len(meters) != 1 || meters[0].ResetsAt != "2026-11-01T00:00:00Z" {
		t.Fatalf("month = %+v", meters)
	}
}

func TestUsage_USG04_MissingWindowSkipped(t *testing.T) {
	payload := statusJSON(map[string]map[string]any{
		"fiveHour": {"usedMicroCents": 1_00_000_00, "limitMicroCents": 10_00_000_00},
	}, "")
	if meters := ParseStatus(payload); len(meters) != 1 {
		t.Fatalf("meters = %d, want 1", len(meters))
	}
}

func TestUsage_USG05_MicroCentsToDollars(t *testing.T) {
	payload := statusJSON(map[string]map[string]any{
		"fiveHour": {"usedMicroCents": 100_000_000, "limitMicroCents": 200_000_000},
	}, "")
	meters := ParseStatus(payload)
	if meters[0].UsedUSD != 1.0 || meters[0].LimitUSD != 2.0 {
		t.Fatalf("money = %v/%v", meters[0].UsedUSD, meters[0].LimitUSD)
	}
}

func TestUsage_USG06_MalformedPayload(t *testing.T) {
	if meters := ParseStatus([]byte("not json")); meters != nil {
		t.Fatalf("meters = %v, want nil", meters)
	}
	if meters := ParseStatus([]byte("")); meters != nil {
		t.Fatalf("meters = %v, want nil", meters)
	}
}

func TestUsage_USG07_FetchSendsAuth(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write(statusJSON(map[string]map[string]any{
			"fiveHour": {"usedMicroCents": 1_00_000_00, "limitMicroCents": 10_00_000_00},
		}, ""))
	}))
	t.Cleanup(srv.Close)
	if _, err := FetchUsage(context.Background(), Config{Server: srv.URL}, "go-key", "org1"); err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if got.Get("Authorization") != "Bearer go-key" {
		t.Fatalf("Authorization = %q", got.Get("Authorization"))
	}
	if got.Get("x-org-id") != "org1" || got.Get("x-opencode-org-id") != "org1" {
		t.Fatalf("org headers = %q / %q", got.Get("x-org-id"), got.Get("x-opencode-org-id"))
	}
}

func TestUsage_USG08_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	_, err := FetchUsage(context.Background(), Config{Server: srv.URL}, "k", "")
	if ue := usageErr(t, err); ue.Kind != ErrUnauthorized {
		t.Fatalf("kind = %q", ue.Kind)
	}
}

func TestUsage_USG09_HTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	_, err := FetchUsage(context.Background(), Config{Server: srv.URL}, "k", "")
	ue := usageErr(t, err)
	if ue.Kind != ErrHTTP || ue.Status != 500 {
		t.Fatalf("kind = %q status = %d", ue.Kind, ue.Status)
	}
}

func TestUsage_USG10_NoSubscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access":null}`))
	}))
	t.Cleanup(srv.Close)
	_, err := FetchUsage(context.Background(), Config{Server: srv.URL}, "k", "")
	if ue := usageErr(t, err); ue.Kind != ErrNoSubscription {
		t.Fatalf("kind = %q", ue.Kind)
	}
}

func TestUsage_USG11_Unrecognized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access":{"meters":{}}}`))
	}))
	t.Cleanup(srv.Close)
	_, err := FetchUsage(context.Background(), Config{Server: srv.URL}, "k", "")
	if ue := usageErr(t, err); ue.Kind != ErrUnrecognized {
		t.Fatalf("kind = %q", ue.Kind)
	}
}

func TestUsage_USG12_NetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := FetchUsage(context.Background(), Config{Server: url}, "k", "")
	if ue := usageErr(t, err); ue.Kind != ErrNetwork {
		t.Fatalf("kind = %q", ue.Kind)
	}
}

func TestUsage_USG13_FormatUSD(t *testing.T) {
	if got := FormatUSD(0.15); got != "$0.150" {
		t.Fatalf("0.15 -> %q", got)
	}
	if got := FormatUSD(12); got != "$12.00" {
		t.Fatalf("12 -> %q", got)
	}
}

func TestUsage_USG14_FormatPercent(t *testing.T) {
	if got := FormatPercent(62.5); got != "62.5%" {
		t.Fatalf("62.5 -> %q", got)
	}
	if got := FormatPercent(31); got != "31%" {
		t.Fatalf("31 -> %q", got)
	}
}

func TestUsage_USG15_FormatBar(t *testing.T) {
	got := FormatBar(0.5, 18)
	if strings.Count(got, "█") != 9 || strings.Count(got, "░") != 9 {
		t.Fatalf("bar = %q", got)
	}
}

func TestUsage_USG16_FormatCountdown(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if got := FormatCountdown("2026-10-13T13:12:00Z", now); got != "1h 12m" {
		t.Fatalf("future -> %q", got)
	}
	if got := FormatCountdown("2026-10-13T11:00:00Z", now); got != "resets now" {
		t.Fatalf("past -> %q", got)
	}
	if got := FormatCountdown("", now); got != "" {
		t.Fatalf("empty -> %q", got)
	}
}

func TestUsage_USG17_FormatMeterRow(t *testing.T) {
	m := UsageMeter{Label: "Rolling 5h", Short: "5h", Percent: 62.5, UsedUSD: 12, LimitUSD: 19.2, ResetsAt: "2026-10-13T13:12:00Z"}
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	row := FormatMeterRow(m, 18, now)
	for _, want := range []string{"Rolling 5h", "62.5%", "$12.00 / $19.20", "1h 12m"} {
		if !strings.Contains(row, want) {
			t.Fatalf("row = %q, missing %q", row, want)
		}
	}
}

func TestUsage_USG18_DescribeError(t *testing.T) {
	cases := map[UsageErrorKind]string{
		ErrNoCredentials:  "Not signed in",
		ErrUnauthorized:   "Session expired",
		ErrNoSubscription: "No OpenCode Go subscription",
		ErrUnrecognized:   "not recognised",
	}
	for kind, want := range cases {
		if got := DescribeUsageError(&UsageError{Kind: kind}); !strings.Contains(got, want) {
			t.Fatalf("%s -> %q, want %q", kind, got, want)
		}
	}
}

func TestUsage_USG19_FooterStatus(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	meters := []UsageMeter{
		{Kind: MeterFiveHour, Short: "5h", Percent: 62.5, ResetsAt: "2026-10-13T13:12:00Z"},
		{Kind: MeterWeek, Short: "wk", Percent: 31},
		{Kind: MeterMonth, Short: "mo", Percent: 44},
	}
	got := FooterStatus(meters, now)
	want := "go 5h 62.5% (1h 12m) · wk 31% · mo 44%"
	if got != want {
		t.Fatalf("footer = %q, want %q", got, want)
	}
	if FooterStatus(nil, now) != "" {
		t.Fatal("empty meters should give an empty footer")
	}
}

func TestUsage_USG20_NoKey(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	_, err := FetchUsage(context.Background(), Config{Server: srv.URL}, "", "")
	if ue := usageErr(t, err); ue.Kind != ErrNoCredentials {
		t.Fatalf("kind = %q", ue.Kind)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("a request was sent without a key")
	}
}

// usageErr extracts a *UsageError or fails.
func usageErr(t *testing.T, err error) *UsageError {
	t.Helper()
	ue, ok := err.(*UsageError)
	if !ok {
		t.Fatalf("err = %v (%T), want *UsageError", err, err)
	}
	return ue
}
