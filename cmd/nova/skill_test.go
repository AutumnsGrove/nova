package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bundledskills "github.com/AutumnsGrove/nova/bundled/skills"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
)

func TestSkill_SKA01_EmbeddedAndListed(t *testing.T) {
	builtins := bundledskills.Builtin()
	if len(builtins) != 1 {
		t.Fatalf("builtin skills = %d, want 1", len(builtins))
	}
	s := builtins[0]
	if s.Name != "extension-author" || s.Source != "builtin" {
		t.Fatalf("builtin skill = %+v, want extension-author from builtin", s)
	}
	prompt := config.BuildSystemPrompt(config.PromptInput{Skills: builtins})
	if !strings.Contains(prompt, "extension-author") {
		t.Fatalf("system prompt does not list the builtin skill:\n%s", prompt)
	}
}

func TestSkill_SKA02_LowestPrecedence(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "extension-author")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: extension-author\ndescription: the user's own skill\n---\nuser body\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, _ := config.LoadSkillsWithBuiltins([]string{dir}, bundledskills.Builtin())
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want 1", len(skills))
	}
	if skills[0].Description != "the user's own skill" || skills[0].Source == "builtin" {
		t.Fatalf("user skill did not win: %+v", skills[0])
	}
}

func TestSkill_SKA03_WorkflowSteps(t *testing.T) {
	s := bundledskills.Builtin()[0]
	for _, want := range []string{
		"nova docs extensions", "nova docs protocol", "nova docs examples",
		"nova ext check", "nova ext install", "/reload",
		"register", "append_entry", "Truncate", "is_error", "shell",
	} {
		if !strings.Contains(s.Body, want) {
			t.Fatalf("skill body is missing %q", want)
		}
	}
}

func TestSkill_SKA04_BodySize(t *testing.T) {
	s := bundledskills.Builtin()[0]
	est := compaction.Estimate([]message.Message{{Role: message.RoleUser, Content: s.Body}})
	if est >= 1500 {
		t.Fatalf("skill body estimates %d tokens, want under 1500", est)
	}
}

func TestSkill_SKA05_NameAndDescriptionOnly(t *testing.T) {
	s := bundledskills.Builtin()[0]
	prompt := config.BuildSystemPrompt(config.PromptInput{Skills: []config.Skill{s}})
	if !strings.Contains(prompt, s.Name) || !strings.Contains(prompt, s.Description) {
		t.Fatalf("prompt lacks name or description:\n%s", prompt)
	}
	if strings.Contains(prompt, "extension.yaml") || strings.Contains(prompt, "Work in this order") {
		t.Fatalf("prompt leaked the skill body:\n%s", prompt)
	}
}
