//go:build !notui

package main

import (
	"context"
	"io"
	"path/filepath"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/tui/trust"
	"github.com/AutumnsGrove/nova/tui/ui"
)

// startTUI runs the interactive UI against a built server.
func startTUI(ctx context.Context, s *mode.Server) error {
	return ui.Run(ctx, s, ui.RunOptions{Version: version})
}

// shouldStartTUI reports whether a bare invocation should open the TUI.
func shouldStartTUI(mode string, stdinTTY, stdoutTTY bool) bool {
	return ui.Interactive(mode, stdinTTY, stdoutTTY)
}

// ensureTrust runs the pre-start trust prompt. It returns false when the user
// chose to quit.
func ensureTrust(cwd, home string, in io.Reader, out io.Writer) (bool, error) {
	store := config.NewTrustStore(filepath.Join(config.UserConfigDir(home), "trust.json"))
	d, err := trust.Ensure(cwd, store, trust.Discover(cwd), true, in, out)
	if err != nil {
		return false, err
	}
	return d != trust.Quit, nil
}
