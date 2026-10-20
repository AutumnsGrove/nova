//go:build !notui

package ui

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/auth/providerauth"
)

// sequencedBegin registers a provider whose Begin hands out sessions in order.
func sequencedBegin(t *testing.T, id string, sessions ...providerauth.Session) {
	t.Helper()
	var mu sync.Mutex
	next := 0
	fakeRegister(t, id, []providerauth.Method{providerauth.OAuth}, &fakeStore{}, map[providerauth.Method]providerauth.BeginFunc{
		providerauth.OAuth: func(context.Context, providerauth.Config) (providerauth.Session, error) {
			mu.Lock()
			defer mu.Unlock()
			s := sessions[next]
			next++
			return s, nil
		},
	})
}

// rejectingSession is a flow whose pasted redirect is always refused.
type rejectingSession struct{ *fakeSession }

func (rejectingSession) Submit(string) error { return errors.New("bad redirect") }

func TestUI_SGN63_RetryClosesTheOldSession(t *testing.T) {
	first, second := &rejectingSession{newFakeSession()}, newFakeSession()
	sequencedBegin(t, "fake-retry-c22", first, second)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-retry-c22 oauth")
	drive(m, update(m, enterKey))
	typeKeys(m, "https://127.0.0.1/callback?code=x")
	update(m, enterKey) // the rejected paste reopens the flow dialog with an error
	drive(m, update(m, enterKey))
	if !first.wasClosed() {
		t.Fatal("retrying a sign-in left the first session open; its listener would block the retry")
	}
	if second.wasClosed() || m.overlay.Len() != 1 {
		t.Fatalf("the retried flow should be showing: closed=%v overlay=%d", second.wasClosed(), m.overlay.Len())
	}
}

func TestUI_SGN64_SupersededBeginIsClosed(t *testing.T) {
	first, second := newFakeSession(), newFakeSession()
	sequencedBegin(t, "fake-twice-c22", first, second)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-twice-c22 oauth")
	begin1 := update(m, enterKey)
	m.setEditorText("/login fake-twice-c22 oauth")
	begin2 := update(m, enterKey)
	drive(m, begin1)
	if !first.wasClosed() {
		t.Fatal("a superseded sign-in left its session open")
	}
	if m.overlay.Len() != 0 {
		t.Fatalf("the superseded flow opened a dialog (overlay=%d)", m.overlay.Len())
	}
	drive(m, begin2)
	if m.overlay.Len() != 1 || second.wasClosed() {
		t.Fatalf("the current flow should be showing: closed=%v overlay=%d", second.wasClosed(), m.overlay.Len())
	}
}
