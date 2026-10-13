//go:build !notui

// Package cli styles the nova command tree with fang. It is the only place in
// the binary that imports a UI library for command-line help, so a notui build
// stays free of charm.land and prints plain cobra help instead (D45).
package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"

	"charm.land/fang/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// defaultWidth is the fallback when stdout is not a terminal.
const defaultWidth = 80

// Execute runs root with fang's styled help and error output. Output is hard
// wrapped to the terminal width, because fang styles but does not wrap the
// command and flag groups, and help must never overflow the terminal.
func Execute(root *cobra.Command) error {
	w := terminalWidth()
	out := newWrapWriter(root.OutOrStdout(), w)
	errOut := newWrapWriter(root.ErrOrStderr(), w)
	root.SetOut(out)
	root.SetErr(errOut)
	err := fang.Execute(context.Background(), root,
		fang.WithoutManpage(),
		fang.WithoutVersion(),
		fang.WithColorSchemeFunc(fang.DefaultColorScheme),
	)
	_ = out.Flush()
	_ = errOut.Flush()
	return err
}

// terminalWidth returns the width help should wrap to. __FANG_TEST_WIDTH lets a
// test fix it; otherwise it is the real terminal size, else defaultWidth.
func terminalWidth() int {
	if s := os.Getenv("__FANG_TEST_WIDTH"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return defaultWidth
}

// wrapWriter buffers writes and emits each complete line hard wrapped to width,
// ignoring ANSI escapes when measuring so styled output stays aligned.
type wrapWriter struct {
	dst    io.Writer
	width  int
	buffer bytes.Buffer
}

func newWrapWriter(dst io.Writer, width int) *wrapWriter {
	return &wrapWriter{dst: dst, width: width}
}

func (w *wrapWriter) Write(p []byte) (int, error) {
	n, _ := w.buffer.Write(p)
	for {
		line, err := w.buffer.ReadString('\n')
		if err != nil {
			// Incomplete line: put it back and wait for more.
			w.buffer.Reset()
			w.buffer.WriteString(line)
			break
		}
		if err := w.emit(line); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Flush emits any trailing line that was not newline-terminated.
func (w *wrapWriter) Flush() error {
	if w.buffer.Len() == 0 {
		return nil
	}
	line := w.buffer.String()
	w.buffer.Reset()
	return w.emit(line)
}

// emit hard wraps one line (newline included) and writes it.
func (w *wrapWriter) emit(line string) error {
	trailing := ""
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
		trailing = "\n"
	}
	if w.width > 0 && ansi.StringWidth(line) > w.width {
		line = ansi.Hardwrap(line, w.width, true)
	}
	_, err := io.WriteString(w.dst, line+trailing)
	return err
}
