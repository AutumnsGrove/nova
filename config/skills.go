package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxSkillBytes bounds one SKILL.md file.
const maxSkillBytes = 256 << 10

// maxDescriptionBytes bounds a skill description or template description.
const maxDescriptionBytes = 1024

// skillNameRE is the allowed shape of a skill or template name.
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
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			dir := filepath.Join(root, name)
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			real, err := filepath.EvalSymlinks(dir)
			if err != nil {
				real = dir
			}
			if visited[real] {
				continue
			}
			visited[real] = true

			skillPath := filepath.Join(dir, "SKILL.md")
			data, err := os.ReadFile(skillPath)
			if err != nil {
				// A directory without a readable SKILL.md is not a skill.
				continue
			}
			if len(data) > maxSkillBytes {
				warnings = append(warnings, Warning{Source: skillPath, Message: "skill exceeds 256 KiB; skipping"})
				continue
			}
			skill, reason := parseSkill(name, dir, string(data))
			if reason != "" {
				warnings = append(warnings, Warning{Source: skillPath, Message: reason})
				continue
			}
			if seen[skill.Name] {
				warnings = append(warnings, Warning{Source: skillPath, Message: "duplicate skill name " + skill.Name + "; keeping the first"})
				continue
			}
			seen[skill.Name] = true
			skill.Source = root
			skills = append(skills, skill)
		}
	}
	return skills, warnings
}

// ParseSkill validates one SKILL.md and returns the skill. A non-empty reason
// means it is skipped. It is exported so the built-in skills, embedded outside
// this package, parse exactly like on-disk ones.
func ParseSkill(dirName, dir, content string) (Skill, string) {
	return parseSkill(dirName, dir, content)
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

// parseSkill validates one SKILL.md. A non-empty reason means it is skipped.
func parseSkill(dirName, dir, content string) (Skill, string) {
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
	if len(doc.Description) > maxDescriptionBytes {
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
// YAML or an unterminated fence.
func parseFrontMatter(content string) (frontMatter, string, bool, error) {
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
