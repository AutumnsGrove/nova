package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// recordServer counts requests.
type recordServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newRecordServer(t *testing.T) *recordServer {
	t.Helper()
	rs := &recordServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.paths = append(rs.paths, r.URL.Path)
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordServer) count() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.paths)
}

func envFrom(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestProviders_OPG12_ConfigKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.APIKey = "  cfg-key  "
	key, source, err := ResolveOpenCodeGoKey(&cfg, envFrom(nil))
	if err != nil || key != "cfg-key" || source != "config" {
		t.Fatalf("key=%q source=%q err=%v", key, source, err)
	}
}

func TestProviders_OPG13_EnvKey(t *testing.T) {
	cfg := config.Defaults()
	key, source, err := ResolveOpenCodeGoKey(&cfg, envFrom(map[string]string{auth.EnvOpenCodeGoKey: " env-key "}))
	if err != nil || key != "env-key" || source != "env" {
		t.Fatalf("key=%q source=%q err=%v", key, source, err)
	}
}

func TestProviders_OPG14_ConfigBeatsEnv(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.APIKey = "cfg"
	key, source, _ := ResolveOpenCodeGoKey(&cfg, envFrom(map[string]string{auth.EnvOpenCodeGoKey: "env"}))
	if key != "cfg" || source != "config" {
		t.Fatalf("key=%q source=%q", key, source)
	}
}

func TestProviders_OPG15_NoKey(t *testing.T) {
	cfg := config.Defaults()
	_, _, err := ResolveOpenCodeGoKey(&cfg, envFrom(nil))
	if !errors.Is(err, auth.ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
}

func TestProviders_OPG16_RouterPicksOpenCodeGo(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(nil)})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	if goSrv.count() == 0 {
		t.Fatal("the OpenCode Go server received no request")
	}
	if orSrv.count() != 0 {
		t.Fatal("the OpenRouter server received the request")
	}
}

func TestProviders_OPG17_RouterPicksOpenRouter(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})

	model := openRouterModel()
	_, _ = p.Stream(context.Background(), provider.Request{Model: model}, nil)
	if orSrv.count() == 0 {
		t.Fatalf("the OpenRouter server received no request for %s", model)
	}
	if goSrv.count() != 0 {
		t.Fatal("the OpenCode Go server received the request")
	}
}

func TestProviders_OPG18_UnknownFallsBack(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "not-in-any-catalog"}, nil)
	if orSrv.count() == 0 {
		t.Fatal("an unknown model did not fall back to OpenRouter")
	}
}

func TestProviders_OPG19_BaseURLOverride(t *testing.T) {
	t.Run("config wins", func(t *testing.T) {
		cfgSrv, envSrv := newRecordServer(t), newRecordServer(t)
		cfg := config.Defaults()
		cfg.Provider.OpenCodeGo.BaseURL = cfgSrv.URL
		p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"NOVA_OPENCODE_GO_BASE_URL": envSrv.URL})})
		_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
		if cfgSrv.count() == 0 || envSrv.count() != 0 {
			t.Fatalf("config=%d env=%d, want config", cfgSrv.count(), envSrv.count())
		}
	})
	t.Run("env when config empty", func(t *testing.T) {
		cfgSrv, envSrv := newRecordServer(t), newRecordServer(t)
		cfg := config.Defaults()
		_ = cfgSrv
		p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"NOVA_OPENCODE_GO_BASE_URL": envSrv.URL})})
		_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
		if envSrv.count() == 0 {
			t.Fatal("the env base URL was not used")
		}
	})
}

// openRouterModel returns a catalog model served by OpenRouter.
func openRouterModel() string {
	for _, m := range provider.Models() {
		if m.Provider == "openrouter" {
			return m.ID
		}
	}
	return "openrouter/auto"
}

func TestProviders_TMO11_AgentRetriesTimedOutDialect(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = srv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(nil)})

	clock := testkit.NewFakeClock(time.Now())
	var retries int
	a := agent.New(agent.Config{
		Provider: p, Model: "opencode-go/glm-5.3", Clock: clock,
		Retry: agent.RetryPolicy{Max: 2, Base: time.Second, Jitter: 0},
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventRetry {
				retries++
			}
		},
	})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = a.Run(context.Background(), nil, message.User("go")) }()
	clock.BlockUntil(t, 1)
	clock.Advance(time.Hour)
	<-done
	if retries != 1 {
		t.Fatalf("retry events = %d, want 1", retries)
	}
}
