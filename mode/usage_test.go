package mode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
)

func TestUsage_USG21_GetUsageCommand(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"access":{"meters":{
			"fiveHour":{"usedMicroCents":1200000000,"limitMicroCents":1920000000,"resetsAt":"2026-10-13T13:12:00Z"},
			"week":{"usedMicroCents":3010000000,"limitMicroCents":9600000000},
			"month":{"usedMicroCents":4230000000,"limitMicroCents":9600000000}}}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OPENCODE_GO_API_KEY", "go-key")
	t.Setenv("OPENCODE_CONSOLE_SERVER", srv.URL)

	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetUsage})
	if !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	var data struct {
		Meters []usageInfo `json:"meters"`
		Footer string      `json:"footer"`
		Error  string      `json:"error"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(data.Meters) != 3 {
		t.Fatalf("meters = %d, want 3", len(data.Meters))
	}
	if data.Meters[0].Kind != "five_hour" || data.Meters[0].Percent != 62.5 {
		t.Fatalf("fiveHour = %+v", data.Meters[0])
	}
	if !strings.HasPrefix(data.Footer, "go 5h 62.5%") {
		t.Fatalf("footer = %q", data.Footer)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("usage calls = %d, want 1", calls)
	}
}

func TestUsage_USG22_GetUsageNoKey(t *testing.T) {
	t.Setenv("OPENCODE_GO_API_KEY", "")
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetUsage})
	if !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	var data struct {
		Meters []usageInfo `json:"meters"`
		Error  string      `json:"error"`
	}
	_ = json.Unmarshal(resp.Data, &data)
	if len(data.Meters) != 0 {
		t.Fatalf("meters = %v, want none", data.Meters)
	}
	if !strings.Contains(data.Error, "Not signed in") {
		t.Fatalf("error = %q", data.Error)
	}
}
