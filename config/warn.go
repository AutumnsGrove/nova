package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// unknownKeys reports every key in the merged file layers that Config does not
// define. It walks the struct's yaml tags, so nested problems are named by
// dotted path. Keys of map-typed fields are always accepted.
func unknownKeys(m map[string]any, source string) []Warning {
	var out []Warning
	walkUnknown(m, reflect.TypeOf(Config{}), "", source, &out)
	return out
}

// walkUnknown checks one map against one type. prefix is the dotted path so far.
func walkUnknown(m map[string]any, t reflect.Type, prefix, source string, out *[]Warning) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
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
		if indirect(t.Elem()).Kind() == reflect.Struct {
			if mm, ok := value.(map[string]any); ok {
				for _, item := range sortedValues(mm) {
					if im, ok := item.(map[string]any); ok {
						walkUnknown(im, indirect(t.Elem()), dotted, source, out)
					}
				}
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

// sortedValues returns a map's values in key order so recursion is deterministic.
func sortedValues(m map[string]any) []any {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, m[key])
	}
	return out
}

// fieldType finds a struct field by its yaml name.
func fieldType(t reflect.Type, name string) (reflect.Type, bool) {
	t = indirect(t)
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		if yamlName(field) == name {
			return field.Type, true
		}
	}
	return nil, false
}

// yamlName is the key a field uses in YAML.
func yamlName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	name := strings.Split(tag, ",")[0]
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

// setDotted assigns a flags value into the config at a dotted key path. The
// leading "bash." alias maps onto tools.bash, matching the documented flag names.
func setDotted(cfg *Config, dotted string, value any) error {
	if strings.HasPrefix(dotted, "bash.") {
		dotted = "tools." + dotted
	}
	parts := strings.Split(dotted, ".")
	v := reflect.ValueOf(cfg).Elem()
	for i, part := range parts {
		v = derefValue(v)
		last := i == len(parts)-1
		switch v.Kind() {
		case reflect.Struct:
			fv, ok := fieldValue(v, part)
			if !ok {
				return fmt.Errorf("unknown flag: %s", strings.Join(parts[:i+1], "."))
			}
			if last {
				return assign(fv, value, part)
			}
			v = fv
		case reflect.Map:
			if !last {
				return fmt.Errorf("unknown flag: %s", strings.Join(parts[:i+1], "."))
			}
			return assignMap(v, part, value)
		default:
			return fmt.Errorf("unknown flag: %s", strings.Join(parts[:i+1], "."))
		}
	}
	return nil
}

// fieldValue finds an addressable struct field by yaml name.
func fieldValue(v reflect.Value, name string) (reflect.Value, bool) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		if yamlName(field) == name {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// derefValue allocates and dereferences a pointer field so it can be assigned.
func derefValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

// assign coerces a flags value into a field.
func assign(field reflect.Value, value any, name string) error {
	if value == nil {
		field.Set(reflect.Zero(field.Type()))
		return nil
	}
	rv := reflect.ValueOf(value)
	switch field.Kind() {
	case reflect.String:
		if rv.Kind() == reflect.String {
			field.SetString(rv.String())
			return nil
		}
	case reflect.Bool:
		if b, ok := coerceBool(value); ok {
			field.SetBool(b)
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, ok := coerceInt(value); ok {
			field.SetInt(n)
			return nil
		}
	case reflect.Pointer:
		elem := reflect.New(field.Type().Elem())
		if err := assign(elem.Elem(), value, name); err != nil {
			return err
		}
		field.Set(elem)
		return nil
	case reflect.Slice:
		return assignSlice(field, value)
	}
	return fmt.Errorf("invalid value for flag %q", name)
}

// assignSlice converts a list value into a string slice field.
func assignSlice(field reflect.Value, value any) error {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return fmt.Errorf("invalid value for flag %q", field.Type())
	}
	out := reflect.MakeSlice(field.Type(), rv.Len(), rv.Len())
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i)
		if elem.Kind() == reflect.Interface {
			elem = elem.Elem()
		}
		if elem.Kind() != reflect.String {
			return fmt.Errorf("invalid value for flag %q", field.Type())
		}
		out.Index(i).SetString(elem.String())
	}
	field.Set(out)
	return nil
}

// assignMap sets one key in a map-typed field.
func assignMap(field reflect.Value, key string, value any) error {
	if field.Type().Key().Kind() != reflect.String || field.Type().Elem().Kind() != reflect.String {
		return fmt.Errorf("invalid value for flag %q", key)
	}
	if field.IsNil() {
		field.Set(reflect.MakeMap(field.Type()))
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.String {
		return fmt.Errorf("invalid value for flag %q", key)
	}
	field.SetMapIndex(reflect.ValueOf(key), rv)
	return nil
}

// coerceBool accepts a bool or a parseable string.
func coerceBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	}
	return false, false
}

// coerceInt accepts integer and float kinds and parseable strings.
func coerceInt(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}
