package openaicompat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

// maxErrorBody bounds how much of a failed response body is read before it is
// classified, so a hostile server cannot make the client allocate freely.
const maxErrorBody = 1 << 20

// maxErrorMessage bounds the human-readable text kept from a body that has no
// JSON message.
const maxErrorMessage = 500

// errLineTooLong reports an SSE line over Config.MaxLineBytes. It is a
// programmer- or server-side fault, never worth retrying.
type errLineTooLong struct{ limit int }

func (e *errLineTooLong) Error() string {
	return fmt.Sprintf("openaicompat: SSE line exceeds the %d byte limit", e.limit)
}

// httpError classifies a non-2xx response. The status and Retry-After come from
// provider.Classify; the message is the provider's error text when the body has
// one and tag-stripped prose otherwise.
func httpError(status int, header http.Header, body []byte) *provider.Error {
	retryable, after := provider.Classify(status, header.Get("Retry-After"))
	code, message := bodyError(body)
	return &provider.Error{
		Status:     status,
		Code:       code,
		Message:    message,
		Retryable:  retryable,
		RetryAfter: after,
	}
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

// errorPayload is the body shape shared by `{"error":{...}}` and `{"message":...}`.
type errorPayload struct {
	Error   *errorObject `json:"error"`
	Message string       `json:"message"`
	Code    any          `json:"code"`
}

// streamErrorObject turns a 200 response whose first chunk is an error object
// into a provider error. A rate-limit code or an "overload" message is the only
// thing worth retrying, because the HTTP status alone said success.
func streamErrorObject(o *errorObject) *provider.Error {
	code := codeString(o.Code)
	retryable := code == "rate_limit_exceeded" ||
		strings.Contains(strings.ToLower(o.Message), "overload")
	return &provider.Error{
		Status:    http.StatusOK,
		Code:      code,
		Message:   o.Message,
		Retryable: retryable,
	}
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
// a body and trims the result, so an HTML error page reads as prose.
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
	out := strings.TrimSpace(b.String())
	if len(out) > maxErrorMessage {
		out = out[:maxErrorMessage]
	}
	return out
}

// readCapped reads up to maxErrorBody bytes, so a misbehaving server cannot
// make the error path read without bound.
func readCapped(r io.Reader) []byte {
	body, _ := io.ReadAll(io.LimitReader(r, maxErrorBody))
	return body
}
