package config

import "path/filepath"

// configSubdir is the directory under the user's config home that holds Nova's
// user-level resources.
const configSubdir = "nova"

// UserConfigDir returns <home>/.config/nova.
func UserConfigDir(home string) string {
	return filepath.Join(home, ".config", configSubdir)
}

// UserConfigPath returns the user config base path, without an extension. The
// loader tries .yaml and then .json.
func UserConfigPath(home string) string {
	return filepath.Join(UserConfigDir(home), "config")
}

// ProjectConfigDir returns the .nova directory at the project root.
func ProjectConfigDir(root string) string {
	return filepath.Join(root, ".nova")
}

// ProjectConfigPath returns the project config base path, without an extension.
func ProjectConfigPath(root string) string {
	return filepath.Join(ProjectConfigDir(root), "config")
}

// SkillsDir returns the skills directory under dir.
func SkillsDir(dir string) string {
	return filepath.Join(dir, "skills")
}

// PromptsDir returns the prompt-template directory under dir.
func PromptsDir(dir string) string {
	return filepath.Join(dir, "prompts")
}
