package tool

import (
	"slices"
	"strings"
	"testing"
)

func TestScrubEnv_RemovesExactNamesOnly(t *testing.T) {
	environ := []string{"PATH=/bin", "OPENROUTER_API_KEY=sk-x", "OPENROUTER_API_KEY_EXTRA=keep", "HOME=/h", "EMPTY=", "NOEQUALS"}
	got := ScrubEnv(environ, []string{"OPENROUTER_API_KEY", "NOEQUALS", "MISSING"})
	want := []string{"PATH=/bin", "OPENROUTER_API_KEY_EXTRA=keep", "HOME=/h", "EMPTY="}
	if !slices.Equal(got, want) {
		t.Fatalf("ScrubEnv = %q, want %q", got, want)
	}
}

func TestScrubEnv_NothingToScrubReturnsACopy(t *testing.T) {
	environ := []string{"A=1", "B=2"}
	got := ScrubEnv(environ, nil)
	if !slices.Equal(got, environ) {
		t.Fatalf("ScrubEnv = %q, want %q", got, environ)
	}
	got[0] = "A=changed"
	if environ[0] != "A=1" {
		t.Fatal("the result shares its backing array with the input")
	}
}

func TestFilterEnv_ValueContainingEqualsKeepsItsName(t *testing.T) {
	// Only the text before the first "=" is the name.
	var seen []string
	got := FilterEnv([]string{"URL=http://h/?a=b", "DROP=1"}, func(name string) bool {
		seen = append(seen, name)
		return name == "DROP"
	})
	if !slices.Equal(got, []string{"URL=http://h/?a=b"}) || !slices.Equal(seen, []string{"URL", "DROP"}) {
		t.Fatalf("got %q after checking %q", got, seen)
	}
}

func TestScrubEnvPatterns(t *testing.T) {
	environ := []string{"PATH=/bin", "OPENROUTER_API_KEY=sk-1", "MY_API_KEY=sk-2", "_TOKEN=odd", "HOME=/root"}
	got := ScrubEnvPatterns(environ, []string{"OPENROUTER_API_KEY", "_API_KEY"}, nil)
	want := []string{"PATH=/bin", "_TOKEN=odd", "HOME=/root"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scrub = %v, want %v", got, want)
	}
	// An explicit pass wins over a broad pattern, which is what ext does.
	kept := ScrubEnvPatterns(environ, []string{"_API_KEY"}, []string{"MY_API_KEY"})
	if !slices.Contains(kept, "MY_API_KEY=sk-2") || slices.Contains(kept, "OPENROUTER_API_KEY=sk-1") {
		t.Fatalf("pass = %v, want MY_API_KEY kept and the other key dropped", kept)
	}
}
