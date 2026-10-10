package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// PromptInput is everything BuildSystemPrompt assembles.
type PromptInput struct {
	Base    string
	Context []ContextFile
	Skills  []Skill
	Tools   []tool.Info
	Extra   string
	CWD     string
	Date    time.Time
}

// BuildSystemPrompt assembles the prompt in a fixed order: base, environment,
// context files, skills, tools, extra. Missing parts are omitted, so the output
// is deterministic for the same input.
func BuildSystemPrompt(in PromptInput) string {
	date := in.Date
	if date.IsZero() {
		date = now()
	}
	var blocks []string
	appendBlock := func(text string) {
		if text != "" {
			blocks = append(blocks, text)
		}
	}
	appendBlock(strings.TrimSpace(in.Base))
	appendBlock(environmentBlock(in.CWD, date))
	appendBlock(contextBlock(in.Context))
	appendBlock(skillsBlock(in.Skills))
	appendBlock(toolsBlock(in.Tools))
	appendBlock(strings.TrimSpace(in.Extra))
	return strings.Join(blocks, "\n\n")
}

// environmentBlock states the working directory and date.
func environmentBlock(cwd string, date time.Time) string {
	if cwd == "" && date.IsZero() {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Environment\n")
	if cwd != "" {
		fmt.Fprintf(&b, "\nWorking directory: %s", cwd)
	}
	if !date.IsZero() {
		fmt.Fprintf(&b, "\nDate: %s", date.Format("2006-01-02"))
	}
	return b.String()
}

// contextBlock renders each instruction file under a heading with its path.
func contextBlock(files []ContextFile) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Context")
	for _, file := range files {
		fmt.Fprintf(&b, "\n\n## %s\n\n%s", file.Path, strings.TrimSpace(file.Content))
	}
	return b.String()
}

// skillsBlock lists skills by name and description only.
func skillsBlock(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Skills\n\nRead a skill's SKILL.md when it is relevant to the task.\n")
	for _, skill := range skills {
		fmt.Fprintf(&b, "\n- %s: %s", skill.Name, skill.Description)
	}
	return b.String()
}

// toolsBlock lists each tool's one-line description.
func toolsBlock(tools []tool.Info) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Tools")
	for _, info := range tools {
		description := firstLine(info.Description)
		if description == "" {
			description = info.Name
		}
		fmt.Fprintf(&b, "\n\n- %s: %s", info.Name, description)
	}
	return b.String()
}

// firstLine returns the first non-empty line of a description.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
