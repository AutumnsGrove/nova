package mode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// failServer answers every chat request (a POST) with status and body, and
// ignores the background model discovery GET, so a run against it fails once.
func failServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runEvents sends a prompt and returns every event up to run_end.
func runEvents(t *testing.T, s *Server, text string) []proto.Event {
	t.Helper()
	events, cancel := s.Subscribe(256)
	defer cancel()
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: text}); !resp.OK {
		t.Fatalf("prompt = %+v, want it accepted so the run can fail", resp)
	}
	return collect(t, events)
}

// runError returns the message of the run's one error event, failing when the
// run did not fail.
func runError(t *testing.T, events []proto.Event) string {
	t.Helper()
	for _, ev := range events {
		if ev.Type == proto.EventError && ev.Error != nil {
			return ev.Error.Message
		}
	}
	t.Fatalf("no error event in %d events", len(events))
	return ""
}

// signedInServer builds a server on the real provider wiring, signed in to both
// providers, with the given model and each provider's base URL.
func signedInServer(t *testing.T, model, openRouterURL, goURL string) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test-key-0001")
	cfg := config.Defaults()
	cfg.Model = model
	cfg.Tools.Active = []string{"read"}
	cfg.Provider.OpenRouter.BaseURL = openRouterURL
	cfg.Provider.OpenCodeGo.BaseURL = goURL
	cfg.Provider.OpenCodeGo.APIKey = "go-key-0000001"
	s, err := New(Options{Config: &cfg, CWD: t.TempDir(), Home: home, Session: filepath.Join(t.TempDir(), "s.jsonl")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

const userNotFound = `{"error":{"message":"User not found.","code":401}}`

func TestServer_OPG39_ARejectedOpenRouterKeyTellsTheUserToSignInAgain(t *testing.T) {
	or := failServer(t, http.StatusUnauthorized, userNotFound)
	s := signedInServer(t, config.Defaults().Model, or.URL, "http://127.0.0.1:1")
	msg := runError(t, runEvents(t, s, "hi"))
	if !strings.Contains(msg, "User not found.") {
		t.Fatalf("message %q lost the provider's own text", msg)
	}
	if !strings.Contains(msg, `run "nova login"`) {
		t.Fatalf("message %q should tell the user to run nova login", msg)
	}
	if strings.Contains(msg, "sk-or-test-key-0001") {
		t.Fatalf("message %q leaks the key", msg)
	}
}

func TestServer_OPG40_TheHintFollowsTheModelsProvider(t *testing.T) {
	// The user is signed in to both, but the model is an OpenCode Go one and
	// OpenCode Go rejects its key. Telling them to run plain `nova login`
	// would re-do OpenRouter and leave the real problem.
	goSrv := failServer(t, http.StatusUnauthorized, `{"error":{"message":"Invalid API key"}}`)
	s := signedInServer(t, "opencode-go/glm-5.3", "http://127.0.0.1:1", goSrv.URL)
	msg := runError(t, runEvents(t, s, "hi"))
	if !strings.Contains(msg, "Invalid API key") {
		t.Fatalf("message %q lost the provider's own text", msg)
	}
	if !strings.Contains(msg, `run "nova login opencode-go"`) {
		t.Fatalf("message %q should name nova login opencode-go", msg)
	}
	if strings.Contains(msg, `"nova login"`) {
		t.Fatalf("message %q sends an OpenCode Go user to the OpenRouter login", msg)
	}
}

func TestServer_OPG41_OtherFailuresGetNoHint(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			or := failServer(t, status, `{"error":{"message":"the provider says no","code":"x"}}`)
			s := signedInServer(t, config.Defaults().Model, or.URL, "http://127.0.0.1:1")
			msg := runError(t, runEvents(t, s, "hi"))
			if !strings.Contains(msg, "the provider says no") {
				t.Fatalf("message %q lost the provider's own text", msg)
			}
			if strings.Contains(msg, "nova login") {
				t.Fatalf("message %q blames the key for a %d", msg, status)
			}
		})
	}
}

func TestServer_OPG42_AnEmbedderWithItsOwnProviderGetsNoLoginHint(t *testing.T) {
	// ForModel falls back to OpenRouter for any unknown model, so without a
	// guard a library user with their own provider would be told to run
	// `nova login` for a key Nova never touched.
	s, _ := testServer(t, testkit.Turn{Err: &provider.Error{Status: 401, Message: "bad token"}})
	msg := runError(t, runEvents(t, s, "hi"))
	if !strings.Contains(msg, "bad token") || strings.Contains(msg, "nova login") {
		t.Fatalf("message %q, want the provider's text and no login hint", msg)
	}
}

func TestServer_OPG43_AHealthyRunPrintsNoHint(t *testing.T) {
	fake := newGoServer(t)
	s := newGoModeServer(t, fake, false)
	for _, ev := range runEvents(t, s, "hi") {
		if ev.Type == proto.EventError {
			t.Fatalf("healthy run emitted an error event: %+v", ev.Error)
		}
	}
}
