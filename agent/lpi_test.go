package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/message"
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
