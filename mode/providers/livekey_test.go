package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

func TestProviders_SGN37_LiveOpenCodeGoKey(t *testing.T) {
	var mu sync.Mutex
	var authz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authz = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"index\":0}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = srv.URL
	p := Build(Options{Config: &cfg, Home: home, Getenv: envFrom(nil)})

	drainStream(t, p)
	if authz != "" {
		t.Fatalf("request carried %q before a key existed", authz)
	}
	if err := opencode.SaveKey(opencode.DefaultKeyPath(home), "late-key"); err != nil {
		t.Fatal(err)
	}
	drainStream(t, p)
	if authz != "Bearer late-key" {
		t.Fatalf("request carried %q, want the key saved after Build", authz)
	}
}

// drainStream runs one request so the provider resolves its key.
func drainStream(t *testing.T, p provider.Provider) {
	t.Helper()
	_, err := p.Stream(context.Background(), provider.Request{
		Model:    "opencode-go/glm-5.3",
		Messages: []message.Message{message.User("hi")},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
}
