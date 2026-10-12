// Command matrixcheck proves that every row of a chunk spec's matrix has a
// test, and that no test cites a row the spec does not define. It turns "did
// you test the edge cases" into a check that fails the build.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/AutumnsGrove/nova/scripts/spec"
)

// idRe is the row-ID shape, repeated here so a test name can be scanned for
// IDs without exporting it from package spec.
var idRe = regexp.MustCompile(`^[A-Z]{2,4}[0-9]{2,3}$`)

// testRe matches the names `go test -list` prints for tests, fuzz targets,
// benchmarks, and examples. It excludes the trailing "ok" status line.
var testRe = regexp.MustCompile(`^(Test|Fuzz|Benchmark|Example)[A-Za-z0-9_]*$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

// run is the testable entry point. args excludes the program name; env is the
// extra environment for the go subprocess. It returns the process exit code.
func run(args []string, stdout, stderr io.Writer, env []string) int {
	fs := flag.NewFlagSet("matrixcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tags := fs.String("tags", "", "comma-separated build tags passed to go test")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(stderr, "usage: matrixcheck [-tags list] <spec.md> <pkg>...")
		return 2
	}
	specPath, pkgs := rest[0], rest[1:]

	s, err := spec.Load(specPath)
	if err != nil {
		fmt.Fprintf(stderr, "matrixcheck: %v\n", err)
		return 2
	}

	names, err := listTests(pkgs, *tags, env)
	if err != nil {
		fmt.Fprintf(stderr, "matrixcheck: %v\n", err)
		return 2
	}

	known := make(map[string]bool, len(s.Rows))
	for _, r := range s.Rows {
		known[r.ID] = true
	}

	problems := 0
	for _, name := range names {
		comps := strings.Split(name, "_")
		// A package can serve several chunks: only enforce the ID shape on a
		// test that cites at least one row of this spec, so another chunk's
		// tests in the same package are ignored rather than flagged.
		citesKnown := false
		for _, comp := range comps {
			if idRe.MatchString(comp) && known[comp] {
				citesKnown = true
			}
		}
		if !citesKnown {
			continue
		}
		for _, comp := range comps {
			if idRe.MatchString(comp) && !known[comp] {
				fmt.Fprintf(stdout, "%s: test %s cites unknown matrix ID %s\n", specPath, name, comp)
				problems++
			}
		}
	}
	if len(s.Rows) == 0 {
		fmt.Fprintf(stdout, "%s: spec has no matrix rows\n", specPath)
		problems++
	}
	for _, r := range s.Rows {
		if !hasTest(names, r.ID) {
			fmt.Fprintf(stdout, "%s:%d: row %s (%s) has no test\n", specPath, r.Line, r.ID, r.Case)
			problems++
		}
	}
	if problems > 0 {
		return 1
	}
	return 0
}

// listTests runs `go test -list` over pkgs and returns the test names it
// printed. A non-zero exit (a package that does not compile) is an error, so a
// broken fixture can never look like a passing one.
func listTests(pkgs []string, tags string, env []string) ([]string, error) {
	args := []string{"test", "-list", ".*"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, pkgs...)

	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String() + "\n" + errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("go test -list failed: %s", msg)
	}

	var names []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); testRe.MatchString(line) {
			names = append(names, line)
		}
	}
	return names, nil
}

// hasTest reports whether any listed test name carries id as a whole
// underscore-separated component. Subtests never appear in the list, so they
// cannot satisfy a row.
func hasTest(names []string, id string) bool {
	for _, name := range names {
		for _, comp := range strings.Split(name, "_") {
			if comp == id {
				return true
			}
		}
	}
	return false
}
