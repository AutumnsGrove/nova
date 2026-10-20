package ssestream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/provider"
)

// errorFor serves one non-2xx response and returns the *provider.Error Open
// built from it, sending the given headers on the request.
func errorFor(t *testing.T, status int, body string, headers http.Header) *provider.Error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	_, err := Open(context.Background(), Config{}, srv.URL, headers, []byte(`{}`))
	var perr *provider.Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *provider.Error", err)
	}
	return perr
}

func TestSSEStream_TMO18_ErrorMessageIsBoundedAndValidUTF8(t *testing.T) {
	// "é" is two bytes, so a cut at a byte count lands inside a rune unless it
	// is made rune-aware. The JSON message is as long as the prose is.
	long := strings.Repeat("é", 600)
	for name, body := range map[string]string{
		"json message": `{"error":{"message":"` + long + `"}}`,
		"prose":        "<p>" + long + "</p>",
	} {
		t.Run(name, func(t *testing.T) {
			perr := errorFor(t, http.StatusBadGateway, body, http.Header{})
			if !utf8.ValidString(perr.Message) {
				t.Errorf("message is not valid UTF-8: %q", perr.Message)
			}
			if len(perr.Message) == 0 || len(perr.Message) > maxErrorMessage {
				t.Errorf("message is %d bytes, want 1 to %d", len(perr.Message), maxErrorMessage)
			}
		})
	}
}

func TestSSEStream_TMO19_ErrorBodyNeverEchoesTheKey(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+key)
	headers.Set("X-Api-Key", key)
	perr := errorFor(t, http.StatusUnauthorized,
		`{"error":{"code":"bad_key","message":"invalid key `+key+` supplied"}}`, headers)
	if !strings.Contains(perr.Message, "invalid key") {
		t.Fatalf("message = %q, want the rest of the message kept", perr.Message)
	}
	if strings.Contains(perr.Message, key) || strings.Contains(perr.Error(), key) {
		t.Fatalf("error leaks the key: %v", perr)
	}
}
