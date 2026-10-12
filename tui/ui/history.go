//go:build !notui

package ui

// historyMax is the number of submitted inputs kept in memory.
const historyMax = 500

// history is the in-memory input history. idx == len(entries) means "not
// browsing"; up moves toward older entries and down toward newer ones.
type history struct {
	entries []string
	idx     int
}

// add records a submitted input, skipping a duplicate of the last entry.
func (h *history) add(s string) {
	if s == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == s {
		h.idx = len(h.entries)
		return
	}
	h.entries = append(h.entries, s)
	if len(h.entries) > historyMax {
		h.entries = h.entries[len(h.entries)-historyMax:]
	}
	h.idx = len(h.entries)
}

// up returns the previous entry.
func (h *history) up() (string, bool) {
	if len(h.entries) == 0 || h.idx == 0 {
		return "", false
	}
	h.idx--
	return h.entries[h.idx], true
}

// down returns the next entry, or an empty string when leaving history.
func (h *history) down() (string, bool) {
	if h.idx >= len(h.entries) {
		return "", false
	}
	h.idx++
	if h.idx == len(h.entries) {
		return "", true
	}
	return h.entries[h.idx], true
}
