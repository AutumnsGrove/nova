package fs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// writeAtomic writes data to path atomically: a temporary file in the same
// directory is written, synced, and renamed over the target. A failure leaves
// the original file unchanged and removes the temporary file. perm sets the new
// file's mode (the caller preserves an existing file's mode).
func writeAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nova-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// Any failure, including a rename that did not happen, must not leave
		// the temporary file behind. Closing twice is harmless here: the second
		// error is the only one dropped.
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir flushes a directory entry so a rename survives a crash. Failure is
// ignored: not every platform allows opening a directory, and the file content
// is already safe.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// realTarget follows a symlink so an atomic rename updates the file the link
// points to instead of replacing the link. A path that does not exist is
// returned unchanged; a dangling or looping symlink is an error.
func realTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// fileMode returns the mode of an existing regular file, or def when the file
// does not exist.
func fileMode(path string, def os.FileMode) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return def
	}
	return info.Mode().Perm()
}
