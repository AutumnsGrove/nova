package tool

import (
	"slices"
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
