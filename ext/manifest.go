// Package ext runs third-party extensions as subprocesses speaking the Nova
// protocol. It loads manifests, supervises processes, registers the tools,
// commands, and hooks they declare, forwards their UI requests and session
// entries, and installs, updates, and removes them from git.
//
// The package also holds the small Go SDK in ext/sdk for writing an extension.
package ext

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxManifestBytes is the documented 256 KiB manifest cap.
const maxManifestBytes = 256 << 10

// maxDescription bounds a manifest description.
const maxDescription = 500

// defaultToolTimeoutMS is the declarative tool timeout when none is set.
const defaultToolTimeoutMS = 30_000

// maxToolTimeoutMS caps a declarative tool timeout.
const maxToolTimeoutMS = 600_000

// nameRe is the extension-name shape: lowercase words joined by hyphens.
var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// toolNameRe is a declarative tool name: letters, digits, underscore, hyphen.
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// builtinTools are the core tool names an extension may not take.
var builtinTools = map[string]bool{
	"read": true, "write": true, "edit": true, "list": true,
	"glob": true, "grep": true, "bash": true,
}

// bundledExtensions are the in-tree extension names an extension may not take.
var bundledExtensions = map[string]bool{
	"tasks": true, "ask": true, "plan": true, "subagent": true, "webfetch": true,
}

// Reserved reports whether name belongs to a core tool or a bundled extension,
// which an installed extension may not take.
func Reserved(name string) bool { return builtinTools[name] || bundledExtensions[name] }

// hookNames are the loop's hook points (ARCHITECTURE.md section 4).
var hookNames = map[string]bool{
	"input": true, "before_agent_start": true, "transform_context": true,
	"before_provider_request": true, "provider_stream_event": true,
	"message_end": true, "tool_call": true, "tool_result": true,
	"turn_end": true, "agent_end": true,
	"session_start": true, "session_shutdown": true, "compaction_before": true,
}

// uiMethods are the declarative UI methods an extension may request.
var uiMethods = map[string]bool{
	"notify": true, "confirm": true, "select": true, "input": true,
	"form": true, "status": true, "widget": true,
}

// simpleCapabilities are capabilities that name a whole feature.
var simpleCapabilities = map[string]bool{
	"tools": true, "commands": true, "session:append_entry": true,
	"session:read": true, "model:complete": true, "providers": true,
	"mcp": true, "network": true, "fs": true, "exec": true,
}

// Manifest is a parsed and validated extension.yaml.
type Manifest struct {
	Name         string      `yaml:"name"`
	Version      string      `yaml:"version"`
	Description  string      `yaml:"description"`
	Nova         string      `yaml:"nova"`
	Run          []string    `yaml:"run"`
	Build        []string    `yaml:"build"`
	Restart      RestartSpec `yaml:"restart"`
	Capabilities []string    `yaml:"capabilities"`
	Tools        []DeclTool  `yaml:"tools"`
	Hooks        []HookSpec  `yaml:"hooks"`
	Flags        []FlagSpec  `yaml:"flags"`

	// Dir is the absolute extension directory, set by the loader.
	Dir string `yaml:"-"`
	// Warnings holds non-fatal validation problems such as unknown keys.
	Warnings []string `yaml:"-"`
}

// RestartSpec bounds automatic restarts.
type RestartSpec struct {
	Max int `yaml:"max"`
}

// DeclTool is a declarative exec tool.
type DeclTool struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	Exec        []string       `yaml:"exec"`
	ReadOnly    bool           `yaml:"read_only"`
	TimeoutMS   int            `yaml:"timeout_ms"`
	Render      string         `yaml:"render"`
}

// HookSpec is a command hook.
type HookSpec struct {
	On    string            `yaml:"on"`
	Match map[string]string `yaml:"match"`
	Run   []string          `yaml:"run"`
}

// FlagSpec is a CLI flag an extension reads through init.
type FlagSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Default     string `yaml:"default"`
}

// LoadManifest reads and validates dir/extension.yaml. The returned manifest
// has an absolute Dir. Non-fatal problems are in Warnings.
func LoadManifest(dir string) (*Manifest, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("ext: manifest dir: %w", err)
	}
	data, err := readManifestFile(filepath.Join(abs, "extension.yaml"))
	if err != nil {
		return nil, fmt.Errorf("ext: read manifest: %w", err)
	}
	return parseManifest(abs, data)
}

// readManifestFile reads path, which must be a regular file and not a symlink,
// up to one byte over the manifest cap. A repository chooses what
// extension.yaml is, so it may be a named pipe (opening it would block), a
// device (reading it would never end), or a link to a file elsewhere.
func readManifestFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
}

// parseManifest validates manifest bytes read from dir, so a fuzz target can
// feed arbitrary YAML without touching the filesystem.
func parseManifest(dir string, data []byte) (*Manifest, error) {
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("ext: manifest is %d bytes, over the %d byte limit", len(data), maxManifestBytes)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("ext: manifest: %w", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err == nil {
		m.Warnings = append(m.Warnings, unknownManifestKeys(raw)...)
	}
	m.Dir = dir
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate reports the first manifest problem. It also fills in defaults for
// declarative tool timeouts.
func (m *Manifest) Validate() error {
	if err := validateName(m.Name); err != nil {
		return err
	}
	if _, err := parseVersion(m.Version, 3); err != nil {
		return fmt.Errorf("ext: version: %w", err)
	}
	if err := validateConstraint(m.Nova); err != nil {
		return fmt.Errorf("ext: nova constraint: %w", err)
	}
	if strings.TrimSpace(m.Description) == "" {
		return errors.New("ext: description is required")
	}
	if len([]rune(m.Description)) > maxDescription {
		return fmt.Errorf("ext: description is over %d characters", maxDescription)
	}
	if err := validateCapabilities(m.Capabilities); err != nil {
		return err
	}
	if err := m.validateRun(); err != nil {
		return err
	}
	if err := m.validateTools(); err != nil {
		return err
	}
	return m.validateHooks()
}

// validateRun enforces that the manifest has a way to start.
func (m *Manifest) validateRun() error {
	if len(m.Run) == 0 {
		if !m.hasDeclarative() {
			return errors.New("ext: run is required unless the manifest declares tools or hooks with run")
		}
		return nil
	}
	if err := m.checkCommand(m.Run[0]); err != nil {
		return fmt.Errorf("ext: run[0]: %w", err)
	}
	return nil
}

// hasDeclarative reports whether the manifest can run without a process.
func (m *Manifest) hasDeclarative() bool {
	for _, t := range m.Tools {
		if len(t.Exec) > 0 {
			return true
		}
	}
	for _, h := range m.Hooks {
		if len(h.Run) > 0 {
			return true
		}
	}
	return false
}

// validateTools checks declarative tool names, schemas, commands, and timeouts.
func (m *Manifest) validateTools() error {
	seen := map[string]bool{}
	for i := range m.Tools {
		t := &m.Tools[i]
		if !toolNameRe.MatchString(t.Name) {
			return fmt.Errorf("ext: tool %d: invalid name %q", i, t.Name)
		}
		if builtinTools[t.Name] || bundledExtensions[t.Name] {
			return fmt.Errorf("ext: tool name %q conflicts with a built-in", t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("ext: duplicate tool name %q", t.Name)
		}
		seen[t.Name] = true
		if t.Parameters != nil && t.Parameters["type"] != "object" {
			return fmt.Errorf("ext: tool %q: parameters must be an object schema", t.Name)
		}
		if len(t.Exec) > 0 {
			if placeholderRe.MatchString(t.Exec[0]) {
				return fmt.Errorf("ext: tool %q exec[0] must not contain a placeholder: the command is fixed at load", t.Name)
			}
			if err := m.checkCommand(t.Exec[0]); err != nil {
				return fmt.Errorf("ext: tool %q exec[0]: %w", t.Name, err)
			}
		}
		switch {
		case t.TimeoutMS <= 0:
			t.TimeoutMS = defaultToolTimeoutMS
		case t.TimeoutMS > maxToolTimeoutMS:
			t.TimeoutMS = maxToolTimeoutMS
		}
	}
	return nil
}

// validateHooks checks command hooks.
func (m *Manifest) validateHooks() error {
	for i, h := range m.Hooks {
		if !hookNames[h.On] {
			return fmt.Errorf("ext: hook %d: unknown hook %q", i, h.On)
		}
		if len(h.Run) == 0 {
			return fmt.Errorf("ext: hook %q: run is required", h.On)
		}
		if err := m.checkCommand(h.Run[0]); err != nil {
			return fmt.Errorf("ext: hook %q run[0]: %w", h.On, err)
		}
	}
	return nil
}

// checkCommand enforces the run[0] rule: an absolute path, a bare command on
// PATH, or a relative path that stays inside the extension directory.
func (m *Manifest) checkCommand(cmd string) error {
	if cmd == "" {
		return errors.New("command is empty")
	}
	if filepath.IsAbs(cmd) {
		return nil
	}
	if !strings.ContainsRune(cmd, os.PathSeparator) && !strings.ContainsRune(cmd, '/') {
		if _, err := exec.LookPath(cmd); err != nil {
			return fmt.Errorf("%q is not on PATH", cmd)
		}
		return nil
	}
	joined := filepath.Clean(filepath.Join(m.Dir, cmd))
	if !inside(m.Dir, joined) {
		return fmt.Errorf("%q escapes the extension directory", cmd)
	}
	return nil
}

// inside reports whether path is dir or below it.
func inside(dir, path string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// validateName enforces the extension-name rule.
func validateName(name string) error {
	if name == "" {
		return errors.New("ext: name is required")
	}
	if len(name) > 64 {
		return errors.New("ext: name is over 64 characters")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("ext: invalid name %q", name)
	}
	return nil
}

// validateCapabilities rejects an unknown capability, naming it.
func validateCapabilities(caps []string) error {
	for _, c := range caps {
		if simpleCapabilities[c] {
			continue
		}
		switch {
		case strings.HasPrefix(c, "hooks:"):
			if !hookNames[strings.TrimPrefix(c, "hooks:")] {
				return fmt.Errorf("ext: unknown capability %q", c)
			}
		case strings.HasPrefix(c, "ui:"):
			if !uiMethods[strings.TrimPrefix(c, "ui:")] {
				return fmt.Errorf("ext: unknown capability %q", c)
			}
		default:
			return fmt.Errorf("ext: unknown capability %q", c)
		}
	}
	return nil
}

// hasCapability reports whether the manifest declares capability.
func (m *Manifest) hasCapability(capability string) bool {
	return slices.Contains(m.Capabilities, capability)
}

// semver is a parsed version.
type semver struct {
	major, minor, patch int
	pre                 string
}

// parseVersion parses "1.2.3", "v1.2.3", and an optional "-prerelease". It
// needs at least minParts numeric parts; a missing one counts as zero, so a
// constraint may say "0.1".
func parseVersion(s string, minParts int) (semver, error) {
	orig := s
	s = strings.TrimPrefix(s, "v")
	var v semver
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < minParts || len(parts) > 3 {
		return semver{}, fmt.Errorf("invalid version %q", orig)
	}
	nums := []*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		n, err := parseNumber(p)
		if err != nil {
			return semver{}, fmt.Errorf("invalid version %q", orig)
		}
		*nums[i] = n
	}
	return v, nil
}

// parseNumber parses a non-negative decimal number, refusing the signs and
// spaces strconv.Atoi would accept.
func parseNumber(s string) (int, error) {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, errors.New("not a number")
	}
	return strconv.Atoi(s)
}

// compare orders two versions, treating a prerelease as lower than a release.
func (v semver) compare(o semver) int {
	if c := cmp.Or(cmp.Compare(v.major, o.major), cmp.Compare(v.minor, o.minor), cmp.Compare(v.patch, o.patch)); c != 0 {
		return c
	}
	switch {
	case v.pre == o.pre:
		return 0
	case v.pre == "":
		return 1
	case o.pre == "":
		return -1
	}
	return strings.Compare(v.pre, o.pre)
}

// validateConstraint parses the nova constraint, rejecting an unknown form.
func validateConstraint(c string) error {
	_, err := matchConstraint(c, "0.0.0")
	return err
}

// matchConstraint reports whether version satisfies constraint. Supported
// forms are empty (any), ">=x.y.z", ">x.y.z", "<=x.y.z", "<x.y.z", "=x.y.z",
// "^x.y.z", "~x.y.z", and "x.y.x" or "x.y.*" wildcards.
func matchConstraint(constraint, version string) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true, nil
	}
	ver, err := parseVersion(version, 3)
	if err != nil {
		return false, err
	}
	for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if rest, ok := strings.CutPrefix(constraint, op); ok {
			want, err := parseVersion(strings.TrimSpace(rest), 1)
			if err != nil {
				return false, err
			}
			return compareOp(op, ver, want), nil
		}
	}
	return matchWildcard(constraint, ver)
}

// matchWildcard matches a version against "0.1.x", "0.1.*" or a bare "0.1.2".
func matchWildcard(constraint string, ver semver) (bool, error) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(constraint, "x"), "*"), ".")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false, fmt.Errorf("invalid constraint %q", constraint)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := parseNumber(p)
		if err != nil {
			return false, fmt.Errorf("invalid constraint %q", constraint)
		}
		nums[i] = n
	}
	if nums[0] != ver.major || nums[1] != ver.minor {
		return false, nil
	}
	return len(nums) < 3 || nums[2] == ver.patch, nil
}

// compareOp applies a comparison operator.
func compareOp(op string, v, want semver) bool {
	c := v.compare(want)
	switch op {
	case ">=":
		return c >= 0
	case ">":
		return c > 0
	case "<=":
		return c <= 0
	case "<":
		return c < 0
	case "=":
		return c == 0
	case "^":
		if want.major > 0 {
			return c >= 0 && v.major == want.major
		}
		return c >= 0 && v.major == 0 && v.minor == want.minor
	case "~":
		return c >= 0 && v.major == want.major && v.minor == want.minor
	}
	return false
}

// manifestKeys are the known manifest keys. A key with a list of names holds a
// map or a list of maps whose own keys are checked against that list.
var manifestKeys = map[string][]string{
	"name": nil, "version": nil, "description": nil, "nova": nil,
	"run": nil, "build": nil, "capabilities": nil,
	"restart": {"max"},
	"tools":   {"name", "description", "parameters", "exec", "read_only", "timeout_ms", "render"},
	"hooks":   {"on", "match", "run"},
	"flags":   {"name", "description", "default"},
}

// unknownManifestKeys reports unknown keys by dotted path, deterministically.
func unknownManifestKeys(raw map[string]any) []string {
	var out []string
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		known, ok := manifestKeys[key]
		if !ok {
			out = append(out, "unknown key: "+key)
			continue
		}
		if known == nil {
			continue
		}
		switch v := raw[key].(type) {
		case map[string]any:
			out = appendUnknown(out, v, known, key)
		case []any:
			for i, item := range v {
				if m, ok := item.(map[string]any); ok {
					out = appendUnknown(out, m, known, fmt.Sprintf("%s[%d]", key, i))
				}
			}
		}
	}
	return out
}

// appendUnknown records the keys of m that are not in known, below prefix.
func appendUnknown(out []string, m map[string]any, known []string, prefix string) []string {
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(known, key) {
			out = append(out, "unknown key: "+prefix+"."+key)
		}
	}
	return out
}
