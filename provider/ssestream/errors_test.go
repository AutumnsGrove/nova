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

// The raw body is kept for callers that parse it themselves (#40). It is
// scrubbed like Message and bounded, but never tag-stripped, because the whole
// point is that the caller sees what the server sent.
func TestSSEStream_ErrorKeepsTheRawBody(t *testing.T) {
	body := `{"error":{"message":"rate limited","metadata":{"provider_name":"Baidu"}}}`
	perr := errorFor(t, http.StatusTooManyRequests, body, http.Header{})
	if perr.Body != body {
		t.Fatalf("Body = %q, want the response verbatim", perr.Body)
	}
	if strings.Contains(perr.Error(), "Baidu") {
		t.Errorf("Error() = %q: the body must not leak into the error text", perr.Error())
	}
}

func TestSSEStream_ErrorBodyIsNotTagStripped(t *testing.T) {
	body := `<html><body><h1>Bad gateway</h1></body></html>`
	perr := errorFor(t, http.StatusBadGateway, body, http.Header{})
	if perr.Body != body {
		t.Errorf("Body = %q, want the HTML kept raw", perr.Body)
	}
	if strings.Contains(perr.Message, "<") {
		t.Errorf("Message = %q, want tags stripped as before", perr.Message)
	}
}

func TestSSEStream_ErrorBodyNeverEchoesTheKey(t *testing.T) {
	const key = "sk-test-0123456789abcdef"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+key)
	perr := errorFor(t, http.StatusUnauthorized, `{"error":{"message":"invalid key `+key+`"}}`, headers)
	if !strings.Contains(perr.Body, "invalid key") {
		t.Fatalf("Body = %q, want the rest of the body kept", perr.Body)
	}
	if strings.Contains(perr.Body, key) {
		t.Fatalf("Body leaks the key: %q", perr.Body)
	}
}

func TestSSEStream_ErrorBodyIsBoundedAndValidUTF8(t *testing.T) {
	perr := errorFor(t, http.StatusBadGateway, strings.Repeat("é", maxErrorBodyKept), http.Header{})
	if !utf8.ValidString(perr.Body) {
		t.Errorf("Body is not valid UTF-8")
	}
	if len(perr.Body) == 0 || len(perr.Body) > maxErrorBodyKept {
		t.Errorf("Body is %d bytes, want 1 to %d", len(perr.Body), maxErrorBodyKept)
	}
}
