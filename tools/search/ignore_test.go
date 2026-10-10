package search

import (
	"strings"
	"testing"
)

// ignoredBy parses body as a .gitignore at base and reports whether rel is
// ignored, for a file or a directory.
func ignoredBy(t *testing.T, base, body, rel string, isDir bool) bool {
	t.Helper()
	set := parseIgnore(base, []byte(body))
	ig, _ := set.match(rel, isDir)
	return ig
}

func TestIgnore_IGN01_NameAtAnyDepth(t *testing.T) {
	if !ignoredBy(t, "/r", "*.log\n", "sub/deep/a.log", false) {
		t.Fatal("*.log should match at any depth")
	}
	if ignoredBy(t, "/r", "*.log\n", "sub/a.txt", false) {
		t.Fatal("*.log should not match a .txt file")
	}
}

func TestIgnore_IGN02_Anchored(t *testing.T) {
	if !ignoredBy(t, "/r", "/build\n", "build", true) {
		t.Fatal("/build should match at the ignore file's directory")
	}
	if ignoredBy(t, "/r", "/build\n", "sub/build", true) {
		t.Fatal("/build should not match a nested build")
	}
}

func TestIgnore_IGN03_MiddleSlashAnchors(t *testing.T) {
	if !ignoredBy(t, "/r", "a/b\n", "a/b", false) {
		t.Fatal("a/b should match a/b")
	}
	if ignoredBy(t, "/r", "a/b\n", "x/a/b", false) {
		t.Fatal("a/b should not match x/a/b")
	}
}

func TestIgnore_IGN04_DirectoryOnly(t *testing.T) {
	if !ignoredBy(t, "/r", "out/\n", "out", true) {
		t.Fatal("out/ should match the directory")
	}
	if ignoredBy(t, "/r", "out/\n", "out", false) {
		t.Fatal("out/ should not match a file named out")
	}
}

func TestIgnore_IGN05_Negation(t *testing.T) {
	body := "*.log\n!keep.log\n"
	if !ignoredBy(t, "/r", body, "other.log", false) {
		t.Fatal("other.log should be ignored")
	}
	if ignoredBy(t, "/r", body, "keep.log", false) {
		t.Fatal("keep.log should be re-included")
	}
}

func TestIgnore_IGN06_LaterRuleWins(t *testing.T) {
	if !ignoredBy(t, "/r", "!x\nx\n", "x", false) {
		t.Fatal("the later rule should win")
	}
}

func TestIgnore_IGN08_DoubleStarPrefix(t *testing.T) {
	if !ignoredBy(t, "/r", "**/foo\n", "foo", false) ||
		!ignoredBy(t, "/r", "**/foo\n", "a/b/foo", false) {
		t.Fatal("**/foo should match at any depth")
	}
}

func TestIgnore_IGN09_DoubleStarSuffix(t *testing.T) {
	if !ignoredBy(t, "/r", "foo/**\n", "foo/x", false) ||
		!ignoredBy(t, "/r", "foo/**\n", "foo/x/y", false) {
		t.Fatal("foo/** should match everything inside")
	}
	if ignoredBy(t, "/r", "foo/**\n", "foo", true) {
		t.Fatal("foo/** should not match foo itself")
	}
}

func TestIgnore_IGN10_DoubleStarMiddle(t *testing.T) {
	if !ignoredBy(t, "/r", "a/**/b\n", "a/b", false) ||
		!ignoredBy(t, "/r", "a/**/b\n", "a/x/y/b", false) {
		t.Fatal("a/**/b should match at any depth")
	}
}

func TestIgnore_IGN11_CommentsAndBlanks(t *testing.T) {
	body := "# a comment\n\n*.log\n"
	if !ignoredBy(t, "/r", body, "a.log", false) {
		t.Fatal("*.log should still apply")
	}
	if ignoredBy(t, "/r", body, "# a comment", false) {
		t.Fatal("a comment line is not a pattern")
	}
}

func TestIgnore_IGN12_EscapedHashAndBang(t *testing.T) {
	if !ignoredBy(t, "/r", "\\#foo\n", "#foo", false) {
		t.Fatal("\\#foo should match the literal #foo")
	}
	if !ignoredBy(t, "/r", "\\!bar\n", "!bar", false) {
		t.Fatal("\\!bar should match the literal !bar, not negate")
	}
}

func TestIgnore_IGN13_TrailingSpaces(t *testing.T) {
	if !ignoredBy(t, "/r", "x \n", "x", false) {
		t.Fatal("a trailing space should be stripped")
	}
	if ignoredBy(t, "/r", "x \n", "x ", false) {
		t.Fatal("the stripped pattern should not match a name with a space")
	}
	if !ignoredBy(t, "/r", "x\\ \n", "x ", false) {
		t.Fatal("an escaped trailing space should be kept")
	}
}

func TestIgnore_IGN14_CharacterClasses(t *testing.T) {
	if !ignoredBy(t, "/r", "[ab].txt\n", "a.txt", false) ||
		!ignoredBy(t, "/r", "[ab].txt\n", "b.txt", false) {
		t.Fatal("[ab].txt should match a.txt and b.txt")
	}
	if ignoredBy(t, "/r", "[ab].txt\n", "c.txt", false) {
		t.Fatal("[ab].txt should not match c.txt")
	}
	if !ignoredBy(t, "/r", "[!a].txt\n", "c.txt", false) {
		t.Fatal("[!a].txt should match c.txt")
	}
	if ignoredBy(t, "/r", "[!a].txt\n", "a.txt", false) {
		t.Fatal("[!a].txt should not match a.txt")
	}
}

func TestIgnore_IGN17_InvalidLineSkipped(t *testing.T) {
	body := "[\n*.log\n"
	if !ignoredBy(t, "/r", body, "a.log", false) {
		t.Fatal("a later valid rule should still apply")
	}
	if ignoredBy(t, "/r", body, "[", false) {
		t.Fatal("an invalid pattern should match nothing")
	}
}

func TestIgnore_IGN19_StarDoesNotCrossSlash(t *testing.T) {
	if ignoredBy(t, "/r", "a/*.txt\n", "a/b/c.txt", false) {
		t.Fatal("* should not cross a slash")
	}
	if !ignoredBy(t, "/r", "a/*.txt\n", "a/c.txt", false) {
		t.Fatal("a/*.txt should match a/c.txt")
	}
}

func TestIgnore_IGN20_CRLF(t *testing.T) {
	if !ignoredBy(t, "/r", "*.log\r\n!keep.log\r\n", "a.log", false) {
		t.Fatal("CRLF patterns should work")
	}
}

func TestIgnore_IGN21_BOM(t *testing.T) {
	if !ignoredBy(t, "/r", "\xEF\xBB\xBF*.log\n", "a.log", false) {
		t.Fatal("a BOM before the first pattern should be dropped")
	}
}

func TestIgnore_IGN22_VeryLongPattern(t *testing.T) {
	body := strings.Repeat("a", 10*1024) + "\n*.log\n"
	if !ignoredBy(t, "/r", body, "a.log", false) {
		t.Fatal("a very long line should not break later patterns")
	}
}

func FuzzParseIgnore(f *testing.F) {
	f.Add([]byte("*.log\n!keep.log\n/build/\n[ab].txt\nfoo/**\n"))
	f.Add([]byte("\xEF\xBB\xBF# c\r\nfoo/**\r\na/**/b\n"))
	f.Add([]byte("\\#x\n\\!y\nx\\ \n[\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		set := parseIgnore("/base", data)
		for _, rel := range []string{"x", "a/b.log", "build", "a/x/y/b", "!y", "#x"} {
			set.match(rel, true)
			set.match(rel, false)
		}
	})
}
