// Command importcheck enforces three architectural rules the compiler cannot:
// the layer rules in ARCHITECTURE.md section 2, the UI import boundary, and
// the dependency allow-list (D46). It keeps the core embeddable without UI
// weight and makes every third-party module a recorded decision.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

// run is the testable entry point: 0 clean, 1 violations, 2 usage or tooling
// error.
func run(args []string, stdout, stderr io.Writer, env []string) int {
	fs := flag.NewFlagSet("importcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "module root to check")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	pkgs, main, err := listPackages(*root, "", env)
	if err != nil {
		fmt.Fprintf(stderr, "importcheck: %v\n", err)
		return 2
	}
	// A build without the UI must still compile. If a core package references
	// tui, that build breaks, and the break is a violation, not a tooling error.
	if _, _, err := listPackages(*root, "notui", env); err != nil {
		fmt.Fprintf(stdout, "%v\n", err)
		return 1
	}

	c := &checker{main: main}
	c.checkAll(pkgs)
	for _, v := range c.violations {
		fmt.Fprintln(stdout, v)
	}
	if len(c.violations) > 0 {
		return 1
	}
	return 0
}

// layerAllowed maps a top-level package directory to the internal layers it
// may import. A layer not listed (mode, cmd, tui, scripts, ...) is unconstrained
// because it is where everything is wired together.
var layerAllowed = map[string][]string{
	"message":    {},
	"provider":   {"message"},
	"tool":       {"message", "provider"},
	"tools":      {"message", "provider", "tool"},
	"proto":      {"message", "provider"},
	"permission": {"message", "provider", "tool"},
	"usage":      {"message", "provider", "session"},
	"session":    {"message", "provider", "proto"},
	"compaction": {"message", "provider", "session"},
	"agent":      {"message", "provider", "tool", "permission", "proto"},
	"auth":       {"message", "provider", "tool", "proto", "permission", "config", "session", "usage"},
	"config":     {"message", "provider", "tool", "proto"},
	"ext":        {"message", "provider", "tool", "proto", "permission", "agent", "config", "session", "usage", "compaction"},
	"bundled":    {"message", "provider", "tool", "proto", "permission", "agent", "ext", "config", "session", "usage", "compaction"},
	"testkit":    {"message", "provider", "tool", "proto"},
}

// importRule allows the packages satisfying allow to import an import path.
type importRule struct {
	path  string
	allow func(rel string) bool
}

// allowedImports is the dependency allow-list (D46), one entry per module or
// module family. Adding one is a documented decision.
var allowedImports = []importRule{
	{path: "gopkg.in/yaml.v3", allow: nonUI},
	{path: "github.com/spf13/cobra", allow: under("cmd/nova")},
	{path: "golang.org/x/net/html", allow: under("bundled/webfetch")},
	{path: "charm.land/", allow: under("tui", "bundled")},
	{path: "github.com/charmbracelet/", allow: under("tui", "bundled")},
}

func (r importRule) matches(imp string) bool {
	if strings.HasSuffix(r.path, "/") {
		return strings.HasPrefix(imp, r.path)
	}
	return imp == r.path || strings.HasPrefix(imp, r.path+"/")
}

// under reports whether rel is one of the prefixes or below one of them.
func under(prefixes ...string) func(string) bool {
	return func(rel string) bool {
		for _, p := range prefixes {
			if rel == p || strings.HasPrefix(rel, p+"/") {
				return true
			}
		}
		return false
	}
}

// nonUI allows every package except the UI layers.
func nonUI(rel string) bool { return !under("tui", "bundled")(rel) }

// listPackage is the subset of `go list -json` the rules need.
type listPackage struct {
	ImportPath string
	Module     *struct {
		Path string
		Main bool
	}
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// listPackages runs `go list -deps -json` over ./... and returns the packages
// and the main module path.
func listPackages(root, tags string, env []string) ([]listPackage, string, error) {
	args := []string{"list", "-deps", "-json"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./...")

	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, "", fmt.Errorf("go list failed: %s", msg)
	}

	var pkgs []listPackage
	main := ""
	dec := json.NewDecoder(&out)
	for dec.More() {
		var p listPackage
		if err := dec.Decode(&p); err != nil {
			return nil, "", fmt.Errorf("decode go list: %w", err)
		}
		if p.Module != nil && p.Module.Main {
			main = p.Module.Path
		}
		pkgs = append(pkgs, p)
	}
	if main == "" {
		return nil, "", fmt.Errorf("no main module found under %s", root)
	}
	return pkgs, main, nil
}

// checker applies the rules and collects one line per violation.
type checker struct {
	main       string
	violations []string
}

func (c *checker) checkAll(pkgs []listPackage) {
	for _, p := range pkgs {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		importer := relPath(c.main, p.ImportPath)
		imports := append(append(append([]string{}, p.Imports...), p.TestImports...), p.XTestImports...)
		for _, imp := range imports {
			c.checkImport(importer, imp)
		}
	}
}

func (c *checker) checkImport(importer, imp string) {
	if imp == c.main || strings.HasPrefix(imp, c.main+"/") {
		c.checkLayer(importer, relPath(c.main, imp))
		return
	}
	if isStandard(imp) {
		return
	}
	c.checkAllowed(importer, imp)
}

func (c *checker) checkLayer(importer, imported string) {
	from, to := layerOf(importer), layerOf(imported)
	if from == to {
		return
	}
	allowed, known := layerAllowed[from]
	if !known {
		return
	}
	for _, a := range allowed {
		if a == to {
			return
		}
	}
	c.violations = append(c.violations, fmt.Sprintf(
		"layer rule (ARCHITECTURE.md section 2): %s imports %s, but %s may only import [%s]",
		importer, imported, from, strings.Join(allowed, " ")))
}

func (c *checker) checkAllowed(importer, imp string) {
	for _, r := range allowedImports {
		if !r.matches(imp) {
			continue
		}
		if r.allow(importer) {
			return
		}
		c.violations = append(c.violations, fmt.Sprintf(
			"%s imports %s, which is not allowed for that package; add it to the allow-list in scripts/importcheck and record it in docs/DECISIONS.md (D46)",
			importer, imp))
		return
	}
	c.violations = append(c.violations, fmt.Sprintf(
		"%s imports %s, which is not in the allowed-imports table; if intended, record the addition in docs/DECISIONS.md (D46)",
		importer, imp))
}

// isStandard reports whether imp is a standard-library import path. Standard
// paths have no dot in their first element.
func isStandard(imp string) bool {
	first := imp
	if i := strings.Index(imp, "/"); i >= 0 {
		first = imp[:i]
	}
	return !strings.Contains(first, ".")
}

func relPath(main, imp string) string {
	return strings.TrimPrefix(strings.TrimPrefix(imp, main), "/")
}

func layerOf(rel string) string {
	if i := strings.Index(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return rel
}
