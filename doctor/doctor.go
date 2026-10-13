// Package doctor runs the installation checks behind `nova doctor`. Every
// check is a value, never a crash: a check that cannot run is a warn or a fail
// with a one-line fix. Doctor never modifies anything and never prints a secret.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
)

// Status is how a check ended.
type Status int

const (
	OK Status = iota
	Warn
	Fail
)

// String renders the status as the lowercase word doctor prints.
func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// MarshalJSON keeps the status readable in `--json` output.
func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON reads the lowercase status back, so doctor's JSON round-trips.
func (s *Status) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	switch text {
	case "ok":
		*s = OK
	case "warn":
		*s = Warn
	case "fail":
		*s = Fail
	default:
		return fmt.Errorf("doctor: unknown status %q", text)
	}
	return nil
}

// Check is one line of the doctor table.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// ExtensionStatus is one installed extension's health.
type ExtensionStatus struct {
	Name   string
	OK     bool
	Detail string
}

// HostStatus reports the installed extensions and whether each is healthy. It
// is a function so `cmd/nova` can wire the real host without doctor importing
// the extension host.
type HostStatus func() []ExtensionStatus

// Options controls a doctor run. Zero values are filled with safe defaults.
type Options struct {
	CWD, Home string
	Env       func(string) string
	Offline   bool
	HTTP      *http.Client
	Host      HostStatus
	Out       io.Writer

	// Size reports the terminal size, when known.
	Size func() (width, height int, ok bool)
	// ConfigKey is a credential supplied by the caller (a flag or config),
	// checked before the environment and the store, exactly as auth.Resolve does.
	ConfigKey string
	// Version and Flavour describe the running build.
	Version string
	Flavour string
	// BaseURL is the OpenRouter API base for the network check.
	BaseURL string
	// ReadFile is injected by tests; nil means os.ReadFile.
	ReadFile func(string) ([]byte, error)

	// Timeout bounds the whole run, default 30s.
	Timeout time.Duration
	// NetworkTimeout bounds the network check, default 5s.
	NetworkTimeout time.Duration
}

// Run executes every check and returns them in the order they print. The whole
// run is bounded by Options.Timeout; a check that stalls reports a warning.
func Run(ctx context.Context, opt Options) []Check {
	opt = withDefaults(opt)
	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}

	key, source := resolveKey(opt)
	checks := []Check{
		versionCheck(opt),
		apiKeyCheck(key, source),
		networkCheck(ctx, opt, key),
		binaryCheck("git", Warn, opt, "needed for `nova ext install`"),
		binaryCheck("bash", Fail, opt, "the bash tool needs it"),
		terminalCheck(opt),
	}
	cfg, cfgCheck := configCheck(opt)
	checks = append(checks, cfgCheck, dirsCheck(opt), trustCheck(opt), extensionsCheck(opt))
	checks = append(checks, projectCheck(opt, cfg))
	return checks
}

// withDefaults fills the zero options.
func withDefaults(opt Options) Options {
	if opt.Env == nil {
		opt.Env = os.Getenv
	}
	if opt.ReadFile == nil {
		opt.ReadFile = os.ReadFile
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	if opt.NetworkTimeout <= 0 {
		opt.NetworkTimeout = 5 * time.Second
	}
	if opt.BaseURL == "" {
		opt.BaseURL = "https://openrouter.ai/api/v1"
	}
	if opt.Version == "" {
		opt.Version = "dev"
	}
	if opt.Flavour == "" {
		opt.Flavour = "full"
	}
	return opt
}

// resolveKey finds a credential and its source name without ever exposing the
// value to the caller's output.
func resolveKey(opt Options) (key, source string) {
	store := auth.NewFileStore(filepath.Join(config.UserConfigDir(opt.Home), "auth.json"))
	key, source, err := auth.Resolve(opt.ConfigKey, opt.Env, store)
	if err != nil {
		return "", ""
	}
	return key, source
}

func versionCheck(opt Options) Check {
	return Check{Name: "version", Status: OK, Detail: fmt.Sprintf("nova %s (%s)", opt.Version, opt.Flavour)}
}

func apiKeyCheck(key, source string) Check {
	if key == "" {
		return Check{
			Name:   "api key",
			Status: Fail,
			Detail: "no API key found",
			Fix:    "run `nova login`, or set OPENROUTER_API_KEY",
		}
	}
	return Check{Name: "api key", Status: OK, Detail: "found in " + source}
}

func networkCheck(ctx context.Context, opt Options, key string) Check {
	if opt.Offline {
		return Check{Name: "network", Status: OK, Detail: "skipped (--offline)"}
	}
	client := opt.HTTP
	if client == nil {
		client = &http.Client{}
	}
	cctx, cancel := context.WithTimeout(ctx, opt.NetworkTimeout)
	defer cancel()
	url := strings.TrimRight(opt.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return Check{Name: "network", Status: Warn, Detail: err.Error(), Fix: "check the API base URL"}
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		if cctx.Err() != nil {
			return Check{Name: "network", Status: Warn, Detail: fmt.Sprintf("no response within %s", opt.NetworkTimeout), Fix: "check the network or use --offline"}
		}
		return Check{Name: "network", Status: Warn, Detail: err.Error(), Fix: "check the network or use --offline"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Check{Name: "network", Status: Warn, Detail: fmt.Sprintf("OpenRouter returned %s", resp.Status), Fix: "check the network or use --offline"}
	}
	return Check{Name: "network", Status: OK, Detail: "OpenRouter reachable"}
}

func binaryCheck(name string, missing Status, opt Options, why string) Check {
	if path, ok := lookPath(opt.Env("PATH"), name); ok {
		return Check{Name: name, Status: OK, Detail: path}
	}
	return Check{
		Name:   name,
		Status: missing,
		Detail: "not found on PATH",
		Fix:    "install " + name + "; " + why,
	}
}

// lookPath searches a PATH value for name, so tests can empty PATH without
// touching the process environment.
func lookPath(pathEnv, name string) (string, bool) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode().Perm()&0o111 != 0 {
			return full, true
		}
	}
	return "", false
}

func terminalCheck(opt Options) Check {
	var problems []string
	term := opt.Env("TERM")
	if term == "" || term == "dumb" {
		problems = append(problems, "TERM is "+orDash(term))
	}
	locale := firstNonEmpty(opt.Env("LC_ALL"), opt.Env("LC_CTYPE"), opt.Env("LANG"))
	if locale != "" && !isUTF8Locale(locale) {
		problems = append(problems, "locale "+locale+" is not UTF-8")
	}
	size := "unknown"
	if opt.Size != nil {
		if w, h, ok := opt.Size(); ok {
			size = fmt.Sprintf("%dx%d", w, h)
		}
	}
	colorterm := opt.Env("COLORTERM")
	truecolor := "no"
	if colorterm != "" {
		truecolor = "yes"
	}
	detail := fmt.Sprintf("TERM=%s, truecolor=%s, %s, size=%s", orDash(term), truecolor, "locale="+orDash(locale), size)
	if len(problems) > 0 {
		return Check{Name: "terminal", Status: Warn, Detail: detail + "; " + strings.Join(problems, "; "), Fix: "use a UTF-8 terminal with a real TERM"}
	}
	return Check{Name: "terminal", Status: OK, Detail: detail}
}

func orDash(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func isUTF8Locale(locale string) bool {
	u := strings.ToUpper(locale)
	return strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// configCheck loads the config and reports warnings. Only an unreadable file is
// a failure; every other problem is a warning.
func configCheck(opt Options) (*config.Config, Check) {
	cfg, warnings, err := config.Load(config.LoadOptions{
		CWD:      opt.CWD,
		Home:     opt.Home,
		Env:      opt.Env,
		ReadFile: opt.ReadFile,
	})
	if err != nil {
		return cfg, Check{Name: "config", Status: Fail, Detail: err.Error(), Fix: "fix the config path"}
	}
	if len(warnings) == 0 {
		return cfg, Check{Name: "config", Status: OK, Detail: "loaded"}
	}
	detail := warningText(warnings)
	for _, w := range warnings {
		if strings.Contains(w.Message, "cannot read config") {
			return cfg, Check{Name: "config", Status: Fail, Detail: detail, Fix: "make the config file readable"}
		}
	}
	return cfg, Check{Name: "config", Status: Warn, Detail: detail, Fix: "fix the warnings above"}
}

func warningText(warnings []config.Warning) string {
	parts := make([]string, 0, len(warnings))
	for _, w := range warnings {
		parts = append(parts, w.Source+": "+w.Message)
	}
	return strings.Join(parts, "; ")
}

// dirsCheck verifies the config, state, and session directories exist or can be
// created, and are writable, without creating anything.
func dirsCheck(opt Options) Check {
	dirs := map[string]string{
		"config":   config.UserConfigDir(opt.Home),
		"state":    filepath.Join(opt.Home, ".local", "state", "nova"),
		"sessions": filepath.Join(opt.Home, ".local", "share", "nova", "sessions"),
	}
	var bad []string
	for name, dir := range dirs {
		if !canWriteDir(dir) {
			bad = append(bad, name+" "+dir)
		}
	}
	if len(bad) > 0 {
		return Check{Name: "directories", Status: Fail, Detail: strings.Join(bad, ", "), Fix: "check the permissions of the directories above"}
	}
	return Check{Name: "directories", Status: OK, Detail: "config, state, and sessions are writable"}
}

// canWriteDir reports whether dir or its nearest existing ancestor is writable,
// without creating dir.
func canWriteDir(dir string) bool {
	for p := dir; ; {
		info, err := os.Stat(p)
		if err == nil {
			if !info.IsDir() {
				return false
			}
			return info.Mode().Perm()&0o200 != 0
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

func trustCheck(opt Options) Check {
	store := config.NewTrustStore(filepath.Join(config.UserConfigDir(opt.Home), "trust.json"))
	if store.IsTrusted(opt.CWD) {
		return Check{Name: "trust", Status: OK, Detail: "this directory is trusted"}
	}
	return Check{Name: "trust", Status: OK, Detail: "this directory is not trusted yet"}
}

func extensionsCheck(opt Options) Check {
	if opt.Host == nil {
		return Check{Name: "extensions", Status: OK, Detail: "none installed"}
	}
	statuses := opt.Host()
	if len(statuses) == 0 {
		return Check{Name: "extensions", Status: OK, Detail: "none installed"}
	}
	var parts []string
	failed := false
	for _, s := range statuses {
		if s.OK {
			parts = append(parts, s.Name+" ok")
			continue
		}
		failed = true
		detail := s.Name + " failed"
		if s.Detail != "" {
			detail += ": " + s.Detail
		}
		parts = append(parts, detail)
	}
	status := OK
	fix := ""
	if failed {
		status = Fail
		fix = "run `nova ext check <dir>` for the failing extension"
	}
	return Check{Name: "extensions", Status: status, Detail: strings.Join(parts, "; "), Fix: fix}
}

func projectCheck(opt Options, cfg *config.Config) Check {
	store := config.NewTrustStore(filepath.Join(config.UserConfigDir(opt.Home), "trust.json"))
	files, warnings := config.LoadContext(opt.CWD, opt.Home, config.ContextOptions{Trusted: store.IsTrusted})
	skills, skillWarnings := config.LoadSkills([]string{
		config.SkillsDir(config.ProjectConfigDir(rootOr(opt.CWD))),
		config.SkillsDir(config.UserConfigDir(opt.Home)),
	})
	warnings = append(warnings, skillWarnings...)
	detail := fmt.Sprintf("%d context file(s), %d skill(s)", len(files), len(skills))
	if len(warnings) > 0 {
		return Check{Name: "project", Status: Warn, Detail: detail + "; " + warningText(warnings), Fix: "fix the warnings above"}
	}
	return Check{Name: "project", Status: OK, Detail: detail}
}

// rootOr returns the project root when one exists, else cwd.
func rootOr(cwd string) string {
	if root, ok := config.ProjectRoot(cwd); ok {
		return root
	}
	return cwd
}

// Worst returns the most severe status in checks.
func Worst(checks []Check) Status {
	worst := OK
	for _, c := range checks {
		if c.Status > worst {
			worst = c.Status
		}
	}
	return worst
}

// ExitCode maps the checks to doctor's exit code: 0 unless something failed.
func ExitCode(checks []Check) int {
	if Worst(checks) == Fail {
		return 1
	}
	return 0
}

// Write prints the checks as plain text. The caller chooses the writer so a
// piped run can stay colourless.
func Write(out io.Writer, checks []Check) {
	for _, c := range checks {
		fmt.Fprintf(out, "%-12s %-4s %s\n", c.Name, c.Status, c.Detail)
		if c.Status != OK && c.Fix != "" {
			fmt.Fprintf(out, "%-12s      fix: %s\n", "", c.Fix)
		}
	}
}

// WriteJSON prints the checks as a JSON array.
func WriteJSON(out io.Writer, checks []Check) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(checks)
}

// GOOS is the runtime OS, exported so `cmd/nova` can show it without importing
// runtime itself in the doctor command.
func GOOS() string { return runtime.GOOS }
