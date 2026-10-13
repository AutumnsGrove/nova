//go:build !notui

package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/tui/cli"
)

// execute runs the command tree with fang's styled help and errors. fang
// writes to the command's own writers, so stderr is unused here.
func execute(root *cobra.Command, stderr io.Writer) error {
	_ = stderr
	return cli.Execute(root)
}

// buildFlavour names the build in `nova version`.
func buildFlavour() string { return "full" }
