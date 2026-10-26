package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/testkit"
)

// An assistant message with no content, parts or tool calls is recorded (the
// transcript is the caller's) but never sent: some providers reject it (#43).
func Test_LPN01_EmptyAssistantMessageIsRecordedButNotSent(t *testing.T) {
	cases := []struct {
		name string
		turn testkit.Turn
	}{
		{"nothing at all", testkit.Turn{}},
		{"reasoning only", testkit.Turn{Reasoning: "pondering"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := testkit.NewFakeClock(baseTime)
			p := testkit.NewProvider(clock, tc.turn, textTurn("now an answer"))
			var asked atomic.Bool
			cfg := Config{Provider: p, Model: "m", Clock: clock}
			cfg.Hooks.TurnEnd = func(_ context.Context, turn Turn) TurnDecision {
				if turn.Index == 0 && !asked.Swap(true) {
					return TurnDecision{Continue: true, Inject: []message.Message{message.User("answer, please")}}
				}
				return TurnDecision{}
			}
			res, _ := runCollect(t, cfg, nil, message.User("go"))

			reqs := p.Requests()
			if len(reqs) != 2 {
				t.Fatalf("requests = %d, want 2", len(reqs))
			}
			for _, m := range reqs[1].Messages {
				if m.Role == message.RoleAssistant {
					t.Fatalf("follow-up request carries an empty assistant message: %+v", reqs[1].Messages)
				}
			}
			if !hasMessageContent(reqs[1].Messages, "answer, please") {
				t.Fatalf("injected message missing from the follow-up: %+v", reqs[1].Messages)
			}
			var stored int
			for _, m := range res.Messages {
				if m.Role == message.RoleAssistant && m.Content == "" {
					stored++
				}
			}
			if stored != 1 {
				t.Errorf("transcript holds %d empty assistant messages, want the 1 that was recorded", stored)
			}
		})
	}
}

func Test_LPN02_EmptyAssistantFilterKeepsRealMessages(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	history := []message.Message{
		message.User("q"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c1", Type: "function", Function: message.FunctionCall{Name: "echo", Arguments: "{}"}}}},
		message.ToolResult("c1", "r"),
		message.UserParts("look").WithParts(message.ImagePart("image/png", "AAAA")),
		{Role: message.RoleAssistant, Parts: []message.ContentPart{message.ImagePart("image/png", "AAAA")}},
		message.Assistant("text"),
	}
	runCollect(t, cfg, history, message.User("go"))
	got := p.Requests()[0].Messages
	if len(got) != len(history)+1 {
		t.Fatalf("request has %d messages, want all %d kept: %+v", len(got), len(history)+1, got)
	}
}

func Test_LPN03_TransformContextStillSeesTheEmptyAssistantMessage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	var sawEmpty bool
	cfg.Hooks.TransformContext = func(_ context.Context, msgs []message.Message) []message.Message {
		for _, m := range msgs {
			if m.Role == message.RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
				sawEmpty = true
			}
		}
		return nil
	}
	runCollect(t, cfg, []message.Message{message.User("q"), {Role: message.RoleAssistant}}, message.User("go"))
	if !sawEmpty {
		t.Fatal("the hook did not see the empty assistant message: the filter must run after it")
	}
}
