package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
	if out, err := opt.Exec(ctx, "", nil, s.path, "version"); err != nil {
		return fmt.Errorf("the new binary failed its version check: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(s.path, s.target); err != nil {
		return fmt.Errorf("replace %s: %w", s.target, err)
	}
	return nil
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
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if path.Clean(hdr.Name) != member {
			continue
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("archive has no %s binary", member)
}
