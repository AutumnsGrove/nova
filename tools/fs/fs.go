// Package fs provides the core filesystem tools: read, write, edit, and list.
package fs

import "sync"

// Limits. They are variables so tests can lower them without re-reading a huge
// file, and named here so every tool agrees on the caps.
var (
	maxReadLines   = 2000     // lines read returns by default and at most
	maxReadBytes   = 51200    // bytes of read output before it stops
	maxLineChars   = 2000     // runes of one displayed line before truncation
	maxFileBytes   = 10 << 20 // write and edit refuse content larger than this
	maxListEntries = 500      // list rows before it stops
	binarySniff    = 8000     // bytes sniffed for a NUL when classifying a file
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
