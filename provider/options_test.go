package provider

import (
	"reflect"
	"testing"
)

func TestProvider_PRV01_MergeReturnsNewMap(t *testing.T) {
	base := map[string]any{"a": 1}
	over := map[string]any{"b": 2}
	got := MergeOptions(base, over)
	if got["a"] != 1 || got["b"] != 2 {
		t.Fatalf("result = %v, want a=1 b=2", got)
	}
	got["a"] = 99
	if base["a"] != 1 {
		t.Errorf("mutating the result changed the input: %v", base)
	}
}

func TestProvider_PRV02_MergeReplacesScalars(t *testing.T) {
	got := MergeOptions(map[string]any{"a": 1}, map[string]any{"a": 2})
	if got["a"] != 2 {
		t.Errorf("a = %v, want 2", got["a"])
	}
}

func TestProvider_PRV03_MergeMergesNestedMaps(t *testing.T) {
	base := map[string]any{"p": map[string]any{"x": 1}}
	over := map[string]any{"p": map[string]any{"y": 2}}
	got := MergeOptions(base, over)
	nested, ok := got["p"].(map[string]any)
	if !ok {
		t.Fatalf("p = %T, want map", got["p"])
	}
	if nested["x"] != 1 || nested["y"] != 2 {
		t.Errorf("p = %v, want x=1 y=2", nested)
	}
	if _, leaked := base["p"].(map[string]any)["y"]; leaked {
		t.Errorf("merge mutated the base nested map: %v", base)
	}
}

func TestProvider_PRV04_MergeReplacesSlices(t *testing.T) {
	got := MergeOptions(
		map[string]any{"o": []any{1, 2}},
		map[string]any{"o": []any{3}},
	)
	if !reflect.DeepEqual(got["o"], []any{3}) {
		t.Errorf("o = %v, want [3]", got["o"])
	}
}

func TestProvider_PRV05_MergeNilInputs(t *testing.T) {
	if got := MergeOptions(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("nil, nil = %v, want empty non-nil map", got)
	}
	m := map[string]any{"a": 1}
	got := MergeOptions(nil, m)
	if got["a"] != 1 {
		t.Errorf("nil, m = %v, want a=1", got)
	}
	got["a"] = 2
	if m["a"] != 1 {
		t.Errorf("nil, m did not copy: m = %v", m)
	}
	if got := MergeOptions(m, nil); got["a"] != 1 {
		t.Errorf("m, nil = %v, want a=1", got)
	}
}

func TestProvider_PRV06_MergeMapScalarBothWays(t *testing.T) {
	got := MergeOptions(
		map[string]any{"p": map[string]any{"x": 1}},
		map[string]any{"p": 5},
	)
	if got["p"] != 5 {
		t.Errorf("map replaced by scalar: p = %v, want 5", got["p"])
	}
	got = MergeOptions(
		map[string]any{"p": 5},
		map[string]any{"p": map[string]any{"y": 2}},
	)
	if nested, ok := got["p"].(map[string]any); !ok || nested["y"] != 2 {
		t.Errorf("scalar replaced by map: p = %v, want y=2", got["p"])
	}
}

func TestProvider_PRV07_NormalizeFinish(t *testing.T) {
	cases := map[string]string{
		"stop":           "stop",
		"end_turn":       "stop",
		"length":         "length",
		"max_tokens":     "length",
		"tool_calls":     "tool_calls",
		"tool_use":       "tool_calls",
		"function_call":  "tool_calls",
		"content_filter": "content_filter",
		"safety":         "content_filter",
		"error":          "error",
		"":               "",
		"weird":          "",
	}
	for in, want := range cases {
		if got := NormalizeFinish(in); got != want {
			t.Errorf("NormalizeFinish(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzMergeOptions(f *testing.F) {
	f.Add([]byte("a=1"), []byte("b=2"))
	f.Fuzz(func(t *testing.T, baseBytes, overBytes []byte) {
		base := map[string]any{"k": string(baseBytes)}
		over := map[string]any{"j": string(overBytes)}
		baseBefore := MergeOptions(base, nil)
		overBefore := MergeOptions(over, nil)

		got := MergeOptions(base, over)
		if _, ok := got["k"]; !ok {
			t.Errorf("result lost key from base: %v", got)
		}
		if _, ok := got["j"]; !ok {
			t.Errorf("result lost key from over: %v", got)
		}
		if !reflect.DeepEqual(base, baseBefore) || !reflect.DeepEqual(over, overBefore) {
			t.Errorf("inputs changed: base %v -> %v, over %v -> %v", baseBefore, base, overBefore, over)
		}
	})
}
