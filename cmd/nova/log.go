package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/diag"
)

// logCommand prints the most recent crash report, so a user who saw the
// "crash report: <path>" line can read the stack without finding the file.
func logCommand(stdout io.Writer) *cobra.Command {
	var showPath bool
	cmd := &cobra.Command{
		Use:     "log",
		Aliases: []string{"crash"},
		Short:   "Show the most recent crash report",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			path := crashPath()
			if showPath {
				fmt.Fprintln(stdout, path)
				return nil
			}
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(stdout, "no crash reports in %s\n", path)
				return nil
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(stdout, string(diag.LatestReport(data)))
			return err
		},
	}
	cmd.Flags().BoolVar(&showPath, "path", false, "print the crash log path and exit")
	return cmd
}

// crashPath is the crash file for this process. run installs the default
// recorder before any command executes; the fallback covers a direct call.
func crashPath() string {
	if r := diag.Default(); r != nil && r.CrashPath() != "" {
		return r.CrashPath()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return diag.CrashFile
	}
	return filepath.Join(config.UserConfigDir(home), diag.CrashFile)
}
