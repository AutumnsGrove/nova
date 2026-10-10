// Command echoext is the scripted test extension for the ext package. It is a
// real subprocess speaking the Nova extension protocol; environment variables
// make it misbehave in the ways the matrix needs.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	outMu sync.Mutex
	out   = bufio.NewWriter(os.Stdout)
	trace *os.File
)

// send writes one frame.
func send(v any) {
	outMu.Lock()
	defer outMu.Unlock()
	b, _ := json.Marshal(v)
	out.Write(b)
	out.WriteByte('\n')
	out.Flush()
}

// record appends one received frame type to the trace file when set.
func record(typ string) {
	if trace == nil {
		return
	}
	fmt.Fprintln(trace, typ)
	trace.Sync()
}

// env reports whether a variable is set. A value of 1/true applies to every
// extension; any other non-empty value applies only to the extension whose
// name matches, so one test host can script two extensions differently.
func env(name string) bool {
	v := os.Getenv(name)
	if v == "1" || v == "true" {
		return true
	}
	return v != "" && v == os.Getenv("NOVA_EXTENSION")
}

// envInt reads an integer variable, or the default.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	if p := os.Getenv("ECHO_TRACE"); p != "" {
		trace, _ = os.Create(p)
		defer trace.Close()
	}
	if env("ECHO_CRASH_ON_START") {
		os.Exit(1)
	}
	var child *exec.Cmd
	if env("ECHO_FORK") {
		child = exec.Command("sleep", "60")
		_ = child.Start()
		if trace != nil {
			fmt.Fprintf(trace, "CHILD %d\n", child.Process.Pid)
			trace.Sync()
		}
	}
	_ = child

	calls := 0
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	registered := false
	for sc.Scan() {
		var f map[string]any
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		typ, _ := f["type"].(string)
		record(typ)
		switch typ {
		case "init":
			if names := os.Getenv("ECHO_REPORT_ENV"); names != "" {
				for _, n := range strings.Split(names, ",") {
					send(map[string]any{"type": "log", "level": "info", "text": "ENV " + n + "=" + os.Getenv(n)})
				}
			}
			if env("ECHO_REPORT_FLAGS") {
				b, _ := json.Marshal(f["flags"])
				send(map[string]any{"type": "log", "level": "info", "text": "FLAGS " + string(b)})
			}
			if kind := os.Getenv("ECHO_APPEND"); kind != "" {
				send(map[string]any{"type": "append_entry", "kind": kind, "data": map[string]any{"n": 1}, "keep": true})
			}
			if method := os.Getenv("ECHO_UI"); method != "" {
				send(map[string]any{"type": "ui_request", "id": "u1", "method": method, "title": "t", "message": "m"})
			}
			for i := 0; i < envInt("ECHO_COMPLETE_COUNT", 0); i++ {
				send(map[string]any{"type": "complete", "id": "m" + strconv.Itoa(i), "request": map[string]any{"Model": "x"}})
			}
			if env("ECHO_BIG_FRAME") {
				sendRaw(strings.Repeat("a", 17<<20))
			}
			if env("ECHO_GARBAGE") {
				outMu.Lock()
				fmt.Fprintln(out, "this is not a frame")
				out.Flush()
				outMu.Unlock()
			}
			if !env("ECHO_NO_REGISTER") && !registered {
				sendRegister()
				registered = true
				if env("ECHO_REGISTER_TWICE") {
					sendRegister()
				}
			}
			if env("ECHO_STDERR") {
				fmt.Fprintln(os.Stderr, "this is stderr")
			}
			if env("ECHO_CRASH_AFTER_REGISTER") {
				os.Exit(3)
			}
			if env("ECHO_CRASH_ONCE") {
				marker := os.Getenv("ECHO_CRASH_MARKER")
				if marker != "" {
					if _, err := os.Stat(marker); err != nil {
						_ = os.WriteFile(marker, []byte("1"), 0o644)
						os.Exit(3)
					}
				}
			}
		case "tool_call":
			calls++
			if n := envInt("ECHO_EXIT_AFTER", 0); n > 0 && calls >= n {
				os.Exit(0)
			}
			if ms := envInt("ECHO_SLOW_TOOL_MS", 0); ms > 0 {
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
			id, _ := f["id"].(string)
			name, _ := f["name"].(string)
			args, _ := json.Marshal(f["arguments"])
			reply := map[string]any{"type": "tool_result", "id": id, "content": name + ":" + string(args), "is_error": false}
			send(reply)
			if env("ECHO_ANSWER_TWICE") {
				send(reply)
			}
		case "hook":
			id, _ := f["id"].(string)
			result := map[string]any{}
			event, _ := f["event"].(map[string]any)
			if env("ECHO_HOOK_BLOCK") {
				result["block"] = true
				result["reason"] = envString("ECHO_HOOK_REASON")
			}
			if a := envString("ECHO_HOOK_ARGS"); a != "" {
				var args any
				if json.Unmarshal([]byte(a), &args) == nil {
					result["arguments"] = args
				}
			}
			if env("ECHO_HOOK_ECHO") && event != nil {
				result["arguments"] = event["arguments"]
			}
			if c := envString("ECHO_HOOK_RESULT"); c != "" {
				result["result"] = map[string]any{"content": c}
			}
			if env("ECHO_HOOK_END") {
				result["end"] = true
			}
			if env("ECHO_HOOK_CONTINUE") {
				result["continue"] = true
			}
			if t := envString("ECHO_HOOK_INJECT"); t != "" {
				result["inject"] = []any{map[string]any{"role": "user", "content": t}}
			}
			if env("ECHO_HOOK_NO_ANSWER") {
				continue
			}
			if env("ECHO_HOOK_BAD") {
				send(map[string]any{"type": "hook_result", "id": id, "result": "not an object"})
				continue
			}
			send(map[string]any{"type": "hook_result", "id": id, "result": result})
		case "command":
			id, _ := f["id"].(string)
			send(map[string]any{"type": "command_result", "id": id, "content": "command ok"})
		case "complete_result":
			record("complete_result")
		case "ui_response":
			record("ui_response")
		case "shutdown":
			if env("ECHO_IGNORE_SHUTDOWN") {
				continue
			}
			if trace != nil {
				trace.Close()
			}
			os.Exit(0)
		}
	}
}

// sendRaw writes a raw line to stdout.
func sendRaw(line string) {
	outMu.Lock()
	defer outMu.Unlock()
	out.WriteString(line)
	out.WriteByte('\n')
	out.Flush()
}

// envString reads a variable, preferring the per-extension suffix form
// NAME_<extension> so one host can script two extensions differently.
func envString(name string) string {
	if v := os.Getenv(name + "_" + os.Getenv("NOVA_EXTENSION")); v != "" {
		return v
	}
	return os.Getenv(name)
}

// sendRegister declares the tools, commands, and hooks this run wants.
func sendRegister() {
	tools := []map[string]any{}
	params := map[string]any{"type": "object"}
	if p := os.Getenv("ECHO_TOOL_PARAMS"); p != "" {
		_ = json.Unmarshal([]byte(p), &params)
	}
	for _, name := range splitList(os.Getenv("ECHO_TOOL_NAMES"), "echo") {
		tools = append(tools, map[string]any{
			"name":        name,
			"description": "echoes its arguments",
			"parameters":  params,
			"read_only":   true,
			"parallel":    true,
			"timeout_ms":  envInt("ECHO_TOOL_TIMEOUT_MS", 0),
		})
	}
	hooks := splitList(os.Getenv("ECHO_HOOKS"), "")
	entries := splitList(os.Getenv("ECHO_ENTRIES"), "")
	frame := map[string]any{
		"type":     "register",
		"tools":    tools,
		"commands": []map[string]any{{"name": "hello", "description": "say hello"}},
		"hooks":    hooks,
		"entries":  entries,
	}
	send(frame)
}

// splitList splits a comma list, returning def when empty.
func splitList(s, def string) []string {
	if s == "" {
		if def == "" {
			return nil
		}
		return []string{def}
	}
	return strings.Split(s, ",")
}
