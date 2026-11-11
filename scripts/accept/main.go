// Command accept is the definition of done for a chunk. It runs the checks in
// docs/TESTING.md section 6 in order, stops at the first failure, and prints a
// one-line summary per step. "Done" is a check, not a judgement.
//
// A chunk is accepted in two phases: steps that do not depend on the chunk run
// once, and then the chunk's own tests and its matrix and budget checks. With
// --all the module-wide phase runs a single time for every archived chunk,
// which is what the release gate does: the same work 23 times over was a fifth
// of a sixteen-minute gate (#97).
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		fmt.Fprintln(stderr, "usage: accept <CNN> [-from step] | accept --all")
		return 2
	}
	if chunk == allChunks {
		if from != "" {
			fmt.Fprintln(stderr, "accept: -from is for a single chunk")
			return 2
		}
		return runAll(stdout, stderr, env)
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

	steps := append(globalSteps(), chunkSteps(specPath, s.Packages)...)
	start, ok := startIndex(steps, from)
	if !ok {
		fmt.Fprintf(stderr, "accept: unknown step %q\n", from)
		return 2
	}

	return runSteps(steps[start:], stdout, env)
}

// splashPkg costs minutes under -race: it rasterises 150 frames per test. The
// Makefile keeps it out of the fast gate and gives `race-splash` 25m, so a
// chunk that owns it needs the same budget here or the gate dies on Go's 10m
// default, which is exactly how C21 failed on every release run.
const splashPkg = "./tui/splash/splashgif"

// testTimeout is the `-timeout` for a chunk's test steps: the splash budget
// when the chunk owns that package, the ordinary one otherwise. It is always
// explicit, so the gate never inherits whatever default the toolchain carries.
func testTimeout(pkgs []string) string {
	for _, p := range pkgs {
		if p == splashPkg {
			return "25m"
		}
	}
	return "10m"
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

// globalSteps do not depend on which chunk is being accepted, so the gate runs
// them once. The documented order from docs/TESTING.md section 6 keeps gofmt
// first, and importcheck moves up with the rest of the module-wide work.
func globalSteps() []step {
	return []step{
		{name: "gofmt", argv: []string{"gofmt", "-l", "."}, dirty: true},
		{name: "vet", argv: []string{"go", "vet", "./..."}},
		{name: "build", argv: []string{"go", "build", "./..."}},
		{name: "build-notui", argv: []string{"go", "build", "-tags", "notui", "./..."}},
		{name: "importcheck", argv: []string{"go", "run", "./scripts/importcheck"}},
	}
}

// chunkSteps are the ones that name a chunk. There is one test step: -race is
// kept, but the earlier -short run was a strict subset of this one, so the gate
// paid for the detector twice per chunk and learned nothing the second time.
func chunkSteps(specPath string, pkgs []string) []step {
	timeout := testTimeout(pkgs)
	return []step{
		{name: "test", argv: append([]string{"go", "test", "-race", "-p", "2", "-timeout", timeout}, pkgs...)},
		{name: "matrixcheck", argv: append([]string{"go", "run", "./scripts/matrixcheck", specPath}, pkgs...)},
		{name: "linecount", argv: []string{"go", "run", "./scripts/linecount", specPath}},
	}
}

// allChunks is the argument that accepts every archived chunk in one pass.
const allChunks = "--all"

// runAll runs the module-wide steps once and each chunk's own steps in order,
// stopping at the first failure with the chunk named. It is the release gate.
func runAll(stdout, stderr io.Writer, env []string) int {
	specs, err := filepath.Glob(filepath.Join("docs", "archive", "chunks", "C*.md"))
	if err != nil || len(specs) == 0 {
		fmt.Fprintln(stderr, "accept: no chunk specs under docs/archive/chunks")
		return 2
	}
	sort.Strings(specs)
	if code := runSteps(globalSteps(), stdout, env); code != 0 {
		return code
	}
	for _, specPath := range specs {
		s, err := spec.Load(specPath)
		if err != nil {
			fmt.Fprintf(stderr, "accept: %v\n", err)
			return 2
		}
		if missing := firstMissing(s.Packages); missing != "" {
			fmt.Fprintf(stderr, "accept: package %s does not exist\n", missing)
			return 1
		}
		chunk := strings.SplitN(filepath.Base(specPath), "-", 2)[0]
		fmt.Fprintf(stdout, "== accept %s ==\n", chunk)
		if code := runSteps(chunkSteps(specPath, s.Packages), stdout, env); code != 0 {
			fmt.Fprintf(stderr, "accept: %s failed\n", chunk)
			return code
		}
	}
	return 0
}

// runSteps runs one phase in order and stops at the first failure.
func runSteps(steps []step, stdout io.Writer, env []string) int {
	for _, st := range steps {
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
