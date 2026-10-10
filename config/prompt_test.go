package config

import (
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// fullPromptInput is a complete PromptInput for the ordering tests.
func fullPromptInput() PromptInput {
	return PromptInput{
		Base:    "BASE PROMPT",
		Context: []ContextFile{{Path: "/proj/AGENTS.md", Content: "context body"}},
		Skills:  []Skill{{Name: "foo", Description: "skill description", Body: "skill body secret"}},
		Tools:   []tool.Info{{Name: "read", Description: "read a file\nmore detail"}},
		Extra:   "EXTRA SECTION",
		CWD:     "/proj",
		Date:    time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
	}
}

func TestPrompt_SYS01_FullOrder(t *testing.T) {
	got := BuildSystemPrompt(fullPromptInput())
	markers := []string{"BASE PROMPT", "Working directory: /proj", "# Context", "# Skills", "# Tools", "EXTRA SECTION"}
	last := -1
	for _, marker := range markers {
		index := strings.Index(got, marker)
		if index < 0 {
			t.Fatalf("prompt missing %q:\n%s", marker, got)
		}
		if index < last {
			t.Fatalf("%q appears out of order:\n%s", marker, got)
		}
		last = index
	}
}

func TestPrompt_SYS02_EmptyPartsOmitted(t *testing.T) {
	got := BuildSystemPrompt(PromptInput{Base: "only base", CWD: "/x", Date: time.Unix(0, 0)})
	for _, marker := range []string{"# Context", "# Skills", "# Tools"} {
		if strings.Contains(got, marker) {
			t.Fatalf("empty part %q should be omitted:\n%s", marker, got)
		}
	}
}

func TestPrompt_SYS03_Deterministic(t *testing.T) {
	in := fullPromptInput()
	if BuildSystemPrompt(in) != BuildSystemPrompt(in) {
		t.Fatal("the same input should produce the same prompt")
	}
}

func TestPrompt_SYS04_ContextPaths(t *testing.T) {
	got := BuildSystemPrompt(fullPromptInput())
	if !strings.Contains(got, "/proj/AGENTS.md") || !strings.Contains(got, "context body") {
		t.Fatalf("prompt should show the context path and content:\n%s", got)
	}
}

func TestPrompt_SYS05_SkillListHasNoBodies(t *testing.T) {
	got := BuildSystemPrompt(fullPromptInput())
	if !strings.Contains(got, "- foo: skill description") {
		t.Fatalf("prompt should list the skill:\n%s", got)
	}
	if strings.Contains(got, "skill body secret") {
		t.Fatalf("prompt should not contain the skill body:\n%s", got)
	}
}

func TestPrompt_SYS06_DateAndCWD(t *testing.T) {
	got := BuildSystemPrompt(fullPromptInput())
	if !strings.Contains(got, "2026-10-11") || !strings.Contains(got, "/proj") {
		t.Fatalf("prompt should show the date and cwd:\n%s", got)
	}
}

func TestPrompt_SKL12_SkillBodyOmitted(t *testing.T) {
	got := BuildSystemPrompt(PromptInput{Skills: []Skill{{Name: "a", Description: "d", Body: "UNIQUEBODY"}}})
	if strings.Contains(got, "UNIQUEBODY") {
		t.Fatalf("skill bodies must not enter the prompt:\n%s", got)
	}
}
