package config

import (
	"errors"
	"fmt"
	"os"
)

// maxReadBytes caps any single file the loader reads whole. It is far above a
// real config, skill, or template, and exists so a hostile or runaway file
// cannot exhaust memory.
const maxReadBytes = 16 << 20

var (
	// errNotRegular marks a pipe, socket, or device: reading one can block
	// startup forever, and a symlink to /dev/zero never ends.
	errNotRegular = errors.New("not a regular file")
	// errTooLarge marks a file over the read limit.
	errTooLarge = errors.New("file is too large")
)

// checkReadable reports why a stat'd file must not be read whole, or nil.
func checkReadable(info os.FileInfo, limit int64) error {
	if !info.Mode().IsRegular() {
		return errNotRegular
	}
	if info.Size() > limit {
		return fmt.Errorf("%w (%d bytes)", errTooLarge, info.Size())
	}
	return nil
}

// readRegularFile reads path, following symlinks, but only when it is a regular
// file of at most limit bytes. The stat comes first so a named pipe is refused
// without opening it. A missing file keeps its fs.ErrNotExist.
func readRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := checkReadable(info, limit); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
