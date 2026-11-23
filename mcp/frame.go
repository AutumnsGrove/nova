// Package mcp speaks the Model Context Protocol to servers over stdio: one
// JSON-RPC 2.0 message per line in each direction.
//
// v1 is deliberately small (#51): initialize, tools/list and tools/call, with
// no resources, prompts, sampling, HTTP or OAuth. A server is declared in
// config, started as a supervised child process, and its tools are registered
// with the harness like any other tool, so the permission policy applies to
// them unchanged.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	// ProtocolVersion is what Nova asks for in initialize. The server answers
	// with the version it speaks, and that answer is the one in force.
	ProtocolVersion = "2025-06-18"

	// MaxLineBytes caps one message. A server that writes more is disconnected
	// rather than buffered: the line is a runaway log or a hostile payload, and
	// the cap is what stops it becoming memory.
	MaxLineBytes = 16 << 20

	// maxPages bounds a paginated tools/list, so a server that always answers
	// with another cursor cannot spin the loader forever.
	maxPages = 100
)

// Typed transport failures. A caller can tell a broken server from a bad
// argument with errors.Is; an error the server itself reports arrives as
// *RPCError.
var (
	ErrTooLarge  = errors.New("mcp: message over the size cap")
	ErrMalformed = errors.New("mcp: malformed message")
	ErrClosed    = errors.New("mcp: connection closed")
)

// RPCError is an error a server reported for one request.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp: server error %d: %s", e.Code, e.Message)
}

// frame is one JSON-RPC message in either direction. A response carries its ID
// with a result or an error, a notification carries only a method, and a server
// request carries both.
type frame struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *wireError       `json:"error,omitempty"`
}

// wireError is the error object of a JSON-RPC response.
type wireError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// decodeFrame parses one message line. Everything here arrives from another
// program, so the shape is checked rather than trusted, and a fuzz target feeds
// this function directly.
func decodeFrame(line []byte) (*frame, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: empty line", ErrMalformed)
	}
	var f frame
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrMalformed)
	}
	if f.JSONRPC != "" && f.JSONRPC != "2.0" {
		return nil, fmt.Errorf("%w: jsonrpc %q", ErrMalformed, f.JSONRPC)
	}
	if f.ID == nil && f.Method == "" && f.Result == nil && f.Error == nil {
		return nil, fmt.Errorf("%w: no id, method, result or error", ErrMalformed)
	}
	return &f, nil
}

// readFrame reads one message, enforcing MaxLineBytes.
func readFrame(r *bufio.Reader) (*frame, error) {
	line, err := readLimited(r, MaxLineBytes)
	if err != nil {
		return nil, err
	}
	return decodeFrame(line)
}

// readLimited reads one newline-terminated line of at most max bytes. A line
// over the cap fails at the cap, without reading the rest into memory.
func readLimited(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > max {
			return nil, ErrTooLarge
		}
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			return bytes.TrimSuffix(buf, []byte("\n")), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(buf) > 0:
			return nil, fmt.Errorf("%w: truncated message", ErrClosed)
		case errors.Is(err, io.EOF):
			return nil, ErrClosed
		default:
			return nil, fmt.Errorf("%w: %v", ErrClosed, err)
		}
	}
}
