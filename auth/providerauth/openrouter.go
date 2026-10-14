package providerauth

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider/openrouter"
)

func init() { Register(openRouterProvider()) }

// openRouterProvider describes OpenRouter: PKCE sign-in or a pasted key, stored
// in the auth.json the CLI login already uses.
func openRouterProvider() Provider {
	return Provider{
		ID:           "openrouter",
		Name:         "OpenRouter",
		Methods:      []Method{OAuth, APIKey},
		EnvVars:      []string{auth.EnvAPIKey},
		DefaultModel: openrouter.DefaultModel,
		// The config has no OpenRouter key field, so there is no ConfigKey.
		CatalogProvider: "openrouter",
		Begin: map[Method]BeginFunc{
			OAuth:  beginOpenRouterOAuth,
			APIKey: func(context.Context, Config) (Session, error) { return newKeySession("Paste your OpenRouter key"), nil },
		},
		Save:   saveOpenRouter,
		Load:   loadOpenRouter,
		Delete: deleteOpenRouter,
	}
}

// openRouterStorePath is the file the CLI login writes.
func openRouterStorePath(home string) string {
	return filepath.Join(config.UserConfigDir(home), "auth.json")
}

// beginOpenRouterOAuth binds the loopback listener and returns the flow. The
// endpoints come from the environment so a test can point them at a local
// server, exactly as the CLI does.
func beginOpenRouterOAuth(_ context.Context, cfg Config) (Session, error) {
	flow, url, err := auth.Begin(auth.LoginOptions{
		AuthURL:     cfg.getenv("NOVA_AUTH_URL"),
		ExchangeURL: cfg.getenv("NOVA_EXCHANGE_URL"),
		Out:         io.Discard,
		OpenBrowser: cfg.OpenBrowser,
	})
	if err != nil {
		return nil, err
	}
	return &oauthSession{flow: flow, url: url, name: "OpenRouter"}, nil
}

// oauthSession adapts an auth.Flow to the registry's Session.
type oauthSession struct {
	flow *auth.Flow
	url  string
	name string
}

func (s *oauthSession) Prompt() string            { return "Sign in with " + s.name }
func (s *oauthSession) URL() string               { return s.url }
func (s *oauthSession) Code() string              { return "" }
func (s *oauthSession) Submit(input string) error { return s.flow.Submit(input) }
func (s *oauthSession) Close() error              { return s.flow.Close() }

func (s *oauthSession) Wait(ctx context.Context) (Credential, error) {
	key, err := s.flow.Wait(ctx)
	if err != nil {
		return Credential{}, err
	}
	return Credential{Method: OAuth, Key: key}, nil
}

func saveOpenRouter(home string, c Credential) (string, error) {
	path := openRouterStorePath(home)
	if err := auth.NewFileStore(path).Save(c.Key); err != nil {
		return "", err
	}
	return path, nil
}

func loadOpenRouter(home string) (Credential, bool, error) {
	key, err := auth.NewFileStore(openRouterStorePath(home)).Load()
	if err != nil {
		if errors.Is(err, auth.ErrNoKey) {
			return Credential{}, false, nil
		}
		return Credential{}, false, err
	}
	return Credential{Method: APIKey, Key: key}, true, nil
}

func deleteOpenRouter(home string) error {
	return auth.NewFileStore(openRouterStorePath(home)).Delete()
}
