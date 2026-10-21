package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

func TestProvider_PRV29_MergeJSONKeepsNumbersExact(t *testing.T) {
	// 2^53 + 1 does not survive a round trip through float64.
	raw := []byte(`{"model":"m","tools":[{"maximum":9007199254740993,"ratio":1.50}]}`)
	out, err := MergeJSON(raw, map[string]any{"seed": int64(9007199254740995)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`9007199254740993`, `1.50`, `9007199254740995`, `"model":"m"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("merged body %s lacks %s", out, want)
		}
	}
}

func TestProvider_PRV30_MergeJSONOptionsOverrideAndNest(t *testing.T) {
	out, err := MergeJSON([]byte(`{"a":1,"nest":{"x":1,"y":2}}`), map[string]any{"a": 2, "nest": map[string]any{"y": 3}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	nest, _ := got["nest"].(map[string]any)
	if got["a"] != 2.0 || nest["x"] != 1.0 || nest["y"] != 3.0 {
		t.Errorf("merged = %s", out)
	}
}

func TestProvider_PRV31_MergeJSONNoOptionsIsIdentity(t *testing.T) {
	raw := []byte(`{"b":1,"a":2}`)
	out, err := MergeJSON(raw, nil)
	if err != nil || string(out) != string(raw) {
		t.Errorf("out = %s err = %v, want the input untouched", out, err)
	}
	if _, err := MergeJSON([]byte(`[1]`), map[string]any{"a": 1}); err == nil {
		t.Error("a body that is not an object was merged")
	}
}

func TestProvider_PRV32_SplitSystem(t *testing.T) {
	msgs := []message.Message{
		message.System("one"),
		message.User("hi"),
		message.System(""),
		message.System("two"),
		message.Assistant("yo"),
	}
	system, rest := SplitSystem(msgs)
	if system != "one\n\ntwo" {
		t.Errorf("system = %q", system)
	}
	if len(rest) != 2 || rest[0].Content != "hi" || rest[1].Content != "yo" {
		t.Errorf("rest = %+v", rest)
	}
	if system, rest := SplitSystem(nil); system != "" || rest != nil {
		t.Errorf("nil transcript = %q, %v", system, rest)
	}
}

func TestProvider_PRV33_ContentParts(t *testing.T) {
	text := func(s string) string { return "t:" + s }
	image := func(u string) string { return "i:" + u }

	if got := ContentParts(message.User("plain"), text, image); got != nil {
		t.Errorf("a message without parts = %v, want nil so it stays a string", got)
	}
	m := message.UserParts("look",
		message.ImagePart("image/png", "AAAA"),
		message.ContentPart{Type: "audio", URL: "https://x/a"},
		message.ContentPart{Type: message.PartImage},
		message.ContentPart{Type: message.PartImage, URL: "https://x/i.png"},
	)
	got := ContentParts(m, text, image)
	want := []string{"t:look", "i:data:image/png;base64,AAAA", "i:https://x/i.png"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("parts = %v, want %v", got, want)
	}
}

func TestProvider_PRV34_MapTools(t *testing.T) {
	if got := MapTools(nil, func(f ToolFunction) string { return f.Name }); got != nil {
		t.Errorf("no tools = %v, want nil so the field is omitted", got)
	}
	tools := []ToolDef{{Type: "function", Function: ToolFunction{Name: "a"}}, {Type: "function", Function: ToolFunction{Name: "b"}}}
	got := MapTools(tools, func(f ToolFunction) string { return f.Name })
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("names = %v", got)
	}
}
