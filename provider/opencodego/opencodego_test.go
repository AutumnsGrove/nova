package opencodego

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
}

func newRecordServer(t *testing.T) *recordServer {
	t.Helper()
	rs := &recordServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.paths = append(rs.paths, r.URL.Path)
		rs.headers = append(rs.headers, r.Header.Clone())
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

func TestOpencodego_SessionHeader(t *testing.T) {
	rs := newRecordServer(t)
	p := New(Config{BaseURL: rs.URL, SessionID: "sess-1"})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	_, header := rs.first()
	if header.Get(SessionHeader) != "sess-1" {
		t.Fatalf("%s = %q", SessionHeader, header.Get(SessionHeader))
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
