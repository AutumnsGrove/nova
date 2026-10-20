package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWebfetch_WFT43_CutsCountCharactersNotBytes(t *testing.T) {
	s := strings.Repeat("é", 100) // two bytes each
	for _, n := range []int{0, 1, 3, 99, 100, 101} {
		want := min(n, 100)
		if got := cutRunes(s, n); !utf8.ValidString(got) || utf8.RuneCountInString(got) != want {
			t.Fatalf("cutRunes(%d) = %q", n, got)
		}
		got, omitted := cutLines(s, n)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) != want || omitted != 100-want {
			t.Fatalf("cutLines(%d) = %q, omitted %d", n, got, omitted)
		}
	}
	if got, omitted := cutLines("ab\ncd\nef", 6); got != "ab\ncd" || omitted != 3 {
		t.Fatalf("cutLines should end at a line boundary: %q %d", got, omitted)
	}
}

func TestWebfetch_WFT44_ConnectionClosedAfterFetch(t *testing.T) {
	closed := make(chan struct{}, 4)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello")
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	srv.Start()
	defer srv.Close()
	if res := local(t, newLocal(), srv.URL, false, 0); res.IsError {
		t.Fatalf("result = %q", res.Content)
	}
	// Every fetch builds its own transport, so a kept-alive connection would
	// stay open forever with its two goroutines.
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was left open after the fetch returned")
	}
}

func TestWebfetch_WFT45_CancelIsNotATimeout(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	body, _ := json.Marshal(map[string]any{"url": srv.URL})
	done := make(chan string, 1)
	go func() {
		res, _ := newLocal().run(ctx, body, nil)
		done <- res.Content
	}()
	<-started
	cancel()
	select {
	case got := <-done:
		if strings.Contains(got, "timed out") || !strings.Contains(got, "cancelled") {
			t.Fatalf("result = %q, want a cancellation", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not return after cancel")
	}
}

func TestWebfetch_WFT46_RedirectTargetValidatedLikeTheURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "http://user:secret@127.0.0.1:1/x")
			w.WriteHeader(http.StatusFound)
			return
		}
		fmt.Fprint(w, "reached")
	}))
	defer srv.Close()
	res := local(t, newLocal(), srv.URL, false, 0)
	if !res.IsError || !strings.Contains(res.Content, "credentials") {
		t.Fatalf("result = %q, want the embedded credentials refused", res.Content)
	}
	if strings.Contains(res.Content, "secret") {
		t.Fatalf("the error echoed the credentials: %q", res.Content)
	}
}
