package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/testkit"
)

// fakeGitHubREST is a pull request with one review file, and it records every
// path it is asked for. A path it does not serve is a 404, which ends a review
// before it reaches the model: the recorded path is then the assertion.
type fakeGitHubREST struct {
	mu      sync.Mutex
	paths   []string
	reviews []postedReview
}

type postedReview struct {
	Body string `json:"body"`
	Path string `json:"path"`
}

func (f *fakeGitHubREST) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files"):
		reviewJSON(w, []map[string]any{{"filename": "a.go", "patch": "@@ -1,2 +1,2 @@\n a\n-b\n+B\n"}})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/reviews"):
		reviewJSON(w, []any{})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reviews"):
		var req postedReview
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		f.reviews = append(f.reviews, req)
		f.mu.Unlock()
		reviewJSON(w, map[string]any{"id": 1, "body": req.Body})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/"):
		reviewJSON(w, map[string]any{"number": 7, "title": "Fix things", "body": "Please review."})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// recordOnly notes the path and answers 404, which ends a review before it
// reaches the model: the recorded path is then the assertion.
func (f *fakeGitHubREST) recordOnly(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNotFound)
}

// reviewJSON writes a JSON response for the fake GitHub server.
func reviewJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeGitHubREST) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *fakeGitHubREST) posted() []postedReview {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]postedReview(nil), f.reviews...)
}

// actionEnv points the command at a fake GitHub and a fake provider, so a
// review can be driven from the command line without touching either network.
func actionEnv(t *testing.T, apiURL string, turns ...testkit.ServerTurn) {
	t.Helper()
	withServer(t, testkit.NewServer(t, turns...))
	t.Setenv("GITHUB_API_URL", apiURL)
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_EVENT_PATH", "")
}

// A pull request number has to be nameable from the command line: an
// issue_comment payload has no pull_request, and a manual run has no payload at
// all, so without the flag neither trigger can drive a review.
func TestCLI_ActionReviewPrFlag(t *testing.T) {
	gh := &fakeGitHubREST{}
	ts := httptest.NewServer(http.HandlerFunc(gh.recordOnly))
	defer ts.Close()
	actionEnv(t, ts.URL)

	code, _, errOut := runCLI(t, "action", "review", "--pr", "7", "--dry-run")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 after the fake API refuses: %s", code, errOut)
	}
	paths := gh.seen()
	if len(paths) == 0 || paths[0] != "GET /repos/o/r/pulls/7" {
		t.Fatalf("requested %v, want GET /repos/o/r/pulls/7 first", paths)
	}
}

// With no flag and no payload there is nothing to review, and the message has
// to say where a number could have come from rather than surfacing the failed
// read of an empty path.
func TestCLI_ActionReviewWithoutAPrIsAUsageError(t *testing.T) {
	actionEnv(t, "https://ghe.invalid/api/v3")

	code, out, errOut := runCLI(t, "action", "review", "--dry-run")
	if code != 2 {
		t.Fatalf("exit = %d, want 2: %s", code, out+errOut)
	}
	if !strings.Contains(out+errOut, "GITHUB_EVENT_PATH") {
		t.Fatalf("the message should name the missing input: %q", out+errOut)
	}
}

// The run is a read-only agent: the tools it was built with are the whole
// surface, and a policy that denies them leaves it with nothing to say. The
// command denies everything, so it has to allow those, or the review is empty
// and nothing is posted -- which is what it did, silently, until this test.
func TestCLI_ActionReviewCallsItsOwnTools(t *testing.T) {
	gh := &fakeGitHubREST{}
	ts := httptest.NewServer(http.HandlerFunc(gh.serve))
	defer ts.Close()
	actionEnv(t, ts.URL,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{
			{ID: "c1", Name: "review_summary", Args: `{"body":"One problem, see below."}`},
		}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
	)

	code, out, errOut := runCLI(t, "action", "review", "--pr", "7")
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s%s", code, out, errOut)
	}
	posted := gh.posted()
	if len(posted) != 1 {
		t.Fatalf("posted %d reviews, want 1 (paths: %v)", len(posted), gh.seen())
	}
	if !strings.Contains(posted[0].Body, "One problem, see below.") {
		t.Fatalf("the posted review does not carry the summary: %q", posted[0].Body)
	}
}

// The flag is not a fallback: when both are present the number on the command
// line wins, which is what lets a manual run review a pull request other than
// the one the event happened to name.
func TestCLI_ActionReviewPrFlagOverridesTheEvent(t *testing.T) {
	gh := &fakeGitHubREST{}
	ts := httptest.NewServer(http.HandlerFunc(gh.recordOnly))
	defer ts.Close()
	actionEnv(t, ts.URL)
	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(`{"pull_request":{"number":9}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_PATH", event)

	if code, _, errOut := runCLI(t, "action", "review", "--pr", "7", "--dry-run"); code != 1 {
		t.Fatalf("exit = %d, want 1: %s", code, errOut)
	}
	paths := gh.seen()
	if len(paths) == 0 || paths[0] != "GET /repos/o/r/pulls/7" {
		t.Fatalf("requested %v, want GET /repos/o/r/pulls/7 first", paths)
	}
}

// The decision to run follows the model. Before this, any OpenRouter key counted
// for every model, so an opencode-go review started with an OpenRouter key and
// then authenticated with nothing; and with no OpenRouter key it skipped green
// even though the model's own key was there.
func TestCLI_ActionReviewAsksTheModelsProviderForKey(t *testing.T) {
	gh := &fakeGitHubREST{}
	ts := httptest.NewServer(http.HandlerFunc(gh.recordOnly))
	defer ts.Close()
	actionEnv(t, ts.URL) // sets OPENROUTER_API_KEY
	t.Setenv("OPENCODE_GO_API_KEY", "")

	code, out, errOut := runCLI(t, "action", "review", "--pr", "7", "--model", "opencode-go/deepseek-v4.1-flash")
	if code != 0 {
		t.Fatalf("exit = %d, want the green skip: %s%s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "::notice::") {
		t.Fatalf("no skip notice: %q", out+errOut)
	}
	if seen := gh.seen(); len(seen) != 0 {
		t.Fatalf("the review ran with only another provider's key: %v", seen)
	}
}

// The key the run will authenticate with is not always in the environment. The
// request path resolves the opencode-go key from the config, then the
// environment, then the store /login writes -- but the decision to run looked
// only at the environment, so a config key skipped green on a review it could
// have posted, and the config's own model was never consulted at all (#146).
func TestCLI_ActionReviewFindsAKeyOutsideTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home string)
		args  []string
	}{
		{
			name: "config key and config model",
			setup: func(t *testing.T, home string) {
				writeHomeConfig(t, home, "model: opencode-go/deepseek-v4.1-flash\nprovider:\n  opencode_go:\n    api_key: config-go-key\n")
			},
		},
		{
			name: "login store key and config model",
			setup: func(t *testing.T, home string) {
				writeHomeConfig(t, home, "model: opencode-go/deepseek-v4.1-flash\n")
				if err := opencode.SaveKey(opencode.DefaultKeyPath(home), "stored-go-key"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "login store key and an explicit model",
			setup: func(t *testing.T, home string) {
				if err := opencode.SaveKey(opencode.DefaultKeyPath(home), "stored-go-key"); err != nil {
					t.Fatal(err)
				}
			},
			args: []string{"--model", "opencode-go/deepseek-v4.1-flash"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testkit.NewServer(t,
				testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{
					{ID: "c1", Name: "review_summary", Args: `{"body":"Nothing to flag."}`},
				}}},
				testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
			)
			gh := &fakeGitHubREST{}
			ts := httptest.NewServer(http.HandlerFunc(gh.serve))
			defer ts.Close()
			actionEnv(t, ts.URL)
			// The review authenticates against the opencode-go endpoint, so it
			// has to be the fake provider, not the real one.
			t.Setenv("NOVA_OPENCODE_GO_BASE_URL", srv.URL)
			// No key in the environment at all: the config or the store has to
			// be what the gate sees, or the test proves nothing.
			t.Setenv("OPENCODE_GO_API_KEY", "")
			t.Setenv("OPENROUTER_API_KEY", "")
			home := os.Getenv("HOME")
			tc.setup(t, home)

			code, out, errOut := runCLI(t, append([]string{"action", "review", "--pr", "7"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("exit = %d: %s%s", code, out, errOut)
			}
			if strings.Contains(out+errOut, "::notice::") {
				t.Fatalf("the review skipped with a key it could have used: %q", out+errOut)
			}
			if posted := gh.posted(); len(posted) != 1 {
				t.Fatalf("posted %d reviews, want 1 (paths: %v)", len(posted), gh.seen())
			}
			if reqs := srv.Requests(); len(reqs) == 0 {
				t.Fatal("the model was never called")
			}
		})
	}
}

// writeHomeConfig writes the user config beside the home directory the command
// reads.
func writeHomeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "nova")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A review that is going to skip green must not pay for what it will not use.
// The decision is made on the loaded config, before a server exists, because
// building one starts the extension host and every configured MCP server and
// Close then waits out their handshakes -- so a notice cost a minute and left
// subprocesses started and killed for nothing (#146).
func TestCLI_ActionReviewSkipStartsNothing(t *testing.T) {
	gh := &fakeGitHubREST{}
	ts := httptest.NewServer(http.HandlerFunc(gh.recordOnly))
	defer ts.Close()
	actionEnv(t, ts.URL)
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_KEY", "")
	home := os.Getenv("HOME")
	marker := filepath.Join(home, "mcp-started")
	writeHomeConfig(t, home, "mcp:\n  servers:\n    - name: marker\n      command: /bin/sh\n"+
		"      args: [\"-c\", \"touch "+marker+"; sleep 30\"]\n      timeout_ms: 500\n")

	code, out, errOut := runCLI(t, "action", "review", "--pr", "7")
	if code != 0 {
		t.Fatalf("exit = %d, want the green skip: %s%s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "::notice::") {
		t.Fatalf("no skip notice: %q", out+errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the skip started a configured MCP server it never used")
	}
}
