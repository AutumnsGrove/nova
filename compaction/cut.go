package compaction

import "github.com/AutumnsGrove/nova/message"

// CutPoint returns the index of the first message to keep after compaction, so
// msgs[:cut] is the span to summarise. The cut is a user-message boundary; when
// no user boundary exists it falls back to the assistant that begins a
// tool-call group. ok is false when everything fits or nothing can be cut.
func CutPoint(msgs []message.Message, keepRecent int) (int, bool) {
	n := len(msgs)
	if n == 0 {
		return 0, false
	}
	start := 0
	if msgs[0].Role == message.RoleSystem {
		start = 1
	}
	if start >= n {
		return 0, false
	}

	// Walk back from the end until the recent tail reaches keepRecent.
	point := n - 1
	sum := 0
	for i := n - 1; i >= start; i-- {
		sum += EstimateMessage(msgs[i])
		point = i
		if keepRecent <= 0 || sum >= keepRecent {
			break
		}
	}
	if keepRecent > 0 && sum < keepRecent {
		return 0, false
	}

	if cut, ok := nearestUser(msgs, point, start); ok {
		return cut, true
	}
	if cut, ok := toolGroupStart(msgs, point, start); ok {
		return cut, true
	}
	return 0, false
}

// nearestUser is the closest user message at or before point, after start, so
// the kept part begins at a user turn. The system message is never returned.
func nearestUser(msgs []message.Message, point, start int) (int, bool) {
	for i := point; i > start; i-- {
		if msgs[i].Role == message.RoleUser {
			return i, true
		}
	}
	return 0, false
}

// toolGroupStart is the fallback for a history with one long turn: the closest
// assistant with tool calls at or before point, after start, provided a user
// message precedes it so the compacted span is not empty.
func toolGroupStart(msgs []message.Message, point, start int) (int, bool) {
	for i := point; i > start; i-- {
		if msgs[i].Role != message.RoleAssistant || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		for j := start; j < i; j++ {
			if msgs[j].Role == message.RoleUser {
				return i, true
			}
		}
	}
	return 0, false
}
