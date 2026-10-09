package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryRegisterAndExecute(t *testing.T) {
	reg := NewRegistry()
	t1 := New(Info{Name: "a", Description: "first"}, func(context.Context, json.RawMessage, *Context) (Result, error) {
		return Text("ran a"), nil
	})
	if err := reg.Register(t1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := reg.Register(t1); err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "a" {
		t.Fatalf("names = %v", names)
	}
	if defs := reg.Defs(); len(defs) != 1 || defs[0].Function.Name != "a" {
		t.Fatalf("defs = %+v", defs)
	}

	res := reg.Execute(context.Background(), "a", nil, nil)
	if res.IsError || res.Content != "ran a" {
		t.Fatalf("execute = %+v", res)
	}

	res = reg.Execute(context.Background(), "missing", nil, nil)
	if !res.IsError || !strings.Contains(res.Content, "unknown tool") {
		t.Fatalf("unknown tool result = %+v", res)
	}
}
