package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// Test_LPW01_PromptCachePlumbed checks Config.PromptCache reaches the wire
// request unchanged. Nil must stay nil, because that is what asks the provider
// to decide from the catalog. A hook that rewrites the request keeps it.
func Test_LPW01_PromptCachePlumbed(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name string
		opt  *bool
		want *bool
	}{
		{"unset", nil, nil},
		{"opt in", &on, &on},
		{"opt out", &off, &off},
	}
	for _, tc := range cases {
		clock := testkit.NewFakeClock(baseTime)
		p := testkit.NewProvider(clock, textTurn("done"))
		cfg := Config{Provider: p, Model: "m", Clock: clock, PromptCache: tc.opt}
		cfg.Hooks.BeforeProviderRequest = func(_ context.Context, req *provider.Request) {
			req.ProviderOptions = map[string]any{"pinned": "yes"}
		}
		runCollect(t, cfg, nil, message.User("go"))
		reqs := p.Requests()
		if len(reqs) != 1 {
			t.Fatalf("%s: %d requests, want 1", tc.name, len(reqs))
		}
		got := reqs[0].PromptCache
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: PromptCache = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Test_LPW02_PrefixStableAcrossTurns checks the property a cache depends on:
// every turn's request starts with the previous turn's messages byte for byte,
// so an end-of-prompt breakpoint keeps hitting. A rewrite in the middle of the
// history would invalidate the whole cache and fail here.
func Test_LPW02_PrefixStableAcrossTurns(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{"text":"x"}`)}},
		textTurn("done"),
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2", res.Turns)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2", len(reqs))
	}
	first, second := reqs[0].Messages, reqs[1].Messages
	if len(second) <= len(first) {
		t.Fatalf("second request has %d messages, first %d: the loop did not append", len(second), len(first))
	}
	if !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatalf("second request rewrote the prefix:\nfirst  %+v\nsecond %+v", first, second[:len(first)])
	}
}
