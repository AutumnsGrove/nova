//go:build !notui

package main

import (
	"context"

	"github.com/AutumnsGrove/nova/mode"
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
