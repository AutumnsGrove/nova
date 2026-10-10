package mode

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
)

func TestRPC_RPC01_HelloFirst(t *testing.T) {
	s, _ := testServer(t)
	var out bytes.Buffer
	if err := ServeRPC(context.Background(), s, strings.NewReader(""), &out); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}
	line, _, _ := strings.Cut(out.String(), "\n")
	var hello proto.Hello
	if err := json.Unmarshal([]byte(line), &hello); err != nil {
		t.Fatalf("hello decode: %v (line %q)", err, line)
	}
	if hello.Type != proto.FrameHello || hello.Version != proto.Version {
		t.Fatalf("hello = %+v", hello)
	}
}

func TestRPC_RPC04_MalformedLine(t *testing.T) {
	s, _ := testServer(t)
	var out bytes.Buffer
	if err := ServeRPC(context.Background(), s, strings.NewReader("not json\n"), &out); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(proto.CodeBadRequest)) {
		t.Fatalf("output should carry a bad_request response:\n%s", out.String())
	}
}
