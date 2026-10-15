package install

import (
	"context"
	"errors"
	"fmt"
	"github.com/AutumnsGrove/nova/ext"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
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

// splitSourceRef splits the trailing @ref, keeping a scp-like git@host prefix
// intact by only splitting when the remainder has no path or host separator.
func splitSourceRef(s string) (string, string) {
	i := strings.LastIndexByte(s, '@')
	if i <= 0 {
		return s, ""
	}
	ref := s[i+1:]
	if ref == "" || strings.ContainsAny(ref, "/:") {
		if strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://") {
			return s, ""
		}
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

// normaliseGitURL turns the accepted URL forms into a clone URL.
func normaliseGitURL(raw string) (string, error) {
	if strings.HasPrefix(raw, "git@") {
		return raw, nil
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "ssh://") {
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
		return i.installLocal(ctx, src)
	}
	return i.installGit(ctx, src)
}

// installGit clones into a temp directory, plans, confirms, builds, and moves.
func (i *Installer) installGit(ctx context.Context, src Source) (*ext.Manifest, error) {
	tmp, err := os.MkdirTemp(i.opt.Root, ".install-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	sub := filepath.Join(tmp, "repo")
	if err := gitClone(ctx, i.git, src.URL, src.Ref, sub); err != nil {
		return nil, err
	}
	commit, err := gitHead(ctx, i.git, sub)
	if err != nil {
		return nil, err
	}
	found, err := i.discover(sub)
	if err != nil {
		return nil, err
	}
	lock, _ := readLock(i.opt.LockPath)
	if err := i.checkCollisions(found, lock, src.URL); err != nil {
		return nil, err
	}
	plans := make([]Plan, len(found))
	for n, f := range found {
		plans[n] = i.plan(f, src.URL, commit, lock)
		if i.opt.Confirm == nil || !i.opt.Confirm(plans[n]) {
			return nil, errors.New("ext: install refused")
		}
	}
	for _, f := range found {
		if len(f.manifest.Build) > 0 {
			if err := i.build(ctx, f.dir, f.manifest.Build); err != nil {
				return nil, err
			}
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
			ManifestHash: ext.HashBytes(f.data),
		}
	}
	if err := writeLock(i.opt.LockPath, lock); err != nil {
		for _, dir := range moved {
			_ = os.RemoveAll(dir)
		}
		return nil, err
	}
	return found[0].manifest, nil
}

// installLocal links a local directory into Root.
func (i *Installer) installLocal(ctx context.Context, src Source) (*ext.Manifest, error) {
	m, err := ext.LoadManifest(src.Path)
	if err != nil {
		return nil, err
	}
	lock, _ := readLock(i.opt.LockPath)
	if e, ok := lock[m.Name]; ok {
		return nil, fmt.Errorf("ext: %s is already installed from %s", m.Name, e.Source)
	}
	plan := Plan{Name: m.Name, Source: src.Path, Run: m.Run, Build: m.Build, Capabilities: m.Capabilities}
	if i.opt.Confirm == nil || !i.opt.Confirm(plan) {
		return nil, errors.New("ext: install refused")
	}
	dst := filepath.Join(i.opt.Root, m.Name)
	if pathExists(dst) {
		return nil, fmt.Errorf("ext: %s is already installed", m.Name)
	}
	if err := os.Symlink(src.Path, dst); err != nil {
		return nil, err
	}
	lock[m.Name] = LockEntry{Name: m.Name, Source: src.Path, Ref: src.Ref, Commit: "link", Installed: time.Now(), ManifestHash: ext.ManifestHash(src.Path), Linked: true}
	if err := writeLock(i.opt.LockPath, lock); err != nil {
		_ = os.Remove(dst)
		return nil, err
	}
	_ = ctx
	return m, nil
}

// discovered is one validated extension found in a clone.
type discovered struct {
	dir      string
	manifest *ext.Manifest
	data     []byte
}

// discover loads and validates every extension in dir.
func (i *Installer) discover(dir string) ([]discovered, error) {
	dirs := findExtensions(dir)
	if len(dirs) == 0 {
		return nil, errors.New("ext: no extension.yaml found")
	}
	var out []discovered
	for _, d := range dirs {
		m, err := ext.LoadManifest(d)
		if err != nil {
			return nil, err
		}
		data, _ := os.ReadFile(filepath.Join(d, "extension.yaml"))
		out = append(out, discovered{dir: d, manifest: m, data: data})
	}
	return out, nil
}

// checkCollisions rejects a name already taken by a different source or a
// built-in.
func (i *Installer) checkCollisions(found []discovered, lock map[string]LockEntry, source string) error {
	for _, f := range found {
		name := f.manifest.Name
		if ext.Reserved(name) {
			return fmt.Errorf("ext: %s conflicts with a built-in or bundled extension", name)
		}
		if e, ok := lock[name]; ok {
			return fmt.Errorf("ext: %s is already installed from %s", name, e.Source)
		}
		if pathExists(filepath.Join(i.opt.Root, name)) {
			return fmt.Errorf("ext: %s already exists", name)
		}
	}
	return nil
}

// plan builds the trust prompt for one extension.
func (i *Installer) plan(f discovered, source, commit string, lock map[string]LockEntry) Plan {
	p := Plan{
		Name:         f.manifest.Name,
		Source:       source,
		Commit:       commit,
		Run:          f.manifest.Run,
		Build:        f.manifest.Build,
		Capabilities: f.manifest.Capabilities,
	}
	if e, ok := lock[f.manifest.Name]; ok {
		p.Replaces = e.Commit
	}
	return p
}

// build runs a manifest's build step with a timeout.
func (i *Installer) build(ctx context.Context, dir string, argv []string) error {
	bctx, cancel := context.WithTimeout(ctx, i.opt.Timeout)
	defer cancel()
	return i.run.Run(bctx, dir, argv)
}

// moveAll renames each extension into Root, rolling back on failure.
func (i *Installer) moveAll(found []discovered) ([]string, error) {
	var moved []string
	for _, f := range found {
		dst := filepath.Join(i.opt.Root, f.manifest.Name)
		if pathExists(dst) {
			old := dst + ".old"
			_ = os.RemoveAll(old)
			if err := os.Rename(dst, old); err != nil {
				return moved, err
			}
		}
		if err := os.Rename(f.dir, dst); err != nil {
			for _, dir := range moved {
				_ = os.RemoveAll(dir)
			}
			return moved, err
		}
		moved = append(moved, dst)
	}
	return moved, nil
}

// Update fetches the locked source again and replaces the extension.
func (i *Installer) Update(ctx context.Context, name string) (*ext.Manifest, error) {
	i.installMu.Lock()
	defer i.installMu.Unlock()
	lock, _ := readLock(i.opt.LockPath)
	entry, ok := lock[name]
	if !ok {
		return nil, fmt.Errorf("ext: %s is not installed", name)
	}
	src, err := ParseSource(entry.Source)
	if err != nil {
		src = Source{URL: entry.Source}
	}
	src.Ref = entry.Ref
	tmp, err := os.MkdirTemp(i.opt.Root, ".update-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if entry.Linked {
		return nil, errors.New("ext: a linked extension has nothing to update")
	}
	sub := filepath.Join(tmp, "repo")
	if err := gitClone(ctx, i.git, src.URL, src.Ref, sub); err != nil {
		return nil, err
	}
	commit, err := gitHead(ctx, i.git, sub)
	if err != nil {
		return nil, err
	}
	if commit == entry.Commit && pathExists(filepath.Join(i.opt.Root, name)) {
		return nil, errors.New("ext: already up to date")
	}
	found, err := i.discover(sub)
	if err != nil {
		return nil, err
	}
	var target *discovered
	for n := range found {
		if found[n].manifest.Name == name {
			target = &found[n]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("ext: no extension named %s in %s", name, src.URL)
	}
	plan := i.plan(*target, src.URL, commit, lock)
	plan.Replaces = entry.Commit
	if i.opt.Confirm == nil || !i.opt.Confirm(plan) {
		return nil, errors.New("ext: update refused")
	}
	if len(target.manifest.Build) > 0 {
		if err := i.build(ctx, target.dir, target.manifest.Build); err != nil {
			return nil, err
		}
	}
	dst := filepath.Join(i.opt.Root, name)
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil {
		return nil, err
	}
	if err := os.Rename(target.dir, dst); err != nil {
		_ = os.Rename(old, dst)
		return nil, err
	}
	entry.Commit = commit
	entry.Installed = time.Now()
	entry.ManifestHash = ext.HashBytes(target.data)
	lock[name] = entry
	if err := writeLock(i.opt.LockPath, lock); err != nil {
		_ = os.RemoveAll(dst)
		_ = os.Rename(old, dst)
		return nil, err
	}
	_ = os.RemoveAll(old)
	return target.manifest, nil
}

// Remove deletes an installed extension and its lock entry.
func (i *Installer) Remove(name string) error {
	i.installMu.Lock()
	defer i.installMu.Unlock()
	lock, _ := readLock(i.opt.LockPath)
	if _, ok := lock[name]; !ok {
		return fmt.Errorf("ext: %s is not installed", name)
	}
	dst := filepath.Join(i.opt.Root, name)
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(dst); err != nil {
				return err
			}
		} else if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	delete(lock, name)
	return writeLock(i.opt.LockPath, lock)
}

// List returns the lockfile entries with drift information.
func (i *Installer) List() ([]Installed, error) {
	lock, _ := readLock(i.opt.LockPath)
	out := make([]Installed, 0, len(lock))
	for name, e := range lock {
		inst := Installed{Name: name, Source: e.Source, Ref: e.Ref, Commit: e.Commit, Dir: filepath.Join(i.opt.Root, name)}
		if !pathExists(inst.Dir) {
			inst.Drifted = true
		} else if e.Commit != "link" {
			if head, err := gitHead(context.Background(), i.git, inst.Dir); err != nil || head != e.Commit {
				inst.Drifted = true
			}
		}
		out = append(out, inst)
	}
	sortInstalled(out)
	return out, nil
}

// sortInstalled orders entries by name.
func sortInstalled(in []Installed) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Name < in[j-1].Name; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
