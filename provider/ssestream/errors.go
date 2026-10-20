package ssestream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/provider"
)

// httpError builds a provider.Error for a non-2xx response. The status and
// Retry-After come from provider.Classify; the code and message come from the
// body (see bodyError). The message is scrubbed of the request's credentials
// before it is cut to length, because a gateway that echoes a rejected key back
// must not put it in an error that reaches logs and the screen.
func httpError(status int, header http.Header, body []byte, secrets []string) *provider.Error {
	retryable, retryAfter := provider.Classify(status, header.Get("Retry-After"))
	code, message := bodyError(body)
	scrub := redactor(secrets)
	return &provider.Error{
		Status:     status,
		Code:       truncateUTF8(scrub(code), maxErrorMessage),
		Message:    truncateUTF8(scrub(message), maxErrorMessage),
		Retryable:  retryable,
		RetryAfter: retryAfter,
	}
}

// credentialHeaders are the request headers that carry a secret.
var credentialHeaders = []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "Api-Key"}

// minSecretLen is the shortest credential worth scrubbing. A shorter string
// would also match ordinary words in an error message.
const minSecretLen = 8

// credentials returns the secret token of each credential header, without the
// auth scheme ("Bearer ") that precedes it.
func credentials(h http.Header) []string {
	var out []string
	for _, name := range credentialHeaders {
		for _, value := range h.Values(name) {
			if f := strings.Fields(value); len(f) > 0 && len(f[len(f)-1]) >= minSecretLen {
				out = append(out, f[len(f)-1])
			}
		}
	}
	return out
}

// redactor returns a function that replaces every secret in a string.
func redactor(secrets []string) func(string) string {
	if len(secrets) == 0 {
		return func(s string) string { return s }
	}
	pairs := make([]string, 0, 2*len(secrets))
	for _, s := range secrets {
		pairs = append(pairs, s, "[redacted]")
	}
	return strings.NewReplacer(pairs...).Replace
}

// truncateUTF8 cuts s to at most n bytes without splitting a multi-byte rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// errorPayload is the body shape shared by `{"error":{...}}` and `{"message":...}`.
type errorPayload struct {
	Error *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
	Code    any    `json:"code"`
}

// bodyError pulls the machine code and human message out of a failed body,
// preferring error.message, then a top-level message, then tag-stripped prose.
// Neither value is length-bounded here; httpError does that.
func bodyError(body []byte) (code, message string) {
	var p errorPayload
	if json.Unmarshal(body, &p) != nil {
		return "", stripHTML(body)
	}
	code, message = CodeString(p.Code), p.Message
	if p.Error != nil {
		code = CodeString(p.Error.Code)
		if p.Error.Message != "" {
			message = p.Error.Message
		}
	}
	if message == "" {
		message = stripHTML(body)
	}
	return code, message
}

// CodeString renders a JSON error code that may be a string, a number, or
// absent. Every dialect's error objects share it.
func CodeString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// stripHTML removes angle-bracket tags from a body and collapses whitespace,
// so an HTML error page reads as prose. The caller bounds the length.
func stripHTML(body []byte) string {
	var b strings.Builder
	inTag := false
	for _, c := range body {
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag:
			b.WriteByte(c)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
