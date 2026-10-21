package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// staged is a new binary written beside the target so the final rename stays
// on one filesystem and the old binary survives every earlier failure.
type staged struct {
	path   string
	target string
}

// stage creates an empty temporary file in the target's directory.
func stage(target string) (*staged, error) {
	f, err := os.CreateTemp(filepath.Dir(target), ".nova-update-*")
	if err != nil {
		return nil, fmt.Errorf("stage beside %s: %w", target, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(name)
		return nil, err
	}
	return &staged{path: name, target: target}, nil
}

// cleanup removes the staged file; it is harmless after a successful rename.
func (s *staged) cleanup() { os.Remove(s.path) }

// commit sanity-checks the staged binary and then renames it over the target.
func (s *staged) commit(ctx context.Context, opt Options) error {
	if err := os.Chmod(s.path, 0o755); err != nil {
		return err
	}
	// An empty, non-nil environment: the version check needs none of the caller's,
	// which holds the user's keys.
	if out, err := opt.Exec(ctx, "", []string{}, s.path, "version"); err != nil {
		return fmt.Errorf("the new binary failed its version check: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(s.path, s.target); err != nil {
		return fmt.Errorf("replace %s: %w", s.target, err)
	}
	return nil
}

// write copies src into dst, refusing more than maxBinaryBytes: a small archive
// can unpack to far more than it weighs.
func write(dst string, src io.Reader) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	n, err := io.Copy(f, io.LimitReader(src, maxBinaryBytes+1))
	if err == nil && n > maxBinaryBytes {
		err = fmt.Errorf("the binary is too large (over %d bytes)", maxBinaryBytes)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// extract writes the archive member whose cleaned path is exactly member into
// dst. Every other member is ignored, so a name such as "../nova" never lands.
func extract(dst string, archive []byte, member string) error {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if path.Clean(hdr.Name) != member {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive member %s is not a regular file", member)
		}
		return write(dst, tr)
	}
	return fmt.Errorf("archive has no %s binary", member)
}
