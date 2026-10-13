//go:build notui

package main

import (
	"context"
	"io"
)

// maybeFirstRun is a no-op without the UI: a notui build never has a terminal
// first-run flow.
func maybeFirstRun(context.Context, string, io.Reader, io.Writer) error { return nil }
