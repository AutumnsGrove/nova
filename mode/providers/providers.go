// Package providers builds the cross-provider router Nova serves: OpenRouter
// and OpenCode Go, with each request routed to the provider its model names.
package providers

import (
	"context"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/opencodego"
	"github.com/AutumnsGrove/nova/provider/openrouter"
)

// Options configures Build.
type Options struct {
	Config *config.Config
	Home   string
	Getenv func(string) string
	// Store is the OpenRouter credential store; nil means no stored key.
	Store auth.Store
}

// Build returns a provider.Provider that serves the OpenRouter and OpenCode Go
// catalogs and routes each request to the provider its model names.
func Build(opt Options) provider.Provider {
	getenv := opt.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	store := opt.Store

	orBase := opt.Config.Provider.OpenRouter.BaseURL
	if orBase == "" {
		orBase = getenv("NOVA_OPENROUTER_BASE_URL")
	}
	openRouter := openrouter.New(openrouter.Config{
		BaseURL: orBase,
		APIKeyFunc: func(context.Context) (string, error) {
			key, _, err := auth.Resolve("", getenv, store)
			return key, err
		},
	})

	goKey, _, _ := ResolveOpenCodeGoKey(opt.Config, getenv)
	if goKey == "" && opt.Home != "" {
		if stored, err := opencode.LoadKey(opencode.DefaultKeyPath(opt.Home)); err == nil {
			goKey = stored
		}
	}
	goBase := opt.Config.Provider.OpenCodeGo.BaseURL
	if goBase == "" {
		goBase = getenv("NOVA_OPENCODE_GO_BASE_URL")
	}
	openCodeGo := opencodego.New(opencodego.Config{APIKey: goKey, BaseURL: goBase})

	return &router{openRouter: openRouter, openCodeGo: openCodeGo}
}

// router sends each request to the provider its model names.
type router struct {
	openRouter provider.Provider
	openCodeGo provider.Provider
}

// Name returns the router's id.
func (r *router) Name() string { return "router" }

// Stream routes by the model's catalog provider; an unknown id falls back to
// OpenRouter.
func (r *router) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if m, ok := provider.GetModel(req.Model); ok && m.Provider == opencodego.ProviderName {
		return r.openCodeGo.Stream(ctx, req, onEvent)
	}
	return r.openRouter.Stream(ctx, req, onEvent)
}

// ResolveOpenCodeGoKey resolves the Go key from config, then the environment.
func ResolveOpenCodeGoKey(cfg *config.Config, getenv func(string) string) (key, source string, err error) {
	var cfgKey string
	if cfg != nil {
		cfgKey = cfg.Provider.OpenCodeGo.APIKey
	}
	return auth.ResolveNamed(cfgKey, auth.EnvOpenCodeGoKey, getenv, nil)
}
