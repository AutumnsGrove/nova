package openaicompat

import (
	"net/http"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// streamErrorObject turns a 200 response whose chunk is an error object into a
// provider error. A rate-limit code or an "overload" message is the only thing
// worth retrying, because the HTTP status alone said success.
func streamErrorObject(o *errorObject) *provider.Error {
	code := ssestream.CodeString(o.Code)
	retryable := code == "rate_limit_exceeded" ||
		strings.Contains(strings.ToLower(o.Message), "overload")
	return &provider.Error{
		Status:    http.StatusOK,
		Code:      code,
		Message:   o.Message,
		Retryable: retryable,
	}
}
