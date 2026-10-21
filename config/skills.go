package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// maxSkillBytes bounds one SKILL.md file.
const maxSkillBytes = 256 << 10

// maxDescriptionChars bounds a skill description. Characters, not bytes, so a
// description in a script that needs three bytes a letter is not penalised.
const maxDescriptionChars = 1024

// skillNameRE is the allowed shape of a skill name.
var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill is one discoverable capability. Only Name and Description reach the
// system prompt; Body is read on demand.
type Skill struct {
	Name        string
	Description string
	Dir         string
	Body        string
	Source      string
}

// LoadSkills finds one-level skill directories under each dir in order. A
// duplicate name keeps the first and warns.
func LoadSkills(dirs []string) ([]Skill, []Warning) {
	var skills []Skill
	var warnings []Warning
	seen := map[string]bool{}
	visited := map[string]bool{}

	for _, root := range dirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			dir, ok := skillDir(root, entry.Name(), visited)
			if !ok {
				continue
			}
			skill, warning := loadSkill(dir, entry.Name())
			if warning != nil {
				warnings = append(warnings, *warning)
			}
			if skill == nil {
				continue
			}
			if seen[skill.Name] {
				warnings = append(warnings, Warning{Source: filepath.Join(dir, "SKILL.md"), Message: "duplicate skill name " + skill.Name + "; keeping the first"})
				continue
			}
			seen[skill.Name] = true
			skill.Source = root
			skills = append(skills, *skill)
		}
	}
	return skills, warnings
}

// skillDir resolves a directory entry to a candidate skill directory. Hidden
// entries, non-directories, and a directory already reached through another
// path (a symlink followed once) are not candidates.
func skillDir(root, name string, visited map[string]bool) (string, bool) {
	if strings.HasPrefix(name, ".") {
		return "", false
	}
	dir := filepath.Join(root, name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	if visited[real] {
		return "", false
	}
	visited[real] = true
	return dir, true
}

// loadSkill reads and validates dir/SKILL.md. It returns the skill, or nil
// with a warning when the file breaks a rule. A directory without a readable
// SKILL.md is simply not a skill, so it yields neither.
func loadSkill(dir, dirName string) (*Skill, *Warning) {
	path := filepath.Join(dir, "SKILL.md")
	data, err := readRegularFile(path, maxSkillBytes)
	switch {
	case errors.Is(err, errTooLarge):
		return nil, &Warning{Source: path, Message: "skill exceeds 256 KiB; skipping"}
	case errors.Is(err, errNotRegular):
		return nil, &Warning{Source: path, Message: "skill file is not a regular file; skipping"}
	case err != nil:
		return nil, nil
	}
	skill, reason := ParseSkill(dirName, dir, string(data))
	if reason != "" {
		return nil, &Warning{Source: path, Message: reason}
	}
	return &skill, nil
}

// LoadSkillsWithBuiltins loads directory skills and then appends the built-in
// skills, which have the lowest precedence: a user or project skill of the same
// name keeps its place and the built-in is dropped.
func LoadSkillsWithBuiltins(dirs []string, builtins []Skill) ([]Skill, []Warning) {
	skills, warnings := LoadSkills(dirs)
	seen := make(map[string]bool, len(skills)+len(builtins))
	for _, s := range skills {
		seen[s.Name] = true
	}
	for _, b := range builtins {
		if seen[b.Name] {
			continue
		}
		seen[b.Name] = true
		skills = append(skills, b)
	}
	return skills, warnings
}

// ParseSkill validates one SKILL.md and returns the skill. A non-empty reason
// means it is skipped. It is exported so the built-in skills, embedded outside
// this package, parse exactly like on-disk ones.
func ParseSkill(dirName, dir, content string) (Skill, string) {
	doc, body, present, err := parseFrontMatter(content)
	if err != nil {
		return Skill{}, "malformed front matter: " + err.Error()
	}
	if !present {
		return Skill{}, "missing front matter"
	}
	if !skillNameRE.MatchString(doc.Name) || len(doc.Name) > 64 {
		return Skill{}, "invalid skill name " + doc.Name
	}
	if doc.Name != dirName {
		return Skill{}, "skill name does not match its directory"
	}
	if doc.Description == "" {
		return Skill{}, "skill description is required"
	}
	if utf8.RuneCountInString(doc.Description) > maxDescriptionChars {
		return Skill{}, "skill description exceeds 1024 characters"
	}
	return Skill{Name: doc.Name, Description: doc.Description, Dir: dir, Body: body}, ""
}

// frontMatter is the YAML head shared by skills and templates.
type frontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseFrontMatter splits a Markdown file into its YAML front matter and body.
// present is false when there is no front matter at all; err is for malformed
// YAML or an unterminated fence. A leading byte-order mark is ignored, because
// some editors add one and it would hide the opening fence.
func parseFrontMatter(content string) (frontMatter, string, bool, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	raw, body, ok := splitFrontMatter(content)
	if !ok {
		return frontMatter{}, content, false, nil
	}
	var doc frontMatter
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return frontMatter{}, "", true, err
	}
	return doc, body, true, nil
}

// splitFrontMatter finds the "---" fenced block at the start of content. ok is
// false when the fence is missing or unterminated.
func splitFrontMatter(content string) (raw, body string, ok bool) {
	if !strings.HasPrefix(content, "---") {
		return "", content, false
	}
	i := 3
	if i < len(content) && content[i] == '\r' {
		i++
	}
	if i >= len(content) || content[i] != '\n' {
		return "", content, false
	}
	i++
	start := i
	for i < len(content) {
		j := strings.IndexByte(content[i:], '\n')
		line := content[i:]
		next := len(content)
		if j >= 0 {
			line = content[i : i+j]
			next = i + j + 1
		}
		if strings.TrimRight(line, "\r") == "---" {
			return content[start:i], content[next:], true
		}
		i = next
	}
	return "", content, false
}
