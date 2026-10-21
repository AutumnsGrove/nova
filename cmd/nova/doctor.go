package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/doctor"
	"github.com/AutumnsGrove/nova/ext"
)

// doctorCommand runs the installation checks and prints the table or JSON.
func doctorCommand(stdout io.Writer, exit *int) *cobra.Command {
	var offline, asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the installation and configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			checks := doctor.Run(cmd.Context(), doctor.Options{
				CWD:     cwd,
				Home:    home,
				Env:     os.Getenv,
				Offline: offline,
				Out:     stdout,
				Version: version,
				Flavour: buildFlavour(),
				Host:    extensionStatus(home, cwd),
			})
			if asJSON {
				if err := doctor.WriteJSON(stdout, checks); err != nil {
					return err
				}
			} else {
				doctor.Write(stdout, checks)
			}
			*exit = doctor.ExitCode(checks)
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the network check")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the checks as JSON")
	return cmd
}

// extensionStatus builds the extension health probe from the standard
// extension directories. It returns nil (no check) when none are installed.
func extensionStatus(home, cwd string) doctor.HostStatus {
	return func() []doctor.ExtensionStatus {
		dirs := extensionDirs(home, cwd)
		out := make([]doctor.ExtensionStatus, 0, len(dirs))
		for _, dir := range dirs {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			problems := ext.Check(ctx, dir, ext.CheckOptions{Timeout: 5 * time.Second, Run: ext.NewExecRunner(providerauth.CredentialEnv())})
			cancel()
			status := doctor.ExtensionStatus{Name: filepath.Base(dir), OK: len(problems) == 0}
			if len(problems) > 0 {
				status.Detail = problems[0].Error()
			}
			out = append(out, status)
		}
		return out
	}
}

// extensionDirs lists the immediate extension directories under the user and
// project extension roots.
func extensionDirs(home, cwd string) []string {
	roots := []string{filepath.Join(config.UserConfigDir(home), "extensions")}
	if root, ok := config.ProjectRoot(cwd); ok {
		roots = append(roots, filepath.Join(config.ProjectConfigDir(root), "extensions"))
	}
	var out []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if _, err := os.Stat(filepath.Join(dir, "extension.yaml")); err == nil {
				out = append(out, dir)
			}
		}
	}
	return out
}
