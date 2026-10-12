//go:build notui

package main

import (
	"context"
	"fmt"

	"github.com/AutumnsGrove/nova/mode"
)

// startTUI is the notui build stub: it has no interactive UI.
func startTUI(context.Context, *mode.Server) error {
	return fmt.Errorf("nova: this build has no TUI; use -p or --mode")
}

// shouldStartTUI is always false without the UI built in.
func shouldStartTUI(string, bool, bool) bool { return false }
