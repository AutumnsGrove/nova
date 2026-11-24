package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/testkit"
)

// fakeGitHubREST records the REST paths it is asked for and answers 404, which
// ends a review before it reaches the model. The recorded path is the
// assertion: it is what proves the pull request number reached the client.
type fakeGitHubREST struct {
	mu    sync.Mutex
	paths []string
}

func (f *fakeGitHubREST) serve(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeGitHubREST) record(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()
	f.serve(w, r)
}

func (f *fakeGitHubREST) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// actionEnv points the command at a fake GitHub and a fake provider, so a
// review can be driven from the command line without touching either network.
func actionEnv(t *testing.T, apiURL string) {
	t.Helper()
	withServer(t, testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "unused"}}))
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
	ts := httptest.NewServer(http.HandlerFunc(gh.record))
	defer ts.Close()
	actionEnv(t, ts.URL)

	code, _, errOut := runCLI(t, "action", "review", "--pr", "7", "--dry-run")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 after the fake API refuses: %s", code, errOut)
	}
	paths := gh.seen()
	if len(paths) == 0 || paths[0] != "/repos/o/r/pulls/7" {
		t.Fatalf("requested %v, want /repos/o/r/pulls/7 first", paths)
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
