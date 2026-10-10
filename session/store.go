package session

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Version is the current session file version.
const Version = 1

// headerType is the type value on the first line of a session file.
const headerType = "session"

// errClosed is returned by a write after Close.
var errClosed = errors.New("session: store is closed")

// Options configures a Store.
type Options struct {
	Sync bool             // fsync after every append
	Now  func() time.Time // timestamp source; default time.Now
	ID   func() string    // id generator; default 8 random hex digits
}

// Header is the first line of a session file.
type Header struct {
	Type          string    `json:"type"`
	Version       int       `json:"v"`
	ID            string    `json:"id"`
	CWD           string    `json:"cwd"`
	Created       time.Time `json:"created"`
	ParentSession string    `json:"parent_session,omitempty"`
}

// Warning is one skipped or repaired line, with its 1-based file line.
type Warning struct {
	Line int
	Err  error
}

func (w Warning) Error() string { return fmt.Sprintf("session: line %d: %v", w.Line, w.Err) }

// Store is an open session file. It is safe for concurrent use.
type Store struct {
	path string
	opt  Options

	mu       sync.Mutex
	file     *os.File
	header   Header
	entries  []Entry
	index    map[string]int
	leaf     string
	warnings []Warning
	closed   bool
	offset   int64 // bytes consumed so far
	lines    int   // complete lines consumed so far
}

// Open opens or creates the session at path.
func Open(path string, opt Options) (*Store, error) {
	return open(path, opt, nil)
}

// open opens path, creating it and writing a header when missing. A non-nil
// template supplies header fields for Fork.
func open(path string, opt Options, template *Header) (*Store, error) {
	opt = withDefaults(opt)
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, opt: opt, file: f, index: map[string]int{}}
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if len(data) == 0 {
		h := Header{Type: headerType, Version: Version}
		if template != nil {
			h = *template
		}
		if h.Type == "" {
			h.Type = headerType
		}
		if h.Version == 0 {
			h.Version = Version
		}
		if h.ID == "" {
			h.ID = opt.ID()
		}
		if h.CWD == "" {
			h.CWD, _ = os.Getwd()
		}
		if h.Created.IsZero() {
			h.Created = opt.Now()
		}
		s.header = h
		if err := s.writeHeader(); err != nil {
			f.Close()
			return nil, err
		}
		return s, nil
	}
	s.offset = int64(len(data))
	if err := s.load(data); err != nil {
		f.Close()
		return nil, err
	}
	if data[len(data)-1] != '\n' {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			f.Close()
			return nil, err
		}
		s.offset++
		s.lines++
	}
	return s, nil
}

// writeHeader writes the header line and records the file offset.
func (s *Store) writeHeader() error {
	b, err := json.Marshal(s.header)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := s.file.Write(b); err != nil {
		return err
	}
	s.offset = int64(len(b))
	s.lines = 1
	return nil
}

// load parses the whole file. The first non-blank line must be a header.
func (s *Store) load(data []byte) error {
	lines := bytes.Split(data, []byte{'\n'})
	last := len(lines)
	if data[len(data)-1] == '\n' {
		last--
	}
	for i := 0; i < last; i++ {
		raw := lines[i]
		s.lines++
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if s.header.Type == "" {
			var h Header
			if err := json.Unmarshal(raw, &h); err != nil || h.Type != headerType {
				return fmt.Errorf("session: %s: missing or invalid header", s.path)
			}
			if h.Version != Version {
				return fmt.Errorf("session: %s: unsupported version %d", s.path, h.Version)
			}
			s.header = h
			continue
		}
		s.loadEntry(s.lines, raw)
	}
	if s.header.Type == "" {
		return fmt.Errorf("session: %s: missing header", s.path)
	}
	s.fixParents()
	s.pickLeaf()
	return nil
}

// loadEntry parses one entry line forgivingly, recording a warning on failure.
func (s *Store) loadEntry(lineNo int, raw []byte) {
	var probe struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Parent string `json:"parent"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		s.warn(lineNo, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if probe.Type == "" {
		s.warn(lineNo, errors.New("entry has no type"))
		return
	}
	var e Entry
	if knownType(probe.Type) {
		if err := json.Unmarshal(raw, &e); err != nil {
			s.warn(lineNo, fmt.Errorf("invalid %s entry: %w", probe.Type, err))
			return
		}
	} else {
		e = Entry{Type: probe.Type, ID: probe.ID, Parent: probe.Parent, Raw: append(json.RawMessage(nil), raw...)}
	}
	if e.ID == "" {
		e.ID = s.opt.ID()
	}
	if _, dup := s.index[e.ID]; dup {
		s.warn(lineNo, fmt.Errorf("duplicate id %s", e.ID))
		return
	}
	e.line = lineNo
	s.index[e.ID] = len(s.entries)
	s.entries = append(s.entries, e)
	if !e.displayOnly() {
		s.leaf = e.ID
	}
}

// fixParents turns an entry with a missing parent into a root, then breaks any
// parent cycle at the entry that closes it.
func (s *Store) fixParents() {
	for i := range s.entries {
		e := &s.entries[i]
		if e.Parent == "" {
			continue
		}
		if _, ok := s.index[e.Parent]; !ok {
			s.warn(e.line, fmt.Errorf("entry %s has missing parent %s; treated as a root", e.ID, e.Parent))
			e.Parent = ""
		}
	}
	state := make(map[string]int, len(s.entries)) // 0 unvisited, 1 on the walk, 2 done
	for i := range s.entries {
		if state[s.entries[i].ID] == 2 {
			continue
		}
		var path []int
		cur := i
		for {
			e := &s.entries[cur]
			if state[e.ID] == 1 {
				s.warn(e.line, fmt.Errorf("entry %s closes a parent cycle; treated as a root", e.ID))
				e.Parent = ""
				break
			}
			if state[e.ID] == 2 {
				break
			}
			state[e.ID] = 1
			path = append(path, cur)
			if e.Parent == "" {
				break
			}
			next, ok := s.index[e.Parent]
			if !ok {
				break
			}
			cur = next
		}
		for _, idx := range path {
			state[s.entries[idx].ID] = 2
		}
	}
}

// pickLeaf sets the leaf to the last entry that is not display-only.
func (s *Store) pickLeaf() {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if !s.entries[i].displayOnly() {
			s.leaf = s.entries[i].ID
			return
		}
	}
	s.leaf = ""
}

// warn records a skipped or repaired line.
func (s *Store) warn(line int, err error) {
	s.warnings = append(s.warnings, Warning{Line: line, Err: err})
}

// Header returns the session header.
func (s *Store) Header() Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header
}

// Warnings returns the load and repair warnings, oldest first.
func (s *Store) Warnings() []Warning {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Warning(nil), s.warnings...)
}

// Append writes an entry whose parent defaults to the current leaf.
func (s *Store) Append(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Entry{}, errClosed
	}
	return s.appendLocked(true, "", e)
}

// AppendOn writes an entry with an explicit parent.
func (s *Store) AppendOn(parent string, e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Entry{}, errClosed
	}
	return s.appendLocked(false, parent, e)
}

// appendLocked assigns an id and timestamp, writes one line under the file
// lock, and moves the leaf. useLeaf attaches to the current leaf (Append);
// otherwise the explicit parent is used (AppendOn).
func (s *Store) appendLocked(useLeaf bool, parent string, e Entry) (Entry, error) {
	if err := lockFile(s.file); err != nil {
		return Entry{}, err
	}
	defer unlockFile(s.file)
	if err := s.refresh(); err != nil {
		return Entry{}, err
	}
	if e.Parent == "" {
		if useLeaf {
			e.Parent = s.leaf
		} else {
			e.Parent = parent
		}
	}
	if e.Parent != "" {
		if _, ok := s.index[e.Parent]; !ok {
			return Entry{}, fmt.Errorf("session: unknown parent %q", e.Parent)
		}
	}
	if e.ID == "" {
		e.ID = s.newID()
	}
	if e.TS.IsZero() {
		e.TS = s.opt.Now()
	}
	b, err := marshalEntry(e)
	if err != nil {
		return Entry{}, err
	}
	if _, err := s.file.Write(b); err != nil {
		return Entry{}, err
	}
	if s.opt.Sync {
		if err := s.file.Sync(); err != nil {
			return Entry{}, err
		}
	}
	s.offset += int64(len(b))
	s.lines++
	e.line = s.lines
	s.index[e.ID] = len(s.entries)
	s.entries = append(s.entries, e)
	if !e.displayOnly() {
		s.leaf = e.ID
	}
	return e, nil
}

// marshalEntry encodes an entry, preserving an unknown entry's raw line.
func marshalEntry(e Entry) ([]byte, error) {
	if len(e.Raw) > 0 {
		b := append([]byte(nil), e.Raw...)
		if len(b) == 0 || b[len(b)-1] != '\n' {
			b = append(b, '\n')
		}
		return b, nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// newID returns an unused id, retrying on a collision.
func (s *Store) newID() string {
	for i := 0; i < 100; i++ {
		id := s.opt.ID()
		if _, ok := s.index[id]; !ok {
			return id
		}
	}
	return s.opt.ID() + "x"
}

// refresh reads any bytes appended by another process since the last read,
// consuming only complete lines.
func (s *Store) refresh() error {
	info, err := s.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= s.offset {
		return nil
	}
	buf := make([]byte, info.Size()-s.offset)
	if _, err := s.file.ReadAt(buf, s.offset); err != nil {
		return err
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil
	}
	complete := buf[:end+1]
	s.offset += int64(len(complete))
	for _, raw := range bytes.Split(complete[:len(complete)-1], []byte{'\n'}) {
		s.lines++
		s.loadEntry(s.lines, raw)
	}
	return nil
}

// Close releases the file. It is idempotent.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.file.Close()
}

// withDefaults fills the zero Options fields.
func withDefaults(o Options) Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ID == nil {
		o.ID = randomID
	}
	return o
}

// randomID returns "e_" plus 8 hex digits.
func randomID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "e_" + hex.EncodeToString(b[:])
}
