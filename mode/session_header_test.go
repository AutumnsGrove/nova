package mode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
)

// goServer is a fake OpenCode Go endpoint. It records the headers of every chat
// request (a POST; the background model discovery GET is ignored) and answers
// each with a complete streamed reply, so the agent has nothing to retry.
type goServer struct {
	*httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func newGoServer(t *testing.T) *goServer {
	t.Helper()
	gs := &goServer{}
	gs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		gs.mu.Lock()
		gs.headers = append(gs.headers, r.Header.Clone())
		gs.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(gs.Close)
	return gs
}

func (gs *goServer) sessionHeaders() []string {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	var out []string
	for _, h := range gs.headers {
		out = append(out, h.Get("x-opencode-session"))
	}
	return out
}

// newGoModeServer builds a server that owns its provider router, signed in to
// OpenCode Go only (there is no OpenRouter key), serving an OpenCode Go model.
func newGoModeServer(t *testing.T, gs *goServer, noSession bool) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "")
	cfg := config.Defaults()
	cfg.Model = "opencode-go/glm-5.3"
	cfg.Tools.Active = []string{"read"}
	cfg.Provider.OpenCodeGo.BaseURL = gs.URL
	cfg.Provider.OpenCodeGo.APIKey = "go-key"
	opt := Options{Config: &cfg, CWD: t.TempDir(), Home: home}
	if noSession {
		opt.NoSession = true
	} else {
		opt.Session = filepath.Join(t.TempDir(), "s.jsonl")
	}
	s, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// promptAndWait sends a prompt and waits for the run to end.
func promptAndWait(t *testing.T, s *Server, text string) proto.Response {
	t.Helper()
	events, cancel := s.Subscribe(256)
	defer cancel()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: text})
	if !resp.OK {
		return resp
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before run_end")
			}
			if ev.Type == proto.EventRunEnd {
				return resp
			}
		case <-deadline:
			t.Fatal("timed out waiting for run_end")
		}
	}
}

func TestServer_OPG28_AnOpenCodeGoOnlyUserCanPrompt(t *testing.T) {
	// No OpenRouter key exists anywhere. The model is an OpenCode Go model and
	// OpenCode Go is signed in, so the prompt must go through.
	fake := newGoServer(t)
	s := newGoModeServer(t, fake, false)
	resp := promptAndWait(t, s, "hello")
	if !resp.OK {
		t.Fatalf("prompt = %+v, want ok: the model's own provider is signed in", resp)
	}
	if len(fake.sessionHeaders()) == 0 {
		t.Fatal("the request never reached OpenCode Go")
	}
}

func TestServer_OPG29_TheRequestCarriesTheSessionID(t *testing.T) {
	fake := newGoServer(t)
	s := newGoModeServer(t, fake, false)
	promptAndWait(t, s, "hello")
	got := fake.sessionHeaders()
	want := s.sessionID()
	if want == "" || len(got) == 0 || got[len(got)-1] != want {
		t.Fatalf("x-opencode-session = %v, want the server's session id %q", got, want)
	}
}

func TestServer_OPG30_ANewConversationGetsANewID(t *testing.T) {
	fake := newGoServer(t)
	s := newGoModeServer(t, fake, false)
	promptAndWait(t, s, "first")
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionNew}); !resp.OK {
		t.Fatalf("session.new = %+v", resp)
	}
	promptAndWait(t, s, "second")
	got := fake.sessionHeaders()
	if len(got) < 2 || got[0] == "" || got[len(got)-1] == "" || got[0] == got[len(got)-1] {
		t.Fatalf("headers across session.new = %v, want two different non-empty ids", got)
	}
	if got[len(got)-1] != s.sessionID() {
		t.Fatalf("last header %q is not the new session %q", got[len(got)-1], s.sessionID())
	}
}

func TestServer_OPG31_WithoutAStoreTheIDIsStillStable(t *testing.T) {
	fake := newGoServer(t)
	s := newGoModeServer(t, fake, true)
	promptAndWait(t, s, "first")
	promptAndWait(t, s, "second")
	got := fake.sessionHeaders()
	if len(got) < 2 || got[0] == "" || got[0] != got[len(got)-1] {
		t.Fatalf("headers with no session store = %v, want one stable non-empty id", got)
	}
}

func TestServer_OPG32_NotSignedInNamesTheRightProvider(t *testing.T) {
	fake := newGoServer(t)
	s := newGoModeServer(t, fake, false)
	s.cfg.Provider.OpenCodeGo.APIKey = ""
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hello"})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeProviderError {
		t.Fatalf("prompt = %+v, want provider_error", resp)
	}
	for _, want := range []string{"OpenCode Go", "nova login opencode-go", "OPENCODE_GO_API_KEY"} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Fatalf("message %q should mention %q, not OpenRouter's key", resp.Error.Message, want)
		}
	}
}
