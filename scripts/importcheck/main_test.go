package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMod is a third-party module written into the fixture and wired up with a
// local replace, so the test needs no network and no module cache.
type fakeMod struct {
	modulePath string
	dir        string
	pkgName    string
}

// fixtureModule writes a main module at example.com/m with the given packages
// plus any faked third-party modules, and returns the fixture root.
func fixtureModule(t *testing.T, pkgs map[string]string, mods ...fakeMod) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{}
	var require, replace strings.Builder
	for _, m := range mods {
		files[filepath.Join(m.dir, "go.mod")] = fmt.Sprintf("module %s\n\ngo 1.26\n", m.modulePath)
		files[filepath.Join(m.dir, "lib.go")] = fmt.Sprintf("package %s\n", m.pkgName)
		version := "v0.0.0"
		switch {
		case strings.HasSuffix(m.modulePath, "/v2"), strings.HasSuffix(m.modulePath, ".v2"):
			version = "v2.0.0"
		case strings.HasSuffix(m.modulePath, "/v3"), strings.HasSuffix(m.modulePath, ".v3"):
			version = "v3.0.0"
		}
		fmt.Fprintf(&require, "require %s %s\n", m.modulePath, version)
		fmt.Fprintf(&replace, "replace %s => ./%s\n", m.modulePath, m.dir)
	}
	files["go.mod"] = "module example.com/m\n\ngo 1.26\n\n" + require.String() + "\n" + replace.String()
	for name, content := range pkgs {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// runImportcheck runs importcheck with the fixture as the working directory and
// returns the exit code and the combined output.
func runImportcheck(t *testing.T, root string) (int, string) {
	t.Helper()
	t.Chdir(root)
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut, nil)
	return code, out.String() + errOut.String()
}

const charmLipgloss = "charm.land/lipgloss/v2"

var lipgloss = fakeMod{modulePath: charmLipgloss, dir: "tp/lipgloss", pkgName: "lipgloss"}

func TestImportcheck_ICK01_CoreImportsCharm(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"agent/agent.go": "package agent\n\nimport _ \"" + charmLipgloss + "\"\n"},
		lipgloss)
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "agent") || !strings.Contains(out, charmLipgloss) {
		t.Errorf("output should name both packages:\n%s", out)
	}
}

func TestImportcheck_ICK02_TuiImportsCharm(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"tui/tui.go": "package tui\n\nimport _ \"" + charmLipgloss + "\"\n"},
		lipgloss)
	if code, out := runImportcheck(t, root); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestImportcheck_ICK03_MessageImportsProvider(t *testing.T) {
	root := fixtureModule(t, map[string]string{
		"message/message.go":   "package message\n\nimport _ \"example.com/m/provider\"\n",
		"provider/provider.go": "package provider\n",
	})
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "layer rule") {
		t.Errorf("output should cite the layer rule:\n%s", out)
	}
}

func TestImportcheck_ICK04_AgentImportsMessage(t *testing.T) {
	root := fixtureModule(t, map[string]string{
		"agent/agent.go":     "package agent\n\nimport _ \"example.com/m/message\"\n",
		"message/message.go": "package message\n",
	})
	if code, out := runImportcheck(t, root); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestImportcheck_ICK05_TestFileInCoreImportsCharm(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{
			"tool/tool.go":      "package tool\n",
			"tool/tool_test.go": "package tool\n\nimport _ \"" + charmLipgloss + "\"\n",
		},
		lipgloss)
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "tool") || !strings.Contains(out, charmLipgloss) {
		t.Errorf("output should name the test package and the charm import:\n%s", out)
	}
}

func TestImportcheck_ICK06_UnknownModule(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"agent/agent.go": "package agent\n\nimport _ \"github.com/x/y\"\n"},
		fakeMod{modulePath: "github.com/x/y", dir: "tp/xy", pkgName: "y"})
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "D46") {
		t.Errorf("output should point at D46:\n%s", out)
	}
}

func TestImportcheck_ICK07_CoreImportsYAML(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"agent/agent.go": "package agent\n\nimport _ \"gopkg.in/yaml.v3\"\n"},
		fakeMod{modulePath: "gopkg.in/yaml.v3", dir: "tp/yaml", pkgName: "yaml"})
	if code, out := runImportcheck(t, root); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestImportcheck_ICK12_CmdImportsCobra(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"cmd/nova/main.go": "package main\n\nimport _ \"github.com/spf13/cobra\"\n"},
		fakeMod{modulePath: "github.com/spf13/cobra", dir: "tp/cobra", pkgName: "cobra"})
	if code, out := runImportcheck(t, root); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestImportcheck_ICK13_CobraOutsideCmd(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"agent/agent.go": "package agent\n\nimport _ \"github.com/spf13/cobra\"\n"},
		fakeMod{modulePath: "github.com/spf13/cobra", dir: "tp/cobra", pkgName: "cobra"})
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "github.com/spf13/cobra") {
		t.Errorf("output should name the module:\n%s", out)
	}
}

func TestImportcheck_ICK14_FangAllowedOnlyInTuiCLI(t *testing.T) {
	const fang = "charm.land/fang/v2"
	mod := fakeMod{modulePath: fang, dir: "tp/fang", pkgName: "fang"}

	okRoot := fixtureModule(t,
		map[string]string{"tui/cli/cli.go": "package cli\n\nimport _ \"" + fang + "\"\n"}, mod)
	if code, out := runImportcheck(t, okRoot); code != 0 {
		t.Fatalf("tui/cli: exit = %d, want 0\n%s", code, out)
	}

	badRoot := fixtureModule(t,
		map[string]string{"cmd/nova/main.go": "package main\n\nimport _ \"" + fang + "\"\n"}, mod)
	if code, out := runImportcheck(t, badRoot); code != 1 {
		t.Fatalf("cmd/nova: exit = %d, want 1\n%s", code, out)
	}
}

func TestImportcheck_ICK15_ModuleAllowedForOnePackageUsedByAnother(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"tool/tool.go": "package tool\n\nimport _ \"golang.org/x/net/html\"\n"},
		fakeMod{modulePath: "golang.org/x/net/html", dir: "tp/html", pkgName: "html"})
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "golang.org/x/net/html") {
		t.Errorf("output should name the module:\n%s", out)
	}
}

func TestImportcheck_ICK08_PackageReachableOnlyViaTui(t *testing.T) {
	root := fixtureModule(t, map[string]string{
		"tui/tui.go":       "//go:build !notui\n\npackage tui\n\nimport _ \"example.com/m/helper\"\n",
		"helper/helper.go": "package helper\n",
	})
	if code, out := runImportcheck(t, root); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestImportcheck_ICK09_NotUI_BuildBreaks(t *testing.T) {
	root := fixtureModule(t, map[string]string{
		"tui/tui.go":     "//go:build !notui\n\npackage tui\n\nfunc F() {}\n",
		"agent/agent.go": "package agent\n\nimport _ \"example.com/m/tui\"\n",
	})
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
}

func TestImportcheck_ICK10_BundledImportsCharm(t *testing.T) {
	root := fixtureModule(t,
		map[string]string{"bundled/bundled.go": "package bundled\n\nimport _ \"" + charmLipgloss + "\"\n"},
		lipgloss)
	if code, out := runImportcheck(t, root); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestImportcheck_ICK11_BundledImportedByCore(t *testing.T) {
	root := fixtureModule(t, map[string]string{
		"agent/agent.go":     "package agent\n\nimport _ \"example.com/m/bundled\"\n",
		"bundled/bundled.go": "package bundled\n",
	})
	code, out := runImportcheck(t, root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "bundled") {
		t.Errorf("output should name bundled:\n%s", out)
	}
}

// TestImportcheck_TestkitAllowedOnlyInTests pins the D48 exception: testkit is
// test support, so a package's _test.go may import it even when the layer table
// does not list it, but production code may not.
func TestImportcheck_TestkitAllowedOnlyInTests(t *testing.T) {
	okRoot := fixtureModule(t, map[string]string{
		"provider/provider.go":      "package provider\n",
		"testkit/testkit.go":        "package testkit\n",
		"provider/provider_test.go": "package provider\n\nimport _ \"example.com/m/testkit\"\n",
	})
	if code, out := runImportcheck(t, okRoot); code != 0 {
		t.Fatalf("test-only import: exit = %d, want 0\n%s", code, out)
	}

	badRoot := fixtureModule(t, map[string]string{
		"provider/provider.go": "package provider\n\nimport _ \"example.com/m/testkit\"\n",
		"testkit/testkit.go":   "package testkit\n",
	})
	code, out := runImportcheck(t, badRoot)
	if code != 1 {
		t.Fatalf("production import: exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "layer rule") {
		t.Errorf("output should cite the layer rule:\n%s", out)
	}
}
