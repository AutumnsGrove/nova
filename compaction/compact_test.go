package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/session"
)

func compactor(fake *fakeSummariser, s Settings) *Compactor {
	return &Compactor{Provider: fake, Model: "m", Settings: s}
}

func callMsg(id, name, args string) message.Message {
	return message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{
		ID:       id,
		Type:     "function",
		Function: message.FunctionCall{Name: name, Arguments: args},
	}}}
}

func Test_CMP01_SummaryProduced(t *testing.T) {
	fake := newFake("the summary")
	c := compactor(fake, Settings{KeepRecent: 200})
	res, err := c.Compact(context.Background(), historySpan(), "")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Summary == "" || !strings.Contains(res.Summary, "the summary") {
		t.Fatalf("summary = %q", res.Summary)
	}
	if res.FirstKept <= 0 || res.FirstKept >= len(historySpan()) {
		t.Fatalf("FirstKept = %d", res.FirstKept)
	}
}

func Test_CMP02_PromptHasSections(t *testing.T) {
	fake := newFake("s")
	c := compactor(fake, Settings{KeepRecent: 200})
	if _, err := c.Compact(context.Background(), historySpan(), ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	prompt := fake.promptText(t)
	for _, heading := range []string{
		"## Goal", "## Constraints and preferences", "## Progress (done, in progress, blocked)",
		"## Key decisions", "## Next steps", "## Critical context",
	} {
		if !strings.Contains(prompt, heading) {
			t.Fatalf("prompt missing %q:\n%s", heading, prompt)
		}
	}
}

func Test_CMP03_InstructionsAppended(t *testing.T) {
	fake := newFake("s")
	c := compactor(fake, Settings{KeepRecent: 200})
	if _, err := c.Compact(context.Background(), historySpan(), "focus on the tests"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !strings.Contains(fake.promptText(t), "focus on the tests") {
		t.Fatal("instructions not in the prompt")
	}
}

func Test_CMP04_UpdatesEarlierSummary(t *testing.T) {
	fake := newFake("updated")
	c := compactor(fake, Settings{KeepRecent: 200})
	msgs := append([]message.Message{message.User("<summary>\nOLD SUMMARY\n</summary>")}, historySpan()...)
	if _, err := c.Compact(context.Background(), msgs, ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	prompt := fake.promptText(t)
	if !strings.Contains(prompt, "OLD SUMMARY") || !strings.Contains(prompt, "Update it in place") {
		t.Fatalf("prompt does not ask to update:\n%s", prompt)
	}
}

func Test_CMP05_ToolResultsTruncated(t *testing.T) {
	big := strings.Repeat("x", 10000)
	got := serializeSpan([]message.Message{{Role: message.RoleTool, ToolCallID: "c", Content: big}})
	if !strings.Contains(got, strings.Repeat("x", 2000)) {
		t.Fatal("first 2000 chars missing")
	}
	if strings.Contains(got, strings.Repeat("x", 2001)) {
		t.Fatal("tool result not cut to 2000")
	}
}

func Test_CMP06_InputChunkedAndMerged(t *testing.T) {
	fake := newFake()
	c := compactor(fake, Settings{KeepRecent: 200, InputMaxTokens: 50})
	res, err := c.Compact(context.Background(), longHistory(30), "")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if fake.calls() < 2 {
		t.Fatalf("calls = %d, want a merge after chunk summaries", fake.calls())
	}
	if strings.TrimSpace(res.Summary) == "" {
		t.Fatal("empty summary")
	}
}

func Test_CMP07_FileExtraction(t *testing.T) {
	span := []message.Message{
		callMsg("1", "read", `{"path":"a.go"}`),
		callMsg("2", "read", `{"path":"a.go"}`),
		callMsg("3", "write", `{"path":"b.go"}`),
		callMsg("4", "edit", `{"path":"c.go"}`),
	}
	read, modified := extractFiles(span)
	if strings.Join(read, ",") != "a.go" {
		t.Fatalf("read = %v, want [a.go]", read)
	}
	if strings.Join(modified, ",") != "b.go,c.go" {
		t.Fatalf("modified = %v, want [b.go c.go]", modified)
	}
	full := withFileBlocks("body", read, modified)
	for _, want := range []string{"<read-files>", "a.go", "<modified-files>", "b.go", "c.go"} {
		if !strings.Contains(full, want) {
			t.Fatalf("summary missing %q:\n%s", want, full)
		}
	}
}

func Test_CMP08_ReadAndEditedFile(t *testing.T) {
	span := []message.Message{
		callMsg("1", "read", `{"path":"x.go"}`),
		callMsg("2", "edit", `{"path":"x.go"}`),
	}
	read, modified := extractFiles(span)
	if len(read) != 1 || read[0] != "x.go" {
		t.Fatalf("read = %v", read)
	}
	if len(modified) != 1 || modified[0] != "x.go" {
		t.Fatalf("modified = %v", modified)
	}
}

func Test_CMP09_EmptySummary(t *testing.T) {
	for _, text := range []string{"", "   "} {
		fake := newFake(text)
		c := compactor(fake, Settings{KeepRecent: 200})
		_, err := c.Compact(context.Background(), historySpan(), "")
		if err == nil || !strings.Contains(err.Error(), "empty summary") {
			t.Fatalf("text %q: err = %v, want empty summary", text, err)
		}
	}
}

func Test_CMP10_ProviderErrorNoEntry(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	before := len(st.Entries())
	fake := newFake()
	fake.errs = []error{errors.New("boom")}
	c := compactor(fake, Settings{KeepRecent: 400})
	_, err := Run(context.Background(), st, c, "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if len(st.Entries()) != before {
		t.Fatal("session changed after a provider error")
	}
}

func Test_CMP11_CancelledNoEntry(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	before := len(st.Entries())
	c := compactor(newFake(), Settings{KeepRecent: 400})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, st, c, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(st.Entries()) != before {
		t.Fatal("session changed after a cancelled compaction")
	}
}

func Test_CMP12_TokensAfterSmaller(t *testing.T) {
	fake := newFake("short")
	c := compactor(fake, Settings{KeepRecent: 200})
	res, err := c.Compact(context.Background(), longHistory(20), "")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.TokensAfter >= res.TokensBefore {
		t.Fatalf("TokensAfter %d >= TokensBefore %d", res.TokensAfter, res.TokensBefore)
	}
}

func Test_CMP13_RunAppendsEntry(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	oldLeaf := st.Leaf()
	before := len(st.Entries())
	c := compactor(newFake("summary"), Settings{KeepRecent: 400})
	out, err := Run(context.Background(), st, c, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries := st.Entries()
	if len(entries) != before+1 {
		t.Fatalf("entries = %d, want %d", len(entries), before+1)
	}
	last := entries[len(entries)-1]
	if last.Type != session.TypeCompaction || last.Parent != oldLeaf {
		t.Fatalf("entry = %+v, want compaction on %s", last, oldLeaf)
	}
	branch, _ := st.Branch(last.ID)
	found := false
	for _, e := range branch {
		if e.ID == last.FirstKept {
			found = true
		}
	}
	if !found {
		t.Fatalf("FirstKept %q is not on the branch", last.FirstKept)
	}
	if out.Entry.ID != last.ID {
		t.Fatalf("outcome entry = %+v", out.Entry)
	}
}

func Test_CMP14_ContextAfterRun(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	c := compactor(newFake("summary"), Settings{KeepRecent: 400})
	if _, err := Run(context.Background(), st, c, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs, err := st.Messages("")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) < 2 || msgs[0].Role != message.RoleUser || !strings.Contains(msgs[0].Content, "<summary>") {
		t.Fatalf("context does not start with the summary: %+v", msgs)
	}
	if err := message.Validate(msgs); err != nil {
		t.Fatalf("context invalid: %v", err)
	}
}

func Test_CMP15_SecondCompactionCarriesSummary(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	fake := newFake("first", "second")
	c := compactor(fake, Settings{KeepRecent: 400})
	if _, err := Run(context.Background(), st, c, ""); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	appendTurns(t, st, 6)
	if _, err := Run(context.Background(), st, c, ""); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !strings.Contains(fake.promptText(t), "first") {
		t.Fatal("the earlier summary was not carried into the second prompt")
	}
	msgs, _ := st.Messages("")
	if !strings.Contains(msgs[0].Content, "second") {
		t.Fatalf("the later summary does not apply: %q", msgs[0].Content)
	}
}

func Test_CMP16_NothingAfterCompaction(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 1)
	u := message.User("only")
	if _, err := st.Append(session.Entry{Type: session.TypeMessage, Message: &u}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := st.Append(session.Entry{Type: session.TypeCompaction, Summary: "s"}); err != nil {
		t.Fatalf("append compaction: %v", err)
	}
	c := compactor(newFake(), Settings{KeepRecent: 400})
	if _, err := Run(context.Background(), st, c, ""); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
}

func Test_CMP17_EmptySession(t *testing.T) {
	st := newStore(t)
	c := compactor(newFake(), Settings{KeepRecent: 400})
	if _, err := Run(context.Background(), st, c, ""); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
}

func Test_CMP18_SessionKeepsEntries(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	before := st.Entries()
	c := compactor(newFake("summary"), Settings{KeepRecent: 400})
	if _, err := Run(context.Background(), st, c, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := st.Entries()
	if len(after) != len(before)+1 {
		t.Fatalf("entries = %d, want %d", len(after), len(before)+1)
	}
	for i, e := range before {
		if after[i].ID != e.ID {
			t.Fatalf("entry %d changed: %s != %s", i, after[i].ID, e.ID)
		}
	}
}

func Test_CMP19_KeepEntrySurvives(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	kept, err := st.Append(session.Entry{Type: session.TypeCustom, Kind: "note", Content: "keep me", Keep: true})
	if err != nil {
		t.Fatalf("append custom: %v", err)
	}
	c := compactor(newFake("summary"), Settings{KeepRecent: 400})
	if _, err := Run(context.Background(), st, c, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, e := range st.Entries() {
		if e.ID == kept.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("keep entry was lost")
	}
}

func Test_CMP20_SummarisationUsageRecorded(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 6)
	c := compactor(newFake("summary"), Settings{KeepRecent: 400})
	out, err := Run(context.Background(), st, c, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Entry.Usage == nil || out.Entry.Usage.CompletionTokens == 0 {
		t.Fatalf("entry usage = %+v, want the summarisation usage", out.Entry.Usage)
	}
}

func Test_CMP21_ErrorMidSummaryNoWrite(t *testing.T) {
	st := newStore(t)
	appendTurns(t, st, 30)
	before := len(st.Entries())
	fake := newFake()
	fake.errs = []error{nil, errors.New("boom")}
	c := compactor(fake, Settings{KeepRecent: 200, InputMaxTokens: 50})
	_, err := Run(context.Background(), st, c, "")
	if err == nil {
		t.Fatal("expected a mid-summary error")
	}
	if len(st.Entries()) != before {
		t.Fatal("a partial write happened")
	}
}

// The completion of the last response is not in its PromptTokens, so the

// When the span is split, a tool result still gets the name of its call even

// though the call is in another chunk.
func Test_CMP22_ChunkKeepsToolNames(t *testing.T) {
	fake := newFake()
	c := compactor(fake, Settings{InputMaxTokens: 20})
	span := []message.Message{
		message.User(strings.Repeat("u", 100)),
		callMsg("c1", "read", `{"path":"a"}`),
		message.ToolResult("c1", strings.Repeat("r", 100)),
	}
	if _, _, err := c.summariseChunks(context.Background(), span, "", ""); err != nil {
		t.Fatalf("summariseChunks: %v", err)
	}
	if fake.calls() < 3 {
		t.Fatalf("calls = %d, want the span split into several chunks", fake.calls())
	}
	if got := fake.promptText(t); !strings.Contains(got, "[tool read]") {
		t.Fatalf("prompt lacks the tool name:\n%s", got)
	}
}

// The transcript is data to summarise. Text inside it, such as a tool result

// that says "ignore previous instructions", must not be read as a command.
func Test_CMP23_PromptMarksTranscriptAsData(t *testing.T) {
	fake := newFake()
	c := compactor(fake, Settings{KeepRecent: 200})
	if _, err := c.Compact(context.Background(), longHistory(10), ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := fake.promptText(t); !strings.Contains(got, "not instructions") {
		t.Fatalf("prompt does not say the transcript is data:\n%s", got)
	}
}

// docs/IMAGE.md: compaction treats an image part as a fixed placeholder. Without
