package openaicompat

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

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
