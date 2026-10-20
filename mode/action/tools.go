package action

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/tool"
)

// Prompt is the embedded review system prompt.
//
//go:embed prompt.md
var Prompt string

// Comment is one review comment the model wants placed on the diff.
type Comment struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Side     string `json:"side,omitempty"`
	Severity string `json:"severity"`
	Body     string `json:"body"`
}

// State collects the comments and summary a review run produces. The review
// tools write to it and Review reads it.
type State struct {
	mu       sync.Mutex
	files    map[string]*FileDiff
	max      int
	comments []Comment
	summary  string
	seen     map[string]bool
}

// NewState returns a review state that accepts at most max comments.
func NewState(max int) *State {
	if max <= 0 {
		max = 15
	}
	return &State{files: map[string]*FileDiff{}, max: max, seen: map[string]bool{}}
}

// Tools returns the two review tools bound to this state.
func (st *State) Tools() []tool.Tool { return []tool.Tool{st.commentTool(), st.summaryTool()} }

// setFiles installs the parsed diff line maps before the run.
func (st *State) setFiles(files []DiffFile) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.files = map[string]*FileDiff{}
	for _, f := range files {
		if f.Parsed != nil {
			st.files[f.Path] = f.Parsed
		}
	}
}

// result returns a copy of the accepted comments and the summary.
func (st *State) result() ([]Comment, string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]Comment(nil), st.comments...), st.summary
}

// Caps on the text the model may post, in characters. GitHub itself refuses a
// review body over 65536, so these keep a runaway reply from becoming one.
const (
	maxCommentChars = 4000
	maxSummaryChars = 20000
)

// severities are the accepted review severities.
var severities = map[string]bool{"nit": true, "suggestion": true, "issue": true, "blocker": true}

// commentTool records a comment on a line present in the diff.
func (st *State) commentTool() tool.Tool {
	return tool.New(tool.Info{
		Name:        "review_comment",
		Description: "Place a review comment on a line that appears in the pull request diff. A line outside the diff is rejected.",
		ReadOnly:    true,
		Parallel:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "description": "File path in the pull request"},
				"line":     map[string]any{"type": "integer", "description": "Line number in that file"},
				"side":     map[string]any{"type": "string", "enum": []string{"LEFT", "RIGHT"}, "description": "Which side of the diff (default RIGHT)"},
				"severity": map[string]any{"type": "string", "enum": []string{"nit", "suggestion", "issue", "blocker"}},
				"body":     map[string]any{"type": "string", "description": "The comment text"},
			},
			"required": []string{"path", "line", "severity", "body"},
		},
	}, st.runComment)
}

// runComment validates and records one comment.
func (st *State) runComment(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	var c Comment
	if err := json.Unmarshal(args, &c); err != nil {
		return tool.Errorf("invalid arguments: %v", err), nil
	}
	switch {
	case strings.TrimSpace(c.Body) == "":
		return tool.Errorf("body is required"), nil
	case utf8.RuneCountInString(c.Body) > maxCommentChars:
		return tool.Errorf("body is over %d characters; say less, or put it in the summary", maxCommentChars), nil
	case !severities[c.Severity]:
		return tool.Errorf("severity must be nit, suggestion, issue, or blocker"), nil
	}
	side := def(strings.ToUpper(strings.TrimSpace(c.Side)), "RIGHT")
	if side != "LEFT" && side != "RIGHT" {
		return tool.Errorf("side must be LEFT or RIGHT"), nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	fd := st.files[c.Path]
	if fd == nil {
		return tool.Errorf("path %s is not part of this pull request", c.Path), nil
	}
	if !fd.Valid(c.Line, side) {
		return tool.Errorf("line %d is not part of the diff; comment on a changed line or put this in the summary", c.Line), nil
	}
	key := fmt.Sprintf("%s:%d:%s", c.Path, c.Line, c.Body)
	if st.seen[key] {
		return tool.Errorf("duplicate comment on %s:%d", c.Path, c.Line), nil
	}
	if len(st.comments) >= st.max {
		return tool.Errorf("comment limit reached (%d); prioritise the most important findings", st.max), nil
	}
	st.seen[key] = true
	c.Side = side
	st.comments = append(st.comments, c)
	return tool.Text("accepted: %s:%d (%s)", c.Path, c.Line, c.Severity), nil
}

// summaryTool records the overall summary; the last call wins.
func (st *State) summaryTool() tool.Tool {
	return tool.New(tool.Info{
		Name:        "review_summary",
		Description: "Record the overall review summary. The last call wins.",
		ReadOnly:    true,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"body": map[string]any{"type": "string"}},
			"required":   []string{"body"},
		},
	}, func(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
		var in struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Errorf("invalid arguments: %v", err), nil
		}
		if utf8.RuneCountInString(in.Body) > maxSummaryChars {
			return tool.Errorf("body is over %d characters; shorten it", maxSummaryChars), nil
		}
		st.mu.Lock()
		st.summary = in.Body
		st.mu.Unlock()
		return tool.Text("summary recorded"), nil
	})
}
