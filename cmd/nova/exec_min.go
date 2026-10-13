//go:build notui

package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// execute runs the command tree with plain cobra help and errors, printing the
// error here because fang is not available without the UI build.
func execute(root *cobra.Command, stderr io.Writer) error {
	err := root.Execute()
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	return err
}

// buildFlavour names the build in `nova version`.
func buildFlavour() string { return "min" }
