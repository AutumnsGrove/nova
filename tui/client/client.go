//go:build !notui

// Package client adapts a mode.Client to Bubble Tea messages. The model sends
// commands and drains events through tea.Cmds; no server state is shared any
// other way.
package client

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/proto"
)

// Client is the TUI's view of the server connection.
type Client interface {
	// Send runs a command and yields a ResponseMsg.
	Send(cmd proto.Command) tea.Cmd
	// Next waits for the next event or UI request and yields an EventMsg,
	// UIRequestMsg, or ClosedMsg. The model must issue it again after each
	// message.
	Next() tea.Cmd
	// AnswerUI answers an outstanding UI request.
	AnswerUI(resp proto.UIResponse) tea.Cmd
}

// ResponseMsg is the result of a command.
type ResponseMsg struct{ proto.Response }

// EventMsg carries one protocol event.
type EventMsg struct{ proto.Event }

// UIRequestMsg carries one UI request that needs an answer.
type UIRequestMsg struct{ proto.UIRequest }

// ClosedMsg means the connection has ended.
type ClosedMsg struct{}

// pipe is the subset of mode.Client the adapter uses, so tests can substitute
// a fake without a server.
type pipe interface {
	Send(context.Context, proto.Command) proto.Response
	Events() <-chan proto.Event
	Requests() <-chan proto.UIRequest
	Answer(proto.UIResponse)
}

// FromPipe adapts an in-process mode.Client.
func FromPipe(c *mode.Client) Client { return &pipeClient{p: c} }

// pipeClient implements Client over a pipe.
type pipeClient struct{ p pipe }

// Send runs the command on the pipe and returns its response.
func (c *pipeClient) Send(cmd proto.Command) tea.Cmd {
	return func() tea.Msg {
		return ResponseMsg{Response: c.p.Send(context.Background(), cmd)}
	}
}

// Next blocks for one event or UI request, or reports a closed pipe.
func (c *pipeClient) Next() tea.Cmd {
	return func() tea.Msg {
		select {
		case e, ok := <-c.p.Events():
			if !ok {
				return ClosedMsg{}
			}
			return EventMsg{Event: e}
		case r, ok := <-c.p.Requests():
			if !ok {
				return ClosedMsg{}
			}
			return UIRequestMsg{UIRequest: r}
		}
	}
}

// AnswerUI delivers an answer to the waiting server.
func (c *pipeClient) AnswerUI(resp proto.UIResponse) tea.Cmd {
	return func() tea.Msg {
		c.p.Answer(resp)
		return nil
	}
}
