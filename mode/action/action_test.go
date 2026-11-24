package action

import (
	"os"
	"path/filepath"
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
// parse error, not a default.
func TestAction_ACT06_OptionalInputsReachTheCommand(t *testing.T) {
	for _, step := range loadAction(t).Runs.Steps {
		if !strings.Contains(step.Run, "action review") {
			continue
		}
		for _, want := range []string{"--pr", "--dry-run"} {
			if !strings.Contains(step.Run, want) {
				t.Fatalf("the review step does not pass %s: %q", want, step.Run)
			}
		}
		for _, want := range []string{"INPUT_PR", "INPUT_DRY_RUN"} {
			if _, ok := step.Env[want]; !ok {
				t.Fatalf("the review step does not read %s from the environment: %v", want, step.Env)
			}
		}
		return
	}
	t.Fatal("no review step found")
}
