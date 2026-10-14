package mode

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// A client that sends abort right after prompt must never lose it: prompt
// replies only once the run exists, so abort always has something to cancel.
func TestServer_ABT01_AbortRightAfterPromptLands(t *testing.T) {
	for i := 0; i < 100; i++ {
		s, _ := testServer(t, testkit.Turn{Hang: true})
		events, cancel := s.Subscribe(64)
		if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"}); !resp.OK {
			t.Fatalf("iteration %d: prompt = %+v", i, resp)
		}
		s.Handle(context.Background(), proto.Command{Type: proto.CmdAbort})
		deadline := time.After(5 * time.Second)
	wait:
		for {
			select {
			case ev := <-events:
				if ev.Type == proto.EventRunEnd {
					if ev.Reason != "aborted" {
						t.Fatalf("iteration %d: run_end reason = %q", i, ev.Reason)
					}
					break wait
				}
			case <-deadline:
				t.Fatalf("iteration %d: the abort was lost; the run never ended", i)
			}
		}
		cancel()
	}
}

func TestServer_ABT02_PromptReportsRunningImmediately(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"}); !resp.OK {
		t.Fatalf("prompt = %+v", resp)
	}
	defer s.Handle(context.Background(), proto.Command{Type: proto.CmdAbort})
	if !s.agent.Running() {
		t.Fatal("Running() = false right after prompt returned ok")
	}
	second := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "again"})
	if second.OK || second.Error == nil || second.Error.Code != proto.CodeBusy {
		t.Fatalf("second prompt = %+v, want busy", second)
	}
}
