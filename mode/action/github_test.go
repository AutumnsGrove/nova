package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub starts a server and a client that never really sleeps.
func fakeGitHub(t *testing.T, handler http.HandlerFunc) (*GitHub, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	g := NewGitHub(ts.URL, "secret-token")
	g.Sleep = func(context.Context, time.Duration) error { return nil }
	return g, ts
}

// filesPage serves a JSON array of n files.
func filesPage(w http.ResponseWriter, n int) {
	files := make([]File, n)
	for i := range files {
		files[i] = File{Filename: fmt.Sprintf("f%d.go", i), Patch: "@@ -1 +1 @@\n+x\n"}
	}
	_ = json.NewEncoder(w).Encode(files)
}

func TestAction_GHC01_Pagination(t *testing.T) {
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1", "2":
			filesPage(w, 100)
		case "3":
			filesPage(w, 50)
		default:
			filesPage(w, 0)
		}
	})
	files, capped, err := g.Files(context.Background(), "o/r", 1)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 250 || capped {
		t.Fatalf("files = %d capped = %v, want 250 false", len(files), capped)
	}
}

func TestAction_GHC02_MoreThanThreeHundred(t *testing.T) {
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) { filesPage(w, 100) })
	files, capped, err := g.Files(context.Background(), "o/r", 1)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 300 || !capped {
		t.Fatalf("files = %d capped = %v, want 300 true", len(files), capped)
	}
}

func TestAction_GHC03_ForbiddenRetryAfter(t *testing.T) {
	var calls atomic.Int32
	var delays []time.Duration
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(Pull{Number: 1, Title: "t"})
	})
	g.Sleep = func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
	if _, err := g.Pull(context.Background(), "o/r", 1); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
	if len(delays) != 2 || delays[0] != 2*time.Second {
		t.Fatalf("delays = %v, want two 2s waits", delays)
	}
}

func TestAction_GHC04_TooManyRequests(t *testing.T) {
	var calls atomic.Int32
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(Pull{Number: 1})
	})
	if _, err := g.Pull(context.Background(), "o/r", 1); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestAction_GHC05_ServerErrorGivesUp(t *testing.T) {
	var calls atomic.Int32
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusBadGateway)
	})
	_, err := g.Pull(context.Background(), "o/r", 1)
	if err == nil {
		t.Fatal("want an error after retries")
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want 4 (initial + 3 retries)", calls.Load())
	}
}

func TestAction_GHC06_NotFoundNoRetry(t *testing.T) {
	var calls atomic.Int32
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "missing", http.StatusNotFound)
	})
	_, err := g.Pull(context.Background(), "o/r", 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want a 404 APIError", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestAction_GHC07_HeadersSent(t *testing.T) {
	var got http.Header
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_ = json.NewEncoder(w).Encode(Pull{Number: 1})
	})
	if _, err := g.Pull(context.Background(), "o/r", 1); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got.Get("Authorization"))
	}
	if got.Get("Accept") != "application/vnd.github+json" {
		t.Fatalf("Accept = %q", got.Get("Accept"))
	}
	if got.Get("X-GitHub-Api-Version") != apiVersion {
		t.Fatalf("X-GitHub-Api-Version = %q", got.Get("X-GitHub-Api-Version"))
	}
	if got.Get("User-Agent") != "nova-action" {
		t.Fatalf("User-Agent = %q", got.Get("User-Agent"))
	}
}

func TestAction_GHC08_TokenNeverPrinted(t *testing.T) {
	const token = "ghp_supersecretvalue"
	g, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"nope"}`, http.StatusInternalServerError)
	})
	g.Token = token
	_, err := g.Pull(context.Background(), "o/r", 1)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaks the token: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", g), token) {
		t.Fatalf("client print leaks the token: %+v", g)
	}
}

func TestAction_GHC09_APIURLOverride(t *testing.T) {
	var hit string
	g, ts := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_ = json.NewEncoder(w).Encode(Pull{Number: 1})
	})
	if _, err := g.Pull(context.Background(), "o/r", 7); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if hit != "/repos/o/r/pulls/7" {
		t.Fatalf("path = %q, want /repos/o/r/pulls/7", hit)
	}
	if !strings.HasPrefix(ts.URL, "http://127.0.0.1:") {
		t.Fatalf("fake server URL = %q", ts.URL)
	}
}
