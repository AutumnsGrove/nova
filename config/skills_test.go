package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSkill writes a SKILL.md under root/name.
func makeSkill(t *testing.T, root, name, front, body string) string {
	t.Helper()
	path := filepath.Join(root, name, "SKILL.md")
	write(t, path, front+body)
	return path
}

// skillFront builds simple front matter.
func skillFront(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n"
}

func TestSkills_SKL01_Valid(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", skillFront("foo", "does foo"), "body text")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 1 || len(warnings) != 0 {
		t.Fatalf("skills = %+v warnings = %v", skills, warnings)
	}
	if skills[0].Name != "foo" || skills[0].Description != "does foo" || !strings.Contains(skills[0].Body, "body text") {
		t.Fatalf("skill = %+v", skills[0])
	}
}

func TestSkills_SKL02_NameMismatch(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", skillFront("bar", "d"), "")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) == 0 {
		t.Fatalf("skills = %v warnings = %v, want skipped with a warning", skills, warnings)
	}
}

func TestSkills_SKL03_BadNames(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "Foo", skillFront("Foo", "d"), "")
	makeSkill(t, root, "foo_bar", skillFront("foo_bar", "d"), "")
	long := strings.Repeat("a", 65)
	makeSkill(t, root, long, skillFront(long, "d"), "")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 {
		t.Fatalf("skills = %v, want none", skills)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v, want three", warnings)
	}
}

func TestSkills_SKL04_MissingDescription(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", "---\nname: foo\n---\n", "")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) == 0 {
		t.Fatalf("skills = %v warnings = %v, want skipped with a warning", skills, warnings)
	}
}

func TestSkills_SKL05_LongDescription(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", skillFront("foo", strings.Repeat("d", maxDescriptionBytes+1)), "")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) == 0 {
		t.Fatalf("skills = %v warnings = %v, want skipped with a warning", skills, warnings)
	}
}

func TestSkills_SKL06_NoFrontMatter(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", "", "just a body")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) == 0 {
		t.Fatalf("skills = %v warnings = %v, want skipped with a warning", skills, warnings)
	}
}

func TestSkills_SKL07_MalformedFrontMatter(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", "---\nname: [\n---\n", "")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) == 0 {
		t.Fatalf("skills = %v warnings = %v, want skipped with a warning", skills, warnings)
	}
}

func TestSkills_SKL08_DuplicateAcrossDirs(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()
	makeSkill(t, project, "foo", skillFront("foo", "project"), "")
	makeSkill(t, user, "foo", skillFront("foo", "user"), "")
	skills, warnings := LoadSkills([]string{project, user})
	if len(skills) != 1 {
		t.Fatalf("skills = %+v, want one", skills)
	}
	if skills[0].Description != "project" {
		t.Fatalf("description = %q, want the first (project)", skills[0].Description)
	}
	if len(warnings) == 0 {
		t.Fatal("a duplicate should warn")
	}
}

func TestSkills_SKL09_FileTooLarge(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, root, "foo", skillFront("foo", "d"), strings.Repeat("x", maxSkillBytes+1))
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) == 0 {
		t.Fatalf("skills = %v warnings = %v, want skipped with a warning", skills, warnings)
	}
}

func TestSkills_SKL10_HiddenAndStrayFiles(t *testing.T) {
	root := t.TempDir()
	makeSkill(t, filepath.Join(root, ".hidden"), "foo", skillFront("foo", "d"), "")
	write(t, filepath.Join(root, "note.md"), "not a skill")
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 0 || len(warnings) != 0 {
		t.Fatalf("skills = %v warnings = %v, want none", skills, warnings)
	}
}

func TestSkills_SKL11_SymlinkedDirectory(t *testing.T) {
	real, links := t.TempDir(), t.TempDir()
	makeSkill(t, real, "foo", skillFront("foo", "d"), "")
	if err := os.Symlink(filepath.Join(real, "foo"), filepath.Join(links, "alias")); err != nil {
		t.Fatal(err)
	}
	skills, warnings := LoadSkills([]string{real, links})
	if len(skills) != 1 {
		t.Fatalf("skills = %+v, want one", skills)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none (the symlink target was already seen)", warnings)
	}
}
