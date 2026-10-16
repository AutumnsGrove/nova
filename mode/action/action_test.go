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
			Run string `yaml:"run"`
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
	for _, name := range []string{"model", "max-comments", "max-cost", "wall-clock", "openrouter-api-key", "github-token"} {
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
