package provider

import (
	"fmt"
	"sort"
	"sync"
)

var (
	regMu    sync.RWMutex
	registry = map[string]Provider{}
)

// Register adds a provider to the process-wide registry. It is meant to be
// called during startup (typically from an init function or explicit wiring)
// and panics on duplicate or unnamed registration so configuration mistakes
// surface immediately.
func Register(p Provider) {
	if p == nil || p.Name() == "" {
		panic("provider: Register requires a named, non-nil provider")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, exists := registry[p.Name()]; exists {
		panic(fmt.Sprintf("provider: duplicate registration for %q", p.Name()))
	}
	registry[p.Name()] = p
}

// Get returns a registered provider by name.
func Get(name string) (Provider, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Names returns the registered provider names, sorted.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
