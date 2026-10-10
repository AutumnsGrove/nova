package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Info summarizes one session file for a picker.
type Info struct {
	Path     string
	ID       string
	Name     string
	CWD      string
	Created  time.Time
	Modified time.Time
	Entries  int
}

// List reports the sessions in dir, newest first. Files that cannot be parsed
// are skipped without an error.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		if info, ok := scanInfo(filepath.Join(dir, de.Name())); ok {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// scanInfo reads only a header and counts entry lines, taking the last
// session_info name. It reports false when the header is missing or invalid.
func scanInfo(path string) (Info, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Info{}, false
	}
	info := Info{Path: path, Modified: fi.ModTime()}
	headerSeen := false
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Type    string    `json:"type"`
			ID      string    `json:"id"`
			CWD     string    `json:"cwd"`
			Created time.Time `json:"created"`
			Name    string    `json:"name"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			if !headerSeen {
				return Info{}, false
			}
			continue
		}
		if !headerSeen {
			if probe.Type != headerType {
				return Info{}, false
			}
			headerSeen = true
			info.ID, info.CWD, info.Created = probe.ID, probe.CWD, probe.Created
			continue
		}
		info.Entries++
		if probe.Type == TypeSessionInfo && probe.Name != "" {
			info.Name = probe.Name
		}
	}
	if !headerSeen {
		return Info{}, false
	}
	if info.ID == "" {
		info.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	return info, true
}

// Fork writes a new session at dst holding the branch ending at from. The new
// header names src as ParentSession. An unknown from or an existing dst is an
// error, and no file is left behind.
func Fork(src *Store, from, dst string, opt Options) (*Store, error) {
	branch, err := src.Branch(from)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil, fmt.Errorf("session: destination %s already exists", dst)
	}
	h := src.Header()
	dst, err = filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	s, err := open(dst, opt, &Header{CWD: h.CWD, ParentSession: h.ID})
	if err != nil {
		return nil, err
	}
	for _, e := range branch {
		if _, err := s.AppendOn(e.Parent, e); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

// DefaultDir returns the stable per-working-directory session directory. The
// raw path never appears; a SHA-256 prefix stands in for it.
func DefaultDir(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	sum := sha256.Sum256([]byte(abs))
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "nova", "sessions", hex.EncodeToString(sum[:8]))
}
