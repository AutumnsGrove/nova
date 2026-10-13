package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// helpTokens are the pieces of content both builds must show: modes, session
// and run flags, subcommands, an example, and a documentation link.
var helpTokens = []string{
	"tui", "print", "json", "rpc",
	"--session", "--continue", "--resume", "--no-session", "--prompt", "--mode",
	"models", "sessions", "login", "logout", "version", "docs", "ext", "serve",
	"nova -p", "nova --mode rpc",
	"github.com/AutumnsGrove/nova",
}

func TestHelp_CLH01_Content(t *testing.T) {
	code, out, _ := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, token := range helpTokens {
		if !strings.Contains(out, token) {
			t.Fatalf("--help is missing %q:\n%s", token, out)
		}
	}
}

// plainHelp renders the same command tree with cobra's unstyled help, standing
// in for a notui build.
func plainHelp(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	root := newRoot(strings.NewReader(""), &out, &out, true, new(int))
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("plain help: %v", err)
	}
	return out.String()
}

func TestHelp_CLH02_SameContentBothBuilds(t *testing.T) {
	_, styled, _ := runCLI(t, "--help")
	plain := plainHelp(t)
	for _, token := range helpTokens {
		if !strings.Contains(styled, token) {
			t.Fatalf("styled help is missing %q", token)
		}
		if !strings.Contains(plain, token) {
			t.Fatalf("plain help is missing %q:\n%s", token, plain)
		}
	}
}

func TestHelp_CLH03_NoEscapesWhenNotATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	_, out, _ := runCLI(t, "--help")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("help contains ANSI escapes without a terminal:\n%q", out)
	}
}

func TestHelp_CLH04_WidthAdapts(t *testing.T) {
	bin := builtNova(t)
	for _, width := range []int{40, 80, 200} {
		out, _, code := runBinary(t, bin, []string{"NO_COLOR=1", "__FANG_TEST_WIDTH=" + strconv.Itoa(width)}, "--help")
		if code != 0 {
			t.Fatalf("width %d: exit %d", width, code)
		}
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if got := len([]rune(line)); got > width {
				t.Fatalf("width %d: line is %d runes: %q", width, got, line)
			}
		}
	}
}

func TestHelp_CLH05_Subcommands(t *testing.T) {
	cases := map[string]string{
		"version":  "Print the version",
		"models":   "List the model catalog",
		"sessions": "List sessions",
		"login":    "Log in to OpenRouter",
		"docs":     "Print embedded documentation",
		"ext":      "Install, list, update, remove, and check extensions",
		"serve":    "Serve the protocol",
	}
	for name, want := range cases {
		code, out, _ := runCLI(t, "help", name)
		if code != 0 {
			t.Fatalf("help %s: exit %d", name, code)
		}
		if !strings.Contains(out, want) {
			t.Fatalf("help %s missing %q:\n%s", name, want, out)
		}
	}
}

// withVersion sets the link-time build variables for one test and restores them.
func withVersion(t *testing.T, v, c, d string) {
	t.Helper()
	ov, oc, od := version, commit, date
	version, commit, date = v, c, d
	t.Cleanup(func() { version, commit, date = ov, oc, od })
}

func TestVersion_VER01_StampedFields(t *testing.T) {
	withVersion(t, "v0.1.0", "abc1234567", "2026-10-09")
	code, out, _ := runCLI(t, "version")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		"nova v0.1.0", "commit abc1234", "built 2026-10-09",
		runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH, "full",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("version line missing %q:\n%s", want, out)
		}
	}
}

func TestVersion_VER02_JSON(t *testing.T) {
	withVersion(t, "v0.1.0", "abc1234567", "2026-10-09")
	code, out, _ := runCLI(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got versionInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not valid JSON: %v\n%s", err, out)
	}
	if got.Version != "v0.1.0" || got.Commit != "abc1234567" || got.Date != "2026-10-09" {
		t.Fatalf("fields wrong: %+v", got)
	}
	if got.Go != runtime.Version() || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH || got.Flavour != "full" {
		t.Fatalf("runtime fields wrong: %+v", got)
	}
}

func TestVersion_VER03_PlainBuildDevAndVCS(t *testing.T) {
	withVersion(t, "dev", "", "")
	code, out, _ := runCLI(t, "version")
	if code != 0 || !strings.Contains(out, "nova dev") {
		t.Fatalf("exit = %d out = %q, want dev", code, out)
	}
	// The commit is either the stamped value (none here) or the VCS revision,
	// so the field must be present but is not otherwise asserted.
	if !strings.Contains(out, "commit ") {
		t.Fatalf("version line missing the commit field:\n%s", out)
	}
}

func TestVersion_VER04_FlavourBothTags(t *testing.T) {
	withVersion(t, "v0.1.0", "abc1234567", "2026-10-09")
	if buildFlavour() != "full" {
		t.Fatalf("test build flavour = %q, want full", buildFlavour())
	}
	bin := builtNova(t, "notui")
	out, errOut, code := runBinary(t, bin, nil, "version")
	if code != 0 {
		t.Fatalf("min version: exit %d stderr %s", code, errOut)
	}
	if !strings.Contains(out, "min") {
		t.Fatalf("min version line = %q, want the min flavour", out)
	}
}
