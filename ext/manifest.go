// Package ext runs third-party extensions as subprocesses speaking the Nova
// protocol. It loads manifests, supervises processes, registers the tools,
// commands, and hooks they declare, forwards their UI requests and session
// entries, and installs, updates, and removes them from git.
//
// The package also holds the small Go SDK in ext/sdk for writing an extension.
package ext

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
	path := filepath.Join(abs, "extension.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ext: read manifest: %w", err)
	}
	return parseManifest(abs, data)
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
	if _, err := parseSemver(m.Version); err != nil {
		return fmt.Errorf("ext: version: %w", err)
	}
	if err := validateConstraint(m.Nova); err != nil {
		return fmt.Errorf("ext: nova constraint: %w", err)
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("ext: description is required")
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
			return fmt.Errorf("ext: run is required unless the manifest declares tools or hooks with run")
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
		return fmt.Errorf("command is empty")
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
		return fmt.Errorf("ext: name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("ext: name is over 64 characters")
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

// hasCapability reports whether the manifest declares cap.
func (m *Manifest) hasCapability(cap string) bool {
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// semver is a parsed version, ignoring build metadata.
type semver struct {
	major, minor, patch int
	pre                 string
}

// parseSemver parses "1.2.3", "v1.2.3", and an optional "-prerelease".
func parseSemver(s string) (semver, error) {
	orig := s
	s = strings.TrimPrefix(s, "v")
	var v semver
	if pre := strings.IndexByte(s, '-'); pre >= 0 {
		v.pre = s[pre+1:]
		s = s[:pre]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid version %q", orig)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if p == "" || strings.ContainsFunc(p, func(r rune) bool { return r < '0' || r > '9' }) {
			return semver{}, fmt.Errorf("invalid version %q", orig)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, fmt.Errorf("invalid version %q", orig)
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, nil
}

// compare orders two versions, treating a prerelease as lower than a release.
func (v semver) compare(o semver) int {
	for _, pair := range [][2]int{{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.pre == o.pre:
		return 0
	case v.pre == "":
		return 1
	case o.pre == "":
		return -1
	case v.pre < o.pre:
		return -1
	default:
		return 1
	}
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
	ver, err := parseSemver(version)
	if err != nil {
		return false, err
	}
	for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if !strings.HasPrefix(constraint, op) {
			continue
		}
		want, err := parseConstraintVersion(strings.TrimSpace(constraint[len(op):]))
		if err != nil {
			return false, err
		}
		return compareOp(op, ver, want), nil
	}
	// A wildcard form such as "0.1.x" or "0.1.*".
	trimmed := strings.TrimSuffix(strings.TrimSuffix(constraint, "x"), "*")
	trimmed = strings.TrimSuffix(trimmed, ".")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false, fmt.Errorf("invalid constraint %q", constraint)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false, fmt.Errorf("invalid constraint %q", constraint)
		}
		nums[i] = n
	}
	if nums[0] != ver.major || nums[1] != ver.minor {
		return false, nil
	}
	if len(nums) == 3 && nums[2] != ver.patch {
		return false, nil
	}
	return true, nil
}

// parseConstraintVersion parses a version with one to three numeric parts,
// filling missing parts with zero, so "0.1" means "0.1.0".
func parseConstraintVersion(s string) (semver, error) {
	orig := s
	s = strings.TrimPrefix(s, "v")
	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return semver{}, fmt.Errorf("invalid version %q", orig)
	}
	v := semver{pre: pre}
	nums := []*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || p == "" {
			return semver{}, fmt.Errorf("invalid version %q", orig)
		}
		*nums[i] = n
	}
	return v, nil
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

// unknownManifestKeys reports unknown keys by dotted path, deterministically.
func unknownManifestKeys(raw map[string]any) []string {
	var out []string
	known := map[string]func(any){
		"name": func(any) {}, "version": func(any) {}, "description": func(any) {},
		"nova": func(any) {}, "run": func(any) {}, "build": func(any) {},
		"restart": func(v any) {
			checkKeys(v, map[string]bool{"max": true}, "restart", &out)
		},
		"capabilities": func(any) {},
		"tools": func(v any) {
			checkList(v, declToolKeys(), "tools", &out)
		},
		"hooks": func(v any) {
			checkList(v, hookSpecKeys(), "hooks", &out)
		},
		"flags": func(v any) {
			checkList(v, map[string]bool{"name": true, "description": true, "default": true}, "flags", &out)
		},
	}
	for _, key := range sortedKeys(raw) {
		fn, ok := known[key]
		if !ok {
			out = append(out, "unknown key: "+key)
			continue
		}
		fn(raw[key])
	}
	return out
}

// declToolKeys are the known keys of one declarative tool.
func declToolKeys() map[string]bool {
	return map[string]bool{
		"name": true, "description": true, "parameters": true, "exec": true,
		"read_only": true, "timeout_ms": true, "render": true,
	}
}

// hookSpecKeys are the known keys of one hook.
func hookSpecKeys() map[string]bool {
	return map[string]bool{"on": true, "match": true, "run": true}
}

// checkList walks a list of maps and records unknown keys below prefix.
func checkList(v any, known map[string]bool, prefix string, out *[]string) {
	list, ok := v.([]any)
	if !ok {
		return
	}
	for i, item := range list {
		checkKeys(item, known, fmt.Sprintf("%s[%d]", prefix, i), out)
	}
}

// checkKeys records keys of a map not in known.
func checkKeys(v any, known map[string]bool, prefix string, out *[]string) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for _, key := range sortedKeys(m) {
		if !known[key] {
			*out = append(*out, "unknown key: "+prefix+"."+key)
		}
	}
}

// sortedKeys returns a map's keys in order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
