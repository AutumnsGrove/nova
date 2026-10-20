package install

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/ext"
)

// Source is a parsed install source: a git URL or a local directory.
type Source struct {
	URL   string
	Ref   string // branch, tag, or commit
	Local bool   // a local directory, installed as a link
	Path  string // absolute path when Local
}

// Plan is what the trust prompt shows before an install or update.
type Plan struct {
	Name         string
	Source       string
	Commit       string
	Run          []string
	Build        []string
	Capabilities []string
	Replaces     string // previous commit on an update
}

// InstallOptions configures an Installer.
type InstallOptions struct {
	Root     string
	LockPath string
	Confirm  func(Plan) bool // nil refuses
	Git      GitRunner
	Run      ext.CommandRunner
	Timeout  time.Duration
	// Warn receives non-fatal problems, such as a damaged lockfile. It may be nil.
	Warn func(msg string)
}

// Installer installs, updates, removes, and lists extensions.
type Installer struct {
	opt InstallOptions
	git GitRunner
	run ext.CommandRunner

	installMu sync.Mutex
}

// NewInstaller builds an installer, filling the default git and command
// runners.
func NewInstaller(opt InstallOptions) *Installer {
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Minute
	}
	i := &Installer{opt: opt, git: opt.Git, run: opt.Run}
	if i.git == nil {
		i.git = execGit{}
	}
	if i.run == nil {
		i.run = ext.ExecRunner{}
	}
	return i
}

// Installed is one entry from the lockfile joined with the filesystem.
type Installed struct {
	Name    string
	Source  string
	Ref     string
	Commit  string
	Dir     string
	Drifted bool
}

// refRe is the shape of a branch or tag name: it cannot start with "-", so a
// ref is never read by git as an option.
var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// ParseSource accepts the documented source forms and rejects the rest.
func ParseSource(s string) (Source, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Source{}, errors.New("ext: empty source")
	}
	if strings.HasPrefix(s, "-") {
		return Source{}, fmt.Errorf("ext: source %q starts with -", s)
	}
	if strings.HasPrefix(s, "file://") || strings.HasPrefix(s, "ext::") {
		return Source{}, fmt.Errorf("ext: unsupported transport in %q", s)
	}
	raw, ref := splitSourceRef(s)
	if strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") {
		return Source{}, fmt.Errorf("ext: invalid ref %q", ref)
	}
	if ref != "" && !looksLikeCommit(ref) && !refRe.MatchString(ref) {
		return Source{}, fmt.Errorf("ext: invalid ref %q", ref)
	}
	if isLocalSource(raw) {
		path, err := expandPath(raw)
		if err != nil {
			return Source{}, err
		}
		return Source{URL: path, Path: path, Ref: ref, Local: true}, nil
	}
	cloneURL, err := normaliseGitURL(raw)
	if err != nil {
		return Source{}, err
	}
	return Source{URL: cloneURL, Ref: ref}, nil
}

// splitSourceRef splits a trailing @ref off s. An "@" followed by a path or host
// separator belongs to the URL (git@host:owner/repo), not to a ref.
func splitSourceRef(s string) (string, string) {
	i := strings.LastIndexByte(s, '@')
	if i <= 0 {
		return s, ""
	}
	ref := s[i+1:]
	if ref == "" || strings.ContainsAny(ref, "/:") {
		return s, ""
	}
	return s[:i], ref
}

// isLocalSource reports whether raw names a local path.
func isLocalSource(raw string) bool {
	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~/") {
		return true
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
		return false
	}
	// A bare owner/repo is GitHub shorthand; anything else with no dot host is
	// treated as a path only when it exists locally.
	if strings.Count(raw, "/") == 1 && !strings.HasPrefix(raw, ".") {
		return false
	}
	return pathExists(raw)
}

// expandPath expands ~ and makes a path absolute.
func expandPath(raw string) (string, error) {
	if strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		raw = filepath.Join(home, raw[2:])
	}
	return filepath.Abs(raw)
}

// normaliseGitURL turns the accepted URL forms into a clone URL. Plain http is
// refused: the code fetched here is run, and http lets anyone on the path
// change it.
func normaliseGitURL(raw string) (string, error) {
	if strings.HasPrefix(raw, "git@") {
		return raw, nil
	}
	if strings.HasPrefix(raw, "http://") {
		return "", fmt.Errorf("ext: plain http is not allowed for %q; use https", raw)
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("ext: invalid URL %q", raw)
		}
		if u.User != nil {
			return "", fmt.Errorf("ext: URL with credentials is not allowed: %q", raw)
		}
		return raw, nil
	}
	if strings.HasPrefix(raw, "github.com/") {
		return "https://" + raw, nil
	}
	parts := strings.Split(raw, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return "https://github.com/" + raw, nil
	}
	return "", fmt.Errorf("ext: unrecognised source %q", raw)
}

// lock reads the lockfile, reporting a damaged one through Warn.
func (i *Installer) lock() map[string]LockEntry {
	entries, warnings := readLock(i.opt.LockPath)
	if i.opt.Warn != nil {
		for _, w := range warnings {
			i.opt.Warn(w)
		}
	}
	return entries
}

// extDir is where the extension called name lives. Names come from manifests,
// which restrict them, and from the lockfile, which anything can edit; one that
// is not a single plain path element is refused, so it can never address a
// path outside Root.
func (i *Installer) extDir(name string) (string, error) {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return "", fmt.Errorf("ext: invalid extension name %q", name)
	}
	return filepath.Join(i.opt.Root, name), nil
}

// Install clones and installs the extensions in src.
func (i *Installer) Install(ctx context.Context, src Source) (*ext.Manifest, error) {
	i.installMu.Lock()
	defer i.installMu.Unlock()
	if i.opt.Root == "" || i.opt.LockPath == "" {
		return nil, errors.New("ext: install root and lock path are required")
	}
	if err := os.MkdirAll(i.opt.Root, 0o755); err != nil {
		return nil, err
	}
	if src.Local {
		return i.installLocal(src)
	}
	return i.installGit(ctx, src)
}

// removeAll deletes path, ignoring failure: it only cleans up after an install
// that has already succeeded or failed on its own terms.
func removeAll(path string) { _ = os.RemoveAll(path) }

// installGit clones into a temp directory, plans, confirms, builds, and moves.
func (i *Installer) installGit(ctx context.Context, src Source) (*ext.Manifest, error) {
	tmp, err := os.MkdirTemp(i.opt.Root, ".install-*")
	if err != nil {
		return nil, err
	}
	defer removeAll(tmp)
	sub := filepath.Join(tmp, "repo")
	if err := gitClone(ctx, i.git, src.URL, src.Ref, sub); err != nil {
		return nil, err
	}
	commit, err := gitHead(ctx, i.git, sub)
	if err != nil {
		return nil, err
	}
	found, err := discover(sub)
	if err != nil {
		return nil, err
	}
	lock := i.lock()
	if err := i.checkCollisions(found.names(), lock); err != nil {
		return nil, err
	}
	for _, f := range found {
		if !i.confirm(plan(f.manifest, src.URL, commit)) {
			return nil, errors.New("ext: install refused")
		}
	}
	for _, f := range found {
		if err := i.build(ctx, f.dir, f.manifest.Build); err != nil {
			return nil, err
		}
	}
	moved, err := i.moveAll(found)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		lock[f.manifest.Name] = LockEntry{
			Name:         f.manifest.Name,
			Source:       src.URL,
			Ref:          src.Ref,
			Commit:       commit,
			Installed:    time.Now(),
			ManifestHash: f.hash,
		}
	}
	if err := writeLock(i.opt.LockPath, lock); err != nil {
		for _, dir := range moved {
			removeAll(dir)
		}
		return nil, err
	}
	return found[0].manifest, nil
}

// confirm asks the trust prompt about p. Without a prompt nothing is trusted.
func (i *Installer) confirm(p Plan) bool { return i.opt.Confirm != nil && i.opt.Confirm(p) }

// installLocal links a local directory into Root.
func (i *Installer) installLocal(src Source) (*ext.Manifest, error) {
	m, err := ext.LoadManifest(src.Path)
	if err != nil {
		return nil, err
	}
	lock := i.lock()
	if err := i.checkCollisions([]string{m.Name}, lock); err != nil {
		return nil, err
	}
	if !i.confirm(plan(m, src.Path, "")) {
		return nil, errors.New("ext: install refused")
	}
	dst, err := i.extDir(m.Name)
	if err != nil {
		return nil, err
	}
	if err := os.Symlink(src.Path, dst); err != nil {
		return nil, err
	}
	lock[m.Name] = LockEntry{Name: m.Name, Source: src.Path, Ref: src.Ref, Commit: linkCommit, Installed: time.Now(), ManifestHash: ext.ManifestHash(src.Path), Linked: true}
	if err := writeLock(i.opt.LockPath, lock); err != nil {
		_ = os.Remove(dst)
		return nil, err
	}
	return m, nil
}

// linkCommit is the lockfile commit of a linked local extension, which has none.
const linkCommit = "link"

// discovered is one validated extension found in a clone.
type discovered struct {
	dir      string
	manifest *ext.Manifest
	hash     string
}

// discoveries is every extension found in a clone.
type discoveries []discovered

// names lists the extension names, in discovery order.
func (ds discoveries) names() []string {
	names := make([]string, len(ds))
	for n, d := range ds {
		names[n] = d.manifest.Name
	}
	return names
}

// discover loads and validates every extension in dir.
func discover(dir string) (discoveries, error) {
	dirs := findExtensions(dir)
	if len(dirs) == 0 {
		return nil, errors.New("ext: no extension.yaml found")
	}
	var out discoveries
	for _, d := range dirs {
		m, err := ext.LoadManifest(d)
		if err != nil {
			return nil, err
		}
		out = append(out, discovered{dir: d, manifest: m, hash: ext.ManifestHash(d)})
	}
	return out, nil
}

// checkCollisions rejects a name that is reserved for a built-in or bundled
// extension, appears twice, or is already installed.
func (i *Installer) checkCollisions(names []string, lock map[string]LockEntry) error {
	for n, name := range names {
		if ext.Reserved(name) {
			return fmt.Errorf("ext: %s conflicts with a built-in or bundled extension", name)
		}
		if slices.Contains(names[:n], name) {
			return fmt.Errorf("ext: the source holds %s more than once", name)
		}
		if e, ok := lock[name]; ok {
			return fmt.Errorf("ext: %s is already installed from %s", name, e.Source)
		}
		dir, err := i.extDir(name)
		if err != nil {
			return err
		}
		if pathExists(dir) {
			return fmt.Errorf("ext: %s already exists", name)
		}
	}
	return nil
}

// plan builds the trust prompt for one extension.
func plan(m *ext.Manifest, source, commit string) Plan {
	return Plan{
		Name:         m.Name,
		Source:       source,
		Commit:       commit,
		Run:          m.Run,
		Build:        m.Build,
		Capabilities: m.Capabilities,
	}
}

// build runs a manifest's build step, if it has one, with a timeout.
func (i *Installer) build(ctx context.Context, dir string, argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, i.opt.Timeout)
	defer cancel()
	return i.run.Run(ctx, dir, argv)
}

// moveAll renames each extension into Root. If any move fails it removes the
// ones already moved, so a failed install leaves no directory behind.
func (i *Installer) moveAll(found discoveries) ([]string, error) {
	var moved []string
	for _, f := range found {
		dst, err := i.extDir(f.manifest.Name)
		if err == nil && pathExists(dst) {
			err = fmt.Errorf("ext: %s already exists", f.manifest.Name)
		}
		if err == nil {
			err = os.Rename(f.dir, dst)
		}
		if err != nil {
			for _, dir := range moved {
				removeAll(dir)
			}
			return nil, err
		}
		moved = append(moved, dst)
	}
	return moved, nil
}

// Update fetches the locked source again and replaces the extension.
func (i *Installer) Update(ctx context.Context, name string) (*ext.Manifest, error) {
	i.installMu.Lock()
	defer i.installMu.Unlock()
	lock := i.lock()
	entry, ok := lock[name]
	if !ok {
		return nil, fmt.Errorf("ext: %s is not installed", name)
	}
	if entry.Linked {
		return nil, errors.New("ext: a linked extension has nothing to update")
	}
	dst, err := i.extDir(name)
	if err != nil {
		return nil, err
	}
	// The locked source is checked again, as at install: the lockfile is a file
	// anyone can edit, and what it names is about to be cloned and run.
	src, err := ParseSource(entry.Source)
	if err != nil {
		return nil, fmt.Errorf("ext: %s: locked source is no longer valid: %w", name, err)
	}
	tmp, err := os.MkdirTemp(i.opt.Root, ".update-*")
	if err != nil {
		return nil, err
	}
	defer removeAll(tmp)
	sub := filepath.Join(tmp, "repo")
	if err := gitClone(ctx, i.git, src.URL, entry.Ref, sub); err != nil {
		return nil, err
	}
	commit, err := gitHead(ctx, i.git, sub)
	if err != nil {
		return nil, err
	}
	if commit == entry.Commit && pathExists(dst) {
		return nil, errors.New("ext: already up to date")
	}
	found, err := discover(sub)
	if err != nil {
		return nil, err
	}
	target := slices.IndexFunc(found, func(d discovered) bool { return d.manifest.Name == name })
	if target < 0 {
		return nil, fmt.Errorf("ext: no extension named %s in %s", name, src.URL)
	}
	next := found[target]
	p := plan(next.manifest, src.URL, commit)
	p.Replaces = entry.Commit
	if !i.confirm(p) {
		return nil, errors.New("ext: update refused")
	}
	if err := i.build(ctx, next.dir, next.manifest.Build); err != nil {
		return nil, err
	}
	old := dst + ".old"
	removeAll(old)
	if err := os.Rename(dst, old); err != nil {
		return nil, err
	}
	if err := os.Rename(next.dir, dst); err != nil {
		_ = os.Rename(old, dst)
		return nil, err
	}
	entry.Commit = commit
	entry.Installed = time.Now()
	entry.ManifestHash = next.hash
	lock[name] = entry
	if err := writeLock(i.opt.LockPath, lock); err != nil {
		removeAll(dst)
		_ = os.Rename(old, dst)
		return nil, err
	}
	removeAll(old)
	return next.manifest, nil
}

// Remove deletes an installed extension and its lock entry.
func (i *Installer) Remove(name string) error {
	i.installMu.Lock()
	defer i.installMu.Unlock()
	lock := i.lock()
	if _, ok := lock[name]; !ok {
		return fmt.Errorf("ext: %s is not installed", name)
	}
	dst, err := i.extDir(name)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// A linked extension removes only the link, never the source.
			err = os.Remove(dst)
		} else {
			err = os.RemoveAll(dst)
		}
		if err != nil {
			return err
		}
	}
	delete(lock, name)
	return writeLock(i.opt.LockPath, lock)
}

// List returns the lockfile entries with drift information. A damaged entry
// name, or a git that does not answer within the installer's timeout, counts as
// drift rather than hanging or failing the listing.
func (i *Installer) List() ([]Installed, error) {
	lock := i.lock()
	out := make([]Installed, 0, len(lock))
	for name, e := range lock {
		dir, err := i.extDir(name)
		inst := Installed{Name: name, Source: e.Source, Ref: e.Ref, Commit: e.Commit, Dir: dir}
		switch {
		case err != nil || !pathExists(dir):
			inst.Drifted = true
		case e.Commit != linkCommit:
			inst.Drifted = i.headDiffers(dir, e.Commit)
		}
		out = append(out, inst)
	}
	slices.SortFunc(out, func(a, b Installed) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// headDiffers reports whether the clone in dir is at a commit other than want,
// or its head cannot be read.
func (i *Installer) headDiffers(dir, want string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), i.opt.Timeout)
	defer cancel()
	head, err := gitHead(ctx, i.git, dir)
	return err != nil || head != want
}
