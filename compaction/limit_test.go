package compaction

import (
	"errors"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

func enabled(s Settings) Settings {
	s.Enabled = true
	return s
}

func Test_LIM01_LimitIsSmallerNonZero(t *testing.T) {
	if got := EffectiveLimit(Settings{ContextLimit: 100000, ModelWindow: 200000}); got != 100000 {
		t.Fatalf("EffectiveLimit = %d, want 100000", got)
	}
}

func Test_LIM02_OnlyModelWindow(t *testing.T) {
	if got := EffectiveLimit(Settings{ModelWindow: 200000}); got != 200000 {
		t.Fatalf("EffectiveLimit = %d, want 200000", got)
	}
}

func Test_LIM03_NeitherKnown(t *testing.T) {
	if got := EffectiveLimit(Settings{}); got != 0 {
		t.Fatalf("EffectiveLimit = %d, want 0", got)
	}
	if ShouldCompact(enabled(Settings{}), []message.Message{msgRunes(message.RoleUser, 1000)}, nil) {
		t.Fatal("no known limit must never compact")
	}
}

func Test_LIM04_BelowThreshold(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{msgTokens(message.RoleUser, 50000)}
	if ShouldCompact(s, msgs, nil) {
		t.Fatal("50k below the 84k threshold")
	}
}

func Test_LIM05_AboveThreshold(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{msgTokens(message.RoleUser, 90000)}
	if !ShouldCompact(s, msgs, nil) {
		t.Fatal("90k above the 84k threshold")
	}
}

func Test_LIM06_ExactlyAtThreshold(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{msgTokens(message.RoleUser, 84000)}
	if ShouldCompact(s, msgs, nil) {
		t.Fatal("exactly at the threshold must not compact")
	}
}

func Test_LIM07_Disabled(t *testing.T) {
	s := Settings{Enabled: false, ContextLimit: 100000}
	msgs := []message.Message{msgTokens(message.RoleUser, 90000)}
	if ShouldCompact(s, msgs, nil) {
		t.Fatal("disabled must never compact")
	}
}

func Test_LIM08_UsesReportedUsage(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{msgTokens(message.RoleUser, 1000)}
	last := &provider.Usage{PromptTokens: 95000}
	if !ShouldCompact(s, msgs, last) {
		t.Fatal("95k reported usage should trigger")
	}
}

func Test_LIM09_IgnoresNonsenseUsage(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{msgTokens(message.RoleUser, 1000)}
	for _, u := range []*provider.Usage{
		{PromptTokens: 999999999},
		{PromptTokens: -5},
		{PromptTokens: 0},
	} {
		if ShouldCompact(s, msgs, u) {
			t.Fatalf("nonsense usage %+v should be ignored", u)
		}
	}
}

func Test_LIM10_AddsEstimateOfNewMessages(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{msgTokens(message.RoleUser, 20000)}
	last := &provider.Usage{PromptTokens: 80000}
	if !ShouldCompact(s, msgs, last) {
		t.Fatal("80k + 20k should trigger")
	}
}

func Test_LIM11_ReserveClampedToHalf(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 90000})
	over := []message.Message{msgTokens(message.RoleUser, 60000)}
	if !ShouldCompact(s, over, nil) {
		t.Fatal("60k above the clamped 50k threshold")
	}
	under := []message.Message{msgTokens(message.RoleUser, 49000)}
	if ShouldCompact(s, under, nil) {
		t.Fatal("49k below the clamped 50k threshold")
	}
}

func Test_LIM12_LimitBelowSystemPrompt(t *testing.T) {
	s := enabled(Settings{ContextLimit: 1000})
	msgs := []message.Message{msgRunes(message.RoleSystem, 5000), message.User("hi")}
	if !ShouldCompact(s, msgs, nil) {
		t.Fatal("a 5k system prompt exceeds a 1k limit")
	}
	c := &Compactor{}
	if _, err := c.Compact(nil, msgs, ""); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("Compact err = %v, want ErrNothingToCompact", err)
	}
}

// assistant message itself must be estimated with the messages after it.
func Test_LIM13_LastAssistantMessageCounted(t *testing.T) {
	s := enabled(Settings{ContextLimit: 100000, Reserve: 16000})
	msgs := []message.Message{message.User("q"), msgTokens(message.RoleAssistant, 20000)}
	last := &provider.Usage{PromptTokens: 70000}
	if !ShouldCompact(s, msgs, last) {
		t.Fatal("70k prompt + a 20k reply is 90k, over the 84k threshold")
	}
}
