package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/mode/serve"
)

// serveCommand builds `nova serve`.
func serveCommand(stderr io.Writer) *cobra.Command {
	var opt serve.Options
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the protocol over HTTP and server-sent events",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := buildServeServer()
			if err != nil {
				return err
			}
			defer s.Close()
			_, _ = io.WriteString(stderr, "nova: listening on "+opt.Listen+"\n")
			return serve.ListenAndServe(cmd.Context(), s, opt)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opt.Listen, "listen", "127.0.0.1:8080", "address to listen on")
	f.StringVar(&opt.Token, "token", "", "bearer token (or NOVA_SERVE_TOKEN)")
	f.StringSliceVar(&opt.AllowedHosts, "allowed-host", nil, "extra Host header values accepted")
	f.StringVar(&opt.CORSOrigin, "cors", "", "exact CORS origin")
	f.IntVar(&opt.RingSize, "ring-size", 0, "events kept for resume")
	f.DurationVar(&opt.Heartbeat, "heartbeat", 0, "SSE heartbeat interval")
	f.Int64Var(&opt.MaxBody, "max-body", 0, "maximum request body bytes")
	return cmd
}

// buildServeServer loads config and builds the serve-mode server.
func buildServeServer() (*mode.Server, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	trust := config.NewTrustStore(filepath.Join(config.UserConfigDir(home), "trust.json"))
	cfg, _, err := config.Load(config.LoadOptions{CWD: cwd, Home: home, Trusted: trust.IsTrusted})
	if err != nil {
		return nil, err
	}
	return mode.New(mode.Options{Config: cfg, CWD: cwd, Home: home, Version: version, Mode: "serve"})
}
