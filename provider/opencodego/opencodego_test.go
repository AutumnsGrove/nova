package opencodego

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/openrouter"
)

// recordServer records the paths and headers of requests and answers with an
// empty SSE body.
type recordServer struct {
	*httptest.Server
	mu      sync.Mutex
	paths   []string
	headers []http.Header
	bodies  [][]byte
}

func newRecordServer(t *testing.T) *recordServer {
	t.Helper()
	rs := &recordServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.paths = append(rs.paths, r.URL.Path)
		rs.headers = append(rs.headers, r.Header.Clone())
		rs.bodies = append(rs.bodies, body)
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordServer) first() (string, http.Header) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.paths) == 0 {
		return "", nil
	}
	return rs.paths[0], rs.headers[0]
}

// requestBodies returns every request body the server saw, in order.
func (rs *recordServer) requestBodies() [][]byte {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([][]byte(nil), rs.bodies...)
}

func TestOpencodego_OPG01_Name(t *testing.T) {
	if got := New(Config{}).Name(); got != ProviderName {
		t.Fatalf("Name = %q, want %q", got, ProviderName)
	}
}

func TestOpencodego_OPG02_ChatRoutes(t *testing.T) {
	if got := APIFor("opencode-go/glm-5.3"); got != DialectChat {
		t.Fatalf("APIFor = %q, want chat", got)
	}
}

func TestOpencodego_OPG03_ResponsesRoutes(t *testing.T) {
	if got := APIFor("opencode-go/grok-4.7"); got != DialectResponses {
		t.Fatalf("APIFor = %q, want responses", got)
	}
}

func TestOpencodego_OPG04_MessagesRoutes(t *testing.T) {
	if got := APIFor("opencode-go/claude-haiku-5-5"); got != DialectMessages {
		t.Fatalf("APIFor = %q, want messages", got)
	}
}

func TestOpencodego_OPG05_UnknownFallsBack(t *testing.T) {
	if got := APIFor("nope"); got != DialectChat {
		t.Fatalf("APIFor = %q, want chat", got)
	}
}

func TestOpencodego_OPG06_BaseURL(t *testing.T) {
	if DefaultBaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
	rs := newRecordServer(t)
	p := New(Config{BaseURL: rs.URL + "/v1/"})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	path, _ := rs.first()
	if path != "/v1/chat/completions" {
		t.Fatalf("path = %q, want the trailing slash trimmed", path)
	}
}

func TestOpencodego_OPG07_BearerPerRequest(t *testing.T) {
	rs := newRecordServer(t)
	p := New(Config{BaseURL: rs.URL, APIKeyFunc: func(context.Context) (string, error) { return "k2", nil }})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	_, header := rs.first()
	if header.Get("Authorization") != "Bearer k2" {
		t.Fatalf("Authorization = %q", header.Get("Authorization"))
	}
}

func TestOpencodego_OPG08_UserAgent(t *testing.T) {
	rs := newRecordServer(t)
	p := New(Config{BaseURL: rs.URL, UserAgent: "nova/1.2.3"})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	_, header := rs.first()
	if !strings.HasPrefix(header.Get("User-Agent"), "nova/") {
		t.Fatalf("User-Agent = %q", header.Get("User-Agent"))
	}
}

func TestOpencodego_OPG21_SessionHeader(t *testing.T) {
	rs := newRecordServer(t)
	p := New(Config{BaseURL: rs.URL, SessionID: "sess-1"})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	_, header := rs.first()
	if header.Get(SessionHeader) != "sess-1" {
		t.Fatalf("%s = %q", SessionHeader, header.Get(SessionHeader))
	}
}

// A catalog id reaches the preset for exact dialect dispatch, but the wire
// carries the bare slug: the catalog prefix is Nova's namespace, not Go's
// model name (#59).
func TestOpencodego_OPG36_CatalogIDStrippedForWire(t *testing.T) {
	rs := newRecordServer(t)
	p := New(Config{BaseURL: rs.URL})
	for _, model := range []string{"opencode-go/glm-5.3", "glm-5.3"} {
		_, _ = p.Stream(context.Background(), provider.Request{Model: model}, nil)
	}
	got := rs.requestBodies()
	if len(got) != 2 {
		t.Fatalf("recorded %d bodies, want 2", len(got))
	}
	for i, raw := range got {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("body %d is not JSON: %v", i, err)
		}
		if body.Model != "glm-5.3" {
			t.Errorf("wire model %d = %q, want the bare slug glm-5.3", i, body.Model)
		}
	}
}

func TestOpencodego_OPG09_CatalogEntriesValid(t *testing.T) {
	if len(Catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	for _, m := range Catalog {
		if m.ID == "" || m.Name == "" || m.Slug == "" {
			t.Fatalf("entry missing a field: %+v", m)
		}
		if m.Provider != ProviderName {
			t.Fatalf("provider = %q", m.Provider)
		}
		if m.ContextWindow <= 0 {
			t.Fatalf("%s context = %d", m.ID, m.ContextWindow)
		}
		if m.InPerM <= 0 || m.OutPerM <= 0 {
			t.Fatalf("%s price = %v/%v", m.ID, m.InPerM, m.OutPerM)
		}
	}
}

// TestOpencodego_OPG38_BaselinePickerFields checks the baseline carries the
// picker fields from the live catalog (captured 2026-11-05) and the Go-plan
// monthly allowances from the Go docs page (#59).
func TestOpencodego_OPG38_BaselinePickerFields(t *testing.T) {
	deepseek, ok := provider.GetModel("opencode-go/deepseek-v4.1-flash")
	if !ok {
		t.Fatal("deepseek entry missing")
	}
	if len(deepseek.ReasoningEfforts) != 3 || deepseek.ReasoningEfforts[2] != "max" {
		t.Errorf("deepseek efforts = %v", deepseek.ReasoningEfforts)
	}
	if len(deepseek.ToolChoice) != 4 || deepseek.ToolChoice[3] != "tool" {
		t.Errorf("deepseek tool choice = %v", deepseek.ToolChoice)
	}
	if len(deepseek.CostMultipliers) != 1 || deepseek.CostMultipliers[0].Factor != 2 {
		t.Errorf("deepseek multipliers = %v", deepseek.CostMultipliers)
	}
	if deepseek.MonthlyLimitUSD != 100 {
		t.Errorf("deepseek monthly limit = %v, want 100", deepseek.MonthlyLimitUSD)
	}
	haiku, ok := provider.GetModel("opencode-go/claude-haiku-5-5")
	if !ok {
		t.Fatal("haiku entry missing")
	}
	if len(haiku.PriceTiers) != 1 || haiku.PriceTiers[0].AboveTokens != 100000 {
		t.Errorf("haiku tiers = %v, want the 100K cliff", haiku.PriceTiers)
	}
	kimi, ok := provider.GetModel("opencode-go/kimi-k3")
	if !ok {
		t.Fatal("kimi entry missing")
	}
	if len(kimi.ReasoningEfforts) != 1 || kimi.ReasoningEfforts[0] != "max" {
		t.Errorf("kimi efforts = %v, want max only", kimi.ReasoningEfforts)
	}
}

func TestOpencodego_OPG10_EndpointMatchesRouting(t *testing.T) {
	for _, m := range Catalog {
		if APIFor(m.ID) != EndpointTable[m.ID] {
			t.Fatalf("%s: APIFor %q != recorded %q", m.ID, APIFor(m.ID), EndpointTable[m.ID])
		}
	}
}

func TestOpencodego_OPG11_ModelIDShape(t *testing.T) {
	for _, m := range Catalog {
		if !strings.HasPrefix(m.ID, "opencode-go/") {
			t.Fatalf("id = %q", m.ID)
		}
		if m.Slug != strings.TrimPrefix(m.ID, "opencode-go/") {
			t.Fatalf("%s slug = %q", m.ID, m.Slug)
		}
	}
}

func TestOpencodego_OPG20_BothCatalogs(t *testing.T) {
	openrouter.RegisterModels()
	RegisterModels()
	seen := map[string]bool{}
	for _, m := range provider.Models() {
		if seen[m.ID] {
			t.Fatalf("duplicate id %q", m.ID)
		}
		seen[m.ID] = true
	}
	providers := map[string]bool{}
	for _, m := range provider.Models() {
		providers[m.Provider] = true
	}
	if !providers["openrouter"] || !providers[ProviderName] {
		t.Fatalf("providers = %v", providers)
	}
}

func TestOpencodego_DSC01_DiscoveryRefresh(t *testing.T) {
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"new-model"},{"id":"claude-new"}]}`))
	}))
	t.Cleanup(modelsSrv.Close)
	devSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"opencode-go":{"models":{"new-model":{"name":"New Model","limit":{"context":100000,"output":8000},"cost":{"input":1.0,"output":2.0}}}}}`))
	}))
	t.Cleanup(devSrv.Close)
	cache := filepath.Join(t.TempDir(), "cache.json")

	if err := Refresh(context.Background(), DiscoveryConfig{
		BaseURL: modelsSrv.URL, ModelsDevURL: devSrv.URL, CachePath: cache,
	}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, ok := provider.GetModel("opencode-go/new-model"); !ok {
		t.Fatal("the live model was not registered")
	}
	if m, _ := provider.GetModel("opencode-go/new-model"); m.ContextWindow != 100000 || m.InPerM != 1.0 || m.OutPerM != 2.0 {
		t.Fatalf("live metadata = %+v", m)
	}
	if APIFor("opencode-go/claude-new") != DialectMessages {
		t.Fatalf("claude-new routed to %q", APIFor("opencode-go/claude-new"))
	}
	if _, ok := provider.GetModel("opencode-go/glm-5.3"); !ok {
		t.Fatal("the baseline model disappeared")
	}
	if !CacheFresh(cache, time.Hour) {
		t.Fatal("the cache is not fresh")
	}
	if err := LoadCache(cache); err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
}

// A reply with no model ids is a failed refresh. Accepting it would replace the
// overlay with nothing and mark the cache fresh, hiding every live model for
// the cache's lifetime.
func TestOpencodego_DSC03_EmptyListKeepsTheOverlay(t *testing.T) {
	for name, reply := range map[string]string{
		"empty data":      `{"data":[]}`,
		"error object":    `{"error":"overloaded"}`,
		"not json":        `<html>gateway</html>`,
		"rows without id": `{"data":[{"name":"x"},""]}`,
	} {
		t.Run(name, func(t *testing.T) {
			setOverlay([]Discovered{{ID: "opencode-go/kept-model", Name: "Kept", Slug: "kept-model", ContextWindow: 1}})
			t.Cleanup(func() { setOverlay(nil) })
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(reply))
			}))
			t.Cleanup(srv.Close)
			cache := filepath.Join(t.TempDir(), "cache.json")

			if err := Refresh(context.Background(), DiscoveryConfig{BaseURL: srv.URL, ModelsDevURL: srv.URL, CachePath: cache}); err == nil {
				t.Fatal("Refresh succeeded on a reply with no models")
			}
			mu.RLock()
			kept := slices.ContainsFunc(Catalog, func(m provider.Model) bool { return m.ID == "opencode-go/kept-model" })
			mu.RUnlock()
			if !kept {
				t.Error("the previous overlay was dropped")
			}
			if CacheFresh(cache, time.Hour) {
				t.Error("a failed refresh stamped the cache fresh")
			}
		})
	}
}

// Discovery runs in a goroutine at startup while the first turn is already
// routing, so a refresh must not race the readers of the endpoint table. Run
// under -race this fails on the unsynchronised map and overlay.
func TestOpencodego_DSC02_RefreshIsSafeWhileRouting(t *testing.T) {
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-new"},{"id":"other-new"}]}`))
	}))
	t.Cleanup(modelsSrv.Close)
	devSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(devSrv.Close)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					APIFor("opencode-go/claude-new")
				}
			}
		}()
	}
	for range 20 {
		if err := Refresh(context.Background(), DiscoveryConfig{BaseURL: modelsSrv.URL, ModelsDevURL: devSrv.URL}); err != nil {
			t.Errorf("Refresh: %v", err)
		}
	}
	close(stop)
	readers.Wait()
	if APIFor("opencode-go/claude-new") != DialectMessages {
		t.Errorf("claude-new routed to %q after the refreshes", APIFor("opencode-go/claude-new"))
	}
}

func TestOpencodego_OPG22_DefaultModel(t *testing.T) {
	if DefaultModel != "opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("DefaultModel = %q", DefaultModel)
	}
	if _, ok := provider.GetModel(DefaultModel); !ok {
		t.Fatalf("%s is not in the catalog", DefaultModel)
	}
}

// TestOpencodego_OPG35_ImplicitCaching checks the flag the cache marker
// derives from: the Messages-served Claude model needs an explicit marker, and
// the models known to cache repeated prefixes on their own must not get one.
// A discovered override must not lose the baseline's answer.
func TestOpencodego_OPG35_ImplicitCaching(t *testing.T) {
	for _, b := range baseline {
		m, ok := provider.GetModel(b.id)
		if !ok {
			t.Fatalf("%s is not registered", b.id)
		}
		want := b.id != "opencode-go/claude-haiku-5-5"
		if m.ImplicitCaching != want {
			t.Errorf("%s ImplicitCaching = %v, want %v", b.id, m.ImplicitCaching, want)
		}
	}
}
