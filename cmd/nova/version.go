package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Link-time build metadata. A plain `go build` leaves these at their defaults;
// the release workflow stamps them with -ldflags -X.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// versionInfo is everything `nova version` prints, in both text and JSON form.
type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Flavour string `json:"flavour"`
}

// currentVersion gathers the build metadata, falling back to the VCS revision
// embedded by the toolchain when no commit was stamped in.
func currentVersion() versionInfo {
	c := commit
	if c == "" {
		c = vcsRevision()
	}
	return versionInfo{
		Version: version,
		Commit:  c,
		Date:    date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Flavour: buildFlavour(),
	}
}

// vcsRevision returns the vcs.revision build setting, or "" outside a repo.
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// shortCommit trims a commit to the conventional seven characters.
func shortCommit(c string) string {
	if c == "" {
		return "unknown"
	}
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// line renders the one-line human form.
func (v versionInfo) line() string {
	built := v.Date
	if built == "" {
		built = "unknown"
	}
	return fmt.Sprintf("nova %s (commit %s, built %s, %s, %s/%s, %s)",
		v.Version, shortCommit(v.Commit), built, v.Go, v.OS, v.Arch, v.Flavour)
}

// versionCommand prints the build metadata, as text or JSON.
func versionCommand(stdout io.Writer) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version and build metadata",
		RunE: func(*cobra.Command, []string) error {
			v := currentVersion()
			if asJSON {
				data, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(stdout, string(data))
				return err
			}
			_, err := fmt.Fprintln(stdout, v.line())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the fields as JSON")
	return cmd
}

// versionSummary is the compact form the TUI header shows.
func versionSummary() string {
	v := currentVersion()
	if v.Version == "dev" {
		return "dev"
	}
	return strings.TrimSpace(v.Version + " " + shortCommit(v.Commit))
}
