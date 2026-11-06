package agent

import (
	"errors"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// errorEvent returns the single error event of a run, failing when there is not
// exactly one: a run that fails must say so once.
func errorEvent(t *testing.T, events []proto.Event) proto.Event {
	t.Helper()
	if n := countType(events, proto.EventError); n != 1 {
		t.Fatalf("error events = %d, want 1", n)
	}
	for _, e := range events {
		if e.Type == proto.EventError {
			return e
		}
	}
	panic("unreachable")
}

// runFailing runs a prompt against a provider that answers 401 and returns the
// events and the error the run ended with.
func runFailing(t *testing.T, hint func(error) string) ([]proto.Event, error) {
	t.Helper()
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: &provider.Error{Status: 401, Code: "401", Message: "User not found."}})
	c := &collector{}
	cfg := Config{Provider: p, Model: "m", Clock: clock, Emit: c.emit, ErrorHint: hint}
	out := runAsync(cfg, nil, message.User("go"))
	return c.all(), out.err
}

func Test_LPR13_ErrorHintIsAppendedToTheErrorEvent(t *testing.T) {
	var got *provider.Error
	events, err := runFailing(t, func(err error) string {
		errors.As(err, &got)
		return "run nova login"
	})
	if err == nil {
		t.Fatal("run returned no error")
	}
	if got == nil || got.Status != 401 {
		t.Fatalf("hint saw %v, want the *provider.Error with status 401", got)
	}
	ev := errorEvent(t, events)
	want := err.Error() + "\nrun nova login"
	if ev.Error == nil || ev.Error.Message != want || ev.Error.Code != proto.CodeProviderError {
		t.Fatalf("error event = %+v, want code provider_error and message %q", ev.Error, want)
	}
}

// The hint adds to the provider's text and never replaces it, and an absent or
// empty hint changes nothing at all.
func Test_LPR14_NoHintLeavesTheMessageVerbatim(t *testing.T) {
	for name, hint := range map[string]func(error) string{
		"no hook":    nil,
		"empty hint": func(error) string { return "" },
	} {
		t.Run(name, func(t *testing.T) {
			events, err := runFailing(t, hint)
			ev := errorEvent(t, events)
			if ev.Error == nil || ev.Error.Message != err.Error() {
				t.Fatalf("error event = %+v, want exactly %q", ev.Error, err.Error())
			}
		})
	}
}

// The hint is a host callback, so a panic in it must not lose the failure
// report or the single run_end.
func Test_LPR15_APanickingHintIsRecovered(t *testing.T) {
	called := false
	events, err := runFailing(t, func(error) string { called = true; panic("boom") })
	if !called {
		t.Fatal("the hint was never asked for, so the recovery below proves nothing")
	}
	ev := errorEvent(t, events)
	if ev.Error == nil || ev.Error.Message != err.Error() {
		t.Fatalf("error event = %+v, want the plain message %q", ev.Error, err.Error())
	}
	if n := countType(events, proto.EventRunEnd); n != 1 {
		t.Fatalf("run_end events = %d, want 1", n)
	}
}
