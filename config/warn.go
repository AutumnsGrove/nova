package config

import (
	"reflect"
	"sort"
	"strings"
)

// unknownKeys reports every key in the merged layers that Config does not
// define. It walks the struct's yaml tags, so nested problems are named by
// dotted path. Keys of map-typed fields are always accepted.
func unknownKeys(m map[string]any, source string) []Warning {
	var out []Warning
	walkUnknown(m, reflect.TypeOf(Config{}), "", source, &out)
	return out
}

// walkUnknown checks one map against one type. prefix is the dotted path so far.
func walkUnknown(m map[string]any, t reflect.Type, prefix, source string, out *[]Warning) {
	for _, key := range sortedKeys(m) {
		dotted := key
		if prefix != "" {
			dotted = prefix + "." + key
		}
		ft, ok := fieldType(t, key)
		if !ok {
			*out = append(*out, Warning{Source: source, Message: "unknown key: " + dotted})
			continue
		}
		walkUnknownValue(m[key], ft, dotted, source, out)
	}
}

// walkUnknownValue checks a nested value against its field type.
func walkUnknownValue(value any, t reflect.Type, dotted, source string, out *[]Warning) {
	t = indirect(t)
	switch t.Kind() {
	case reflect.Map:
		// A map's keys are free, but a struct-valued map still has per-entry
		// fields worth checking.
		if indirect(t.Elem()).Kind() != reflect.Struct {
			return
		}
		entries, ok := value.(map[string]any)
		if !ok {
			return
		}
		for _, key := range sortedKeys(entries) {
			if item, ok := entries[key].(map[string]any); ok {
				walkUnknown(item, indirect(t.Elem()), dotted, source, out)
			}
		}
	case reflect.Slice, reflect.Array:
		elem := indirect(t.Elem())
		if elem.Kind() != reflect.Struct {
			return
		}
		list, ok := value.([]any)
		if !ok {
			return
		}
		for _, item := range list {
			if im, ok := item.(map[string]any); ok {
				walkUnknown(im, elem, dotted, source, out)
			}
		}
	case reflect.Struct:
		if im, ok := value.(map[string]any); ok {
			walkUnknown(im, t, dotted, source, out)
		}
	}
}

// sortedKeys returns a map's keys in order so walking is deterministic.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// fieldType finds a struct field by its yaml name.
func fieldType(t reflect.Type, name string) (reflect.Type, bool) {
	t = indirect(t)
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.IsExported() && yamlName(field) == name {
			return field.Type, true
		}
	}
	return nil, false
}

// yamlName is the key a field uses in YAML.
func yamlName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("yaml"), ",")[0]
	if name == "" {
		return strings.ToLower(field.Name)
	}
	return name
}

// indirect dereferences a pointer type.
func indirect(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
