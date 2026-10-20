package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// maxExecOutputBytes caps a declarative tool's merged output.
const maxExecOutputBytes = 50 << 10

// placeholderRe matches one {{.name}} placeholder in a template element.
var placeholderRe = regexp.MustCompile(`\{\{\.([A-Za-z0-9_]+)\}\}`)

// renderArgv substitutes placeholders in the exec template with argument
// values. Each template element yields exactly one argv element, or none when
// it consists solely of a missing optional argument.
func renderArgv(tmpl []string, raw json.RawMessage) ([]string, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tmpl))
	for _, elem := range tmpl {
		rendered, drop := renderElement(elem, args)
		if drop {
			continue
		}
		out = append(out, rendered)
	}
	return out, nil
}

// decodeArgs parses tool arguments into a map, keeping numbers exact. Invalid
// or non-object arguments are an error.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("arguments must be a JSON object")
	}
	return m, nil
}

// renderElement substitutes every placeholder in one element. drop is true
// only when the element is exactly one placeholder whose argument is missing.
func renderElement(elem string, args map[string]any) (rendered string, drop bool) {
	locs := placeholderRe.FindAllStringSubmatchIndex(elem, -1)
	if len(locs) == 0 {
		return elem, false
	}
	soleMissing := len(locs) == 1 && locs[0][0] == 0 && locs[0][1] == len(elem)
	var b strings.Builder
	prev := 0
	present := false
	for _, loc := range locs {
		b.WriteString(elem[prev:loc[0]])
		value, ok := args[elem[loc[2]:loc[3]]]
		if ok {
			present = true
		}
		b.WriteString(renderValue(value))
		prev = loc[1]
	}
	b.WriteString(elem[prev:])
	if soleMissing && !present {
		return "", true
	}
	return b.String(), false
}

// renderValue renders one argument value as a single argv element. A missing
// argument renders as the empty string.
func renderValue(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case json.Number:
		return value.String()
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// renderString substitutes placeholders in a render template. It keeps the
// empty string for a missing value rather than dropping anything.
func renderString(tmpl string, values map[string]any) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[3 : len(match)-2]
		return renderValue(values[name])
	})
}

// execTool runs one declarative exec command as a tool.
type execTool struct {
	dir  string
	spec DeclTool
}

// Info returns the tool's model-facing definition.
func (t *execTool) Info() tool.Info {
	return tool.Info{
		Name:        t.spec.Name,
		Description: t.spec.Description,
		Parameters:  t.spec.Parameters,
		ReadOnly:    t.spec.ReadOnly,
		Parallel:    t.spec.ReadOnly,
	}
}

// Run executes one call. Every command failure becomes an error result.
func (t *execTool) Run(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
	argv, err := renderArgv(t.spec.Exec, args)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if len(argv) == 0 {
		return tool.Errorf("no command to run"), nil
	}
	values, _ := decodeArgs(args)
	work := t.dir
	if tctx != nil && tctx.WorkDir != "" {
		work = tctx.WorkDir
	}
	argv[0] = resolveExec(t.dir, argv[0])
	out := runExec(ctx, work, argv, time.Duration(t.spec.TimeoutMS)*time.Millisecond)
	return t.result(out, values), nil
}

// result shapes one execution into a tool result, with the render template.
func (t *execTool) result(out execOutcome, args map[string]any) tool.Result {
	content := out.content
	render := ""
	if t.spec.Render != "" {
		values := map[string]any{"output": content, "exit_code": out.exit}
		for k, v := range args {
			values[k] = v
		}
		render = renderString(t.spec.Render, values)
	}
	details := map[string]any{"exit_code": out.exit}
	if render != "" {
		details["render"] = render
	}
	raw, _ := json.Marshal(details)
	return tool.Result{Content: content, IsError: out.isError(), Truncated: out.truncated, Details: raw}
}

// resolveExec makes a relative command path absolute against dir, so it runs
// from the session working directory without losing the extension's location.
func resolveExec(dir, cmd string) string {
	if filepath.IsAbs(cmd) || !strings.ContainsAny(cmd, `/\`) {
		return cmd
	}
	return filepath.Join(dir, cmd)
}

// execOutcome is the raw outcome of one exec command.
type execOutcome struct {
	content   string
	exit      int
	timedOut  bool
	cancelled bool
	notFound  bool
	truncated bool
}

// isError reports whether the result should be marked failed.
func (o execOutcome) isError() bool {
	return o.timedOut || o.cancelled || o.notFound || o.exit != 0
}

// runExec runs argv in work with the given timeout, capturing merged output
// with the bash-style cap. On timeout or cancellation it asks the whole process
// group to stop, then kills it after a short wait.
func runExec(ctx context.Context, work string, argv []string, timeout time.Duration) execOutcome {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(callCtx, argv[0], argv[1:]...)
	cmd.Dir = work
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		terminateGroup(cmd.Process.Pid)
		return nil
	}
	cmd.WaitDelay = groupWaitDelay
	buf := newCapBuffer(maxExecOutputBytes)
	cmd.Stdout, cmd.Stderr = buf, buf

	err := cmd.Run()
	if cmd.Process != nil {
		// Whatever the command left running goes with it; after a timeout this
		// is also the kill the grace period was waiting for.
		forceKillGroup(cmd.Process.Pid)
	}
	out := execOutcome{}
	switch {
	case callCtx.Err() != nil:
		out.cancelled = ctx.Err() != nil
		out.timedOut = !out.cancelled
		out.exit = -1
	case errors.Is(err, exec.ErrWaitDelay):
		// The command succeeded but a descendant kept its output open.
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist):
		return execOutcome{content: "error: command not found: " + argv[0], exit: -1, notFound: true}
	case err != nil:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return execOutcome{content: "error: " + err.Error(), exit: -1}
		}
		out.exit = exitCode(err)
	}
	out.content = shapeExecOutput(buf, out, timeout)
	out.truncated = buf.truncated()
	return out
}

// shapeExecOutput adds the failure marker and sanitises the captured output.
func shapeExecOutput(buf *capBuffer, out execOutcome, timeout time.Duration) string {
	content := strings.ToValidUTF8(strings.ReplaceAll(string(buf.bytes()), "\x00", "\uFFFD"), "\uFFFD")
	label := ""
	switch {
	case out.timedOut:
		label = fmt.Sprintf("error: command timed out after %s", timeout)
	case out.cancelled:
		label = "error: command cancelled"
	case out.exit != 0:
		label = fmt.Sprintf("error: command exited with code %d", out.exit)
	}
	if label != "" {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += label
	}
	if content == "" {
		content = "(no output)"
	}
	return content
}

// capBuffer keeps the first 20% and last 80% of a fixed byte budget.
type capBuffer struct {
	headCap, tailCap int
	head, tail       []byte
	total            int
}

func newCapBuffer(limit int) *capBuffer {
	head := limit * 20 / 100
	return &capBuffer{headCap: head, tailCap: limit - head}
}

func (b *capBuffer) Write(in []byte) (int, error) {
	n := len(in)
	b.total += n
	p := in
	if keep := b.headCap - len(b.head); keep > 0 {
		if keep > len(p) {
			keep = len(p)
		}
		b.head = append(b.head, p[:keep]...)
		p = p[keep:]
	}
	if b.tailCap > 0 && len(p) > 0 {
		b.tail = append(b.tail, p...)
		if len(b.tail) > b.tailCap {
			b.tail = b.tail[len(b.tail)-b.tailCap:]
		}
	}
	return n, nil
}

func (b *capBuffer) bytes() []byte {
	out := append([]byte{}, b.head...)
	if omitted := b.total - len(b.head) - len(b.tail); omitted > 0 {
		out = append(out, fmt.Sprintf("\n... (%d bytes omitted) ...\n", omitted)...)
	}
	return append(out, b.tail...)
}

func (b *capBuffer) truncated() bool { return b.total > len(b.head)+len(b.tail) }
