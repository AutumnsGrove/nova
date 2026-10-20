package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// walkEntry is one visited path.
type walkEntry struct {
	Path    string
	Rel     string // path relative to the walk root, with slash separators
	IsDir   bool
	Regular bool // a regular file; false for a pipe, socket, or device
	Size    int64
}

// walkOptions controls a walk.
type walkOptions struct {
	NoIgnore       bool // do not apply .gitignore
	Hidden         bool // include dot-files and dot-directories
	FollowSymlinks bool // follow symlinks (default false)
	MaxDepth       int  // 0 means unlimited
	stats          *walkStats
}

// walkStats collects facts a tool reports back to the model.
type walkStats struct {
	unreadable int // directories that could not be read
}

// walker holds the state shared by every directory of one walk.
type walker struct {
	ctx     context.Context
	root    string
	opt     walkOptions
	visited map[string]bool // real paths of directories already entered
	fn      func(walkEntry) error
}

// walk visits root depth-first with entries sorted bytewise by name, so output
// is deterministic. It never follows symlinks unless asked, always skips .git,
// applies .gitignore at every level unless NoIgnore, and checks ctx between
// entries. The fn callback is called for every directory and file that survives
// the ignore rules; returning an error from fn stops the walk.
func walk(ctx context.Context, root string, opt walkOptions, fn func(walkEntry) error) error {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fn(walkEntry{Path: root, Rel: filepath.Base(root), Regular: info.Mode().IsRegular(), Size: info.Size()})
	}
	w := &walker{ctx: ctx, root: root, opt: opt, visited: map[string]bool{}, fn: fn}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		w.visited[resolved] = true
	}
	return w.dir(root, 0, w.withIgnore(nil, root))
}

// withIgnore returns stack extended by dir's .gitignore, or stack itself when
// ignore files are off or dir has none. It copies before appending so sibling
// directories never share a backing array.
func (w *walker) withIgnore(stack []*ignoreSet, dir string) []*ignoreSet {
	if w.opt.NoIgnore {
		return stack
	}
	set := loadIgnore(dir)
	if set == nil {
		return stack
	}
	return append(stack[:len(stack):len(stack)], set)
}

// dir lists one directory and recurses. depth is the depth of dir below root
// (root itself is 0). An unreadable directory is counted, not fatal.
func (w *walker) dir(dir string, depth int, stack []*ignoreSet) error {
	if w.opt.MaxDepth > 0 && depth >= w.opt.MaxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if w.opt.stats != nil {
			w.opt.stats.unreadable++
		}
		return nil
	}
	for _, entry := range entries {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if name == ".git" || (!w.opt.Hidden && strings.HasPrefix(name, ".")) {
			continue
		}
		full := filepath.Join(dir, name)
		info, ok := w.stat(full, entry)
		if !ok || matchStack(stack, full, info.IsDir()) {
			continue
		}
		rel, _ := relUnder(w.root, full)
		visit := walkEntry{Path: full, Rel: rel, IsDir: info.IsDir(), Regular: info.Mode().IsRegular(), Size: info.Size()}
		if err := w.fn(visit); err != nil {
			return err
		}
		if visit.IsDir {
			if err := w.descend(full, depth, stack); err != nil {
				return err
			}
		}
	}
	return nil
}

// stat returns an entry's file info, resolving a symlink when the walk follows
// them. ok is false for an entry to skip: any symlink when not following, a
// dangling one, or an entry that vanished.
func (w *walker) stat(full string, entry os.DirEntry) (info os.FileInfo, ok bool) {
	var err error
	if entry.Type()&os.ModeSymlink != 0 {
		if !w.opt.FollowSymlinks {
			return nil, false
		}
		info, err = os.Stat(full)
	} else {
		info, err = entry.Info()
	}
	return info, err == nil
}

// descend walks into a directory unless its real path was already entered,
// which breaks symlink loops and double visits through two links.
func (w *walker) descend(full string, depth int, stack []*ignoreSet) error {
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil || w.visited[resolved] {
		return nil
	}
	w.visited[resolved] = true
	return w.dir(full, depth+1, w.withIgnore(stack, full))
}

// loadIgnore reads dir/.gitignore, or returns nil when there is none or it
// cannot be read. An ignore file's own errors never fail a walk.
func loadIgnore(dir string) *ignoreSet {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	return parseIgnore(dir, data)
}
