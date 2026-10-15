package ssestream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

// httpError builds a provider.Error for a non-2xx response. The status and
// Retry-After come from provider.Classify; the code and message come from the
// body (see bodyError).
func httpError(status int, header http.Header, body []byte) *provider.Error {
	retryable, retryAfter := provider.Classify(status, header.Get("Retry-After"))
	code, message := bodyError(body)
	return &provider.Error{Status: status, Code: code, Message: message, Retryable: retryable, RetryAfter: retryAfter}
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
func bodyError(body []byte) (code, message string) {
	var p errorPayload
	if json.Unmarshal(body, &p) != nil {
		return "", stripHTML(body)
	}
	code, message = codeString(p.Code), p.Message
	if p.Error != nil {
		code = codeString(p.Error.Code)
		if p.Error.Message != "" {
			message = p.Error.Message
		}
	}
	if message == "" {
		message = stripHTML(body)
	}
	return code, message
}

// codeString renders a JSON code that may be a string, a number, or absent.
func codeString(v any) string {
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

// stripHTML removes angle-bracket tags from the first maxErrorMessage bytes of
// a body and collapses whitespace, so an HTML error page reads as prose.
func stripHTML(body []byte) string {
	if len(body) > maxErrorMessage {
		body = body[:maxErrorMessage]
	}
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
	out := strings.Join(strings.Fields(b.String()), " ")
	if len(out) > maxErrorMessage {
		out = out[:maxErrorMessage]
	}
	return out
}
