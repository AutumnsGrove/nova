//go:build unix

package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServeCommand_ShutdownOnSignal starts the real binary, opens an event
// stream, and sends a shutdown signal. A signal is a shutdown, not a kill: the
// process must exit 0 and the stream must end with the closing comment (#126).
func TestServeCommand_ShutdownOnSignal(t *testing.T) {
	// Both build flavours run the tree through a different execute: the full
	// build goes through fang, the minimal one through cobra alone, and the
	// signal context has to reach the command from either.
	for _, flavour := range []struct {
		name string
		tags []string
	}{
		{"full", nil},
		{"minimal", []string{"notui"}},
	} {
		for _, tc := range []struct {
			name string
			sig  syscall.Signal
		}{
			{"SIGTERM", syscall.SIGTERM},
			{"SIGINT", syscall.SIGINT},
		} {
			t.Run(flavour.name+"/"+tc.name, func(t *testing.T) {
				bin := builtNova(t, flavour.tags...)
				addr := freeAddr(t)
				cmd := exec.Command(bin, "serve", "--listen", addr)
				cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "OPENROUTER_API_KEY=", "NO_COLOR=1")
				var logs strings.Builder
				cmd.Stdout, cmd.Stderr = &logs, &logs
				if err := cmd.Start(); err != nil {
					t.Fatalf("start serve: %v", err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				exited := make(chan error, 1)
				go func() { exited <- cmd.Wait() }()

				waitListening(t, addr)
				resp, err := http.Get("http://" + addr + "/v1/events")
				if err != nil {
					t.Fatalf("events: %v\n%s", err, logs.String())
				}
				defer resp.Body.Close()

				if err := cmd.Process.Signal(tc.sig); err != nil {
					t.Fatalf("signal %s: %v", tc.name, err)
				}
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("read stream: %v\n%s", err, logs.String())
				}
				if !strings.HasSuffix(string(body), ": closing\n\n") {
					t.Fatalf("stream ended with %q, want the closing comment", tail(body))
				}
				select {
				case err := <-exited:
					if err != nil {
						t.Fatalf("serve did not exit cleanly on %s: %v\n%s", tc.name, err, logs.String())
					}
				case <-time.After(10 * time.Second):
					t.Fatalf("serve did not exit after %s\n%s", tc.name, logs.String())
				}
			})
		}
	}
}

// freeAddr reserves a loopback port and releases it for serve to bind.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return addr
}

// waitListening polls until the address accepts a connection. A subprocess and
// a socket are real I/O, so this is the one thing a bounded poll can wait for:
// the binary reports its readiness nowhere else (docs/TESTING.md section 7).
func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve never listened on %s: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tail returns the end of a stream for a failure message that stays readable.
func tail(body []byte) string {
	if len(body) > 40 {
		body = body[len(body)-40:]
	}
	return string(body)
}
