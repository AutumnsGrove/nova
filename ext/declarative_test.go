package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

func runDecl(t *testing.T, spec DeclTool, args string) tool.Result {
	t.Helper()
	if spec.TimeoutMS == 0 {
		spec.TimeoutMS = 10_000
	}
	tl := &execTool{dir: t.TempDir(), spec: spec}
	res, err := tl.Run(context.Background(), json.RawMessage(args), &tool.Context{WorkDir: tl.dir})
	if err != nil {
		t.Fatalf("Run returned Go error: %v", err)
	}
	return res
}

func TestDeclarative_DCL01_SimpleExec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("one two three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runDecl(t, DeclTool{Name: "wc", Exec: []string{"wc", "-w", "{{.path}}"}}, `{"path":`+jsonString(path)+`}`)
	if res.IsError || !strings.Contains(res.Content, "3") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDeclarative_DCL02_OneArgvElement(t *testing.T) {
	for _, arg := range []string{"a b", `a"b`, "a;b", "$(whoami)", "a\nb", "a`b", "a|b"} {
		raw, _ := json.Marshal(map[string]string{"arg": arg})
		res := runDecl(t, DeclTool{Name: "p", Exec: []string{"printf", "%s", "{{.arg}}"}}, string(raw))
		if res.Content != arg {
			t.Errorf("arg %q produced %q", arg, res.Content)
		}
	}
}

func TestDeclarative_DCL03_LeadingDash(t *testing.T) {
	res := runDecl(t, DeclTool{Name: "p", Exec: []string{"printf", "%s", "{{.arg}}"}}, `{"arg":"-n"}`)
	if res.Content != "-n" {
		t.Fatalf("content = %q, want -n", res.Content)
	}
}

func TestDeclarative_DCL04_MissingOptional(t *testing.T) {
	argv, err := renderArgv([]string{"cmd", "{{.a}}", "{{.b}}", "--b={{.b}}"}, json.RawMessage(`{"a":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cmd", "x", "--b="}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
}

func TestDeclarative_DCL05_ValueRendering(t *testing.T) {
	raw := `{"n":1.5,"i":7,"b":true,"arr":[1,2],"obj":{"k":"v"}}`
	res := runDecl(t, DeclTool{Name: "p", Exec: []string{"printf", "%s", "{{.n}}|{{.i}}|{{.b}}|{{.arr}}|{{.obj}}"}}, raw)
	want := `1.5|7|true|[1,2]|{"k":"v"}`
	if res.Content != want {
		t.Fatalf("content = %q, want %q", res.Content, want)
	}
}

func TestDeclarative_DCL06_NonZeroExit(t *testing.T) {
	res := runDecl(t, DeclTool{Name: "x", Exec: []string{"sh", "-c", "exit 3"}}, `{}`)
	if !res.IsError || !strings.Contains(res.Content, "3") {
		t.Fatalf("result = %+v", res)
	}
	var details map[string]any
	_ = json.Unmarshal(res.Details, &details)
	if details["exit_code"] != float64(3) {
		t.Fatalf("details = %s", res.Details)
	}
}

func TestDeclarative_DCL07_Timeout(t *testing.T) {
	res := runDecl(t, DeclTool{Name: "s", Exec: []string{"sleep", "5"}, TimeoutMS: 50}, `{}`)
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDeclarative_DCL08_OutputCap(t *testing.T) {
	res := runDecl(t, DeclTool{Name: "big", Exec: []string{"sh", "-c", "printf HEAD; head -c 200000 /dev/zero | tr '\\0' x; printf TAIL"}}, `{}`)
	if !res.Truncated {
		t.Fatalf("not truncated: %q", res.Content[:40])
	}
	if !strings.HasPrefix(res.Content, "HEAD") || !strings.HasSuffix(res.Content, "TAIL") {
		t.Fatalf("head/tail not kept: %q", res.Content[:20])
	}
	if !strings.Contains(res.Content, "bytes omitted") {
		t.Fatal("missing omission marker")
	}
}

func TestDeclarative_DCL09_Render(t *testing.T) {
	res := runDecl(t, DeclTool{Name: "e", Exec: []string{"printf", "%s", "hi"}, Render: "{{.output}}!"}, `{}`)
	var details map[string]any
	if err := json.Unmarshal(res.Details, &details); err != nil {
		t.Fatal(err)
	}
	if details["render"] != "hi!" {
		t.Fatalf("render = %v", details["render"])
	}
}

func TestDeclarative_DCL10_CommandNotFound(t *testing.T) {
	res := runDecl(t, DeclTool{Name: "n", Exec: []string{"definitely-not-a-real-command-xyz"}}, `{}`)
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDeclarative_DCL11_WorkingDirectory(t *testing.T) {
	work := t.TempDir()
	tl := &execTool{dir: t.TempDir(), spec: DeclTool{Name: "pwd", Exec: []string{"pwd"}, TimeoutMS: 5000}}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`), &tool.Context{WorkDir: work})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(res.Content)
	gotReal, err1 := filepath.EvalSymlinks(got)
	wantReal, err2 := filepath.EvalSymlinks(work)
	if err1 == nil && err2 == nil && gotReal != wantReal {
		t.Fatalf("pwd = %q, want %q", gotReal, wantReal)
	}
}

func TestDeclarative_DCL12_ReadOnlyAnnotation(t *testing.T) {
	ro := &execTool{spec: DeclTool{Name: "a", ReadOnly: true}}
	if !ro.Info().ReadOnly {
		t.Fatal("read_only not honoured")
	}
	rw := &execTool{spec: DeclTool{Name: "b"}}
	if rw.Info().ReadOnly {
		t.Fatal("read_only default not false")
	}
}

func FuzzArgTemplate(f *testing.F) {
	f.Add("wc -w {{.path}}", `{"path":"/tmp/x"}`)
	f.Add("{{.a}}{{.b}}", `{"a":"x"}`)
	f.Add("{{.a}}", `{}`)
	f.Fuzz(func(t *testing.T, elem, raw string) {
		argv, err := renderArgv([]string{elem}, json.RawMessage(raw))
		if err != nil {
			return
		}
		if len(argv) > 1 {
			t.Fatalf("one template element gave %d argv elements", len(argv))
		}
	})
}

// jsonString renders s as a JSON string.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
