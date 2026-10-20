package ssestream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

func TestSSEStream_TMO20_CutAndOversizeLinesAreProviderErrors(t *testing.T) {
	// The agent retries only a *provider.Error, so a bare sentinel would make a
	// cut connection fatal on the dialects that returned it as is.
	t.Run("cut mid-line is retryable", func(t *testing.T) {
		srv := sseServer(t, "data: one\n\ndata: cut")
		s, err := open(context.Background(), t, Config{}, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		s.Next()
		_, err = s.Next()
		var perr *provider.Error
		if !errors.As(err, &perr) || !perr.Retryable || !errors.Is(err, ErrTruncatedLine) {
			t.Fatalf("err = %v, want a retryable provider error wrapping ErrTruncatedLine", err)
		}
	})
	t.Run("over the cap is not retryable", func(t *testing.T) {
		srv := sseServer(t, "data: "+strings.Repeat("x", 4096)+"\n\n")
		s, err := open(context.Background(), t, Config{MaxLineBytes: 1024}, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		_, err = s.Next()
		var perr *provider.Error
		var tooLong *LineTooLongError
		if !errors.As(err, &perr) || perr.Retryable || !errors.As(err, &tooLong) {
			t.Fatalf("err = %v, want a non-retryable provider error wrapping *LineTooLongError", err)
		}
	})
}

// A line longer than the reader's buffer but inside the cap must come through
// whole, and one exactly at the cap must be accepted with either terminator.
func TestSSEStream_TMO21_LongLineAcrossBufferRefills(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		big := strings.Repeat("y", 3*readBufferBytes+17)
		srv := sseServer(t, "data: "+big+eol+eol)
		s, err := open(context.Background(), t, Config{MaxLineBytes: len("data: " + big)}, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		ev, err := s.Next()
		s.Close()
		if err != nil || string(ev.Data) != big {
			t.Fatalf("eol %q: err = %v, data length %d, want %d", eol, err, len(ev.Data), len(big))
		}
	}
}

func TestSSEStream_TMO22_EachAndDecode(t *testing.T) {
	srv := sseServer(t, "event: ping\n\ndata: {\"n\":1}\n\ndata: {\"n\":2}\n\ndata: {\"n\":3}\n\n")
	s, err := open(context.Background(), t, Config{}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got []int
	err = s.Each(func(ev Event) (bool, error) {
		var v struct{ N int }
		if err := ev.Decode(&v); err != nil {
			return false, err
		}
		got = append(got, v.N)
		return v.N == 2, nil // stop early: the bare ping event was skipped
	})
	if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("got %v err %v, want 1 and 2 then a clean stop", got, err)
	}

	bad := sseServer(t, "data: {\"n\":1}\n\ndata: {oops\n\n")
	s2, err := open(context.Background(), t, Config{}, bad.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	err = s2.Each(func(ev Event) (bool, error) {
		var v struct{ N int }
		return false, ev.Decode(&v)
	})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Retryable || !strings.Contains(perr.Message, "line 3") {
		t.Fatalf("err = %v, want a non-retryable error naming line 3", err)
	}
}

func TestSSEStream_TMO23_TargetHeaderPrecedence(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(srv.Close)
	target := Target{
		Dialect: "d",
		URL:     srv.URL,
		Headers: map[string]string{"X-Cfg": "cfg", "X-Both": "cfg", "X-Fixed": "cfg", "Authorization": "Bearer stray"},
		Fixed:   map[string]string{"X-Fixed": "fixed"},
		APIKey:  "static-key-0000", APIKeyFunc: func(context.Context) (string, error) { return "fresh-key-1111", nil },
	}
	s, err := target.Open(context.Background(), map[string]string{"X-Both": "req", "X-Fixed": "req"}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for k, want := range map[string]string{
		"X-Cfg": "cfg", "X-Both": "req", "X-Fixed": "fixed", "Authorization": "Bearer fresh-key-1111",
	} {
		if got.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.Get(k), want)
		}
	}

	target.KeyHeader, target.KeyPrefix, target.APIKeyFunc = "x-api-key", "", nil
	s, err = target.Open(context.Background(), nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got.Get("x-api-key") != "static-key-0000" {
		t.Errorf("x-api-key = %q", got.Get("x-api-key"))
	}
}

func TestSSEStream_TMO24_TargetKeyFuncErrorAndCheck(t *testing.T) {
	target := Target{Dialect: "d", URL: "http://127.0.0.1:1", APIKeyFunc: func(context.Context) (string, error) {
		return "", errors.New("no key")
	}}
	_, err := target.Open(context.Background(), nil, nil)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Retryable {
		t.Fatalf("err = %v, want a non-retryable provider error and no request", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		ctx           context.Context
		url, model    string
		want          string
		wantCancelled bool
	}{
		"cancelled": {ctx: cancelled, url: "u", model: "m", wantCancelled: true},
		"no model":  {ctx: context.Background(), url: "u", want: "d: model is required"},
		"no url":    {ctx: context.Background(), model: "m", want: "d: base URL is required"},
	} {
		err := Target{Dialect: "d", URL: tc.url}.Check(tc.ctx, tc.model)
		if tc.wantCancelled && !errors.Is(err, context.Canceled) || !tc.wantCancelled && (err == nil || err.Error() != tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := (Target{Dialect: "d", URL: "u"}).Check(context.Background(), "m"); err != nil {
		t.Errorf("a valid request = %v", err)
	}
}
