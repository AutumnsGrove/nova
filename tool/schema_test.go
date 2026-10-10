package tool

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// validate parses schema and args as JSON and validates, failing the test on a
// schema parse error. Numbers are decoded as json.Number so large bounds keep
// their precision, exactly as a caller building Parameters from JSON would.
func validate(t *testing.T, schema, args string) error {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(schema))
	dec.UseNumber()
	var s map[string]any
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("parse schema %s: %v", schema, err)
	}
	return ValidateArgs(s, json.RawMessage(args))
}

// TestValidateArgs_SCH01_Types checks each supported type accepts its value and
// rejects a mismatched one.
func TestValidateArgs_SCH01_Types(t *testing.T) {
	cases := []struct {
		typ, good, bad string
	}{
		{"string", `"x"`, `1`},
		{"integer", `3`, `3.5`},
		{"number", `3.5`, `"3"`},
		{"boolean", `true`, `"true"`},
		{"array", `[1]`, `{"a":1}`},
		{"object", `{"a":1}`, `[1]`},
		{"null", `null`, `1`},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			schema := `{"type":"` + tc.typ + `"}`
			if err := validate(t, schema, tc.good); err != nil {
				t.Errorf("valid %s rejected: %v", tc.good, err)
			}
			if err := validate(t, schema, tc.bad); err == nil {
				t.Errorf("invalid %s accepted", tc.bad)
			}
		})
	}
}

// TestValidateArgs_SCH02_Integer checks integer accepts whole floats and rejects
// fractions and numeric strings.
func TestValidateArgs_SCH02_Integer(t *testing.T) {
	schema := `{"type":"integer"}`
	if err := validate(t, schema, `3.0`); err != nil {
		t.Errorf("3.0 rejected: %v", err)
	}
	for _, bad := range []string{`3.5`, `"3"`} {
		if err := validate(t, schema, bad); err == nil {
			t.Errorf("%s accepted as integer", bad)
		}
	}
}

// TestValidateArgs_SCH03_LargeIntegers checks values around 2^53 are not rounded.
func TestValidateArgs_SCH03_LargeIntegers(t *testing.T) {
	schema := `{"type":"integer","minimum":9007199254740992,"maximum":9007199254740993}`
	if err := validate(t, schema, `9007199254740992`); err != nil {
		t.Errorf("2^53 rejected: %v", err)
	}
	if err := validate(t, schema, `9007199254740993`); err != nil {
		t.Errorf("2^53+1 rejected: %v", err)
	}
	if err := validate(t, schema, `9007199254740994`); err == nil {
		t.Error("2^53+2 accepted above the maximum")
	}
}

// TestValidateArgs_SCH04_Required checks a missing required property is named.
func TestValidateArgs_SCH04_Required(t *testing.T) {
	err := validate(t, `{"type":"object","required":["field"]}`, `{}`)
	if err == nil || !strings.Contains(err.Error(), "field: required property is missing") {
		t.Fatalf("error = %v, want the required message", err)
	}
}

// TestValidateArgs_SCH05_NestedPath checks the path includes an array index.
func TestValidateArgs_SCH05_NestedPath(t *testing.T) {
	schema := `{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","required":["oldText"]}}}}`
	err := validate(t, schema, `{"edits":[{"oldText":"a"},{}]}`)
	if err == nil || !strings.Contains(err.Error(), "edits[1].oldText") {
		t.Fatalf("error = %v, want edits[1].oldText", err)
	}
}

// TestValidateArgs_SCH06_AdditionalProperties checks extra keys are rejected by
// name when the schema forbids them.
func TestValidateArgs_SCH06_AdditionalProperties(t *testing.T) {
	err := validate(t, `{"type":"object","properties":{"a":{}},"additionalProperties":false}`, `{"a":1,"b":2}`)
	if err == nil || !strings.Contains(err.Error(), "b") {
		t.Fatalf("error = %v, want the extra key named", err)
	}
}

// TestValidateArgs_SCH07_AdditionalDefault checks unknown keys are allowed by default.
func TestValidateArgs_SCH07_AdditionalDefault(t *testing.T) {
	if err := validate(t, `{"type":"object","properties":{"a":{}}}`, `{"a":1,"b":2}`); err != nil {
		t.Fatalf("extra key rejected: %v", err)
	}
}

// TestValidateArgs_SCH08_Enum checks an out-of-enum value is rejected and the
// allowed values are listed.
func TestValidateArgs_SCH08_Enum(t *testing.T) {
	err := validate(t, `{"type":"string","enum":["a","b"]}`, `"c"`)
	if err == nil || !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("error = %v, want the allowed values listed", err)
	}
}

// TestValidateArgs_SCH09_Items checks a bad element is rejected with its index.
func TestValidateArgs_SCH09_Items(t *testing.T) {
	err := validate(t, `{"type":"array","items":{"type":"integer"}}`, `[1,"x"]`)
	if err == nil || !strings.Contains(err.Error(), "[1]") {
		t.Fatalf("error = %v, want index [1]", err)
	}
}

// TestValidateArgs_SCH10_MinMax checks bounds are inclusive.
func TestValidateArgs_SCH10_MinMax(t *testing.T) {
	schema := `{"type":"integer","minimum":2,"maximum":4}`
	for _, good := range []string{`2`, `3`, `4`} {
		if err := validate(t, schema, good); err != nil {
			t.Errorf("%s rejected: %v", good, err)
		}
	}
	for _, bad := range []string{`1`, `5`} {
		if err := validate(t, schema, bad); err == nil {
			t.Errorf("%s accepted out of range", bad)
		}
	}
}

// TestValidateArgs_SCH11_LengthRunes checks length counts runes, not bytes.
func TestValidateArgs_SCH11_LengthRunes(t *testing.T) {
	if err := validate(t, `{"type":"string","maxLength":1}`, `"é"`); err != nil {
		t.Fatalf("one rune rejected: %v", err)
	}
	if err := validate(t, `{"type":"string","minLength":2}`, `"é"`); err == nil {
		t.Fatal("one rune accepted for minLength 2")
	}
}

// TestValidateArgs_SCH12_MinItems checks array length bounds.
func TestValidateArgs_SCH12_MinItems(t *testing.T) {
	schema := `{"type":"array","minItems":1,"maxItems":2}`
	if err := validate(t, schema, `[1]`); err != nil {
		t.Errorf("one item rejected: %v", err)
	}
	if err := validate(t, schema, `[]`); err == nil {
		t.Error("empty array accepted for minItems 1")
	}
	if err := validate(t, schema, `[1,2,3]`); err == nil {
		t.Error("three items accepted for maxItems 2")
	}
}

// TestValidateArgs_SCH13_TypeArray checks a list of types accepts any of them.
func TestValidateArgs_SCH13_TypeArray(t *testing.T) {
	schema := `{"type":["string","null"]}`
	if err := validate(t, schema, `"x"`); err != nil {
		t.Errorf("string rejected: %v", err)
	}
	if err := validate(t, schema, `null`); err != nil {
		t.Errorf("null rejected: %v", err)
	}
	if err := validate(t, schema, `5`); err == nil {
		t.Error("number accepted by [string,null]")
	}
}

// TestValidateArgs_SCH14_UnknownKeywords checks pattern/format/$ref are ignored.
func TestValidateArgs_SCH14_UnknownKeywords(t *testing.T) {
	schema := `{"type":"string","pattern":"^z$","format":"email","$ref":"#/x"}`
	if err := validate(t, schema, `"not matching"`); err != nil {
		t.Fatalf("unknown keywords caused an error: %v", err)
	}
}

// TestValidateArgs_SCH15_MalformedSchema checks a malformed schema is ignored
// and a debug line is logged once.
func TestValidateArgs_SCH15_MalformedSchema(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	if err := validate(t, `{"type":5,"minimum":"x"}`, `{"anything":true}`); err != nil {
		t.Fatalf("malformed schema rejected the call: %v", err)
	}
	if got := strings.Count(buf.String(), "malformed"); got != 1 {
		t.Fatalf("logged %d times, want once: %s", got, buf.String())
	}
}

// TestValidateArgs_SCH16_NilAndEmptySchema checks both accept anything.
func TestValidateArgs_SCH16_NilAndEmptySchema(t *testing.T) {
	if err := ValidateArgs(nil, json.RawMessage(`{"a":1}`)); err != nil {
		t.Errorf("nil schema rejected: %v", err)
	}
	if err := ValidateArgs(map[string]any{}, json.RawMessage(`[1,2]`)); err != nil {
		t.Errorf("empty schema rejected: %v", err)
	}
}

// TestValidateArgs_SCH17_FirstProblem checks the first problem in check order
// is the one reported.
func TestValidateArgs_SCH17_FirstProblem(t *testing.T) {
	schema := `{"type":"object","required":["a"],"properties":{"b":{"type":"string"}}}`
	err := validate(t, schema, `{"b":5}`)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %v, want the required problem first", err)
	}
}

// TestValidateArgs_SCH18_Null checks a null value is rejected by a non-null type.
func TestValidateArgs_SCH18_Null(t *testing.T) {
	if err := validate(t, `{"type":"object","properties":{"a":{"type":"string"}}}`, `{"a":null}`); err == nil {
		t.Fatal("null accepted for a string property")
	}
}

// TestValidateArgs_SCH19_DuplicateKeys checks duplicate JSON keys do not panic
// and the last wins.
func TestValidateArgs_SCH19_DuplicateKeys(t *testing.T) {
	schema := `{"type":"object","properties":{"a":{"type":"integer"}}}`
	if err := validate(t, schema, `{"a":1,"a":2}`); err != nil {
		t.Fatalf("duplicate keys rejected: %v", err)
	}
	if err := validate(t, schema, `{"a":"x","a":2}`); err != nil {
		t.Fatalf("last value did not win: %v", err)
	}
}

// TestValidateArgs_SCH20_DeepArgs checks a deeply nested argument does not
// overflow the stack.
func TestValidateArgs_SCH20_DeepArgs(t *testing.T) {
	var b strings.Builder
	const depth = 1000
	for i := 0; i < depth; i++ {
		b.WriteString(`{"a":`)
	}
	b.WriteString(`1`)
	for i := 0; i < depth; i++ {
		b.WriteByte('}')
	}
	if err := ValidateArgs(map[string]any{"type": "object"}, json.RawMessage(b.String())); err != nil {
		// An error is acceptable for very deep input, as long as it does not panic.
		t.Logf("deep args returned: %v", err)
	}
}

// FuzzValidateArgs checks no schema or argument combination panics.
func FuzzValidateArgs(f *testing.F) {
	f.Add(`{"type":"object","required":["a"]}`, `{"a":1}`)
	f.Add(`{"type":"array","items":{"type":"integer"}}`, `[1,2,"x"]`)
	f.Add(`{"type":5}`, `[1]`)
	f.Fuzz(func(t *testing.T, schemaJSON, args string) {
		var schema map[string]any
		_ = json.Unmarshal([]byte(schemaJSON), &schema)
		_ = ValidateArgs(schema, json.RawMessage(args))
	})
}
