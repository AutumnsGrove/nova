package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxSchemaDepth bounds recursion into nested schemas so a pathological schema
// cannot overflow the stack.
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
	return v.validate(schema, value, "", 0)
}

// parseArgs decodes raw tool arguments with numbers kept exact. Empty and null
// become an empty object, matching the tool contract.
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

// normalizeArgs returns args as a JSON object: empty or null becomes {}, and
// anything that is not a JSON object is an error.
func normalizeArgs(args json.RawMessage) (json.RawMessage, error) {
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

// argValidator validates one call and remembers whether a malformed keyword has
// already been reported.
type argValidator struct {
	logged bool
}

// logMalformed writes one debug line per validation, not one per bad keyword.
func (v *argValidator) logMalformed(schema map[string]any) {
	if v.logged {
		return
	}
	v.logged = true
	slog.Debug("tool: ignoring malformed schema", "schema", schema)
}

// validate checks a value against a schema, reporting the first problem.
func (v *argValidator) validate(schema map[string]any, value any, path string, depth int) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("%s: schema is nested too deeply", at(path))
	}
	if err := v.checkType(schema, value, path); err != nil {
		return err
	}
	if err := v.checkEnum(schema, value, path); err != nil {
		return err
	}
	switch val := value.(type) {
	case map[string]any:
		return v.checkObject(schema, val, path, depth)
	case []any:
		return v.checkArray(schema, val, path, depth)
	case string:
		return v.checkString(schema, val, path)
	case json.Number:
		return v.checkNumber(schema, val, path)
	}
	return nil
}

// checkType enforces the type keyword, which may be one name or a list.
func (v *argValidator) checkType(schema map[string]any, value any, path string) error {
	raw, ok := schema["type"]
	if !ok {
		return nil
	}
	var known []string
	for _, name := range typeNames(raw) {
		if isKnownType(name) {
			known = append(known, name)
		}
	}
	if len(known) == 0 {
		v.logMalformed(schema)
		return nil
	}
	for _, name := range known {
		if typeMatches(name, value) {
			return nil
		}
	}
	return fmt.Errorf("%s: expected %s", at(path), strings.Join(known, " or "))
}

// checkEnum enforces membership in the enum list.
func (v *argValidator) checkEnum(schema map[string]any, value any, path string) error {
	raw, ok := schema["enum"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		v.logMalformed(schema)
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
	if raw, ok := schema["required"]; ok {
		list, ok := raw.([]any)
		if !ok {
			v.logMalformed(schema)
		} else {
			for _, item := range list {
				name, ok := item.(string)
				if !ok {
					v.logMalformed(schema)
					continue
				}
				if _, present := value[name]; !present {
					return fmt.Errorf("%s: required property is missing", at(joinPath(path, name)))
				}
			}
		}
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := schema["properties"]; ok && props == nil {
		v.logMalformed(schema)
	}
	keys := sortedKeys(value)
	for _, key := range keys {
		sub, ok := props[key].(map[string]any)
		if !ok {
			if _, declared := props[key]; declared {
				v.logMalformed(schema)
			}
			continue
		}
		if err := v.validate(sub, value[key], joinPath(path, key), depth+1); err != nil {
			return err
		}
	}
	if raw, ok := schema["additionalProperties"]; ok {
		allow, isBool := raw.(bool)
		switch {
		case isBool && !allow:
			for _, key := range keys {
				if _, declared := props[key]; !declared {
					return fmt.Errorf("%s: unknown property %q", at(path), key)
				}
			}
		case !isBool:
			v.logMalformed(schema)
		}
	}
	return nil
}

// checkArray enforces items and the length bounds.
func (v *argValidator) checkArray(schema map[string]any, value []any, path string, depth int) error {
	if raw, ok := schema["items"]; ok {
		sub, isMap := raw.(map[string]any)
		if !isMap {
			v.logMalformed(schema)
		} else {
			for i, item := range value {
				if err := v.validate(sub, item, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
					return err
				}
			}
		}
	}
	return v.checkBounds(schema, "minItems", "maxItems", big.NewRat(int64(len(value)), 1), path)
}

// checkString enforces the rune-count bounds.
func (v *argValidator) checkString(schema map[string]any, value string, path string) error {
	return v.checkBounds(schema, "minLength", "maxLength", big.NewRat(int64(utf8.RuneCountInString(value)), 1), path)
}

// checkNumber enforces minimum and maximum.
func (v *argValidator) checkNumber(schema map[string]any, value json.Number, path string) error {
	rat, ok := ratOf(value)
	if !ok {
		return fmt.Errorf("%s: invalid number %q", at(path), value.String())
	}
	return v.checkBounds(schema, "minimum", "maximum", rat, path)
}

// checkBounds enforces an inclusive minimum and maximum if present. It serves
// both numeric ranges and integer counts.
func (v *argValidator) checkBounds(schema map[string]any, minKey, maxKey string, value *big.Rat, path string) error {
	if raw, ok := schema[minKey]; ok {
		want, ok := ratFromAny(raw)
		if !ok {
			v.logMalformed(schema)
		} else if value.Cmp(want) < 0 {
			return fmt.Errorf("%s: must be >= %s", at(path), want.RatString())
		}
	}
	if raw, ok := schema[maxKey]; ok {
		want, ok := ratFromAny(raw)
		if !ok {
			v.logMalformed(schema)
		} else if value.Cmp(want) > 0 {
			return fmt.Errorf("%s: must be <= %s", at(path), want.RatString())
		}
	}
	return nil
}

// typeNames extracts the names from a string or array-of-string type keyword.
func typeNames(raw any) []string {
	switch t := raw.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, item := range t {
			if name, ok := item.(string); ok {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// isKnownType reports whether name is one of the supported JSON types.
func isKnownType(name string) bool {
	switch name {
	case "object", "array", "string", "integer", "number", "boolean", "null":
		return true
	}
	return false
}

// typeMatches reports whether a decoded JSON value satisfies one type name.
func typeMatches(name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "integer":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		rat, ok := ratOf(n)
		return ok && rat.IsInt()
	case "number":
		_, ok := value.(json.Number)
		return ok
	}
	return false
}

// ratOf parses a JSON number exactly.
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
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, false
		}
		return new(big.Rat).SetFloat64(n), true
	case float32:
		return new(big.Rat).SetFloat64(float64(n)), true
	case int:
		return big.NewRat(int64(n), 1), true
	case int64:
		return big.NewRat(n, 1), true
	case int32:
		return big.NewRat(int64(n), 1), true
	case uint:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(uint64(n))), true
	case uint64:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(n)), true
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

// sortedKeys returns the map's keys in a deterministic order so the first
// reported problem does not depend on Go's map iteration.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
