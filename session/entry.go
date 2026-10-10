// Package session stores a conversation as an append-only JSONL tree with
// branches, custom entries, and compaction, and rebuilds the model-facing
// context from it.
package session

import (
	"encoding/json"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// Entry type names. A type the package does not know is kept verbatim.
const (
	TypeMessage       = "message"
	TypeModelChange   = "model_change"
	TypeCompaction    = "compaction"
	TypeBranchSummary = "branch_summary"
	TypeCustom        = "custom"
	TypeCustomMessage = "custom_message"
	TypeLabel         = "label"
	TypeSessionInfo   = "session_info"
)

// Entry is one line of a session file.
type Entry struct {
	Type   string    `json:"type"`
	ID     string    `json:"id"`
	Parent string    `json:"parent"`
	TS     time.Time `json:"ts"`

	Message      *message.Message `json:"message,omitempty"`
	Usage        *provider.Usage  `json:"usage,omitempty"`
	Model        string           `json:"model,omitempty"`
	Summary      string           `json:"summary,omitempty"`
	FirstKept    string           `json:"first_kept,omitempty"`
	TokensBefore int              `json:"tokens_before,omitempty"`
	From         string           `json:"from,omitempty"`
	Kind         string           `json:"kind,omitempty"`
	Data         json.RawMessage  `json:"data,omitempty"`
	Keep         bool             `json:"keep,omitempty"`
	Content      string           `json:"content,omitempty"`
	Display      bool             `json:"display,omitempty"`
	InContext    bool             `json:"in_context,omitempty"`
	Target       string           `json:"target,omitempty"`
	Label        string           `json:"label,omitempty"`
	Name         string           `json:"name,omitempty"`

	// Raw holds the original line for an entry whose Type is unknown.
	Raw json.RawMessage `json:"-"`
	// line is the 1-based file line, used for warnings.
	line int
}

// displayOnly reports whether an entry must not become the active leaf.
func (e Entry) displayOnly() bool {
	return e.Type == TypeLabel || e.Type == TypeSessionInfo
}

// knownType reports whether the package models the entry type.
func knownType(t string) bool {
	switch t {
	case TypeMessage, TypeModelChange, TypeCompaction, TypeBranchSummary,
		TypeCustom, TypeCustomMessage, TypeLabel, TypeSessionInfo:
		return true
	}
	return false
}
