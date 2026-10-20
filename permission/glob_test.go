package permission

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMatchPath_GLB01_StarNoSlash checks * does not cross a path separator.
func TestMatchPath_GLB01_StarNoSlash(t *testing.T) {
	if MatchPath("src/*.go", "src/a/b.go") {
		t.Error("* crossed a slash")
	}
	if !MatchPath("src/*.go", "src/a.go") {
		t.Error("src/*.go did not match src/a.go")
	}
}

// TestMatchPath_GLB02_GlobstarCrosses checks ** crosses separators.
func TestMatchPath_GLB02_GlobstarCrosses(t *testing.T) {
	if !MatchPath("src/**/*.go", "src/a/b/c.go") {
		t.Error("** did not cross separators")
	}
}

// TestMatchPath_GLB03_GlobstarZero checks ** matches zero segments.
func TestMatchPath_GLB03_GlobstarZero(t *testing.T) {
	if !MatchPath("src/**/x.go", "src/x.go") {
		t.Error("** did not match zero segments")
	}
}

// TestMatchPath_GLB04_Question checks ? matches one non-slash character.
func TestMatchPath_GLB04_Question(t *testing.T) {
	if !MatchPath("a?c", "abc") {
		t.Error("? did not match one character")
	}
	if MatchPath("a?c", "a/c") {
		t.Error("? matched a slash")
	}
	if MatchPath("a?c", "ac") {
		t.Error("? matched zero characters")
	}
}

// TestMatchPath_GLB05_Classes checks character classes.
func TestMatchPath_GLB05_Classes(t *testing.T) {
	if !MatchPath("[a-c]x", "bx") {
		t.Error("[a-c] did not match b")
	}
	if MatchPath("[a-c]x", "dx") {
		t.Error("[a-c] matched d")
	}
	if !MatchPath("[!a]x", "bx") {
		t.Error("[!a] did not match b")
	}
	if MatchPath("[!a]x", "ax") {
		t.Error("[!a] matched a")
	}
	if !MatchPath("[^a]x", "bx") {
		t.Error("[^a] did not match b")
	}
}

// TestMatchPath_GLB06_TrailingSlash checks a trailing slash matches the
// directory and everything under it.
func TestMatchPath_GLB06_TrailingSlash(t *testing.T) {
	if !MatchPath("build/", "build/a/b.o") {
		t.Error("build/ did not match its contents")
	}
	if !MatchPath("build/", "build") {
		t.Error("build/ did not match the directory itself")
	}
}

// TestMatchPath_GLB07_CaseSensitive checks matching is case-sensitive.
func TestMatchPath_GLB07_CaseSensitive(t *testing.T) {
	if MatchPath("Src/*.go", "src/a.go") {
		t.Error("matching was case-insensitive")
	}
}

// TestMatchPath_GLB08_LeadingGlobstar checks **/ matches at the root.
func TestMatchPath_GLB08_LeadingGlobstar(t *testing.T) {
	if !MatchPath("**/x", "x") {
		t.Error("**/x did not match x")
	}
	if !MatchPath("**/x", "a/b/x") {
		t.Error("**/x did not match a/b/x")
	}
}

// TestMatchString_GLB09_StarCrosses checks MatchString's * crosses slashes.
func TestMatchString_GLB09_StarCrosses(t *testing.T) {
	if !MatchString("git *", "git a/b c") {
		t.Error("MatchString * did not cross a slash")
	}
}

// TestMatchString_GLB10_WholeString checks MatchString is anchored.
func TestMatchString_GLB10_WholeString(t *testing.T) {
	if MatchString("git", "git status") {
		t.Error("MatchString matched a prefix")
	}
}

// TestMatch_GLB11_Unbalanced checks an unbalanced class never matches.
func TestMatch_GLB11_Unbalanced(t *testing.T) {
	if MatchPath("[a", "a") {
		t.Error("MatchPath matched an unbalanced pattern")
	}
	if MatchString("[a", "a") {
		t.Error("MatchString matched an unbalanced pattern")
	}
}

// TestMatch_GLB12_EmptyPattern checks the empty pattern matches only empty.
func TestMatch_GLB12_EmptyPattern(t *testing.T) {
	if !MatchString("", "") {
		t.Error("empty pattern did not match the empty string")
	}
	if MatchString("", "x") {
		t.Error("empty pattern matched a non-empty string")
	}
	if !MatchPath("", "") {
		t.Error("empty path pattern did not match the empty path")
	}
}

// TestMatch_GLB13_Pathological checks a pathological pattern returns quickly.
func TestMatch_GLB13_Pathological(t *testing.T) {
	pattern := "*a*a*a*a*a*a*a*b"
	input := strings.Repeat("a", 64)
	began := time.Now()
	_ = MatchString(pattern, input)
	if elapsed := time.Since(began); elapsed > 100*time.Millisecond {
		t.Fatalf("pathological MatchString took %s", elapsed)
	}
}

// TestMatch_GLB14_Unicode checks multibyte characters match.
func TestMatch_GLB14_Unicode(t *testing.T) {
	if !MatchString("é*", "éclair") {
		t.Error("é* did not match éclair")
	}
}

// TestMatchString_GLB15_StarCrossesNewline checks * and ? match a newline, so a
// deny pattern cannot be dodged by splitting a command across lines.
func TestMatchString_GLB15_StarCrossesNewline(t *testing.T) {
	if !MatchString("*rm -rf*", "echo hi\nrm -rf /") {
		t.Error("* did not match across a newline")
	}
	if !MatchString("a?b", "a\nb") {
		t.Error("? did not match a newline")
	}
	if !MatchPath("a/**", "a/b\nc/d") {
		t.Error("** did not match a path segment containing a newline")
	}
}

// TestMatchPath_GLB16_NegatedClassStopsAtSlash checks a negated class cannot
// match a separator, because a path glob must not span segments except via **.
func TestMatchPath_GLB16_NegatedClassStopsAtSlash(t *testing.T) {
	if MatchPath("a[!b]c", "a/c") {
		t.Error("[!b] matched a slash")
	}
	if MatchPath("a[^b]c", "a/c") {
		t.Error("[^b] matched a slash")
	}
	if !MatchPath("a[!b]c", "axc") {
		t.Error("[!b] did not match x")
	}
	// A string glob has no separators, so a negated class may match "/".
	if !MatchString("a[!b]c", "a/c") {
		t.Error("MatchString [!b] did not match /")
	}
}

// TestMatch_GLB17_CacheBounded checks the compiled-pattern cache stays bounded
// and never changes an answer, including for an invalid pattern seen twice.
func TestMatch_GLB17_CacheBounded(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 2 * globCacheMax {
				pattern := fmt.Sprintf("dir%d_%d/*.go", g, i)
				if !MatchPath(pattern, fmt.Sprintf("dir%d_%d/a.go", g, i)) {
					t.Errorf("%s did not match its own path", pattern)
				}
				if MatchPath(pattern, "other/a.go") {
					t.Errorf("%s matched an unrelated path", pattern)
				}
				if MatchPath("[a", "a") || MatchString("[a", "a") {
					t.Error("an unbalanced pattern matched")
				}
			}
		}()
	}
	wg.Wait()
	globCache.Lock()
	defer globCache.Unlock()
	if n := len(globCache.m); n > globCacheMax {
		t.Fatalf("cache holds %d patterns, limit %d", n, globCacheMax)
	}
}

// FuzzMatchPath checks MatchPath never panics and stays fast.
func FuzzMatchPath(f *testing.F) {
	f.Add("src/**/*.go", "src/a/b/c.go")
	f.Add("[a", "a")
	f.Add("*a*a*b", strings.Repeat("a", 64))
	f.Fuzz(func(t *testing.T, pattern, path string) {
		if len(pattern) > 1024 || len(path) > 1024 {
			return
		}
		began := time.Now()
		_ = MatchPath(pattern, path)
		if elapsed := time.Since(began); elapsed > 100*time.Millisecond {
			t.Fatalf("MatchPath(%q, %q) took %s", pattern, path, elapsed)
		}
	})
}

// FuzzMatchString checks MatchString never panics and stays fast.
func FuzzMatchString(f *testing.F) {
	f.Add("git *", "git a/b c")
	f.Add("[a", "a")
	f.Add("*a*a*b", strings.Repeat("a", 64))
	f.Fuzz(func(t *testing.T, pattern, s string) {
		if len(pattern) > 1024 || len(s) > 1024 {
			return
		}
		began := time.Now()
		_ = MatchString(pattern, s)
		if elapsed := time.Since(began); elapsed > 100*time.Millisecond {
			t.Fatalf("MatchString(%q, %q) took %s", pattern, s, elapsed)
		}
	})
}
