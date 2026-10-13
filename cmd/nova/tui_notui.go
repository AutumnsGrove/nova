//go:build notui

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/AutumnsGrove/nova/mode"
)

// startTUI is the notui build stub: it has no interactive UI.
func startTUI(context.Context, *mode.Server) error {
	return fmt.Errorf("nova: this build has no TUI; use -p or --mode")
}

// shouldStartTUI is always false without the UI built in.
func shouldStartTUI(string, bool, bool) bool { return false }

// ensureTrust is a no-op without the UI built in.
func ensureTrust(string, string, io.Reader, io.Writer) (bool, error) { return true, nil }
