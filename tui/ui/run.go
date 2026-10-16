//go:build !notui

package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/tui/chat"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// ErrNoTerminal is returned when the interactive UI is asked to run without a
// terminal on stdin or stdout.
var ErrNoTerminal = errors.New("nova: the interactive UI needs a terminal; use -p or --mode json")

// RunOptions configures Run.
type RunOptions struct {
	Theme   string
	Version string
	NoColor bool
}

// capabilities are the UI methods the C17 client can answer. The server will
// not send a method the client did not declare.
var capabilities = []string{
	"ui.confirm", "ui.select", "ui.input", "ui.form",
	"ui.notify", "ui.status", "ui.widget", "ui.title", "ui.set_editor_text",
}

// capabilitySet turns a capability list into a set keyed by its "ui." name.
func capabilitySet(caps []string) map[string]bool {
	out := make(map[string]bool, len(caps))
	for _, c := range caps {
		out[c] = true
	}
	return out
}

// Run starts the interactive UI against an in-process server.
func Run(ctx context.Context, srv *mode.Server, opt RunOptions) error {
	return runWith(ctx, srv, opt, os.Stdin, os.Stdout)
}

// runWith is the testable entry point: in and out stand in for the terminal.
func runWith(ctx context.Context, srv *mode.Server, opt RunOptions, in io.Reader, out io.Writer) error {
	if !isTerminal(in) || !isTerminal(out) {
		return ErrNoTerminal
	}
	if srv == nil {
		return errors.New("nova: no server")
	}
	profile := styles.DetectProfile(os.Getenv, out)
	theme := pickTheme(opt, profile)
	st := styles.Build(theme, profile)
	c := mode.NewPipe(srv, capabilities...)
	defer c.Close()
	cwd, _ := os.Getwd()
	// The server already loaded and normalised the config; take its display
	// preferences so the saved line counts survive a restart.
	display := chat.DefaultDisplay()
	display.ThinkingPreviewLines, display.ToolPreviewLines = srv.Display()
	model := New(Options{
		Client:   client.FromPipe(c),
		Theme:    theme,
		Profile:  profile,
		CWD:      cwd,
		Version:  opt.Version,
		Renderer: chat.NewGlamourRenderer(st),
		Display:  display,
	})
	diag.SetMode("tui")
	diag.SetOp("tui running")
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "\nnova: internal error: %v\n", r)
			reportCrash(os.Stderr, r, "tui program")
		}
	}()
	progModel, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if m, ok := progModel.(*Model); ok && m.panicValue != nil {
		fmt.Fprintf(os.Stderr, "\nnova: recovered from a UI panic: %v\n", m.panicValue)
		reportCrash(os.Stderr, m.panicValue, "tui view")
	}
	reportProgramError(os.Stderr, err)
	return err
}

// reportProgramError records a bubbletea event-loop panic, which Run reports as
// ErrProgramPanic after restoring the terminal and printing the stack.
func reportProgramError(w io.Writer, err error) {
	if !errors.Is(err, tea.ErrProgramPanic) {
		return
	}
	fmt.Fprintln(w, "\nnova: internal error in the UI (stack above)")
	reportCrash(w, err, "tui program")
}

// reportCrash records a panic with the process recorder and writes the crash
// file path to w so the user can read the full stack after the terminal
// returns. Nothing is written when no report could be stored.
func reportCrash(w io.Writer, v any, op string) {
	path, err := diag.Report(v, op)
	if err != nil || path == "" {
		return
	}
	fmt.Fprintf(w, "nova: crash report: %s\n", path)
}

// Interactive reports whether the TUI should start for the given mode flag and
// terminal state. mode is the value of --mode ("" when unset).
func Interactive(mode string, stdinTTY, stdoutTTY bool) bool {
	if mode == "tui" {
		return true
	}
	if mode != "" {
		return false
	}
	return stdinTTY && stdoutTTY
}

// pickTheme chooses the theme from the options and the detected profile.
func pickTheme(opt RunOptions, p colorprofile.Profile) styles.Theme {
	if opt.NoColor || p <= colorprofile.ASCII {
		return styles.Mono()
	}
	switch opt.Theme {
	case "light":
		return styles.Light()
	case "mono":
		return styles.Mono()
	default:
		return styles.Dark()
	}
}

// isTerminal reports whether a stream is a terminal.
func isTerminal(v any) bool {
	f, ok := v.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(f.Fd())
}
