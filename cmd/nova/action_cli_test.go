package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the pull request itself is served here: the review has to stop
		// at the 404 for anything else, which leaves the path as the evidence.
		if !strings.HasSuffix(r.URL.Path, "/pulls/7") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gh.serve(w, r)
	}))
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
