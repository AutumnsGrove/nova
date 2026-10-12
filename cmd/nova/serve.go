package main

import (
	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/mode/serve"
)

// serveCommand builds `nova serve`.
func serveCommand() *cobra.Command {
	var opt serve.Options
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the protocol over HTTP and server-sent events",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := mode.Build(mode.BuildOptions{Mode: "serve", Version: version})
			if err != nil {
				return err
			}
			defer s.Close()
			return serve.ListenAndServe(cmd.Context(), s, opt)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opt.Listen, "listen", "127.0.0.1:8080", "address to listen on")
	f.StringVar(&opt.Token, "token", "", "bearer token (or NOVA_SERVE_TOKEN)")
	f.StringSliceVar(&opt.AllowedHosts, "allowed-host", nil, "extra Host header values accepted")
	f.StringVar(&opt.CORSOrigin, "cors", "", "exact CORS origin")
	return cmd
}
