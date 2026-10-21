package session

import "fmt"

// Entries returns every loaded entry in file order, after reading any lines
// another process appended.
func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh()
	return append([]Entry(nil), s.entries...)
}

// Get returns one entry by id.
func (s *Store) Get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh()
	i, ok := s.index[id]
	if !ok {
		return Entry{}, false
	}
	return s.entries[i], true
}

// Leaf returns the active leaf id, or "" for an empty session.
func (s *Store) Leaf() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh()
	return s.leaf
}

// SetLeaf moves the active leaf. It never writes to the file.
func (s *Store) SetLeaf(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh()
	if _, ok := s.index[id]; !ok {
		return fmt.Errorf("session: unknown entry %q", id)
	}
	s.leaf = id
	return nil
}

// Children returns the child ids of id in file order. "" means the roots.
func (s *Store) Children(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh()
	var out []string
	for i := range s.entries {
		if s.entries[i].Parent == id {
			out = append(out, s.entries[i].ID)
		}
	}
	return out
}

// Branch returns the entries from the root to leaf. "" means the current leaf.
func (s *Store) Branch(leaf string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh()
	if leaf == "" {
		leaf = s.leaf
	}
	if leaf == "" {
		return nil, nil
	}
	i, ok := s.index[leaf]
	if !ok {
		return nil, fmt.Errorf("session: unknown entry %q", leaf)
	}
	var reversed []Entry
	// A branch can never be longer than the session; the bound keeps a parent
	// cycle that slipped past the loader from growing without limit.
	for len(reversed) <= len(s.entries) {
		e := s.entries[i]
		reversed = append(reversed, e)
		if e.Parent == "" {
			break
		}
		j, ok := s.index[e.Parent]
		if !ok {
			break
		}
		i = j
	}
	out := make([]Entry, len(reversed))
	for k := range reversed {
		out[len(reversed)-1-k] = reversed[k]
	}
	return out, nil
}
