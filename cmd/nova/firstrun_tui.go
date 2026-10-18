//go:build !notui

package main

import (
	"context"
	"io"
	"os"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tui/firstrun"
)

// maybeFirstRun runs the first-run flow on a terminal when no key resolves. A
// skipped flow is not an error: the app starts and the first prompt reports the
// missing key.
func maybeFirstRun(ctx context.Context, home string, in io.Reader, out io.Writer) error {
	_, err := firstrun.Run(ctx, firstrun.Options{
		Catalog:      provider.Models(),
		DefaultModel: config.Defaults().Model,
		In:           in,
		Out:          out,
		SignIn: providerauth.Login{Cfg: providerauth.Config{
			Home:        home,
			Env:         os.Getenv,
			OpenBrowser: openBrowser,
		}},
		SaveModel: func(id string) error { return config.SetUser(home, "model", id) },
	})
	return err
}
