package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/update"
)

// updateFunc runs one update. It is a parameter so tests can fake the network
// and the go toolchain.
type updateFunc func(context.Context, update.Options) (update.Result, error)

// updateCommand replaces the running binary from a source checkout or a
// release. The mode is detected, and --source and --release override it.
func updateCommand(stdout, stderr io.Writer, exit *int, run updateFunc) *cobra.Command {
	var (
		check   bool
		force   bool
		source  string
		release string
		flavour string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update nova from a source checkout or the latest release",
		Long: "Rebuild nova from a source checkout, or install a GitHub release.\n" +
			"The mode is detected from the current directory: inside a Nova checkout it\n" +
			"rebuilds from that checkout, and anywhere else it installs the latest\n" +
			"release. --source and --release choose explicitly.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("source") && cmd.Flags().Changed("release") {
				return fmt.Errorf("choose --source or --release, not both")
			}
			opt := update.Options{
				Mode:    update.Auto,
				Dir:     source,
				Version: os.Getenv("NOVA_VERSION"),
				Flavour: flavour,
				Check:   check,
				Force:   force,
				BaseURL: os.Getenv("NOVA_RELEASE_BASE"),
				APIURL:  os.Getenv("NOVA_API_URL"),
				Current: version,
			}
			if cmd.Flags().Changed("release") {
				opt.Mode = update.Release
				opt.Version = release
				if release == "latest" {
					opt.Version = ""
				}
			}
			if cmd.Flags().Changed("source") {
				opt.Mode = update.Source
			}
			if v := os.Getenv("NOVA_FLAVOUR"); v != "" && !cmd.Flags().Changed("flavour") {
				opt.Flavour = v
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			opt.Target = exe
			res, err := run(cmd.Context(), opt)
			if err != nil {
				fmt.Fprintf(stderr, "nova update: %v\n", err)
				*exit = 1
				return nil
			}
			if res.Message != "" {
				fmt.Fprintln(stdout, res.Message)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report what would happen without changing anything")
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even when already on the latest version")
	cmd.Flags().StringVar(&source, "source", "", "build from this source checkout")
	cmd.Flags().StringVar(&release, "release", "", "install this release; --release alone means the latest")
	cmd.Flags().Lookup("release").NoOptDefVal = "latest"
	cmd.Flags().StringVar(&flavour, "flavour", buildFlavour(), "build flavour: full or min")
	return cmd
}
