package config

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestContext_CTF14_TruncationKeepsUTF8 checks that cutting a file at the size
// cap never splits a multi-byte character, which would put invalid UTF-8 into
// the system prompt.
func TestContext_CTF14_TruncationKeepsUTF8(t *testing.T) {
	f := newCtxFixture(t)
	// "é" is two bytes; a cap of 3 would cut the second one in half.
	write(t, f.global("AGENTS.md"), strings.Repeat("é", 10))
	files, _ := LoadContext(f.b, f.home, ContextOptions{MaxFileBytes: 3})
	if len(files) != 1 || !files[0].Truncated {
		t.Fatalf("files = %+v, want one truncated file", files)
	}
	if !utf8.ValidString(files[0].Content) {
		t.Fatalf("truncated content is not valid UTF-8: %q", files[0].Content)
	}
	if !strings.HasPrefix(files[0].Content, "é") {
		t.Fatalf("content = %q, want the first character kept", files[0].Content)
	}
}

// TestSkills_SKL13_BOMFrontMatter checks that a SKILL.md saved with a UTF-8
// byte-order mark, as some Windows editors do, still parses.
func TestSkills_SKL13_BOMFrontMatter(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "bom", "SKILL.md"), "\xef\xbb\xbf---\nname: bom\ndescription: has a BOM\n---\nbody\n")
	skills, warnings := LoadSkills([]string{root})
	if len(skills) != 1 || skills[0].Name != "bom" {
		t.Fatalf("skills = %+v, warnings = %v", skills, warnings)
	}
}
