package compaction

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

// toolGroup builds an assistant tool call and its result as one inseparable pair.
func toolGroup(id string) []message.Message {
	return []message.Message{
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{
			ID:       id,
			Type:     "function",
			Function: message.FunctionCall{Name: "read", Arguments: `{"path":"a"}`},
		}}},
		message.ToolResult(id, "result"),
	}
}

func appendGroups(msgs []message.Message, n int) []message.Message {
	for i := 0; i < n; i++ {
		msgs = append(msgs, toolGroup(fmt.Sprintf("c%d", i))...)
	}
	return msgs
}

func Test_CUT01_CutsAtUserBoundary(t *testing.T) {
	var msgs []message.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, msgTokens(message.RoleUser, 100), msgTokens(message.RoleAssistant, 100))
	}
	cut, ok := CutPoint(msgs, 600)
	if !ok {
		t.Fatal("expected a cut")
	}
	if msgs[cut].Role != message.RoleUser {
		t.Fatalf("cut role = %q, want user", msgs[cut].Role)
	}
}

func Test_CUT02_NeverSplitsToolPair(t *testing.T) {
	msgs := []message.Message{message.User("one")}
	msgs = appendGroups(msgs, 3)
	msgs = append(msgs, message.User("two"))
	msgs = appendGroups(msgs, 3)
	cut, ok := CutPoint(msgs, 40)
	if !ok {
		t.Fatal("expected a cut")
	}
	if msgs[cut].Role != message.RoleUser {
		t.Fatalf("cut role = %q, want user", msgs[cut].Role)
	}
	kept := append([]message.Message{message.User("<summary>")}, msgs[cut:]...)
	if err := message.Validate(kept); err != nil {
		t.Fatalf("kept part invalid: %v", err)
	}
}

func Test_CUT03_EverythingFits(t *testing.T) {
	msgs := []message.Message{message.User("hi"), message.Assistant("hello")}
	if _, ok := CutPoint(msgs, 20000); ok {
		t.Fatal("small history must not cut")
	}
}

func Test_CUT04_SystemPreserved(t *testing.T) {
	msgs := []message.Message{message.System("sys")}
	msgs = append(msgs, msgTokens(message.RoleUser, 500), msgTokens(message.RoleAssistant, 500))
	msgs = append(msgs, msgTokens(message.RoleUser, 500), msgTokens(message.RoleAssistant, 500))
	cut, ok := CutPoint(msgs, 600)
	if !ok {
		t.Fatal("expected a cut")
	}
	if cut <= 0 {
		t.Fatalf("cut = %d, want > 0", cut)
	}
	if msgs[0].Role != message.RoleSystem {
		t.Fatal("system message missing")
	}
}

func Test_CUT05_TurnSplitFallback(t *testing.T) {
	msgs := []message.Message{message.User("one huge turn")}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, toolGroup(fmt.Sprintf("c%d", i))...)
	}
	cut, ok := CutPoint(msgs, 50)
	if !ok {
		t.Fatal("expected a fallback cut")
	}
	if msgs[cut].Role != message.RoleAssistant || len(msgs[cut].ToolCalls) == 0 {
		t.Fatalf("fallback cut is not a tool-group assistant: %+v", msgs[cut])
	}
}

func Test_CUT06_FallbackNeverSplitsPair(t *testing.T) {
	msgs := []message.Message{message.User("one")}
	msgs = appendGroups(msgs, 10)
	cut, ok := CutPoint(msgs, 30)
	if !ok {
		t.Fatal("expected a fallback cut")
	}
	if msgs[cut].Role != message.RoleAssistant {
		t.Fatalf("cut role = %q, want assistant", msgs[cut].Role)
	}
	id := msgs[cut].ToolCalls[0].ID
	found := false
	for _, m := range msgs[cut:] {
		if m.Role == message.RoleTool && m.ToolCallID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("cut split call %s from its result", id)
	}
}

func Test_CUT07_NoValidCut(t *testing.T) {
	msgs := []message.Message{message.User("hi"), message.Assistant("hello")}
	if _, ok := CutPoint(msgs, 1); ok {
		t.Fatal("a plain two-message history has no cut")
	}
}

func Test_CUT08_KeepRecentZero(t *testing.T) {
	msgs := []message.Message{
		message.User("a"), message.Assistant("b"),
		message.User("c"), message.Assistant("d"),
	}
	cut, ok := CutPoint(msgs, 0)
	if !ok {
		t.Fatal("keepRecent 0 should cut at the last user")
	}
	if cut != 2 || msgs[cut].Role != message.RoleUser {
		t.Fatalf("cut = %d (%q), want 2 user", cut, msgs[cut].Role)
	}
}

func Test_CUT09_AssistantOnly(t *testing.T) {
	msgs := []message.Message{message.Assistant("a"), message.Assistant("b")}
	if _, ok := CutPoint(msgs, 1); ok {
		t.Fatal("assistant-only history has no cut")
	}
}

// randomHistory builds a valid transcript from a seeded script.
func randomHistory(rng *rand.Rand) []message.Message {
	var msgs []message.Message
	n := 1 + rng.Intn(20)
	calls := 0
	for i := 0; i < n; i++ {
		switch rng.Intn(4) {
		case 0:
			msgs = append(msgs, message.User(fmt.Sprintf("u%d", i)))
		case 1:
			msgs = append(msgs, message.Assistant(fmt.Sprintf("a%d", i)))
		case 2:
			calls++
			msgs = append(msgs, toolGroup(fmt.Sprintf("c%d", calls))...)
		case 3:
			msgs = append(msgs, msgTokens(message.RoleAssistant, 50))
		}
	}
	return msgs
}

func Test_CUT10_KeptPartValidates(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	for i := 0; i < 500; i++ {
		msgs := randomHistory(rng)
		keep := rng.Intn(200)
		cut, ok := CutPoint(msgs, keep)
		if !ok {
			continue
		}
		kept := append([]message.Message{message.User("<summary>\ns\n</summary>")}, msgs[cut:]...)
		if err := message.Validate(kept); err != nil {
			t.Fatalf("iter %d keep %d cut %d: invalid kept part: %v\n%+v", i, keep, cut, err, msgs)
		}
	}
}

func Test_CUT11_NeverInsideToolGroup(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 500; i++ {
		msgs := randomHistory(rng)
		keep := rng.Intn(200)
		cut, ok := CutPoint(msgs, keep)
		if !ok {
			continue
		}
		if msgs[cut].Role == message.RoleTool {
			t.Fatalf("iter %d cut landed on a tool result at %d", i, cut)
		}
	}
}

func messagesFromScript(script string) []message.Message {
	var msgs []message.Message
	calls := 0
	for _, r := range script {
		switch r {
		case 'u':
			msgs = append(msgs, message.User("u"))
		case 'a':
			msgs = append(msgs, message.Assistant("a"))
		case 'A':
			calls++
			msgs = append(msgs, toolGroup(fmt.Sprintf("c%d", calls))...)
		case 's':
			msgs = append(msgs, message.System("s"))
		case 'l':
			msgs = append(msgs, msgRunes(message.RoleAssistant, 400))
		}
	}
	return msgs
}

func FuzzCutPoint(f *testing.F) {
	f.Add("uaua", 0)
	f.Add("uAAu", 10)
	f.Add("suaua", 1)
	f.Fuzz(func(t *testing.T, script string, keep int) {
		if len(script) > 500 {
			t.Skip()
		}
		msgs := messagesFromScript(script)
		cut, ok := CutPoint(msgs, keep)
		if !ok {
			return
		}
		if cut <= 0 || cut > len(msgs) {
			t.Fatalf("cut %d out of range for %d messages", cut, len(msgs))
		}
		kept := append([]message.Message{message.User("<summary>\ns\n</summary>")}, msgs[cut:]...)
		if err := message.Validate(kept); err != nil {
			t.Fatalf("invalid kept part: %v\n%+v", err, msgs)
		}
	})
}
