//go:build !notui

package ui

import (
	"testing"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
)

func selectRequest(id string) proto.UIRequest {
	return proto.UIRequest{Type: proto.FrameUIRequest, ID: id, Method: "select",
		Options: []proto.UIOption{{ID: "x", Label: "X"}}}
}

func TestUI_DLG10_ExpiryShowsTheNextQueuedRequest(t *testing.T) {
	m, _, _ := newModel(t)
	pushUI(m, selectRequest("first"))
	pushUI(m, selectRequest("second"))
	if m.overlay.Len() != 1 || len(m.queuedReqs) != 1 {
		t.Fatalf("setup: overlay=%d queued=%d, want 1 and 1", m.overlay.Len(), len(m.queuedReqs))
	}
	execAll(update(m, client.EventMsg{Event: proto.Event{Type: proto.EventUIExpired, ID: "first"}}))
	if top := m.overlay.Top(); top == nil || top.ID() != "second" {
		t.Fatalf("the queued request was stranded after the open one timed out: top=%v queued=%d", top, len(m.queuedReqs))
	}
	if len(m.queuedReqs) != 0 || len(m.intents) != 1 {
		t.Fatalf("queued=%d intents=%d, want 0 and 1", len(m.queuedReqs), len(m.intents))
	}
}

func TestUI_DLG12_ExpiryWithoutAnIDLeavesLocalDialogsAlone(t *testing.T) {
	m, _, _ := newModel(t)
	m.openHelp()
	execAll(update(m, client.EventMsg{Event: proto.Event{Type: proto.EventUIExpired}}))
	if m.overlay.Len() != 1 || len(m.intents) != 1 {
		t.Fatalf("an expiry with no id closed a local dialog: overlay=%d intents=%d", m.overlay.Len(), len(m.intents))
	}
}

func TestUI_DLG11_ExpiryOfAQueuedRequestDropsIt(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, selectRequest("first"))
	pushUI(m, selectRequest("second"))
	execAll(update(m, client.EventMsg{Event: proto.Event{Type: proto.EventUIExpired, ID: "second"}}))
	if len(m.queuedReqs) != 0 || m.overlay.Len() != 1 {
		t.Fatalf("queued=%d overlay=%d, want the open dialog kept and the queue empty", len(m.queuedReqs), m.overlay.Len())
	}
	key(m, escKey) // the first dialog closes; nothing is left to show
	if m.overlay.Len() != 0 {
		t.Fatalf("the expired request came back: overlay=%d", m.overlay.Len())
	}
	for _, a := range f.answers {
		if a.ID == "second" {
			t.Fatalf("an expired request was answered: %+v", a)
		}
	}
}
