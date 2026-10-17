package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
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
