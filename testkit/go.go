package testkit

import (
	"testing"
)

// SessionHeader is the conversation-id header OpenCode Go requires on every
// inference request. The double refuses a streaming request without it, using
// the live MissingSessionID shape, so a test pins the requirement itself.
const SessionHeader = "x-opencode-session"

// missingSessionBody is the live 400 body for a request without the session
// header, captured 2026-11-05.
const missingSessionBody = `{"type":"error","error":{"type":"MissingSessionID","message":"Request is missing x-opencode-session and cannot be routed efficiently."}}`

// GoConfig configures the Go-dialect behaviors of a test Server: the session
// requirement, the allowance usage block, and the status endpoint. A plain
// NewServer has none of them, so OpenRouter-shaped tests are unaffected.
type GoConfig struct {
	// RequireSession refuses a streaming request without x-opencode-session.
	// The refusal happens before a scripted turn is consumed, because a
	// refused request never reaches the model.
	RequireSession bool
	// StatusBody, when non-nil, is served verbatim at GET /api/go/status.
	StatusBody []byte
}

// NewGoServer starts a Server with the Go-dialect behaviors in cfg. It is
// closed when the test's cleanup runs.
func NewGoServer(t testing.TB, cfg GoConfig, turns ...ServerTurn) *Server {
	t.Helper()
	s := NewServer(t, turns...)
	s.goConfig = cfg
	return s
}
