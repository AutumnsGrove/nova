package action

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// actionYML is the composite action definition.
type actionYML struct {
	Inputs map[string]struct {
		Required bool `yaml:"required"`
	} `yaml:"inputs"`
	Runs struct {
		Using string `yaml:"using"`
		Steps []struct {
			Run string            `yaml:"run"`
			Env map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"runs"`
}

func loadAction(t *testing.T) actionYML {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "action.yml"))
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}
	var doc actionYML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse action.yml: %v", err)
	}
	return doc
}

func TestAction_ACT01_ActionYAML(t *testing.T) {
	doc := loadAction(t)
	if doc.Runs.Using != "composite" {
		t.Fatalf("runs.using = %q, want composite", doc.Runs.Using)
	}
	for _, name := range []string{"model", "max-comments", "max-cost", "wall-clock", "pr", "dry-run", "openrouter-api-key", "github-token"} {
		if _, ok := doc.Inputs[name]; !ok {
			t.Fatalf("input %q is missing from %v", name, doc.Inputs)
		}
	}
	if !doc.Inputs["github-token"].Required {
		t.Fatal("github-token should be required")
	}
}

func TestAction_ACT02_InstallUsesNotui(t *testing.T) {
	doc := loadAction(t)
	for _, step := range doc.Runs.Steps {
		if strings.Contains(step.Run, "go install") {
			if !strings.Contains(step.Run, "-tags notui") {
				t.Fatalf("install step does not use -tags notui: %q", step.Run)
			}
			return
		}
	}
	t.Fatal("no go install step found")
}

// An input interpolated into shell text is code the input's author writes. The
// action takes its inputs from the caller's workflow, so every value reaches the
// script through the environment, never through ${{ }} in the script itself.
func TestAction_ACT03_NoExpressionInAScript(t *testing.T) {
	for _, step := range loadAction(t).Runs.Steps {
		if strings.Contains(step.Run, "${{") {
			t.Errorf("a run script interpolates an expression:\n%s", step.Run)
		}
	}
}

// A local action (`uses: ./`) has no ref: the runner leaves github.action_ref
// empty, so installing by ref is an install of the empty string and the
// repository cannot use its own action. The checkout in front of the step is
// the version to run in that case.
func TestAction_ACT05_InstallFallsBackToTheCheckout(t *testing.T) {
	for _, step := range loadAction(t).Runs.Steps {
		if !strings.Contains(step.Run, "go install") {
			continue
		}
		if !strings.Contains(step.Run, "ACTION_REF") {
			t.Fatalf("the install step does not read the ref it was called with: %q", step.Run)
		}
		if !strings.Contains(step.Run, "./cmd/nova") {
			t.Fatalf("the install step cannot build the checkout: %q", step.Run)
		}
		return
	}
	t.Fatal("no go install step found")
}

// The pull request number and a dry run are options of the command, and an
// unset input must not reach the command line as an empty value: `--pr ""` is a
// parse error, not a default. The step's own script is run, with a stub `nova`
// on PATH, because the promise is about the argument list rather than about the
// text of the script: a guard removed from around `--pr` still mentions `--pr`.
func TestAction_ACT06_OptionalInputsReachTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the step runs a bash script")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to run the step with")
	}
	var script string
	for _, step := range loadAction(t).Runs.Steps {
		if strings.Contains(step.Run, "action review") {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("no review step found")
	}

	bin := t.TempDir()
	stub := "#!/usr/bin/env bash\nfor arg in \"$@\"; do printf '%s\\n' \"$arg\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "nova"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// The other inputs are always set, so a missing name in the output is a
	// guard that did not fire rather than a variable the step never had.
	base := []string{"INPUT_MODEL=m", "INPUT_MAX_COMMENTS=15", "INPUT_MAX_COST=1", "INPUT_WALL_CLOCK=10m"}

	run := func(t *testing.T, extra ...string) []string {
		t.Helper()
		cmd := exec.Command(bash, "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
		cmd.Env = append(append([]string{"PATH=" + bin + ":" + os.Getenv("PATH")}, base...), extra...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the step failed: %v", err)
		}
		return strings.Fields(string(out))
	}

	unset := run(t, "INPUT_PR=", "INPUT_DRY_RUN=false")
	for _, absent := range []string{"--pr", "--dry-run", ""} {
		if slices.Contains(unset, absent) {
			t.Fatalf("an unset input reached the command line: %v", unset)
		}
	}

	set := run(t, "INPUT_PR=42", "INPUT_DRY_RUN=true")
	for _, want := range [][]string{{"--pr", "42"}, {"--dry-run"}} {
		if !hasRun(set, want) {
			t.Fatalf("the command line is missing %v: %v", want, set)
		}
	}
}

// hasRun reports whether args contains seq, in order.
func hasRun(args, seq []string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		if slices.Equal(args[i:i+len(seq)], seq) {
			return true
		}
	}
	return false
}
