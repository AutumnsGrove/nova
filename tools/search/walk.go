package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// walkEntry is one visited path.
type walkEntry struct {
	Path  string
	Rel   string // path relative to the walk root, with slash separators
	IsDir bool
	Size  int64
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
		return fn(walkEntry{Path: root, Rel: filepath.Base(root), Size: info.Size()})
	}
	var stack []*ignoreSet
	if !opt.NoIgnore {
		if set := loadIgnore(root); set != nil {
			stack = append(stack, set)
		}
	}
	visited := map[string]bool{}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		visited[real] = true
	}
	return walkDir(ctx, root, root, 0, opt, stack, visited, fn)
}

// walkDir lists one directory and recurses. depth is the depth of dir below
// root (root itself is 0).
func walkDir(ctx context.Context, root, dir string, depth int, opt walkOptions, stack []*ignoreSet, visited map[string]bool, fn func(walkEntry) error) error {
	if opt.MaxDepth > 0 && depth >= opt.MaxDepth {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if opt.stats != nil {
			opt.stats.unreadable++
		}
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if name == ".git" {
			continue
		}
		if !opt.Hidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		isSymlink := entry.Type()&os.ModeSymlink != 0
		if isSymlink && !opt.FollowSymlinks {
			continue
		}
		info, err := entry.Info()
		if isSymlink {
			if info, err = os.Stat(full); err != nil {
				continue // dangling link
			}
			isSymlink = false
		} else if err != nil {
			continue
		}
		isDir := info.IsDir()
		if matchStack(stack, full, isDir) {
			continue
		}
		rel, _ := relUnder(root, full)
		if err := fn(walkEntry{Path: full, Rel: rel, IsDir: isDir, Size: info.Size()}); err != nil {
			return err
		}
		if !isDir {
			continue
		}
		real, err := filepath.EvalSymlinks(full)
		if err != nil || visited[real] {
			continue // a symlink loop or a directory already seen
		}
		visited[real] = true
		childStack := stack
		if !opt.NoIgnore {
			if set := loadIgnore(full); set != nil {
				childStack = append(append([]*ignoreSet(nil), stack...), set)
			}
		}
		if err := walkDir(ctx, root, full, depth+1, opt, childStack, visited, fn); err != nil {
			return err
		}
	}
	return nil
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
