package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// microCentsPerDollar converts the Console's micro-cents (1e-8 USD).
const microCentsPerDollar = 100_000_000

// MeterKind names one rolling window.
type MeterKind string

const (
	MeterFiveHour MeterKind = "five_hour"
	MeterWeek     MeterKind = "calendar_week"
	MeterMonth    MeterKind = "product_period"
)

// UsageMeter is one rolling allowance.
type UsageMeter struct {
	Kind     MeterKind
	Label    string
	Short    string
	Percent  float64
	UsedUSD  float64
	LimitUSD float64
	// ResetsAt is an RFC3339 timestamp, or "" when unknown.
	ResetsAt string
}

// UsageErrorKind classifies a usage failure.
type UsageErrorKind string

const (
	ErrNoCredentials  UsageErrorKind = "no_credentials"
	ErrUnauthorized   UsageErrorKind = "unauthorized"
	ErrNoSubscription UsageErrorKind = "no_subscription"
	ErrUnrecognized   UsageErrorKind = "unrecognized"
	ErrTimeout        UsageErrorKind = "timeout"
	ErrNetwork        UsageErrorKind = "network"
	ErrHTTP           UsageErrorKind = "http"
)

// UsageError is a classified failure from the usage API.
type UsageError struct {
	Kind    UsageErrorKind
	Message string
	Status  int
}

func (e *UsageError) Error() string { return e.Message }

// usageWindows describes the three meters and their display names.
var usageWindows = []struct {
	key   string
	kind  MeterKind
	label string
	short string
}{
	{"fiveHour", MeterFiveHour, "Rolling 5h", "5h"},
	{"week", MeterWeek, "Weekly", "wk"},
	{"month", MeterMonth, "Monthly", "mo"},
}

// ParseStatus reads access.meters out of a /api/go/status payload.
func ParseStatus(payload []byte) []UsageMeter {
	var parsed struct {
		Access struct {
			EndsAt string `json:"endsAt"`
			Meters map[string]struct {
				ResetsAt       string      `json:"resetsAt"`
				LimitMicroCent json.Number `json:"limitMicroCents"`
				UsedMicroCent  json.Number `json:"usedMicroCents"`
			} `json:"meters"`
		} `json:"access"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil
	}
	periodEnd, periodOK := parseTime(parsed.Access.EndsAt)
	var out []UsageMeter
	for _, w := range usageWindows {
		entry, ok := parsed.Access.Meters[w.key]
		if !ok {
			continue
		}
		used, errU := entry.UsedMicroCent.Float64()
		limit, errL := entry.LimitMicroCent.Float64()
		if errU != nil || errL != nil {
			continue
		}
		usedUSD := used / microCentsPerDollar
		limitUSD := limit / microCentsPerDollar
		percent := 0.0
		if limitUSD > 0 {
			percent = usedUSD / limitUSD * 100
		}
		percent = math.Round(math.Min(100, math.Max(0, percent))*10) / 10
		resets := ""
		if t, ok := parseTime(entry.ResetsAt); ok {
			resets = t.UTC().Format(time.RFC3339)
		} else if w.kind == MeterMonth && periodOK {
			resets = periodEnd.UTC().Format(time.RFC3339)
		}
		out = append(out, UsageMeter{
			Kind: w.kind, Label: w.label, Short: w.short,
			Percent: percent, UsedUSD: usedUSD, LimitUSD: limitUSD, ResetsAt: resets,
		})
	}
	return out
}

// FetchUsage fetches the three meters for a workspace.
func FetchUsage(ctx context.Context, cfg Config, key, orgID string) ([]UsageMeter, error) {
	if key == "" {
		return nil, &UsageError{Kind: ErrNoCredentials, Message: "Not signed in to OpenCode Go."}
	}
	server := strings.TrimRight(cfg.Server, "/")
	if server == "" {
		server = DefaultServer
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/go/status", nil)
	if err != nil {
		return nil, &UsageError{Kind: ErrNetwork, Message: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if orgID != "" {
		req.Header.Set("x-org-id", orgID)
		req.Header.Set("x-opencode-org-id", orgID)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &UsageError{Kind: ErrTimeout, Message: "OpenCode Go usage request timed out."}
		}
		return nil, &UsageError{Kind: ErrNetwork, Message: "OpenCode Go usage request failed: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &UsageError{Kind: ErrUnauthorized, Message: "OpenCode Go session expired; sign in again."}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &UsageError{Kind: ErrHTTP, Status: resp.StatusCode, Message: fmt.Sprintf("OpenCode Go usage request failed (%d).", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, &UsageError{Kind: ErrNetwork, Message: "OpenCode Go usage response could not be read."}
	}
	meters := ParseStatus(body)
	if len(meters) == 0 {
		var probe struct {
			Access json.RawMessage `json:"access"`
		}
		if json.Unmarshal(body, &probe) != nil || len(probe.Access) == 0 || string(probe.Access) == "null" {
			return nil, &UsageError{Kind: ErrNoSubscription, Message: "No OpenCode Go subscription on this workspace."}
		}
		return nil, &UsageError{Kind: ErrUnrecognized, Message: "OpenCode Go usage response was not recognised."}
	}
	return meters, nil
}

// parseTime parses an RFC3339 timestamp, reporting whether it was valid.
func parseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// FormatUSD renders a dollar amount: three decimals below $1, else two.
func FormatUSD(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "$0.00"
	}
	decimals := 2
	if math.Abs(value) > 0 && math.Abs(value) < 1 {
		decimals = 3
	}
	return fmt.Sprintf("$%.*f", decimals, value)
}

// FormatPercent renders a percentage, dropping a trailing .0.
func FormatPercent(percent float64) string {
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return "0%"
	}
	if percent == math.Trunc(percent) {
		return fmt.Sprintf("%.0f%%", percent)
	}
	return fmt.Sprintf("%.1f%%", percent)
}

// FormatBar renders a used-vs-limit bar of the given width.
func FormatBar(ratio float64, width int) string {
	if width <= 0 {
		width = 18
	}
	clamped := math.Max(0, math.Min(1, ratio))
	if math.IsNaN(clamped) {
		clamped = 0
	}
	filled := int(math.Round(clamped * float64(width)))
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// FormatCountdown renders the time until a reset, or "" when unknown.
func FormatCountdown(resetsAt string, now time.Time) string {
	t, ok := parseTime(resetsAt)
	if !ok {
		return ""
	}
	ms := t.Sub(now)
	if ms <= 0 {
		return "resets now"
	}
	totalMinutes := int(ms / time.Minute)
	days := totalMinutes / (60 * 24)
	hours := (totalMinutes % (60 * 24)) / 60
	minutes := totalMinutes % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// FormatMeterRow renders one aligned meter line.
func FormatMeterRow(m UsageMeter, barWidth int, now time.Time) string {
	ratio := 0.0
	if m.LimitUSD > 0 {
		ratio = m.UsedUSD / m.LimitUSD
	}
	row := fmt.Sprintf("%-11s %s  %5s  %-19s", m.Label, FormatBar(ratio, barWidth), FormatPercent(m.Percent),
		FormatUSD(m.UsedUSD)+" / "+FormatUSD(m.LimitUSD))
	if countdown := FormatCountdown(m.ResetsAt, now); countdown != "" {
		row += "  " + countdown
	}
	return row
}

// FooterStatus renders the compact footer string, or "" when there is nothing.
func FooterStatus(meters []UsageMeter, now time.Time) string {
	if len(meters) == 0 {
		return ""
	}
	parts := make([]string, 0, len(meters))
	for _, m := range meters {
		part := m.Short + " " + FormatPercent(m.Percent)
		if m.Kind == MeterFiveHour {
			if countdown := FormatCountdown(m.ResetsAt, now); countdown != "" {
				part += " (" + countdown + ")"
			}
		}
		parts = append(parts, part)
	}
	return "go " + strings.Join(parts, " · ")
}

// DescribeUsageError turns a usage failure into a one-line message.
func DescribeUsageError(err error) string {
	var ue *UsageError
	if errors.As(err, &ue) {
		switch ue.Kind {
		case ErrNoCredentials:
			return "Not signed in. Run `nova login opencode-go`."
		case ErrUnauthorized:
			return "Session expired. Run `nova login opencode-go` again."
		case ErrNoSubscription:
			return "No OpenCode Go subscription on this workspace."
		case ErrUnrecognized:
			return "OpenCode Go usage response was not recognised."
		default:
			return ue.Message
		}
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
