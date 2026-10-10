package sdk

// Field is one property in an object schema built by the helpers.
type Field struct {
	Name   string
	schema map[string]any
	req    bool
}

// Option tweaks a field.
type Option int

// Required marks a field as required.
const Required Option = 1

// String declares a string property.
func String(name, description string, opts ...Option) Field {
	return field(name, "string", description, opts...)
}

// Int declares an integer property.
func Int(name, description string, opts ...Option) Field {
	return field(name, "integer", description, opts...)
}

// Bool declares a boolean property.
func Bool(name, description string, opts ...Option) Field {
	return field(name, "boolean", description, opts...)
}

// Number declares a number property.
func Number(name, description string, opts ...Option) Field {
	return field(name, "number", description, opts...)
}

// field builds one property.
func field(name, typ, description string, opts ...Option) Field {
	schema := map[string]any{"type": typ}
	if description != "" {
		schema["description"] = description
	}
	f := Field{Name: name, schema: schema}
	for _, o := range opts {
		if o == Required {
			f.req = true
		}
	}
	return f
}

// Object builds an object schema accepted by tool.ValidateArgs.
func Object(fields ...Field) map[string]any {
	props := map[string]any{}
	var required []any
	for _, f := range fields {
		props[f.Name] = f.schema
		if f.req {
			required = append(required, f.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
