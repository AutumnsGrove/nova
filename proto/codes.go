// Package proto defines the wire types and JSONL framing for Nova's single
// protocol. It is the contract every front door speaks: the TUI, RPC mode,
// serve, tests, and extension processes.
package proto

// Version is the protocol version carried in the hello exchange.
const Version = 1

// Frame type names.
const (
	FrameHello      = "hello"
	FrameResponse   = "response"
	FrameUIRequest  = "ui_request"
	FrameUIResponse = "ui_response"
)

// Error codes. They are stable strings a client can branch on.
const (
	CodeBadRequest       = "bad_request"
	CodeBusy             = "busy"
	CodeNotFound         = "not_found"
	CodePermissionDenied = "permission_denied"
	CodeUnsupported      = "unsupported"
	CodeTimeout          = "timeout"
	CodeProviderError    = "provider_error"
	CodeInternal         = "internal"
)

// Command names. A command frame's type is the command name itself.
const (
	CmdPrompt          = "prompt"
	CmdSteer           = "steer"
	CmdFollowUp        = "follow_up"
	CmdAbort           = "abort"
	CmdGetState        = "get_state"
	CmdSetModel        = "set_model"
	CmdSetThinking     = "set_thinking"
	CmdSetContextLimit = "set_context_limit"
	CmdSetTools        = "set_tools"
	CmdCompact         = "compact"
	CmdSessionNew      = "session.new"
	CmdSessionResume   = "session.resume"
	CmdSessionFork     = "session.fork"
	CmdSessionTree     = "session.tree"
	CmdSessionNavigate = "session.navigate"
	CmdSessionName     = "session.name"
	CmdReload          = "reload"
	CmdGetMessages     = "get_messages"
	CmdGetCommands     = "get_commands"
	CmdListModels      = "list_models"
	CmdSessionList     = "session.list"
	CmdGetExtensions   = "get_extensions"
	CmdGetUsage        = "get_usage"
	CmdRunCommand      = "run_command"
	CmdBash            = "bash"
	CmdShutdown        = "shutdown"
	CmdListProviders   = "list_providers"
	CmdDecide          = "decide"
	CmdLogin           = "login"
	CmdLoginSubmit     = "login_submit"
	CmdLoginCancel     = "login_cancel"
	CmdLogout          = "logout"
)

// Event names. They are not commands, so Decode maps them to Event.
const (
	EventRunStart       = "run_start"
	EventTurnStart      = "turn_start"
	EventMessageStart   = "message_start"
	EventTextDelta      = "text_delta"
	EventThinkingStart  = "thinking_start"
	EventThinkingDelta  = "thinking_delta"
	EventThinkingEnd    = "thinking_end"
	EventToolCall       = "tool_call"
	EventToolStart      = "tool_start"
	EventToolUpdate     = "tool_update"
	EventToolEnd        = "tool_end"
	EventPermission     = "permission"
	EventMessageEnd     = "message_end"
	EventTurnEnd        = "turn_end"
	EventUsage          = "usage"
	EventRetry          = "retry"
	EventCompaction     = "compaction"
	EventQueueUpdate    = "queue_update"
	EventRunEnd         = "run_end"
	EventError          = "error"
	EventExtensionError = "extension_error"
	EventUIExpired      = "ui_expired"
	EventSessionChanged = "session_changed"
	EventStateChanged   = "state_changed"
	EventLoginEnd       = "login_end"
)

// eventNames is the set Decode sends to Event rather than Command.
var eventNames = map[string]bool{
	EventRunStart: true, EventTurnStart: true, EventMessageStart: true,
	EventTextDelta: true, EventThinkingStart: true, EventThinkingDelta: true,
	EventThinkingEnd: true, EventToolCall: true, EventToolStart: true,
	EventToolUpdate: true, EventToolEnd: true, EventPermission: true,
	EventMessageEnd: true, EventTurnEnd: true, EventUsage: true, EventRetry: true,
	EventCompaction: true, EventQueueUpdate: true, EventRunEnd: true, EventError: true,
	EventExtensionError: true, EventUIExpired: true, EventSessionChanged: true,
	EventStateChanged: true, EventLoginEnd: true,
}
