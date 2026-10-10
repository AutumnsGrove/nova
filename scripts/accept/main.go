// Command accept is the definition of done for a chunk. It runs the checks in
// docs/TESTING.md section 6 in order, stops at the first failure, and prints a
// one-line summary per step. "Done" is a check, not a judgement.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/scripts/spec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

// step is one command in the acceptance sequence.
type step struct {
	name string
	argv []string
	// dirty marks a step that fails on non-empty output even with exit 0.
	dirty bool
}

// run is the testable entry point: 0 all steps pass, 1 a step failed, 2 usage
// or setup error.
func run(args []string, stdout, stderr io.Writer, env []string) int {
	chunk, from, ok := parseArgs(args)
	if !ok {
		fmt.Fprintln(stderr, "usage: accept <CNN> [-from step]")
		return 2
	}
	specPath, err := spec.Find(chunk)
	if err != nil {
		fmt.Fprintf(stderr, "accept: %v\n", err)
		return 2
	}
	s, err := spec.Load(specPath)
	if err != nil {
		fmt.Fprintf(stderr, "accept: %v\n", err)
		return 2
	}
	if missing := firstMissing(s.Packages); missing != "" {
		fmt.Fprintf(stderr, "accept: package %s does not exist\n", missing)
		return 1
	}

	steps := buildSteps(specPath, s.Packages)
	start, ok := startIndex(steps, from)
	if !ok {
		fmt.Fprintf(stderr, "accept: unknown step %q\n", from)
		return 2
	}

	for _, st := range steps[start:] {
		began := time.Now()
		out, err := runStep(st, env)
		elapsed := time.Since(began).Round(time.Millisecond)
		status := "ok"
		if err != nil || (st.dirty && strings.TrimSpace(out) != "") {
			status = "FAIL"
		}
		fmt.Fprintf(stdout, "%-12s %s  %s\n", st.name, status, elapsed)
		if status == "FAIL" {
			if text := strings.TrimSpace(out); text != "" {
				fmt.Fprintln(stdout, text)
			}
			return 1
		}
	}
	return 0
}

// parseArgs reads the chunk and the optional -from step, in either order.
func parseArgs(args []string) (chunk, from string, ok bool) {
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-from":
			i++
			if i >= len(args) {
				return "", "", false
			}
			from = args[i]
		case strings.HasPrefix(args[i], "-from="):
			from = strings.TrimPrefix(args[i], "-from=")
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 {
		return "", "", false
	}
	return positional[0], from, true
}

// buildSteps is the documented order from docs/TESTING.md section 6.
func buildSteps(specPath string, pkgs []string) []step {
	return []step{
		{name: "gofmt", argv: []string{"gofmt", "-l", "."}, dirty: true},
		{name: "vet", argv: []string{"go", "vet", "./..."}},
		{name: "build", argv: []string{"go", "build", "./..."}},
		{name: "build-notui", argv: []string{"go", "build", "-tags", "notui", "./..."}},
		{name: "test-short", argv: append([]string{"go", "test", "-short", "-race", "-p", "2"}, pkgs...)},
		{name: "test", argv: append([]string{"go", "test", "-race", "-p", "2"}, pkgs...)},
		{name: "matrixcheck", argv: append([]string{"go", "run", "./scripts/matrixcheck", specPath}, pkgs...)},
		{name: "linecount", argv: []string{"go", "run", "./scripts/linecount", specPath}},
		{name: "importcheck", argv: []string{"go", "run", "./scripts/importcheck"}},
	}
}

// startIndex maps -from to a step index, or reports an unknown step.
func startIndex(steps []step, from string) (int, bool) {
	if from == "" {
		return 0, true
	}
	for i, s := range steps {
		if s.name == from {
			return i, true
		}
	}
	return 0, false
}

// firstMissing returns the first spec package with no directory, so a package
// that does not exist is an error rather than a silent skip.
func firstMissing(pkgs []string) string {
	for _, p := range pkgs {
		info, err := os.Stat(filepath.Clean(p))
		if err != nil || !info.IsDir() {
			return p
		}
	}
	return ""
}

// runStep runs one command and returns its combined output.
func runStep(s step, env []string) (string, error) {
	cmd := exec.Command(s.argv[0], s.argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
