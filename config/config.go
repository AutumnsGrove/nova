// Package config loads Nova's settings and the resources that shape a session:
// project instructions, skills, and prompt templates. Everything malformed is a
// warning, never a crash, and the merge order is defaults, user, project,
// environment, flags.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/AutumnsGrove/nova/permission"
)

// Config is the merged, normalised settings for one session.
type Config struct {
	Model        string            `yaml:"model"`
	Thinking     string            `yaml:"thinking"` // off | low | medium | high
	ContextLimit int               `yaml:"context_limit"`
	MaxTurns     int               `yaml:"max_turns"`
	Provider     ProviderConfig    `yaml:"provider"`
	Permissions  permission.Policy `yaml:"permissions"`
	Tools        ToolsConfig       `yaml:"tools"`
	Extensions   ExtensionsConfig  `yaml:"extensions"`
	Hooks        []HookConfig      `yaml:"hooks"`
	Compaction   CompactionConfig  `yaml:"compaction"`
	Retry        RetryConfig       `yaml:"retry"`
	Theme        string            `yaml:"theme"`
	Keybindings  map[string]string `yaml:"keybindings"`
	SystemPrompt string            `yaml:"system_prompt"` // text appended to the base prompt
}

// ProviderConfig holds per-provider settings.
type ProviderConfig struct {
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
}

// OpenRouterConfig is the OpenRouter preset's settings. Routing is passed
// through to provider/openrouter's typed helper.
type OpenRouterConfig struct {
	Routing map[string]any `yaml:"routing"`
	BaseURL string         `yaml:"base_url"`
}

// ToolsConfig selects tools and configures the bash tool.
type ToolsConfig struct {
	Active []string   `yaml:"active"` // empty means the default set
	Bash   BashConfig `yaml:"bash"`
}

// BashConfig bounds one bash call.
type BashConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxOutputBytes int `yaml:"max_output_bytes"`
}

// ExtensionsConfig bounds the extension host.
type ExtensionsConfig struct {
	Disable           []string `yaml:"disable"`
	Dirs              []string `yaml:"dirs"`
	ScrubEnv          []string `yaml:"scrub_env"`            // extra variables hidden from extension processes
	PassEnv           []string `yaml:"pass_env"`             // variables explicitly passed through
	CompleteMaxPerRun int      `yaml:"complete_max_per_run"` // default 20
}

// HookConfig is one declarative hook.
type HookConfig struct {
	On    string            `yaml:"on"`
	Match map[string]string `yaml:"match"`
	Run   []string          `yaml:"run"`
}

// CompactionConfig controls summarise-and-splice.
type CompactionConfig struct {
	Enabled    *bool `yaml:"enabled"`
	Reserve    int   `yaml:"reserve"`
	KeepRecent int   `yaml:"keep_recent"`
}

// RetryConfig controls provider retry backoff.
type RetryConfig struct {
	Max    int `yaml:"max"`
	BaseMS int `yaml:"base_ms"`
	CapMS  int `yaml:"cap_ms"`
}

// Warning is a non-fatal problem found while loading: a missing trust grant, an
// unknown key, a file that could not be parsed. Source names the file, layer,
// or resource it came from.
type Warning struct {
	Source  string
	Message string
}

// LoadOptions controls Load. Zero values are filled from the environment.
type LoadOptions struct {
	CWD      string
	Home     string                       // for the user config; default from os
	Env      func(string) string          // default os.Getenv
	Flags    map[string]any               // dotted keys, e.g. "model": "x", "bash.timeout_seconds": 30
	Trusted  func(dir string) bool        // nil means "not trusted"
	ReadFile func(string) ([]byte, error) // default os.ReadFile (tests inject)
}

// Defaults returns the settings before any layer is applied.
func Defaults() Config {
	enabled := true
	return Config{
		Compaction: CompactionConfig{Enabled: &enabled},
		Extensions: ExtensionsConfig{CompleteMaxPerRun: 20},
		Retry:      RetryConfig{Max: 3, BaseMS: 500, CapMS: 30000},
	}
}

// Load reads every layer in precedence order and returns the merged config plus
// warnings. An error is returned only for an invalid call, never for a bad file.
func Load(opt LoadOptions) (*Config, []Warning, error) {
	if opt.CWD == "" {
		return nil, nil, fmt.Errorf("config: LoadOptions.CWD is required")
	}
	cwd := filepath.Clean(opt.CWD)
	home := opt.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, fmt.Errorf("config: home directory: %w", err)
		}
		home = h
	}
	home = filepath.Clean(home)
	env := opt.Env
	if env == nil {
		env = os.Getenv
	}
	readFile := opt.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	var warnings []Warning
	merged := map[string]any{}

	if m, ok := readLayer(readFile, UserConfigPath(home), &warnings); ok {
		mergeMaps(merged, m)
	}

	root, found := findProjectRoot(cwd)
	if found {
		path := ProjectConfigPath(root)
		if opt.Trusted != nil && opt.Trusted(root) {
			if m, ok := readLayer(readFile, path, &warnings); ok {
				mergeMaps(merged, m)
			}
		} else if fileExists(readFile, path) {
			warnings = append(warnings, Warning{Source: path, Message: "project is not trusted; skipping project config"})
		}
	}

	mergeMaps(merged, envMap(env, &warnings))
	mergeMaps(merged, flagsMap(opt.Flags))

	warnings = append(warnings, unknownKeys(merged, "config")...)

	cfg := Defaults()
	decodeInto(&cfg, merged, &warnings)
	normalise(&cfg, home, &warnings)
	return &cfg, warnings, nil
}

// readLayer reads the first existing file for base and decodes it into a map.
// A missing file is not a problem; an unreadable or invalid one is a warning
// and the layer is skipped.
func readLayer(readFile func(string) ([]byte, error), base string, warnings *[]Warning) (map[string]any, bool) {
	data, path, err := readFirst(readFile, base)
	if err != nil {
		*warnings = append(*warnings, Warning{Source: path, Message: "cannot read config: " + err.Error()})
		return nil, false
	}
	if path == "" {
		return nil, false
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		*warnings = append(*warnings, Warning{Source: path, Message: err.Error()})
		return nil, false
	}
	return m, true
}

// readFirst returns the bytes and path of the first of <base>.yaml, <base>.json
// that exists. A file that exists but cannot be read is an error, not a miss.
func readFirst(readFile func(string) ([]byte, error), base string) ([]byte, string, error) {
	for _, ext := range []string{".yaml", ".json"} {
		path := base + ext
		data, err := readFile(path)
		if err == nil {
			return data, path, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, path, err
		}
	}
	return nil, "", nil
}

// fileExists reports whether either config extension for base can be read.
func fileExists(readFile func(string) ([]byte, error), base string) bool {
	for _, ext := range []string{".yaml", ".json"} {
		if _, err := readFile(base + ext); err == nil {
			return true
		}
	}
	return false
}

// mergeMaps deep-merges maps and replaces every other value, so lists and
// scalars from a higher layer win whole. Nested maps are copied, so a later
// merge into the destination cannot mutate the source layers.
func mergeMaps(dst, src map[string]any) {
	for key, value := range src {
		if sm, ok := value.(map[string]any); ok {
			if dm, ok := dst[key].(map[string]any); ok {
				mergeMaps(dm, sm)
				continue
			}
			fresh := map[string]any{}
			mergeMaps(fresh, sm)
			dst[key] = fresh
			continue
		}
		dst[key] = value
	}
}

// decodeInto overlays the merged map onto cfg. Fields absent from the map keep
// their earlier value; a type error is a warning and leaves the default in place.
func decodeInto(cfg *Config, m map[string]any, warnings *[]Warning) {
	if len(m) == 0 {
		return
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		*warnings = append(*warnings, Warning{Source: "config", Message: "cannot re-encode config: " + err.Error()})
		return
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(cfg); err != nil {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			for _, msg := range te.Errors {
				*warnings = append(*warnings, Warning{Source: "config", Message: msg})
			}
			return
		}
		*warnings = append(*warnings, Warning{Source: "config", Message: err.Error()})
	}
}

// envMap builds the environment override layer from the documented NOVA_*
// variables. An empty or whitespace-only value is ignored; one that will not
// parse is a warning.
func envMap(env func(string) string, warnings *[]Warning) map[string]any {
	m := map[string]any{}
	setString := func(key, name string) {
		if v, ok := envValue(env, name); ok {
			m[key] = v
		}
	}
	setInt := func(key, name string) {
		v, ok := envValue(env, name)
		if !ok {
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			*warnings = append(*warnings, Warning{Source: "env", Message: name + ": " + err.Error()})
			return
		}
		m[key] = n
	}
	setString("model", "NOVA_MODEL")
	setString("thinking", "NOVA_THINKING")
	setString("theme", "NOVA_THEME")
	setInt("context_limit", "NOVA_CONTEXT_LIMIT")
	setInt("max_turns", "NOVA_MAX_TURNS")
	return m
}

// envValue returns a variable's value unless it is missing or blank.
func envValue(env func(string) string, name string) (string, bool) {
	v := env(name)
	if strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

// flagsMap turns dotted flag keys into a nested map. The leading "bash." alias
// maps onto tools.bash, matching the documented flag names. Keys are sorted so
// warnings are stable.
func flagsMap(flags map[string]any) map[string]any {
	m := map[string]any{}
	for _, key := range sortedFlagKeys(flags) {
		parts := strings.Split(key, ".")
		if parts[0] == "bash" {
			parts = append([]string{"tools"}, parts...)
		}
		setNested(m, parts, flags[key])
	}
	return m
}

// sortedFlagKeys returns the flag keys in order.
func sortedFlagKeys(flags map[string]any) []string {
	keys := make([]string, 0, len(flags))
	for key := range flags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// setNested writes value at parts, creating intermediate maps.
func setNested(m map[string]any, parts []string, value any) {
	for len(parts) > 1 {
		next, ok := m[parts[0]].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[parts[0]] = next
		}
		m = next
		parts = parts[1:]
	}
	m[parts[0]] = value
}

// normalise applies cross-field rules after every layer is merged.
func normalise(cfg *Config, home string, warnings *[]Warning) {
	for i, dir := range cfg.Extensions.Dirs {
		cfg.Extensions.Dirs[i] = expandHome(dir, home)
	}
	switch cfg.Thinking {
	case "", "off", "low", "medium", "high":
	default:
		*warnings = append(*warnings, Warning{Source: "config", Message: fmt.Sprintf("invalid thinking %q; clearing", cfg.Thinking)})
		cfg.Thinking = ""
	}
	nonNegative(&cfg.ContextLimit, "context_limit", warnings)
	nonNegative(&cfg.MaxTurns, "max_turns", warnings)
	nonNegative(&cfg.Tools.Bash.TimeoutSeconds, "tools.bash.timeout_seconds", warnings)
	nonNegative(&cfg.Tools.Bash.MaxOutputBytes, "tools.bash.max_output_bytes", warnings)
	nonNegative(&cfg.Extensions.CompleteMaxPerRun, "extensions.complete_max_per_run", warnings)
	nonNegative(&cfg.Compaction.Reserve, "compaction.reserve", warnings)
	nonNegative(&cfg.Compaction.KeepRecent, "compaction.keep_recent", warnings)
	nonNegative(&cfg.Retry.Max, "retry.max", warnings)
	nonNegative(&cfg.Retry.BaseMS, "retry.base_ms", warnings)
	nonNegative(&cfg.Retry.CapMS, "retry.cap_ms", warnings)
	if err := cfg.Permissions.Validate(); err != nil {
		*warnings = append(*warnings, Warning{Source: "config", Message: "invalid permissions: " + err.Error()})
		cfg.Permissions = Defaults().Permissions
	}
}

// nonNegative clamps a negative number to zero with a warning.
func nonNegative(v *int, name string, warnings *[]Warning) {
	if *v < 0 {
		*warnings = append(*warnings, Warning{Source: "config", Message: fmt.Sprintf("negative %s; using 0", name)})
		*v = 0
	}
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// findProjectRoot walks up from cwd to the nearest directory holding .nova or
// .git. It returns the root and whether one was found.
func findProjectRoot(cwd string) (string, bool) {
	dir := cwd
	for {
		if exists(filepath.Join(dir, ".nova")) || exists(filepath.Join(dir, ".git")) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ProjectRoot returns the nearest ancestor of cwd holding .nova or .git. It
// is exported so the trust prompt can name the folder it would trust.
func ProjectRoot(cwd string) (string, bool) { return findProjectRoot(cwd) }

// exists reports whether a path can be stat'd.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// now is the clock BuildSystemPrompt uses when PromptInput.Date is zero.
func now() time.Time { return time.Now() }
