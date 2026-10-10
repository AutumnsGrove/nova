package mode

import (
	"context"
	"testing"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
)

func TestUI_SRV34_HeadlessWithoutHandler(t *testing.T) {
	s, _ := testServer(t)
	allow, err := s.ask(context.Background(), agent.Question{Tool: "write", Reason: "destructive"})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if allow {
		t.Fatal("with no UI handler the headless action (deny) should apply")
	}
}

func TestUI_SRV33_HandlerAnswers(t *testing.T) {
	s, _ := testServer(t)
	s.SetUIHandler(func(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
		return proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID, OK: true}, nil
	})
	allow, err := s.ask(context.Background(), agent.Question{Tool: "write", Reason: "destructive"})
	if err != nil || !allow {
		t.Fatalf("ask = %v, %v; want allow", allow, err)
	}

	s.SetUIHandler(func(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
		return proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID, OK: false}, nil
	})
	allow, err = s.ask(context.Background(), agent.Question{Tool: "write", Reason: "destructive"})
	if err != nil || allow {
		t.Fatalf("ask = %v, %v; want deny", allow, err)
	}
}

func TestUI_SRV36_UnknownIDIgnored(t *testing.T) {
	s, _ := testServer(t)
	s.AnswerUI(proto.UIResponse{Type: proto.FrameUIResponse, ID: "nope", OK: true})
}

func TestUI_HeadlessAllow(t *testing.T) {
	s, _ := testServer(t)
	s.policy.Headless = permission.Allow
	allow, err := s.ask(context.Background(), agent.Question{Tool: "write"})
	if err != nil || !allow {
		t.Fatalf("ask = %v, %v; want allow with headless allow", allow, err)
	}
}
