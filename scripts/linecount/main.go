// Command linecount enforces the per-package line budgets in a chunk spec. It
// is a design constraint made checkable: a package that outgrows its budget
// must be split or trimmed.
package main

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AutumnsGrove/nova/scripts/spec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

// run is the testable entry point. It returns the process exit code: 0 within
// budget, 1 over budget, 2 usage or I/O error.
func run(args []string, stdout, stderr io.Writer, _ []string) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: linecount <spec.md>")
		return 2
	}
	specPath := args[0]

	s, err := spec.Load(specPath)
	if err != nil {
		fmt.Fprintf(stderr, "linecount: %v\n", err)
		return 2
	}

	// A package under test with no budget is a spec error, not a pass.
	for _, p := range s.Packages {
		if _, ok := s.Budgets[p]; !ok {
			fmt.Fprintf(stderr, "linecount: %s: no budget in %s\n", p, specPath)
			return 2
		}
	}

	pkgs := make([]string, 0, len(s.Budgets))
	for p := range s.Budgets {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	over := 0
	for _, p := range pkgs {
		n, err := countPackage(p)
		if err != nil {
			fmt.Fprintf(stderr, "linecount: %v\n", err)
			return 2
		}
		if budget := s.Budgets[p]; n > budget {
			fmt.Fprintf(stdout, "%s: %d code lines, budget %d (over by %d)\n", p, n, budget, n-budget)
			over++
		}
	}
	if over > 0 {
		return 1
	}
	return 0
}

// countPackage adds up the code lines of every non-test .go file directly in
// dir. Subdirectories belong to other packages.
func countPackage(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}
	total := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		total += countCode(path, data)
	}
	return total, nil
}

// countCode returns the number of lines that contain at least one Go token.
// Comments and blank lines carry no tokens, so they do not count, while a line
// with a trailing comment still does. Using the scanner (rather than string
// matching) keeps comment markers inside string literals honest.
func countCode(filename string, data []byte) int {
	fset := token.NewFileSet()
	file := fset.AddFile(filename, fset.Base(), len(data))
	var s scanner.Scanner
	s.Init(file, data, func(token.Position, string) {}, 0)

	lines := make(map[int]bool)
	for {
		pos, tok, _ := s.Scan()
		if tok == token.EOF {
			break
		}
		lines[fset.Position(pos).Line] = true
	}
	return len(lines)
}
