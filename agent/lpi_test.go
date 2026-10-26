package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// lastMessage returns the final message in a recorded request.
func lastMessage(t *testing.T, reqs []provider.Request, i int) message.Message {
	t.Helper()
	if i >= len(reqs) {
		t.Fatalf("only %d requests recorded, want index %d", len(reqs), i)
	}
	msgs := reqs[i].Messages
	if len(msgs) == 0 {
		t.Fatalf("request %d has no messages", i)
	}
	return msgs[len(msgs)-1]
}

func Test_LPI01_TextOnlyModelStripsImages(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock} // no modalities means text only
	prompt := message.UserParts("look", message.ImagePart("image/png", "aGk="))

	res, _ := runCollect(t, cfg, nil, prompt)

	got := lastMessage(t, p.Requests(), 0)
	if got.HasImages() {
		t.Fatalf("image reached a text-only model: %+v", got)
	}
	if !strings.Contains(got.Content, "look") || !strings.Contains(got.Content, "image was omitted") {
		t.Errorf("content = %q, want the text plus an omission note", got.Content)
	}
	kept := false
	for _, m := range res.Messages {
		if m.HasImages() {
			kept = true
		}
	}
	if !kept {
		t.Error("transcript lost the image; it must keep parts the request drops")
	}
}

func Test_LPI02_VisionModelKeepsImages(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, InputModalities: []string{"text", "image"}}
	prompt := message.UserParts("look", message.ImagePart("image/png", "aGk="))

	runCollect(t, cfg, nil, prompt)

	got := lastMessage(t, p.Requests(), 0)
	if !got.HasImages() {
		t.Fatalf("image dropped for a vision model: %+v", got)
	}
}

func Test_LPI03_ModalitiesFuncReadPerRequest(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("one"), textTurn("two"))
	var mu sync.Mutex
	mods := []string{"text"}
	cfg := Config{Provider: p, Model: "m", Clock: clock, InputModalitiesFunc: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return mods
	}}
	a := New(cfg)
	prompt := message.UserParts("look", message.ImagePart("image/png", "aGk="))

	if _, err := a.Run(context.Background(), nil, prompt); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	mu.Lock()
	mods = []string{"text", "image"}
	mu.Unlock()
	if _, err := a.Run(context.Background(), nil, prompt); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	reqs := p.Requests()
	if first := lastMessage(t, reqs, 0); first.HasImages() {
		t.Error("first request kept an image for a text-only model")
	}
	if second := lastMessage(t, reqs, 1); !second.HasImages() {
		t.Error("second request dropped an image after the model switched to vision")
	}
}

// imageTool returns a part in its result, with a caption.
func imageTool(name, data string) tool.Tool {
	return toolOf(name, false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Result{
			Content: "took " + name,
			Parts:   []message.ContentPart{message.ImagePart("image/png", data)},
			Caption: "image from " + name,
		}, nil
	})
}

func Test_LPI04_ToolPartsBecomeSyntheticUserMessage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, namedToolTurn("shot"), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(imageTool("shot", "aGk=")), InputModalities: []string{"text", "image"}}

	res, _ := runCollect(t, cfg, nil, message.User("go"))

	var order []string
	var synthetic *message.Message
	for i := range res.Messages {
		m := res.Messages[i]
		if m.Role == message.RoleTool {
			order = append(order, "tool")
		}
		if m.Synthetic {
			order = append(order, "image")
			synthetic = &res.Messages[i]
		}
	}
	if len(order) != 2 || order[0] != "tool" || order[1] != "image" {
		t.Fatalf("message order = %v, want [tool image]", order)
	}
	if synthetic.Role != message.RoleUser || !synthetic.HasImages() || synthetic.Content != "image from shot" {
		t.Fatalf("synthetic message = %+v", synthetic)
	}
	// The model saw the image on a user message, never on the tool message.
	req := lastMessage(t, p.Requests(), 1)
	if req.Role != message.RoleUser || !req.HasImages() {
		t.Fatalf("request tail = %+v, want the synthetic user image", req)
	}
}

func Test_LPI05_TwoToolImagesCombine(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	calls := testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "a", `{}`), toolCall("c2", "b", `{}`)}}
	p := testkit.NewProvider(clock, calls, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(imageTool("a", "aGk="), imageTool("b", "bm8=")), InputModalities: []string{"text", "image"}}

	res, _ := runCollect(t, cfg, nil, message.User("go"))

	synthetic := 0
	parts := 0
	for _, m := range res.Messages {
		if m.Synthetic {
			synthetic++
			parts = len(m.Parts)
		}
	}
	if synthetic != 1 || parts != 2 {
		t.Fatalf("synthetic messages = %d with %d parts, want 1 with 2", synthetic, parts)
	}
}

func Test_LPI06_ContextInject(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	inject := toolOf("inject", false, func(_ context.Context, _ json.RawMessage, tc *tool.Context) (tool.Result, error) {
		tc.Inject(message.UserParts("injected note", message.ImagePart("image/png", "aGk=")))
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("inject"), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(inject), InputModalities: []string{"text", "image"}}

	res, _ := runCollect(t, cfg, nil, message.User("go"))

	var order []string
	for _, m := range res.Messages {
		if m.Role == message.RoleTool {
			order = append(order, "tool")
		}
		if m.Content == "injected note" {
			order = append(order, "injected")
			if !m.HasImages() {
				t.Errorf("injected message lost its image: %+v", m)
			}
		}
	}
	if len(order) != 2 || order[0] != "tool" || order[1] != "injected" {
		t.Fatalf("message order = %v, want [tool injected]", order)
	}
}

func Test_LPI07_ToolUsageIsCounted(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	meter := toolOf("meter", false, func(_ context.Context, _ json.RawMessage, tc *tool.Context) (tool.Result, error) {
		tc.AddUsage(provider.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, CostUSD: 0.5})
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("meter"), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(meter)}

	res, events := runCollect(t, cfg, nil, message.User("go"))

	if res.Usage.TotalTokens < 12 || res.Usage.CostUSD < 0.5 {
		t.Fatalf("usage = %+v, want the tool's nested call included", res.Usage)
	}
	found := false
	for _, ev := range events {
		if ev.Type == proto.EventUsage && ev.Usage != nil && ev.Usage.TotalTokens == 12 {
			found = true
		}
	}
	if !found {
		t.Error("no usage event carried the tool's nested usage")
	}
}

func Test_LPI08_SteerCarriesImage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var a *Agent
	steerTool := toolOf("steer", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		_ = a.Steer(message.UserParts("look", message.ImagePart("image/png", "aGk=")))
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("steer"), textTurn("done"))
	a = New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(steerTool), InputModalities: []string{"text", "image"}})
	if _, err := a.Run(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := p.Requests()
	if len(reqs) < 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for _, m := range reqs[1].Messages {
		if m.Content == "look" && m.HasImages() {
			return
		}
	}
	t.Fatalf("steered image not in the next request: %+v", reqs[1].Messages)
}

// callIDRecorder returns a tool that records the call id each invocation saw.
func callIDRecorder(name string, parallel bool, mu *sync.Mutex, seen *[]string) tool.Tool {
	return toolOf(name, parallel, func(_ context.Context, _ json.RawMessage, tc *tool.Context) (tool.Result, error) {
		mu.Lock()
		*seen = append(*seen, tc.CallID)
		mu.Unlock()
		return tool.Text("ok"), nil
	})
}

func Test_LPI09_ToolSeesCallID(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var mu sync.Mutex
	var seen []string
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("call-abc", "rec", `{}`)}},
		textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(callIDRecorder("rec", false, &mu, &seen))}

	_, events := runCollect(t, cfg, nil, message.User("go"))

	if len(seen) != 1 || seen[0] != "call-abc" {
		t.Fatalf("tool saw call ids %v, want [call-abc]", seen)
	}
	// The id the tool saw must be the one tool_start carried, so a client can
	// correlate the tool's own events with the card.
	for _, ev := range events {
		if ev.Type == proto.EventToolStart && ev.CallID != seen[0] {
			t.Errorf("tool_start call id = %q, tool saw %q", ev.CallID, seen[0])
		}
	}
}

func Test_LPI10_ParallelCallsSeeOwnIDs(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var mu sync.Mutex
	var seen []string
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{
			toolCall("c1", "rec", `{}`), toolCall("c2", "rec", `{}`), toolCall("c3", "rec", `{}`),
		}},
		textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, MaxParallel: 3, Tools: registry(callIDRecorder("rec", true, &mu, &seen))}

	runCollect(t, cfg, nil, message.User("go"))

	sort.Strings(seen)
	if strings.Join(seen, ",") != "c1,c2,c3" {
		t.Fatalf("tools saw call ids %v, want each of c1, c2, c3 once", seen)
	}
}

func Test_LPI11_ZeroContextHasNoCallID(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	tl := callIDRecorder("rec", false, &mu, &seen)

	res, err := tl.Run(context.Background(), json.RawMessage(`{}`), &tool.Context{})

	if err != nil || res.IsError || len(seen) != 1 || seen[0] != "" {
		t.Fatalf("result %+v, err %v, saw %q; want a clean run with an empty call id", res, err, seen)
	}
}

func Test_LPI12_PanicKeepsCallID(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var got string
	boom := toolOf("boom", false, func(_ context.Context, _ json.RawMessage, tc *tool.Context) (tool.Result, error) {
		got = tc.CallID
		panic("kaboom")
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("call-panic", "boom", `{}`)}},
		textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(boom)}

	res, events := runCollect(t, cfg, nil, message.User("go"))

	if got != "call-panic" {
		t.Errorf("tool saw call id %q before panicking, want call-panic", got)
	}
	recorded := false
	for _, m := range res.Messages {
		if m.Role == message.RoleTool && m.ToolCallID == "call-panic" {
			recorded = true
		}
	}
	if !recorded {
		t.Error("no tool result recorded under call-panic after the panic")
	}
	ended := false
	for _, ev := range events {
		if ev.Type == proto.EventToolEnd && ev.CallID == "call-panic" {
			ended = true
		}
	}
	if !ended {
		t.Error("no tool_end carried call-panic after the panic")
	}
}
