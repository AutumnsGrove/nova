//go:build !notui

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tui/firstrun"
)

// maybeFirstRun runs the first-run flow on a terminal when no key resolves. A
// skipped flow is not an error: the app starts and the first prompt reports the
// missing key.
func maybeFirstRun(ctx context.Context, home string, in io.Reader, out io.Writer) error {
	store := auth.NewFileStore(filepath.Join(config.UserConfigDir(home), "auth.json"))
	_, err := firstrun.Run(ctx, firstrun.Options{
		Catalog:      provider.Models(),
		DefaultModel: config.Defaults().Model,
		In:           in,
		Out:          out,
		Login: func(ctx context.Context) (string, error) {
			return auth.Login(ctx, auth.LoginOptions{Out: os.Stderr, OpenBrowser: openBrowser})
		},
		SaveKey:   store.Save,
		SaveModel: func(id string) error { return config.SetUser(home, "model", id) },
	})
	return err
}
