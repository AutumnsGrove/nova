package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/update"
)

// updateFunc runs one update. It is a parameter so tests can fake the network
// and the go toolchain.
type updateFunc func(context.Context, update.Options) (update.Result, error)

// executable finds the running binary. It is a variable so tests can fake it.
var executable = os.Executable

// resolveTarget picks the binary to replace: --target, then NOVA_INSTALL_DIR
// (the same variable install.sh reads, so the binary is <dir>/nova), then the
// running binary. A running binary under a Go build cache is a `go run`
// throwaway that vanishes on exit, so replacing it would report success and
// change nothing; that case is refused rather than guessed at.
func resolveTarget(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if dir := os.Getenv("NOVA_INSTALL_DIR"); dir != "" {
		name := "nova"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, name), nil
	}
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if isGoRunBinary(exe) {
		return "", errors.New("this nova was started with `go run`, so its binary is a temporary file " +
			"that is deleted on exit; pass --target <path> or set NOVA_INSTALL_DIR to say what to replace")
	}
	// Replace the file behind a symlink, not the link: renaming over the link
	// would leave the real binary stale.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// isGoRunBinary reports whether path is a `go run` or `go test` temporary
// binary: a file under a go-build directory inside the temp directory.
func isGoRunBinary(path string) bool {
	rel, err := filepath.Rel(os.TempDir(), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

// updateCommand replaces the running binary from a source checkout or a
// release. The mode is detected, and --source and --release override it.
func updateCommand(stdout, stderr io.Writer, exit *int, run updateFunc) *cobra.Command {
	var (
		check   bool
		force   bool
		source  string
		release string
		flavour string
		target  string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update nova from a source checkout or the latest release",
		Long: "Rebuild nova from a source checkout, or install a GitHub release.\n" +
			"The mode is detected from the current directory: inside a Nova checkout it\n" +
			"rebuilds from that checkout, and anywhere else it installs the latest\n" +
			"release. --source and --release choose explicitly. It replaces the running\n" +
			"binary; --target or NOVA_INSTALL_DIR choose another.",
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
			resolved, err := resolveTarget(target)
			if err != nil {
				fmt.Fprintf(stderr, "nova update: %v\n", err)
				*exit = 1
				return nil
			}
			opt.Target = resolved
			fmt.Fprintf(stdout, "target: %s\n", resolved)
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
	cmd.Flags().StringVar(&target, "target", "", "the binary to replace (default: $NOVA_INSTALL_DIR/nova, else the running binary)")
	cmd.Flags().StringVar(&flavour, "flavour", buildFlavour(), "build flavour: full or min")
	return cmd
}
