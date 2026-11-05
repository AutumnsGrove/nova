// Package route is a provider router a library consumer builds from values:
// named providers, per-model attempt chains, and a fallback predicate. It
// returns a provider.Provider, so it drops into agent.Config.Provider
// unchanged. The caller owns the model registry and the per-model policy;
// the router only runs what it is given.
//
// A request resolves its catalog id (ModelID, falling back to Model) to an
// attempt chain of (provider, wire model) pairs. The wire model goes onto the
// wire verbatim — a Go slug for Go attempts, a vendor-prefixed slug for
// OpenRouter — while ModelID passes through untouched, so a wire slug never
// leaks into the catalog id. An unknown id is served by the default provider
// with the request unrewritten.
package route

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/opencodego"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// RouterName is the stable id Name returns. It never varies per call; the
// attempt that answered a call is on Response.Provider instead.
const RouterName = "route"

// Kind names a preset the router can build.
type Kind string

const (
	// OpenRouter builds the OpenRouter preset.
	OpenRouter Kind = "openrouter"
	// OpenCodeGo builds the OpenCode Go preset.
	OpenCodeGo Kind = "opencodego"
)

// ProviderDecl builds one backend. APIKeyFunc wins over APIKey when both are
// set, so a rotating credential is read fresh on every call.
type ProviderDecl struct {
	// Name is the stable id used in attempt chains and Response.Provider.
	Name string
	Kind Kind
	// BaseURL overrides the preset's API root, for a proxy or a test double.
	BaseURL    string
	APIKey     string
	APIKeyFunc func(ctx context.Context) (string, error)
	HTTPClient *http.Client
}

// Attempt is one step of a route: the backend and the wire slug it is sent.
// Model is always the wire slug, never a catalog id.
type Attempt struct {
	Provider string
	Model    string
}

// FallbackPredicate reports whether a failed attempt should spill to the
// next attempt in its route.
type FallbackPredicate func(err error) bool

// OnLimitOrRefusal spills on a limit, a refusal, or an upstream failure, and
// nothing else: rate limits, paywalls, credential refusals, timeouts, and
// 5xx from the serving backend. A bad request (400, 404, 422 and friends)
// fails identically on the next backend, so it never spills; neither does a
// caller cancellation nor a failure the provider layer did not classify.
func OnLimitOrRefusal(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var perr *provider.Error
	if !errors.As(err, &perr) {
		return false
	}
	// An in-band stream failure carries StatusOK; only a limit or an
	// overload clears by moving backends.
	if perr.Status == http.StatusOK {
		return ssestream.StreamErrorRetryable(perr.Code, perr.Message)
	}
	switch perr.Status {
	case http.StatusTooManyRequests,
		http.StatusPaymentRequired,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// OnAnyError spills on any failure while the caller's context is live. It is
// the roles behaviour for callers that want it; prefer OnLimitOrRefusal,
// which does not pay for a second attempt when the first one's failure would
// repeat there.
func OnAnyError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return err != nil
}

// Route is the attempt chain for one catalog id. Attempts run in order and
// the chain is walked once, so a route of N attempts makes at most N calls
// and never loops.
type Route struct {
	Attempts []Attempt
	// FallbackOn decides whether a failed attempt spills to the next one.
	// Nil means OnLimitOrRefusal.
	FallbackOn FallbackPredicate
	// OnFallback fires once per spill with the attempt that failed, the
	// attempt tried next, and the classified error. It never fires when the
	// primary succeeds or when a failure does not spill, so a caller can
	// show every paid retry and stay silent otherwise.
	OnFallback func(from, to string, err error)
}

// Config builds a Router. New copies everything it keeps, so a later config
// change cannot move a call in flight.
type Config struct {
	Providers []ProviderDecl
	// Routes maps a catalog id to its attempt chain.
	Routes map[string]Route
	// DefaultProvider serves ids with no route, with the request unrewritten.
	// Empty means the first provider.
	DefaultProvider string
	// ForceProvider pins every call to one provider: the kill switch for an
	// outage. A known id uses its configured attempt for that provider, so
	// the wire model stays valid; an unknown id passes through unrewritten.
	// Empty means no pin.
	ForceProvider string
	// SessionID returns the conversation id for the current call, read fresh
	// on every request because it changes mid-session. It is sent to OpenCode
	// Go as x-opencode-session; nil or an empty result sends no header.
	SessionID func() string
	// UserAgent is sent on every request that does not already carry one, so
	// a backend that requires an honest caller name sees the app, not a SDK.
	UserAgent string
}

// Router sends each request down its catalog id's attempt chain. It is safe
// for concurrent use: a call touches only its own request copy.
type Router struct {
	providers map[string]provider.Provider
	kinds     map[string]Kind
	routes    map[string]Route
	defaultP  string
	forceP    string
	sessionID func() string
	userAgent string
}

// New builds a Router from values. It panics on a misconfigured policy —
// no providers, an unknown kind, a duplicate or empty provider name, an
// attempt naming no provider or no model, or a default or pin naming no
// provider — because those are wiring mistakes, and a router that silently
// misroutes is worse than one that refuses to build.
func New(cfg Config) *Router {
	if len(cfg.Providers) == 0 {
		panic("route: at least one provider is required")
	}
	r := &Router{
		providers: map[string]provider.Provider{},
		kinds:     map[string]Kind{},
		routes:    map[string]Route{},
		sessionID: cfg.SessionID,
		userAgent: cfg.UserAgent,
	}
	for _, decl := range cfg.Providers {
		if decl.Name == "" {
			panic("route: a provider name is required")
		}
		if _, dup := r.providers[decl.Name]; dup {
			panic("route: duplicate provider " + decl.Name)
		}
		var p provider.Provider
		switch decl.Kind {
		case OpenRouter:
			p = openrouter.New(openrouter.Config{
				BaseURL: decl.BaseURL, APIKey: decl.APIKey,
				APIKeyFunc: decl.APIKeyFunc, HTTPClient: decl.HTTPClient,
			})
		case OpenCodeGo:
			p = opencodego.New(opencodego.Config{
				BaseURL: decl.BaseURL, APIKey: decl.APIKey,
				APIKeyFunc: decl.APIKeyFunc, HTTPClient: decl.HTTPClient,
			})
		default:
			panic("route: unknown provider kind " + string(decl.Kind))
		}
		r.providers[decl.Name] = p
		r.kinds[decl.Name] = decl.Kind
	}
	mustName := func(role, name string) {
		if name == "" {
			return
		}
		if _, ok := r.providers[name]; !ok {
			panic("route: " + role + " names unknown provider " + name)
		}
	}
	for id, route := range cfg.Routes {
		if len(route.Attempts) == 0 {
			panic("route: route " + id + " has no attempts")
		}
		attempts := make([]Attempt, len(route.Attempts))
		for i, a := range route.Attempts {
			if _, ok := r.providers[a.Provider]; !ok {
				panic("route: route " + id + " names unknown provider " + a.Provider)
			}
			if a.Model == "" {
				panic("route: route " + id + " attempt " + a.Provider + " has no model")
			}
			attempts[i] = a
		}
		route.Attempts = attempts
		r.routes[id] = route
	}
	r.defaultP = cfg.DefaultProvider
	if r.defaultP == "" {
		r.defaultP = cfg.Providers[0].Name
	}
	mustName("default provider", r.defaultP)
	r.forceP = cfg.ForceProvider
	mustName("force provider", r.forceP)
	return r
}

// Name returns the router's stable id.
func (r *Router) Name() string { return RouterName }

// Resolve reports the attempt order for a catalog id. An unknown id returns
// the effective single-attempt default route — its Model echoes the id — and
// false, so a caller can badge the default without mistaking it for policy.
func (r *Router) Resolve(catalogID string) (Route, bool) {
	if route, ok := r.routes[catalogID]; ok {
		attempts := make([]Attempt, len(route.Attempts))
		copy(attempts, route.Attempts)
		route.Attempts = attempts
		return route, true
	}
	return Route{Attempts: []Attempt{{Provider: r.defaultP, Model: catalogID}}}, false
}

// Primary reports the first attempt's provider and wire model for a catalog
// id. It mirrors Resolve for callers that only badge the primary backend.
func (r *Router) Primary(catalogID string) (providerName, wireModel string, ok bool) {
	route, ok := r.Resolve(catalogID)
	return route.Attempts[0].Provider, route.Attempts[0].Model, ok
}

// Stream runs the catalog id's attempt chain and returns the first success.
// A failed attempt's usage is kept and added to the next attempt's, so a
// spill does not lose the spend it caused; the answering attempt stamps
// Response.Provider. A terminal failure returns nil with the last error
// unchanged. At most one call per attempt happens, and a cancelled context
// returns the caller's own error without spilling.
func (r *Router) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	id := req.ModelID
	if id == "" {
		id = req.Model
	}
	// An explicitly routed attempt carries its wire slug; an unknown id
	// passes through unrewritten so a caller-supplied slug survives. The
	// pin rewrites only when the id configures an attempt for it.
	route, rewrite := r.routes[id]
	attempts := route.Attempts
	if !rewrite {
		attempts = []Attempt{{Provider: r.defaultP}}
	}
	if r.forceP != "" {
		var attempt Attempt
		attempt, rewrite = r.forced(id, req.Model)
		attempts = []Attempt{attempt}
		route = Route{Attempts: attempts}
	}
	var spent provider.Usage
	var err error
	for i, attempt := range attempts {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		out := req
		if rewrite {
			out.Model = attempt.Model
		}
		out.ExtraHeaders = r.headersFor(attempt, req)
		var resp *provider.Response
		resp, err = r.providers[attempt.Provider].Stream(ctx, out, onEvent)
		if resp != nil {
			spent.Add(resp.Usage)
		}
		if err == nil {
			if resp == nil {
				resp = &provider.Response{}
			}
			resp.Usage = spent
			resp.Provider = attempt.Provider
			return resp, nil
		}
		if i+1 >= len(attempts) {
			break
		}
		pred := route.FallbackOn
		if pred == nil {
			pred = OnLimitOrRefusal
		}
		if !pred(err) {
			break
		}
		if route.OnFallback != nil {
			route.OnFallback(attempt.Provider, attempts[i+1].Provider, err)
		}
	}
	return nil, err
}

// forced builds the single pinned attempt for id: the route's attempt for
// the pinned provider when the id configures one (rewrite true), else a
// passthrough carrying the caller's own model (rewrite false).
func (r *Router) forced(id, model string) (Attempt, bool) {
	if route, ok := r.routes[id]; ok {
		for _, a := range route.Attempts {
			if a.Provider == r.forceP {
				return a, true
			}
		}
	}
	return Attempt{Provider: r.forceP, Model: model}, false
}

// headersFor clones the request's headers and adds the router's own: the Go
// session header on OpenCode Go attempts, and the app User-Agent wherever
// the caller did not already set one. The caller's request is never mutated.
func (r *Router) headersFor(attempt Attempt, req provider.Request) map[string]string {
	headers := maps.Clone(req.ExtraHeaders)
	set := func(k, v string) {
		if headers == nil {
			headers = map[string]string{}
		}
		headers[k] = v
	}
	if r.kinds[attempt.Provider] == OpenCodeGo && r.sessionID != nil {
		if id := r.sessionID(); id != "" {
			set(opencodego.SessionHeader, id)
		}
	}
	if r.userAgent != "" && !hasHeader(headers, "User-Agent") {
		set("User-Agent", r.userAgent)
	}
	return headers
}

// hasHeader reports whether h names header, case-insensitively.
func hasHeader(h map[string]string, header string) bool {
	for k := range h {
		if strings.EqualFold(k, header) {
			return true
		}
	}
	return false
}
