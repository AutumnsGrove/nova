package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxSchemaDepth bounds recursion into nested schemas.
const maxSchemaDepth = 64

// ValidateArgs checks args (raw JSON) against a JSON Schema subset. The error
// text is meant for the model, for example
// "edits[1].oldText: required property is missing". A nil or empty schema
// accepts anything; a malformed schema is ignored rather than rejecting every
// call, and the first malformed keyword logs one debug line.
//
// Supported keywords are type, properties, required, additionalProperties,
// enum, items, minimum, maximum, minLength, maxLength, minItems, and maxItems.
// Every other keyword is ignored, never an error.
func ValidateArgs(schema map[string]any, args json.RawMessage) error {
	value, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(schema) == 0 {
		return nil
	}
	v := &argValidator{}
	return v.check(schema, value, "", 0)
}

// parseArgs decodes raw arguments with numbers kept exact. Empty args become an
// empty object; a literal null stays nil, so a null type can still match it.
func parseArgs(args json.RawMessage) (any, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	if string(trimmed) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return value, nil
}

// argsObject returns args as a JSON object: empty or null becomes {}, and
// anything that is not a JSON object is an error.
func argsObject(args json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(`{}`), nil
	}
	value, err := parseArgs(trimmed)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	return json.RawMessage(trimmed), nil
}

// argValidator validates one call and reports the first malformed keyword once.
type argValidator struct{ logged bool }

func (v *argValidator) bad(schema map[string]any) {
	if v.logged {
		return
	}
	v.logged = true
	slog.Debug("tool: ignoring malformed schema", "schema", schema)
}

// check validates a value, reporting the first problem in check order.
func (v *argValidator) check(schema map[string]any, value any, path string, depth int) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("%s: schema is nested too deeply", at(path))
	}
	if raw, ok := schema["type"]; ok {
		if err := v.checkType(raw, value, path, schema); err != nil {
			return err
		}
	}
	if raw, ok := schema["enum"]; ok {
		if err := v.checkEnum(raw, value, path, schema); err != nil {
			return err
		}
	}
	switch val := value.(type) {
	case map[string]any:
		return v.checkObject(schema, val, path, depth)
	case []any:
		return v.checkArray(schema, val, path, depth)
	case string:
		return v.checkBounds(schema, "minLength", "maxLength", big.NewRat(int64(utf8.RuneCountInString(val)), 1), path)
	case json.Number:
		rat, ok := ratOf(val)
		if !ok {
			return fmt.Errorf("%s: invalid number %q", at(path), val.String())
		}
		return v.checkBounds(schema, "minimum", "maximum", rat, path)
	}
	return nil
}

// checkType enforces the type keyword, which may be one name or a list.
func (v *argValidator) checkType(raw, value any, path string, schema map[string]any) error {
	names, ok := typeNames(raw)
	if !ok {
		v.bad(schema)
		return nil
	}
	var known []string
	for _, name := range names {
		matched, knownType := typeMatch(name, value)
		if !knownType {
			continue
		}
		if matched {
			return nil
		}
		known = append(known, name)
	}
	if len(known) == 0 {
		v.bad(schema)
		return nil
	}
	return fmt.Errorf("%s: expected %s", at(path), strings.Join(known, " or "))
}

// checkEnum enforces membership in the enum list.
func (v *argValidator) checkEnum(raw, value any, path string, schema map[string]any) error {
	list, ok := raw.([]any)
	if !ok {
		v.bad(schema)
		return nil
	}
	for _, allowed := range list {
		if valuesEqual(allowed, value) {
			return nil
		}
	}
	return fmt.Errorf("%s: must be one of %v", at(path), list)
}

// checkObject enforces required, properties, and additionalProperties.
func (v *argValidator) checkObject(schema map[string]any, value map[string]any, path string, depth int) error {
	props, _ := schema["properties"].(map[string]any)
	if _, ok := schema["properties"]; ok && props == nil {
		v.bad(schema)
	}
	if raw, ok := schema["required"]; ok {
		list, ok := raw.([]any)
		if !ok {
			v.bad(schema)
		}
		for _, item := range list {
			name, ok := item.(string)
			if !ok || name == "" {
				v.bad(schema)
				continue
			}
			if _, present := value[name]; !present {
				return fmt.Errorf("%s: required property is missing", at(joinPath(path, name)))
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(value)) {
		sub, declared := props[key].(map[string]any)
		if !declared {
			continue
		}
		if err := v.check(sub, value[key], joinPath(path, key), depth+1); err != nil {
			return err
		}
	}
	if raw, present := schema["additionalProperties"]; present {
		allow, isBool := raw.(bool)
		if !isBool {
			v.bad(schema)
		} else if !allow {
			for _, key := range slices.Sorted(maps.Keys(value)) {
				if _, declared := props[key]; !declared {
					return fmt.Errorf("%s: unknown property %q", at(path), key)
				}
			}
		}
	}
	return nil
}

// checkArray enforces items and the length bounds.
func (v *argValidator) checkArray(schema map[string]any, value []any, path string, depth int) error {
	if raw, ok := schema["items"]; ok {
		sub, isMap := raw.(map[string]any)
		if !isMap {
			v.bad(schema)
		} else {
			for i, item := range value {
				if err := v.check(sub, item, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
					return err
				}
			}
		}
	}
	return v.checkBounds(schema, "minItems", "maxItems", big.NewRat(int64(len(value)), 1), path)
}

// checkBounds enforces an inclusive minimum and maximum if present. It serves
// both numeric ranges and integer counts.
func (v *argValidator) checkBounds(schema map[string]any, minKey, maxKey string, value *big.Rat, path string) error {
	if raw, ok := schema[minKey]; ok {
		want, ok := ratFromAny(raw)
		if !ok {
			v.bad(schema)
		} else if value.Cmp(want) < 0 {
			return fmt.Errorf("%s: must be >= %s", at(path), want.RatString())
		}
	}
	if raw, ok := schema[maxKey]; ok {
		want, ok := ratFromAny(raw)
		if !ok {
			v.bad(schema)
		} else if value.Cmp(want) > 0 {
			return fmt.Errorf("%s: must be <= %s", at(path), want.RatString())
		}
	}
	return nil
}

// typeNames extracts the names from a string or array-of-string type keyword.
func typeNames(raw any) ([]string, bool) {
	switch t := raw.(type) {
	case string:
		return []string{t}, true
	case []any:
		var out []string
		for _, item := range t {
			if name, ok := item.(string); ok {
				out = append(out, name)
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}

// typeMatch reports whether a value satisfies one type name, and whether the
// name is one this validator understands. An unknown name is ignored.
func typeMatch(name string, value any) (matched, known bool) {
	if fn, ok := scalarTypes[name]; ok {
		return fn(value), true
	}
	if name != "integer" {
		return false, false
	}
	n, ok := value.(json.Number)
	if !ok {
		return false, true
	}
	rat, ok := ratOf(n)
	return ok && rat.IsInt(), true
}

// scalarTypes are the one-value type checks, excluding integer, which needs to
// inspect the number.
var scalarTypes = map[string]func(any) bool{
	"object":  func(v any) bool { _, ok := v.(map[string]any); return ok },
	"array":   func(v any) bool { _, ok := v.([]any); return ok },
	"string":  func(v any) bool { _, ok := v.(string); return ok },
	"boolean": func(v any) bool { _, ok := v.(bool); return ok },
	"null":    func(v any) bool { return v == nil },
	"number":  func(v any) bool { _, ok := v.(json.Number); return ok },
}

func ratOf(n json.Number) (*big.Rat, bool) {
	rat, ok := new(big.Rat).SetString(n.String())
	return rat, ok
}

// ratFromAny converts a schema numeric value (from JSON it is a float64) to an
// exact rational so bounds do not round.
func ratFromAny(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		return ratOf(n)
	case float64:
		return new(big.Rat).SetFloat64(n), true
	case int:
		return big.NewRat(int64(n), 1), true
	case int64:
		return big.NewRat(n, 1), true
	}
	return nil, false
}

// valuesEqual compares two JSON values, treating numbers by value.
func valuesEqual(a, b any) bool {
	if ra, ok := ratFromAny(a); ok {
		rb, ok := ratFromAny(b)
		return ok && ra.Cmp(rb) == 0
	}
	return reflect.DeepEqual(a, b)
}

// at names a path in an error message, falling back to the argument object.
func at(path string) string {
	if path == "" {
		return "arguments"
	}
	return path
}

// joinPath appends an object property to a path.
func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}
