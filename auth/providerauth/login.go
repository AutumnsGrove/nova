package providerauth

import (
	"context"
	"errors"
	"strings"
)

// Login is the registry-backed sign-in service. The first-run flow and the
// TUI both drive it, so there is one list of providers, one way to start a
// method, and one way to store the credential.
type Login struct {
	Cfg Config
}

// Providers lists every registered provider, in registration order.
func (Login) Providers() []Provider { return All() }

// Begin starts one provider's method. A method the provider does not offer is
// an error naming the provider, not a panic.
func (l Login) Begin(ctx context.Context, p Provider, m Method) (Session, error) {
	begin, ok := p.Begin[m]
	if !ok {
		return nil, errors.New(p.Name + " does not support that sign-in method")
	}
	return begin(ctx, l.Cfg)
}

// Save writes the credential where the provider's own store lives.
func (l Login) Save(p Provider, c Credential) error {
	if p.Save == nil {
		return errors.New(p.Name + " has no credential store")
	}
	_, err := p.Save(l.Cfg.Home, c)
	return err
}

// Methods names a provider's methods as the pickers show them.
func Methods(p Provider) []string {
	out := make([]string, len(p.Methods))
	for i, method := range p.Methods {
		out[i] = method.Label()
	}
	return out
}

// MethodByLabel finds a method by the label a picker returned.
func MethodByLabel(p Provider, label string) (Method, bool) {
	for _, method := range p.Methods {
		if method.Label() == label {
			return method, true
		}
	}
	return "", false
}

// ParseMethod accepts a method's wire name or its label, as typed after
// `/login <provider> <method>`.
func ParseMethod(s string) (Method, bool) {
	switch strings.ToLower(strings.ReplaceAll(s, "-", "_")) {
	case "oauth", "subscription":
		return OAuth, true
	case "device", "device_code":
		return Device, true
	case "api_key", "key":
		return APIKey, true
	}
	return "", false
}
