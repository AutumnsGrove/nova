package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/ext/install"
)

// extCommand builds the `nova ext` command tree.
func extCommand(stdin io.Reader, stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "ext", Short: "Install, list, update, remove, and check extensions"}
	cmd.AddCommand(
		extInstall(stdin, stdout),
		extList(stdout),
		extUpdate(stdin, stdout),
		extRemove(stdout),
		extCheck(stdout),
	)
	return cmd
}

// newInstaller builds an installer rooted in the user config directory.
func newInstaller(stdin io.Reader, stdout io.Writer, yes bool) (*install.Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := config.UserConfigDir(home)
	confirm := func(p install.Plan) bool {
		fmt.Fprintf(stdout, "%s from %s at %s\n", p.Name, p.Source, p.Commit)
		fmt.Fprintf(stdout, "  run: %v\n  build: %v\n  capabilities: %v\n", p.Run, p.Build, p.Capabilities)
		if yes {
			return true
		}
		fmt.Fprint(stdout, "continue? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
	}
	return install.NewInstaller(install.InstallOptions{
		Root:     filepath.Join(dir, "extensions"),
		LockPath: filepath.Join(dir, "extensions.lock"),
		Confirm:  confirm,
		ScrubEnv: providerauth.CredentialEnv(),
	}), nil
}

func extInstall(stdin io.Reader, stdout io.Writer) *cobra.Command {
	var yes bool
	var ref string
	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install an extension from git or a local directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := newInstaller(stdin, stdout, yes)
			if err != nil {
				return err
			}
			src, err := install.ParseSource(args[0])
			if err != nil {
				return err
			}
			if ref != "" {
				src.Ref = ref
			}
			m, err := inst.Install(cmd.Context(), src)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "installed %s %s\n", m.Name, m.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the trust prompt")
	cmd.Flags().StringVar(&ref, "ref", "", "tag, branch, or commit to install")
	return cmd
}

func extList(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed extensions",
		RunE: func(_ *cobra.Command, _ []string) error {
			inst, err := newInstaller(nil, stdout, false)
			if err != nil {
				return err
			}
			list, err := inst.List()
			if err != nil {
				return err
			}
			for _, e := range list {
				state := "ok"
				if e.Drifted {
					state = "drifted"
				}
				fmt.Fprintf(stdout, "%-20s %s %s %s\n", e.Name, e.Commit, e.Source, state)
			}
			return nil
		},
	}
}

func extUpdate(stdin io.Reader, stdout io.Writer) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an installed extension to its ref",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := newInstaller(stdin, stdout, yes)
			if err != nil {
				return err
			}
			m, err := inst.Update(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "updated %s %s\n", m.Name, m.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the trust prompt")
	return cmd
}

func extRemove(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed extension",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			inst, err := newInstaller(nil, stdout, false)
			if err != nil {
				return err
			}
			return inst.Remove(args[0])
		},
	}
}

func extCheck(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "check <dir>",
		Short: "Validate an extension directory without installing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			problems := ext.Check(cmd.Context(), args[0], ext.CheckOptions{Run: ext.NewExecRunner(providerauth.CredentialEnv())})
			for _, p := range problems {
				fmt.Fprintln(stdout, p)
			}
			if len(problems) > 0 {
				return fmt.Errorf("%d problem(s)", len(problems))
			}
			fmt.Fprintln(stdout, "ok")
			return nil
		},
	}
}
