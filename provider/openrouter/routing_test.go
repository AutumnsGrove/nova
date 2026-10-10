package openrouter

import (
	"encoding/json"
	"testing"
)

// ptr returns a pointer to v. Tests use it to tell "unset" from "false"/"0".
func ptr[T any](v T) *T { return &v }

// providerOptions extracts the "provider" object from an Options() result.
func providerOptions(t *testing.T, r Routing) map[string]any {
	t.Helper()
	opts := r.Options()
	if opts == nil {
		return nil
	}
	provider, ok := opts["provider"].(map[string]any)
	if !ok {
		t.Fatalf("Options() has no provider object: %#v", opts)
	}
	return provider
}

// TestRouting_ORT05_DocumentedFields checks that every documented OpenRouter
// routing field is rendered under its wire name and type.
func TestRouting_ORT05_DocumentedFields(t *testing.T) {
	r := Routing{
		Order:                  []string{"a", "b"},
		Only:                   []string{"x"},
		Ignore:                 []string{"y"},
		AllowFallbacks:         ptr(false),
		Sort:                   "price",
		Quantizations:          []string{"fp16"},
		RequireParameters:      ptr(true),
		DataCollection:         "deny",
		ZDR:                    ptr(true),
		MaxPrice:               &Price{Prompt: 1.5, Completion: 2.5},
		PreferredMinThroughput: ptr(10.0),
		PreferredMaxLatency:    ptr(0.5),
	}

	raw, err := json.Marshal(r.Options())
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal options: %v", err)
	}
	provider, _ := got["provider"].(map[string]any)
	want := map[string]any{
		"order":                    []any{"a", "b"},
		"only":                     []any{"x"},
		"ignore":                   []any{"y"},
		"allow_fallbacks":          false,
		"sort":                     "price",
		"quantizations":            []any{"fp16"},
		"require_parameters":       true,
		"data_collection":          "deny",
		"zdr":                      true,
		"max_price":                map[string]any{"prompt": 1.5, "completion": 2.5},
		"preferred_min_throughput": 10.0,
		"preferred_max_latency":    0.5,
	}
	if len(provider) != len(want) {
		t.Fatalf("provider has %d fields, want %d: %#v", len(provider), len(want), provider)
	}
	for k, w := range want {
		got, ok := provider[k]
		if !ok {
			t.Errorf("missing field %q", k)
			continue
		}
		if !jsonEqual(t, got, w) {
			t.Errorf("field %q = %#v, want %#v", k, got, w)
		}
	}
}

// TestRouting_ORT06_OmitsUnset checks that only the fields that are set appear.
func TestRouting_ORT06_OmitsUnset(t *testing.T) {
	provider := jsonObject(t, Routing{Order: []string{"a"}}.Options(), "provider")
	if len(provider) != 1 {
		t.Fatalf("provider = %#v, want only order", provider)
	}
	order, ok := provider["order"].([]any)
	if !ok {
		t.Fatalf("order is %T, want a JSON array", provider["order"])
	}
	if len(order) != 1 || order[0] != "a" {
		t.Errorf("order = %#v, want [a]", order)
	}
}

// TestRouting_ORT07_EmptyIsNil checks the zero Routing renders nothing.
func TestRouting_ORT07_EmptyIsNil(t *testing.T) {
	if got := (Routing{}).Options(); got != nil {
		t.Fatalf("Options() = %#v, want nil", got)
	}
}

// TestRouting_ORT08_BoolFalseDistinct checks a pointer-to-false is emitted and
// an unset pointer is omitted.
func TestRouting_ORT08_BoolFalseDistinct(t *testing.T) {
	set := providerOptions(t, Routing{AllowFallbacks: ptr(false)})
	v, ok := set["allow_fallbacks"]
	if !ok {
		t.Fatal("allow_fallbacks missing when set to false")
	}
	if v != false {
		t.Errorf("allow_fallbacks = %#v, want false", v)
	}
	unset := providerOptions(t, Routing{AllowFallbacks: nil})
	if _, ok := unset["allow_fallbacks"]; ok {
		t.Error("allow_fallbacks present when unset")
	}
}

// TestRouting_ORT09_MaxPriceShape checks max_price is a prompt/completion pair.
func TestRouting_ORT09_MaxPriceShape(t *testing.T) {
	provider := jsonObject(t, Routing{MaxPrice: &Price{Prompt: 0.25, Completion: 1.5}}.Options(), "provider")
	m, ok := provider["max_price"].(map[string]any)
	if !ok {
		t.Fatalf("max_price is %T, want an object", provider["max_price"])
	}
	if m["prompt"] != 0.25 || m["completion"] != 1.5 {
		t.Errorf("max_price = %#v, want {prompt:0.25 completion:1.5}", m)
	}
	if len(m) != 2 {
		t.Errorf("max_price has %d fields, want 2", len(m))
	}
}

// jsonObject marshals v and returns the named top-level object after a JSON
// round trip, so tests assert the wire shape rather than Go's concrete types.
func jsonObject(t *testing.T, v any, key string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj, ok := top[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is not an object: %s", key, raw)
	}
	return obj
}

// jsonEqual compares two JSON-decoded values.
func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	return string(ab) == string(bb)
}

// routingFromBytes builds an arbitrary Routing from fuzz input.
func routingFromBytes(data []byte) Routing {
	strs := func(b []byte) []string {
		if len(b) == 0 {
			return nil
		}
		out := make([]string, 0, len(b))
		for _, c := range b {
			out = append(out, string([]byte{c}))
		}
		return out
	}
	var r Routing
	if len(data) > 0 {
		r.Order = strs(data[:len(data)/2])
		r.Only = strs(data[len(data)/2:])
		r.Sort = string(data)
		r.DataCollection = string(data)
		if data[0]&1 == 1 {
			r.AllowFallbacks = ptr(data[0]&2 == 2)
			r.RequireParameters = ptr(data[0]&4 == 4)
			r.ZDR = ptr(data[0]&8 == 8)
		}
		r.MaxPrice = &Price{Prompt: float64(data[0]), Completion: float64(data[len(data)-1])}
		r.PreferredMinThroughput = ptr(float64(data[0]))
		r.PreferredMaxLatency = ptr(float64(data[len(data)-1]))
	}
	return r
}

// FuzzRoutingOptions asserts any Routing renders to valid JSON.
func FuzzRoutingOptions(f *testing.F) {
	f.Add([]byte("order,only"))
	f.Add([]byte{0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		opts := routingFromBytes(data).Options()
		if opts == nil {
			return
		}
		raw, err := json.Marshal(opts)
		if err != nil {
			t.Fatalf("marshal options: %v", err)
		}
		if !json.Valid(raw) {
			t.Fatalf("options are not valid JSON: %s", raw)
		}
	})
}
