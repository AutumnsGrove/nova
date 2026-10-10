package compaction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

// toolCallTranscript builds an assistant with n calls and their results.
func toolCallTranscript(n int, content string) []message.Message {
	calls := make([]message.ToolCall, n)
	msgs := []message.Message{message.User("go")}
	for i := 0; i < n; i++ {
		calls[i] = message.ToolCall{
			ID:       fmt.Sprintf("c%d", i),
			Type:     "function",
			Function: message.FunctionCall{Name: "read"},
		}
	}
	msgs = append(msgs, message.Message{Role: message.RoleAssistant, ToolCalls: calls})
	for i := 0; i < n; i++ {
		msgs = append(msgs, message.ToolResult(fmt.Sprintf("c%d", i), content))
	}
	return msgs
}

func prunedCount(msgs []message.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == message.RoleTool && strings.HasPrefix(m.Content, "[output pruned:") {
			n++
		}
	}
	return n
}

func Test_PRN01_OldLongResultsPruned(t *testing.T) {
	msgs := toolCallTranscript(10, strings.Repeat("x", 100))
	out := PruneToolOutput(msgs, 3, 10)
	if got := prunedCount(out); got != 7 {
		t.Fatalf("pruned = %d, want 7", got)
	}
}

func Test_PRN02_ShortResultsKept(t *testing.T) {
	msgs := toolCallTranscript(10, "short")
	out := PruneToolOutput(msgs, 3, 10)
	if got := prunedCount(out); got != 0 {
		t.Fatalf("pruned = %d, want 0", got)
	}
}

func Test_PRN03_RecentResultsKept(t *testing.T) {
	msgs := toolCallTranscript(10, strings.Repeat("x", 100))
	out := PruneToolOutput(msgs, 3, 10)
	var results []message.Message
	for _, m := range out {
		if m.Role == message.RoleTool {
			results = append(results, m)
		}
	}
	for i := 7; i < 10; i++ {
		if strings.HasPrefix(results[i].Content, "[output pruned:") {
			t.Fatalf("recent result %d was pruned", i)
		}
	}
}

func Test_PRN04_IDsAndNamesPreserved(t *testing.T) {
	msgs := toolCallTranscript(5, strings.Repeat("x", 100))
	out := PruneToolOutput(msgs, 1, 10)
	for _, m := range out {
		if m.Role == message.RoleTool && m.ToolCallID == "" {
			t.Fatal("tool call id lost")
		}
	}
	if err := message.Validate(out); err != nil {
		t.Fatalf("transcript invalid: %v", err)
	}
}

func Test_PRN05_InputNotModified(t *testing.T) {
	long := strings.Repeat("x", 100)
	msgs := toolCallTranscript(5, long)
	PruneToolOutput(msgs, 0, 10)
	for _, m := range msgs {
		if m.Role == message.RoleTool && m.Content != long {
			t.Fatal("input was modified")
		}
	}
}

func Test_PRN06_KeepLastZero(t *testing.T) {
	msgs := toolCallTranscript(5, strings.Repeat("x", 100))
	out := PruneToolOutput(msgs, 0, 10)
	if got := prunedCount(out); got != 5 {
		t.Fatalf("pruned = %d, want 5", got)
	}
}

func Test_PRN07_NoToolResults(t *testing.T) {
	msgs := []message.Message{message.User("a"), message.Assistant("b")}
	out := PruneToolOutput(msgs, 3, 10)
	if len(out) != len(msgs) {
		t.Fatalf("length = %d, want %d", len(out), len(msgs))
	}
	for i := range msgs {
		if out[i].Content != msgs[i].Content || out[i].Role != msgs[i].Role {
			t.Fatalf("message %d changed", i)
		}
	}
}
