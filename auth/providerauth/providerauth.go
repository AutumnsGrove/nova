// Package providerauth is a registry of interactive provider sign-in flows.
// Each provider lives in its own file and calls Register from init, so adding
// a provider is one file and no change here.
package providerauth

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
)

// Method is one way a provider authenticates.
type Method string

const (
	// OAuth is a browser sign-in backed by PKCE.
	OAuth Method = "subscription"
	// Device is a device-code flow.
	Device Method = "device"
	// APIKey is a pasted key.
	APIKey Method = "api_key"
)

// Label is the human name of a method, as the picker shows it.
func (m Method) Label() string {
	switch m {
	case OAuth:
		return "subscription"
	case Device:
		return "device"
	default:
		return "API key"
	}
}

// Credential is a saved login.
type Credential struct {
	Method Method
	Key    string
}

// Session is a started interactive sign-in. Begin returns one; the dialog
// renders it, Submit and Close feed it, and Wait finishes it.
type Session interface {
	// Prompt is the input title when the flow needs typing.
	Prompt() string
	// URL is the page the user opens, empty when there is none.
	URL() string
	// Code is the user code to enter, empty when there is none.
	Code() string
	// Wait blocks until the user finishes and returns the credential.
	Wait(ctx context.Context) (Credential, error)
	// Submit feeds the flow a pasted redirect URL.
	Submit(input string) error
	// Close releases the flow and is safe to call more than once.
	Close() error
}

// StatusReporter is implemented by a Session that can say what it is waiting
// for, so a UI can show progress. It is optional: a session without it simply
// shows no status line.
type StatusReporter interface {
	// Status is one short line, such as "Waiting for the browser callback...".
	Status() string
}

// BeginFunc starts one method's flow.
type BeginFunc func(ctx context.Context, cfg Config) (Session, error)

// Config is what a provider needs to begin a flow.
type Config struct {
	Home string
	Env  func(string) string
	// OpenBrowser opens the sign-in page. A nil function means print only.
	OpenBrowser func(string) error
}

// getenv is Config.Env with a nil guard.
func (c Config) getenv(name string) string {
	if c.Env == nil {
		return ""
	}
	return c.Env(name)
}

// Provider describes one provider's authentication. Each provider file
// registers one.
type Provider struct {
	ID           string
	Name         string
	Methods      []Method
	EnvVars      []string
	DefaultModel string
	// CatalogProvider is the name the model catalog gives this provider's
	// models (provider.Model.Provider). It differs from ID for OpenCode Go, so
	// ForModel needs it to find the provider that serves a model.
	CatalogProvider string
	// ConfigKey reads this provider's key from the loaded config, nil when the
	// config has no place for one.
	ConfigKey func(*config.Config) string
	Begin     map[Method]BeginFunc
	Save      func(home string, c Credential) (path string, err error)
	Load      func(home string) (Credential, bool, error)
	Delete    func(home string) error
}

var (
	mu        sync.RWMutex
	providers []Provider
	byID      = map[string]int{}
)

// Register adds a provider, replacing an entry with the same id.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if i, ok := byID[p.ID]; ok {
		providers[i] = p
		return
	}
	byID[p.ID] = len(providers)
	providers = append(providers, p)
}

// All returns every registered provider in registration order.
func All() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, len(providers))
	copy(out, providers)
	return out
}

// Get returns a provider by id.
func Get(id string) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	i, ok := byID[id]
	if !ok {
		return Provider{}, false
	}
	return providers[i], true
}

// ConfigKeyFrom returns the provider's key from cfg, or "" when there is none.
func (p Provider) ConfigKeyFrom(cfg *config.Config) string {
	if p.ConfigKey == nil || cfg == nil {
		return ""
	}
	return p.ConfigKey(cfg)
}

// fallbackProvider serves any model the catalog does not know. The request
// router (mode/providers) sends such a model to OpenRouter, so sign-in checks
// must agree with it.
const fallbackProvider = "openrouter"

// ForModel returns the provider that serves model: the one whose
// CatalogProvider matches the model's catalog entry, else OpenRouter for an
// unknown id, which is where the request router sends it.
func ForModel(model string) (Provider, bool) {
	if m, ok := provider.GetModel(model); ok {
		for _, p := range All() {
			if p.CatalogProvider != "" && p.CatalogProvider == m.Provider {
				return p, true
			}
		}
	}
	return Get(fallbackProvider)
}

// Status is a provider's resolved authentication.
type Status struct {
	Provider Provider
	Method   Method
	Source   string // config | env | store | ""
	Ready    bool
}

// Resolve reports config, then env, then store, and reads the store on every
// call so a credential saved in the same process resolves immediately. cfgKey
// is the provider's key from config, empty when the caller has none.
func Resolve(p Provider, cfgKey string, env func(string) string, home string) Status {
	s := Status{Provider: p}
	if strings.TrimSpace(cfgKey) != "" {
		s.Method, s.Source, s.Ready = APIKey, "config", true
		return s
	}
	if env != nil {
		for _, name := range p.EnvVars {
			if strings.TrimSpace(env(name)) != "" {
				s.Method, s.Source, s.Ready = APIKey, "env", true
				return s
			}
		}
	}
	if p.Load != nil && home != "" {
		if c, ok, err := p.Load(home); err == nil && ok && strings.TrimSpace(c.Key) != "" {
			s.Method, s.Source, s.Ready = c.Method, "store", true
			return s
		}
	}
	return s
}

// keySession is the API-key method: Wait blocks until Submit delivers a key.
type keySession struct {
	prompt string
	ch     chan string
	once   sync.Once
}

// newKeySession starts a key flow with the given input title.
func newKeySession(prompt string) *keySession {
	return &keySession{prompt: prompt, ch: make(chan string, 1)}
}

func (s *keySession) Prompt() string { return s.prompt }
func (s *keySession) URL() string    { return "" }
func (s *keySession) Code() string   { return "" }

func (s *keySession) Submit(input string) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return errors.New("the key is empty")
	}
	s.once.Do(func() { s.ch <- input })
	return nil
}

func (s *keySession) Wait(ctx context.Context) (Credential, error) {
	select {
	case <-ctx.Done():
		return Credential{}, ctx.Err()
	case key := <-s.ch:
		return Credential{Method: APIKey, Key: key}, nil
	}
}

func (s *keySession) Close() error { return nil }
