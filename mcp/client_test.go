package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serverBin is the scripted MCP server built once for the package.
var serverBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nova-mcpserver")
	if err != nil {
		panic(err)
	}
	serverBin = filepath.Join(dir, "mcpserver")
	out, err := exec.Command("go", "build", "-o", serverBin, "./testdata/mcpserver").CombinedOutput()
	if err != nil {
		panic("build mcpserver: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// options builds a connection to the helper server in one role.
func options(t *testing.T, role string, extra ...string) Options {
	t.Helper()
	return Options{
		Name:    "srv",
		Command: serverBin,
		Args:    append([]string{"-role", role}, extra...),
		Inherit: []string{"PATH=" + os.Getenv("PATH")},
		Timeout: 5 * time.Second,
		Version: "test",
	}
}

// dial connects to the helper server and closes it when the test ends.
func dial(t *testing.T, role string, extra ...string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), options(t, role, extra...))
	if err != nil {
		t.Fatalf("dial %s: %v", role, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClient_MCP01_InitializeHandshake(t *testing.T) {
	c := dial(t, "ok")
	if got, want := c.Version(), ProtocolVersion; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
	// The helper refuses to list tools until it has seen
	// notifications/initialized, so a listing that works proves the client sent
	// it after initialize.
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("tools/list after the handshake: %v", err)
	}
}

func TestClient_MCP01_NegotiatesTheServerVersion(t *testing.T) {
	c := dial(t, "ok", "-proto", "2024-11-05")
	if got, want := c.Version(), "2024-11-05"; got != want {
		t.Fatalf("version = %q, want the server's %q", got, want)
	}
}

func TestClient_MCP03_PaginatedToolList(t *testing.T) {
	c := dial(t, "paginate", "-page", "2")
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("tools = %d, want 6 from three pages of two", len(tools))
	}
	if tools[0].Name != "p0" || tools[5].Name != "p5" {
		t.Fatalf("tools = %v, want p0..p5", []string{tools[0].Name, tools[5].Name})
	}
}

func TestClient_MCP04_CallMapsTextAndError(t *testing.T) {
	c := dial(t, "ok")
	res, err := c.Call(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if res.Content != "echo: hi" || res.IsError {
		t.Fatalf("result = %+v, want the echo text", res)
	}
	failed, err := c.Call(context.Background(), "fail", nil)
	if err != nil {
		t.Fatalf("call fail: %v", err)
	}
	if !failed.IsError || failed.Content != "it broke" {
		t.Fatalf("result = %+v, want an error result carrying the text", failed)
	}
}

func TestClient_MCP05_CallMapsImagesAndOmitsTheRest(t *testing.T) {
	c := dial(t, "ok")
	res, err := c.Call(context.Background(), "image", nil)
	if err != nil {
		t.Fatalf("call image: %v", err)
	}
	if len(res.Parts) != 1 || res.Parts[0].Type != "image" || res.Parts[0].MimeType != "image/png" {
		t.Fatalf("parts = %+v, want one image part", res.Parts)
	}
	if res.Caption == "" {
		t.Fatal("an image result needs a caption for its synthetic message")
	}
	if !strings.Contains(res.Content, "one image follows") {
		t.Fatalf("content = %q, want the text block", res.Content)
	}
	if !strings.Contains(res.Content, "1 resource block(s) omitted") {
		t.Fatalf("content = %q, want an omission note for the resource block", res.Content)
	}
}

func TestClient_MCP07_CallTimesOut(t *testing.T) {
	opt := options(t, "silent")
	opt.Timeout = 200 * time.Millisecond
	c, err := Dial(context.Background(), opt)
	if err != nil {
		t.Fatalf("dial silent: %v", err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.Call(context.Background(), "echo", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call = %v, want a deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v, want the configured 200ms", elapsed)
	}
}

func TestClient_MCP08_MalformedAndOversizeAreTyped(t *testing.T) {
	for _, tc := range []struct {
		role string
		want error
	}{
		{"malformed", ErrMalformed},
		{"oversize", ErrTooLarge},
	} {
		t.Run(tc.role, func(t *testing.T) {
			c := dial(t, tc.role)
			_, err := c.Call(context.Background(), "echo", nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("call = %v, want %v", err, tc.want)
			}
			// A broken server is disconnected, not retried.
			if !errors.Is(c.Err(), tc.want) {
				t.Fatalf("client error = %v, want %v", c.Err(), tc.want)
			}
		})
	}
}

func TestClient_MCP09_ServerErrorIsRPCError(t *testing.T) {
	c := dial(t, "ok")
	_, err := c.Call(context.Background(), "nope", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("call = %v, want an RPCError", err)
	}
	if rpcErr.Code != -32602 {
		t.Fatalf("code = %d, want -32602", rpcErr.Code)
	}
}

// frameOf decodes one frame or fails the test.
func frameOf(t *testing.T, line string) *frame {
	t.Helper()
	f, err := decodeFrame([]byte(line))
	if err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return f
}

func TestFrame_ReadsEveryShape(t *testing.T) {
	if f := frameOf(t, `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`); f.ID == nil || f.Result == nil {
		t.Fatalf("response = %+v", f)
	}
	if f := frameOf(t, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`); f.Method == "" || f.ID != nil {
		t.Fatalf("notification = %+v", f)
	}
	if f := frameOf(t, `{"jsonrpc":"2.0","id":"s1","method":"sampling/createMessage","params":{}}`); f.Method == "" || f.ID == nil {
		t.Fatalf("server request = %+v", f)
	}
	if f := frameOf(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no"}}`); f.Error == nil {
		t.Fatalf("error frame = %+v", f)
	}
}

func TestFrame_RejectsBrokenMessages(t *testing.T) {
	for _, line := range []string{
		``,
		`   `,
		`{"jsonrpc":"2.0"`,
		`{"jsonrpc":"1.0","id":1,"result":{}}`,
		`{}`,
		`{"jsonrpc":"2.0","id":1,"result":{}} {"jsonrpc":"2.0","id":2,"result":{}}`,
	} {
		if _, err := decodeFrame([]byte(line)); !errors.Is(err, ErrMalformed) {
			t.Errorf("decode %q = %v, want ErrMalformed", line, err)
		}
	}
}

func TestFrame_ReadLimitedCapsALine(t *testing.T) {
	long := append(bytes.Repeat([]byte("x"), 64), '\n')
	if _, err := readLimited(bufio.NewReader(bytes.NewReader(long)), 16); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("readLimited = %v, want ErrTooLarge", err)
	}
	line, err := readLimited(bufio.NewReader(strings.NewReader("hello\n")), 16)
	if err != nil || string(line) != "hello" {
		t.Fatalf("readLimited = %q, %v, want hello", line, err)
	}
	if _, err := readLimited(bufio.NewReader(strings.NewReader("")), 16); !errors.Is(err, ErrClosed) {
		t.Fatalf("readLimited on an empty reader = %v, want ErrClosed", err)
	}
}

func TestFrame_ReadLimitedHandlesALongLine(t *testing.T) {
	// A message longer than the bufio buffer but inside the cap still reads.
	body := strings.Repeat("y", 40<<10) + "\n"
	line, err := readLimited(bufio.NewReaderSize(strings.NewReader(body), 4096), MaxLineBytes)
	if err != nil {
		t.Fatalf("readLimited: %v", err)
	}
	if len(line) != 40<<10 {
		t.Fatalf("line = %d bytes, want 40960", len(line))
	}
}

func FuzzDecodeFrame_MCP14(f *testing.F) {
	for _, seed := range []string{
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`,
		`{"jsonrpc":"2.0","id":null,"result":null}`,
		``,
		`[]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		fr, err := decodeFrame(line)
		if err != nil {
			return
		}
		// A frame that decoded must answer the questions the client asks of it
		// without panicking: an id that parses, or one that does not.
		if fr.ID != nil {
			var id int64
			_ = json.Unmarshal(*fr.ID, &id)
		}
		_ = fr.Error
	})
}
