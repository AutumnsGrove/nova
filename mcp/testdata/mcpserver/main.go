// Command mcpserver is a Model Context Protocol server over stdio, scripted for
// the mcp package's tests. It speaks newline-delimited JSON-RPC 2.0 and can
// misbehave on purpose: exit at once, stay silent on a call, answer with
// malformed JSON, write a line past the size cap, page a long tool list, send
// tools/list_changed, or ignore SIGTERM.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// defaultProtocol is the version the server answers with unless -proto says
// otherwise, so a test can prove the client adopts the server's answer.
const defaultProtocol = "2025-06-18"

// pngBase64 is a 1x1 transparent PNG, so an image block needs no file.
const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg=="

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// block is one content block of a call result.
type block struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

func main() {
	role := flag.String("role", "ok", "behaviour: ok, exit, silent, malformed, oversize, paginate, listchanged, ignore-term")
	page := flag.Int("page", 2, "tools per page in the paginate role")
	proto := flag.String("proto", defaultProtocol, "protocol version to answer initialize with")
	flag.Parse()

	switch *role {
	case "exit":
		os.Exit(3)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
	}

	s := &server{role: *role, page: *page, proto: *proto, out: bufio.NewWriter(os.Stdout), tools: baseTools()}
	if err := s.serve(); err != nil {
		fmt.Fprintln(os.Stderr, "mcpserver:", err)
		os.Exit(1)
	}
}

// server is the scripted implementation.
type server struct {
	role    string
	page    int
	proto   string
	ready   bool
	out     *bufio.Writer
	tools   []wireTool
	paged   bool
	changed bool
}

// baseTools are the tools every role serves.
func baseTools() []wireTool {
	return []wireTool{
		{
			Name:        "echo",
			Description: "Echo the text back.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			Name:        "image",
			Description: "Return one image.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: map[string]any{"readOnlyHint": true},
		},
		{
			Name:        "fail",
			Description: "Fail on purpose.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true},
		},
		{
			Name:        "env",
			Description: "Return the value of one environment variable.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			Annotations: map[string]any{"readOnlyHint": true},
		},
	}
}

func (s *server) serve() error {
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var msg frame
			if jsonErr := json.Unmarshal(line, &msg); jsonErr != nil {
				return fmt.Errorf("decode %q: %w", line, jsonErr)
			}
			s.handle(msg)
		}
		if err != nil {
			if s.role == "ignore-term" {
				// Ignore SIGTERM and outlive stdin: only SIGKILL ends this one,
				// which is what the escalation test needs. A sleeping loop, not
				// an empty select, because the Go runtime calls that a deadlock.
				for {
					time.Sleep(time.Hour)
				}
			}
			return nil
		}
	}
}

func (s *server) handle(msg frame) {
	if s.role == "noinit" {
		// Reads and answers nothing at all, so a caller's handshake timeout is
		// the only way out of the dial.
		return
	}
	switch msg.Method {
	case "initialize":
		s.respond(msg.ID, map[string]any{
			"protocolVersion": s.proto,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": s.role == "listchanged"}},
			"serverInfo":      map[string]any{"name": "mcpserver", "version": "1.0.0"},
		})
		if s.role == "crash-after-init" {
			// Dies after the handshake and before answering tools/list, so a
			// death lands while the first listing is in flight.
			os.Exit(5)
		}
	case "notifications/initialized":
		// A notification carries no id and no answer. Nothing else is served
		// until it arrives, so a client that skips it cannot use the server.
		s.ready = true
	case "tools/list", "tools/call":
		if !s.ready {
			s.write(frame{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: -32002, Message: "not initialized"}})
			return
		}
		if msg.Method == "tools/list" {
			s.toolsList(msg)
		} else {
			s.toolsCall(msg)
		}
	default:
		if len(msg.ID) > 0 && string(msg.ID) != "null" {
			s.write(frame{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + msg.Method}})
		}
	}
}

func (s *server) toolsList(msg frame) {
	if s.role == "paginate" && !s.paged {
		s.paged = true
		s.listPage(msg)
		return
	}
	// A cursor past the last page ends a paginated listing.
	var params struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(msg.Params, &params)
	if s.role == "paginate" && params.Cursor != "" {
		s.listPage(msg)
		return
	}
	s.respond(msg.ID, map[string]any{"tools": s.tools})
	if s.role == "crash-after-list" {
		// Dies once its tools are registered, so a host can be caught holding a
		// connection that is already gone.
		os.Exit(4)
	}
	if s.role == "listchanged" && !s.changed {
		s.changed = true
		s.tools = append(s.tools, wireTool{
			Name:        "late",
			Description: "Appeared after the first listing.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		})
		s.write(frame{JSONRPC: "2.0", Method: "notifications/tools/list_changed"})
	}
}

// listPage answers one page of the paginate role: -page tools per page, three
// pages, then an empty cursor.
func (s *server) listPage(msg frame) {
	var params struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(msg.Params, &params)
	next := 0
	if params.Cursor != "" {
		next, _ = strconv.Atoi(params.Cursor)
	}
	page := make([]wireTool, 0, s.page)
	for i := 0; i < s.page; i++ {
		n := next*s.page + i
		page = append(page, wireTool{
			Name:        fmt.Sprintf("p%d", n),
			Description: fmt.Sprintf("Paged tool %d.", n),
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		})
	}
	result := map[string]any{"tools": page}
	if next+1 < 3 {
		result["nextCursor"] = strconv.Itoa(next + 1)
	}
	s.respond(msg.ID, result)
}

func (s *server) toolsCall(msg frame) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(msg.Params, &params)

	switch s.role {
	case "silent":
		return
	case "malformed":
		fmt.Fprint(s.out, "{\"jsonrpc\":\"2.0\",\"result\":\n")
		_ = s.out.Flush()
		return
	case "oversize":
		s.respond(msg.ID, map[string]any{"content": []block{{Type: "text", Text: strings.Repeat("x", 20<<20)}}})
		return
	}

	switch params.Name {
	case "echo":
		text, _ := params.Arguments["text"].(string)
		s.respond(msg.ID, map[string]any{"content": []block{{Type: "text", Text: "echo: " + text}}})
	case "image":
		s.respond(msg.ID, map[string]any{"content": []block{
			{Type: "text", Text: "one image follows"},
			{Type: "image", Data: pngBase64, MimeType: "image/png"},
			{Type: "resource", Text: "ignored"},
		}})
	case "fail":
		s.respond(msg.ID, map[string]any{"content": []block{{Type: "text", Text: "it broke"}}, "isError": true})
	case "env":
		name, _ := params.Arguments["name"].(string)
		s.respond(msg.ID, map[string]any{"content": []block{{Type: "text", Text: os.Getenv(name)}}})
	case "slow":
		// No answer at all, so the caller's timeout is the only way out.
	default:
		s.write(frame{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: -32602, Message: "unknown tool: " + params.Name}})
	}
}

func (s *server) respond(id json.RawMessage, result any) {
	s.write(frame{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) write(f frame) {
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	s.out.Write(data)
	s.out.WriteByte('\n')
	_ = s.out.Flush()
}
