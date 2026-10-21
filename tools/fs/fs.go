// Package fs provides the core filesystem tools: read, write, edit, and list.
package fs

import (
	"fmt"
	"os"
	"sync"
)

// Limits. They are variables so tests can lower them without re-reading a huge
// file, and named here so every tool agrees on the caps.
var (
	maxReadLines   = 2000     // lines read returns by default and at most
	maxReadBytes   = 51200    // bytes of read output before it stops
	maxLineChars   = 2000     // runes of one displayed line before truncation
	maxFileBytes   = 10 << 20 // write and edit refuse content larger than this
	maxListEntries = 500      // list rows before it stops
	maxDiffLines   = 5000     // files larger than this get no diff in Details
)

// pathLocks serialises write and edit on the same path without serialising
// unrelated paths. The map is only ever appended to; a lock is tiny and the
// process holds at most one per path it has touched.
var pathLocks sync.Map

// lockPath locks the mutex for path and returns its unlock function.
func lockPath(path string) func() {
	value, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// statRegular stats abs and refuses anything but a regular file. A pipe or
// device can block forever when it is opened or read, so the check is a stat,
// made before the open.
func statRegular(abs string) (os.FileInfo, error) {
	info, err := os.Stat(abs)
	switch {
	case err != nil:
		return nil, err
	case info.IsDir():
		return nil, fmt.Errorf("%s is a directory; use list", abs)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", abs)
	}
	return info, nil
}
