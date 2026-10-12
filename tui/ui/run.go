//go:build !notui

package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/AutumnsGrove/nova/config"
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

// capabilities are the fire-and-forget UI methods the C15 client can show.
var capabilities = []string{"notify", "status", "widget", "title", "set_editor_text"}

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
	model := New(Options{
		Client:   client.FromPipe(c),
		Theme:    theme,
		Profile:  profile,
		CWD:      cwd,
		Version:  opt.Version,
		Renderer: chat.NewGlamourRenderer(st),
	})
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "nova: panic in the UI: %v\n", r)
			logPanic(fmt.Sprint(r))
		}
	}()
	if log, err := openLog(); err == nil {
		defer log.Close()
	}
	progModel, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if m, ok := progModel.(*Model); ok && m.panicValue != nil {
		logPanic(fmt.Sprint(m.panicValue))
	}
	return err
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

// logFilePath is <config dir>/nova/tui.log, or empty when the home directory
// cannot be found.
func logFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(config.UserConfigDir(home), "tui.log")
}

// openLog opens the TUI log file, creating its directory.
func openLog() (io.WriteCloser, error) {
	path := logFilePath()
	if path == "" {
		return nil, errors.New("no home directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// logPanic records a recovered panic to the log file, if it can be opened.
func logPanic(text string) {
	f, err := openLog()
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, text)
}
