// Package providers builds the cross-provider router Nova serves: OpenRouter
// and OpenCode Go, with each request routed to the provider its model names.
// The routing itself lives in provider/route, shared with every library
// consumer; this package keeps Nova's app shell around it: config and file
// reading, key resolution, Home-based cache paths, and the decision and
// embedding builders.
package providers

import (
	"cmp"
	"context"
	"path/filepath"
	"time"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/decide"
	decideor "github.com/AutumnsGrove/nova/decide/openrouter"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/embed"
	embedor "github.com/AutumnsGrove/nova/embed/openrouter"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/opencodego"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/provider/route"
	"github.com/AutumnsGrove/nova/voice"
	voiceor "github.com/AutumnsGrove/nova/voice/openrouter"
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
	orKeyFunc := func(context.Context) (string, error) {
		key, _, err := auth.Resolve("", getenv, store)
		return key, err
	}

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
	// The route policy is Nova's own catalog, read per request so a model
	// the background discovery learns mid-session routes correctly. Each
	// side is a single attempt: Nova has no cross-provider model mapping,
	// so a fallback chain is the consumer's policy to configure, not
	// Nova's to guess.
	return route.New(route.Config{
		Providers: []route.ProviderDecl{
			{Name: openrouter.ProviderName, Kind: route.OpenRouter, BaseURL: orBase, APIKeyFunc: orKeyFunc},
			{Name: opencodego.ProviderName, Kind: route.OpenCodeGo, BaseURL: goBase, APIKeyFunc: goKeyFunc},
		},
		DefaultProvider: openrouter.ProviderName,
		SessionID:       opt.SessionID,
		Unknown: func(id string) (route.Attempt, bool) {
			m, ok := provider.GetModel(id)
			if !ok {
				return route.Attempt{}, false
			}
			switch m.Provider {
			case opencodego.ProviderName:
				return route.Attempt{Provider: opencodego.ProviderName, Model: cmp.Or(m.Slug, id)}, true
			case openrouter.ProviderName:
				return route.Attempt{Provider: openrouter.ProviderName, Model: cmp.Or(m.Slug, id)}, true
			default:
				return route.Attempt{}, false
			}
		},
	})
}

// decisionClient builds the shared OpenRouter decision client. The chat
// provider and the decision catalog use the same base URL and credential.
func decisionClient(opt Options) *decideor.Client {
	getenv := opt.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	store := opt.Store
	orBase := cmp.Or(opt.Config.Provider.OpenRouter.BaseURL, getenv("NOVA_OPENROUTER_BASE_URL"))
	return decideor.New(decideor.Config{
		BaseURL: orBase,
		APIKeyFunc: func(context.Context) (string, error) {
			key, _, err := auth.Resolve("", getenv, store)
			return key, err
		},
	})
}

// BuildDecider returns a decision client over OpenRouter, wrapped in the roles
// from the config. A role's timeout is per attempt; a fallback model is tried
// when the primary fails and the caller's context is still live.
func BuildDecider(opt Options) decide.Decider {
	client := decisionClient(opt)
	roles := make(map[string]decide.Role, len(opt.Config.Decide.Roles))
	for name, role := range opt.Config.Decide.Roles {
		roles[name] = decide.Role{
			Model:    role.Model,
			Fallback: role.Fallback,
			Timeout:  time.Duration(role.TimeoutMS) * time.Millisecond,
		}
	}
	return decide.NewRoles(decide.RolesConfig{
		Decider:      client,
		Roles:        roles,
		AllowAliases: opt.Config.Decide.AllowAliases,
	})
}

// embeddingClient builds the shared OpenRouter embeddings client. It uses the
// same base URL and credential as chat and decisions, so there is no third
// sign-in.
func embeddingClient(opt Options) *embedor.Client {
	getenv := opt.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	store := opt.Store
	orBase := cmp.Or(opt.Config.Provider.OpenRouter.BaseURL, getenv("NOVA_OPENROUTER_BASE_URL"))
	return embedor.New(embedor.Config{
		BaseURL: orBase,
		APIKeyFunc: func(context.Context) (string, error) {
			key, _, err := auth.Resolve("", getenv, store)
			return key, err
		},
	})
}

// BuildEmbedder returns an OpenRouter embeddings client wrapped in the roles
// from the config. A role carries the dimension count its vectors must have, so
// a fallback that answers in another vector space is refused rather than
// silently stored.
func BuildEmbedder(opt Options) embed.Embedder {
	client := embeddingClient(opt)
	roleTable := make(map[string]embed.Role, len(opt.Config.Embed.Roles))
	for name, role := range opt.Config.Embed.Roles {
		roleTable[name] = embed.Role{
			Model:    role.Model,
			Fallback: role.Fallback,
			Timeout:  time.Duration(role.TimeoutMS) * time.Millisecond,
			Dims:     role.Dims,
		}
	}
	return embed.NewRoles(embed.RolesConfig{
		Embedder:     client,
		Roles:        roleTable,
		AllowAliases: opt.Config.Embed.AllowAliases,
	})
}

// BuildEmbeddingCatalog returns the live embeddings roster reader. It shares the
// embeddings client, so the roster needs no separate sign-in.
func BuildEmbeddingCatalog(opt Options) embed.Catalog {
	return embeddingClient(opt)
}

// voiceClient builds the shared OpenRouter voice client. It uses the same base
// URL and credential as chat, decisions and embeddings, and asks for exact
// speech cost only when the config says so.
func voiceClient(opt Options) *voiceor.Client {
	getenv := opt.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	store := opt.Store
	orBase := cmp.Or(opt.Config.Provider.OpenRouter.BaseURL, getenv("NOVA_OPENROUTER_BASE_URL"))
	return voiceor.New(voiceor.Config{
		BaseURL: orBase,
		APIKeyFunc: func(context.Context) (string, error) {
			key, _, err := auth.Resolve("", getenv, store)
			return key, err
		},
		CostLookup: opt.Config.Voice.CostLookupEnabled(),
	})
}

// BuildSpeaker returns the voice client wrapped in the speech roles from the
// config. A speech role carries its own voice, because a voice name belongs to
// one model and a fallback restates its own.
func BuildSpeaker(opt Options) voice.Speaker {
	client := voiceClient(opt)
	roles := make(map[string]voice.VoiceRole, len(opt.Config.Voice.Speech.Roles))
	for name, role := range opt.Config.Voice.Speech.Roles {
		entry := voice.VoiceRole{
			Model:        role.Model,
			Voice:        role.Voice,
			Format:       voice.Format(role.Format),
			Speed:        role.Speed,
			Instructions: role.Instructions,
			Provider:     role.Provider,
			Timeout:      time.Duration(role.TimeoutMS) * time.Millisecond,
		}
		if role.Fallback != nil {
			entry.Fallback = &voice.VoiceAttempt{
				Model:    role.Fallback.Model,
				Voice:    role.Fallback.Voice,
				Format:   voice.Format(role.Fallback.Format),
				Speed:    role.Fallback.Speed,
				Provider: role.Fallback.Provider,
			}
		}
		roles[name] = entry
	}
	return voice.NewRoles(voice.RolesConfig{
		Speaker:      client,
		Speech:       roles,
		AllowAliases: opt.Config.Voice.AllowAliases,
	})
}

// BuildTranscriber returns the voice client wrapped in the transcription roles
// from the config.
func BuildTranscriber(opt Options) voice.Transcriber {
	client := voiceClient(opt)
	roles := make(map[string]voice.TranscribeRole, len(opt.Config.Voice.Transcribe.Roles))
	for name, role := range opt.Config.Voice.Transcribe.Roles {
		roles[name] = voice.TranscribeRole{
			Model:    role.Model,
			Fallback: role.Fallback,
			Timeout:  time.Duration(role.TimeoutMS) * time.Millisecond,
		}
	}
	return voice.NewRoles(voice.RolesConfig{
		Transcriber:  client,
		Transcribe:   roles,
		AllowAliases: opt.Config.Voice.AllowAliases,
	})
}

// BuildDecisionCatalog returns the live decision-model roster reader. It shares
// the decision client, so the roster needs no separate sign-in.
func BuildDecisionCatalog(opt Options) decide.Catalog {
	return decisionClient(opt)
}

// ResolveOpenCodeGoKey resolves the Go key from config, then the environment.
func ResolveOpenCodeGoKey(cfg *config.Config, getenv func(string) string) (key, source string, err error) {
	var cfgKey string
	if cfg != nil {
		cfgKey = cfg.Provider.OpenCodeGo.APIKey
	}
	return auth.ResolveNamed(cfgKey, auth.EnvOpenCodeGoKey, getenv, nil)
}
