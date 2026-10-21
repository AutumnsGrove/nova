// Package providers builds the cross-provider router Nova serves: OpenRouter
// and OpenCode Go, with each request routed to the provider its model names.
package providers

import (
	"cmp"
	"context"
	"maps"
	"path/filepath"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/diag"
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
	// SessionID returns the current conversation's id, read on every request
	// because session.new, resume, and fork change it. It is sent to OpenCode Go
	// as x-opencode-session; nil or an empty result sends no header.
	SessionID func() string
}

// Build returns a provider.Provider that serves the OpenRouter and OpenCode Go
// catalogs and routes each request to the provider its model names.
func Build(opt Options) provider.Provider {
	getenv := opt.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	store := opt.Store

	orBase := cmp.Or(opt.Config.Provider.OpenRouter.BaseURL, getenv("NOVA_OPENROUTER_BASE_URL"))
	openRouter := openrouter.New(openrouter.Config{
		BaseURL: orBase,
		APIKeyFunc: func(context.Context) (string, error) {
			key, _, err := auth.Resolve("", getenv, store)
			return key, err
		},
	})

	// The OpenCode Go key is resolved per request, so a credential saved by
	// /login at runtime is used by the next request with no restart.
	goKeyFunc := func(context.Context) (string, error) {
		if k, _, err := ResolveOpenCodeGoKey(opt.Config, getenv); err == nil {
			return k, nil
		}
		if opt.Home != "" {
			if stored, err := opencode.LoadKey(opencode.DefaultKeyPath(opt.Home)); err == nil {
				return stored, nil
			}
		}
		// No key is not an error here: the request goes out unauthenticated,
		// exactly as it did before the key became lazy, and the server answers.
		return "", nil
	}
	goBase := cmp.Or(opt.Config.Provider.OpenCodeGo.BaseURL, getenv("NOVA_OPENCODE_GO_BASE_URL"))
	// Load the cached live model overlay, then refresh it in the background when
	// it is stale. Discovery never blocks startup and never fails it.
	cachePath := ""
	if opt.Home != "" {
		cachePath = filepath.Join(opt.Home, ".config", "nova", "opencode-go-models.json")
		_ = opencodego.LoadCache(cachePath)
	}
	if starter, _ := goKeyFunc(context.Background()); starter != "" && cachePath != "" && !opencodego.CacheFresh(cachePath, 0) {
		go func() {
			defer diag.Catch("provider discovery")
			_ = opencodego.Refresh(context.Background(), opencodego.DiscoveryConfig{
				BaseURL: goBase, APIKey: starter, CachePath: cachePath,
			})
		}()
	}
	openCodeGo := opencodego.New(opencodego.Config{APIKeyFunc: goKeyFunc, BaseURL: goBase})

	return &router{openRouter: openRouter, openCodeGo: openCodeGo, sessionID: opt.SessionID}
}

// router sends each request to the provider its model names.
type router struct {
	openRouter provider.Provider
	openCodeGo provider.Provider
	sessionID  func() string
}

// Name returns the router's id.
func (r *router) Name() string { return "router" }

// Stream routes by the model's catalog provider, found from the request's
// ModelID (the server sends the wire slug as Model, which does not name a
// provider); an unknown id falls back to OpenRouter.
func (r *router) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	id := req.ModelID
	if id == "" {
		id = req.Model
	}
	if m, ok := provider.GetModel(id); ok && m.Provider == opencodego.ProviderName {
		return r.openCodeGo.Stream(ctx, r.withSession(req), onEvent)
	}
	return r.openRouter.Stream(ctx, req, onEvent)
}

// withSession adds the conversation id as OpenCode Go's session header. It
// copies the header map, so the caller's request is never mutated and a retry
// sees the same headers it started with.
func (r *router) withSession(req provider.Request) provider.Request {
	if r.sessionID == nil {
		return req
	}
	id := r.sessionID()
	if id == "" {
		return req
	}
	headers := maps.Clone(req.ExtraHeaders)
	if headers == nil {
		headers = map[string]string{}
	}
	headers[opencodego.SessionHeader] = id
	req.ExtraHeaders = headers
	return req
}

// ResolveOpenCodeGoKey resolves the Go key from config, then the environment.
func ResolveOpenCodeGoKey(cfg *config.Config, getenv func(string) string) (key, source string, err error) {
	var cfgKey string
	if cfg != nil {
		cfgKey = cfg.Provider.OpenCodeGo.APIKey
	}
	return auth.ResolveNamed(cfgKey, auth.EnvOpenCodeGoKey, getenv, nil)
}
