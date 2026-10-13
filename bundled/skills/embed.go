// Package skills embeds Nova's built-in skills. They are compiled into the
// binary and loaded at the lowest precedence, so a user or project skill of the
// same name always wins.
package skills

import (
	"embed"

	"github.com/AutumnsGrove/nova/config"
)

//go:embed extension-author/SKILL.md
var files embed.FS

// extensionAuthor is the directory and skill name of the built-in skill.
const extensionAuthor = "extension-author"

// Builtin returns the built-in skills. A malformed embedded file is skipped
// rather than failing startup, because the binary was built from it.
func Builtin() []config.Skill {
	data, err := files.ReadFile(extensionAuthor + "/SKILL.md")
	if err != nil {
		return nil
	}
	skill, reason := config.ParseSkill(extensionAuthor, extensionAuthor, string(data))
	if reason != "" {
		return nil
	}
	skill.Source = "builtin"
	return []config.Skill{skill}
}
