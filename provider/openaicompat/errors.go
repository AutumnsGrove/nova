package openaicompat

import (
	"net/http"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// streamErrorObject turns a 200 response whose chunk is an error object into a
// provider error. The HTTP status alone said success, so the code and message
// decide whether a retry can help.
func streamErrorObject(o *errorObject) *provider.Error {
	code := ssestream.CodeString(o.Code)
	return &provider.Error{
		Status:    http.StatusOK,
		Code:      code,
		Message:   o.Message,
		Retryable: ssestream.StreamErrorRetryable(code, o.Message),
	}
}
