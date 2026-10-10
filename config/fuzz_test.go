package config

import (
	"fmt"
	"io/fs"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func FuzzLoadYAML(f *testing.F) {
	f.Add([]byte("model: a\n"))
	f.Add([]byte("a: [\n"))
	f.Add([]byte("max_turns: \"ten\"\n"))
	f.Add([]byte("\x00\xff\xfe"))
	f.Add([]byte("permissions:\n  headless: ask\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		home := t.TempDir()
		cwd := t.TempDir()
		path := filepath.Join(home, ".config", "nova", "config.yaml")
		cfg, _, err := Load(LoadOptions{
			CWD:  cwd,
			Home: home,
			ReadFile: func(p string) ([]byte, error) {
				if p == path {
					return data, nil
				}
				return nil, fs.ErrNotExist
			},
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg == nil {
			t.Fatal("Load returned a nil config")
		}
	})
}

func FuzzSplitArgs(f *testing.F) {
	f.Add(`a "b c" 'd e'`)
	f.Add(`a "b`)
	f.Add(`f\ g`)
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		args, err := SplitArgs(s)
		if err != nil {
			return
		}
		// A space join is only lossless when no argument can swallow the
		// separator or rewrite a quote; those cases are checked for no panic only.
		for _, arg := range args {
			if arg == "" || strings.ContainsAny(arg, " \t\n\r\\'\"") {
				return
			}
		}
		joined := strings.Join(args, " ")
		again, err := SplitArgs(joined)
		if err != nil {
			t.Fatalf("re-splitting %q: %v", joined, err)
		}
		if !reflect.DeepEqual(args, again) {
			t.Fatalf("split is not stable: %q -> %v -> %v", s, args, again)
		}
	})
}

func FuzzExpand(f *testing.F) {
	f.Add("hello $1", "world")
	f.Add("$@ and $ARGUMENTS", "a b c")
	f.Add(`cost \$1`, "x")
	f.Add("$3 $9", "a")
	f.Fuzz(func(t *testing.T, body, args string) {
		tpl := Template{Body: body}
		out := tpl.Expand(args)
		parts, err := SplitArgs(args)
		if err != nil {
			parts = []string{args}
		}
		total := 0
		for _, part := range parts {
			total += len(part)
		}
		placeholders := countPlaceholders(body)
		bound := len(body) + (total+len(parts))*placeholders
		if len(out) > bound {
			t.Fatalf("output %d bytes exceeds bound %d (body %q args %q)", len(out), bound, body, args)
		}
	})
}

// countPlaceholders counts substitution sites in a template body.
func countPlaceholders(body string) int {
	count := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) && body[i+1] == '$' {
			i++
			continue
		}
		if body[i] != '$' || i+1 >= len(body) {
			continue
		}
		c := body[i+1]
		switch {
		case c >= '1' && c <= '9', c == '@':
			count++
			i++
		case strings.HasPrefix(body[i+1:], "ARGUMENTS"):
			count++
			i += len("ARGUMENTS")
		}
	}
	return count
}

func TestProperty_CFG_MergeAssociative(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		a, b, c := randomMap(rng), randomMap(rng), randomMap(rng)
		bCopy, cCopy := copyMap(b), copyMap(c)

		// Merging A, B, C in order.
		ordered := copyMap(a)
		mergeMaps(ordered, b)
		mergeMaps(ordered, c)

		// Merging (A, B) then C must give the same map.
		ab := copyMap(a)
		mergeMaps(ab, bCopy)
		mergeMaps(ab, cCopy)

		if !reflect.DeepEqual(ordered, ab) {
			t.Fatalf("merge order changed the result:\nordered=%v\nab=%v", ordered, ab)
		}
		if !reflect.DeepEqual(b, bCopy) || !reflect.DeepEqual(c, cCopy) {
			t.Fatalf("mergeMaps mutated a source layer:\nb=%v\nbCopy=%v\nc=%v\ncCopy=%v", b, bCopy, c, cCopy)
		}
	}
}

// randomMap makes a nested map for the merge property.
func randomMap(rng *rand.Rand) map[string]any {
	m := map[string]any{}
	for i := 0; i < rng.Intn(4); i++ {
		m[fmt.Sprintf("k%d", rng.Intn(4))] = randomValue(rng, 2)
	}
	return m
}

// randomValue makes a scalar, list, or nested map.
func randomValue(rng *rand.Rand, depth int) any {
	maxKind := 3
	if depth > 0 {
		maxKind = 5
	}
	switch rng.Intn(maxKind) {
	case 0:
		return rng.Intn(100)
	case 1:
		return fmt.Sprintf("s%d", rng.Intn(5))
	case 2:
		return rng.Intn(2) == 0
	case 3:
		list := make([]any, rng.Intn(4))
		for i := range list {
			list[i] = randomValue(rng, depth-1)
		}
		return list
	default:
		m := map[string]any{}
		for i := 0; i < rng.Intn(3); i++ {
			m[fmt.Sprintf("n%d", rng.Intn(3))] = randomValue(rng, depth-1)
		}
		return m
	}
}

// copyMap deep-copies a map.
func copyMap(m map[string]any) map[string]any {
	return deepCopy(m).(map[string]any)
}

// deepCopy clones a value so the property does not share mutable maps.
func deepCopy(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = deepCopy(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = deepCopy(item)
		}
		return out
	default:
		return value
	}
}
