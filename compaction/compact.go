package compaction

import (
	"context"
	"errors"
	"strings"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// ErrNothingToCompact reports that there is no safe span to summarise.
var ErrNothingToCompact = errors.New("compaction: nothing to compact")

// Compactor summarises a span of a transcript with a provider.
type Compactor struct {
	Provider provider.Provider
	Model    string
	Settings Settings
}

// Result is one compaction.
type Result struct {
	Summary       string
	FirstKept     int // index into the input messages
	TokensBefore  int
	TokensAfter   int
	ReadFiles     []string
	ModifiedFiles []string
	Usage         provider.Usage // cost of the summarisation itself
}

// Compact summarises the safe prefix of msgs and returns the summary. It never
// writes anything; a provider error, an empty summary, or a cancelled context
// all leave the caller with no partial state to store.
func (c *Compactor) Compact(ctx context.Context, msgs []message.Message, instructions string) (*Result, error) {
	s := c.Settings.withDefaults()
	cut, ok := CutPoint(msgs, s.KeepRecent)
	if !ok {
		return nil, ErrNothingToCompact
	}
	start := 0
	if len(msgs) > 0 && msgs[0].Role == message.RoleSystem {
		start = 1
	}
	if cut <= start || len(msgs) <= start {
		return nil, ErrNothingToCompact
	}
	if c.Provider == nil {
		return nil, errors.New("compaction: no provider")
	}
	span := msgs[start:cut]
	if len(span) == 0 {
		return nil, ErrNothingToCompact
	}

	input := serializeSpan(span)
	existing := existingSummary(span)
	read, modified := extractFiles(span)

	var summary string
	var usage provider.Usage
	var err error
	if estimateRunes(input) > s.InputMaxTokens {
		summary, usage, err = c.summariseChunks(ctx, span, existing, instructions)
	} else {
		summary, usage, err = c.summarise(ctx, input, existing, instructions)
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("compaction: empty summary")
	}

	full := withFileBlocks(summary, read, modified)
	kept := msgs[cut:]
	after := append([]message.Message{message.User("<summary>\n" + full + "\n</summary>")}, kept...)
	return &Result{
		Summary:       full,
		FirstKept:     cut,
		TokensBefore:  Estimate(msgs),
		TokensAfter:   Estimate(after),
		ReadFiles:     read,
		ModifiedFiles: modified,
		Usage:         usage,
	}, nil
}

// Outcome is the result of appending a compaction entry.
type Outcome struct {
	Entry        session.Entry
	TokensBefore int
	TokensAfter  int
	Replaced     int
}

// Run compacts the current branch of a session and appends one compaction entry
// whose parent is the old leaf.
func Run(ctx context.Context, st *session.Store, c *Compactor, instructions string) (*Outcome, error) {
	if st == nil {
		return nil, errors.New("compaction: nil session")
	}
	msgs, ids, err := contextWithIDs(st)
	if err != nil {
		return nil, err
	}
	res, err := c.Compact(ctx, msgs, instructions)
	if err != nil {
		return nil, err
	}
	firstKept := ""
	if res.FirstKept >= 0 && res.FirstKept < len(ids) {
		firstKept = ids[res.FirstKept]
	}
	usage := res.Usage
	appended, err := st.Append(session.Entry{
		Type:         session.TypeCompaction,
		Summary:      res.Summary,
		FirstKept:    firstKept,
		TokensBefore: res.TokensBefore,
		Usage:        &usage,
	})
	if err != nil {
		return nil, err
	}
	return &Outcome{
		Entry:        appended,
		TokensBefore: res.TokensBefore,
		TokensAfter:  res.TokensAfter,
		Replaced:     res.FirstKept,
	}, nil
}

// contextWithIDs rebuilds the branch context and the session entry id behind
// each message, mirroring session.Messages so Compact's index maps back.
func contextWithIDs(st *session.Store) ([]message.Message, []string, error) {
	branch, err := st.Branch("")
	if err != nil {
		return nil, nil, err
	}
	comp := -1
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == session.TypeCompaction {
			comp = i
			break
		}
	}
	var msgs []message.Message
	var ids []string
	add := func(m message.Message, id string) {
		msgs = append(msgs, m)
		ids = append(ids, id)
	}
	if comp < 0 {
		for i := range branch {
			if m, ok := entryMessage(branch[i]); ok {
				add(m, branch[i].ID)
			}
		}
		return msgs, ids, nil
	}
	c := branch[comp]
	add(message.User("<summary>\n"+c.Summary+"\n</summary>"), c.ID)
	start := comp + 1
	if c.FirstKept != "" {
		for i := range branch {
			if branch[i].ID == c.FirstKept {
				start = i
				break
			}
		}
	}
	for i := start; i < len(branch); i++ {
		if branch[i].Type == session.TypeCompaction {
			continue
		}
		if m, ok := entryMessage(branch[i]); ok {
			add(m, branch[i].ID)
		}
	}
	return msgs, ids, nil
}

// entryMessage maps a context-bearing entry to a message, matching
// session.Messages.
func entryMessage(e session.Entry) (message.Message, bool) {
	switch e.Type {
	case session.TypeMessage:
		if e.Message == nil {
			return message.Message{}, false
		}
		return *e.Message, true
	case session.TypeCustomMessage:
		if e.InContext {
			return message.User(e.Content), true
		}
	case session.TypeBranchSummary:
		return message.User("<branch_summary>\n" + e.Summary + "\n</branch_summary>"), true
	}
	return message.Message{}, false
}
