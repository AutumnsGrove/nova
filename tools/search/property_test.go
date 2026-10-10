package search

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// randomName returns one file or directory component from a small alphabet
// chosen to interact with the sample ignore patterns.
func randomName(rng *rand.Rand) string {
	names := []string{"a", "b", "c", "d.log", "e.txt", "f", "g", ".hidden"}
	return names[rng.Intn(len(names))]
}

// buildRandomTree writes a handful of random paths under dir. A path that
// collides with an already-created file is skipped, because the generator does
// not know which names became files and which directories.
func buildRandomTree(t *testing.T, rng *rand.Rand, dir string) {
	t.Helper()
	for i := 0; i < 1+rng.Intn(12); i++ {
		parts := make([]string, 0, 4)
		for depth := rng.Intn(4); depth > 0; depth-- {
			parts = append(parts, randomName(rng))
		}
		if len(parts) == 0 {
			parts = append(parts, randomName(rng))
		}
		full := filepath.Join(dir, filepath.Join(parts...))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			continue
		}
		os.WriteFile(full, []byte("x"), 0o644)
	}
}

// randomIgnore returns a random .gitignore body drawn from patterns that the
// generator's names can match.
func randomIgnore(rng *rand.Rand) string {
	patterns := []string{"*.log", "a/", "!a/b", "b/**", "/c", "# comment", "", "d/", "!*.txt", "a/b", "**/g", "e.txt"}
	var b strings.Builder
	for i := 0; i < rng.Intn(6); i++ {
		b.WriteString(patterns[rng.Intn(len(patterns))])
		b.WriteByte('\n')
	}
	return b.String()
}

// relSet turns walked entries into a set of relative paths.
func relSet(entries []walkEntry) map[string]bool {
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[e.Rel] = true
	}
	return set
}

// TestProperty_IGN_Subset asserts that disabling .gitignore can only add paths:
// the set returned with NoIgnore is always a superset of the set without it.
func TestProperty_IGN_Subset(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		dir := t.TempDir()
		buildRandomTree(t, rng, dir)
		writeFile(t, filepath.Join(dir, ".gitignore"), randomIgnore(rng))
		ignored := relSet(collect(t, dir, walkOptions{Hidden: true}))
		all := relSet(collect(t, dir, walkOptions{Hidden: true, NoIgnore: true}))
		for rel := range ignored {
			if !all[rel] {
				t.Fatalf("iteration %d: %q was returned with .gitignore but not with NoIgnore", i, rel)
			}
		}
	}
}
