package openrouter

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

// TestCatalog_ORT11_EntriesValid checks every catalog entry is usable: a
// stable id, a wire slug, a provider, and a positive context window. The other
// fields (name, price) are validated where they are read.
func TestCatalog_ORT11_EntriesValid(t *testing.T) {
	if len(Catalog) == 0 {
		t.Fatal("Catalog is empty")
	}
	seen := map[string]bool{}
	for _, m := range Catalog {
		if m.ID == "" {
			t.Errorf("entry %#v has no ID", m)
		}
		if seen[m.ID] {
			t.Errorf("duplicate catalog id %q", m.ID)
		}
		seen[m.ID] = true
		if m.Slug == "" {
			t.Errorf("entry %q has no slug", m.ID)
		}
		if m.Provider == "" {
			t.Errorf("entry %q has no provider", m.ID)
		}
		if m.ContextWindow <= 0 {
			t.Errorf("entry %q has non-positive context window %d", m.ID, m.ContextWindow)
		}
		if m.InPerM < 0 || m.OutPerM < 0 {
			t.Errorf("entry %q has negative price: %v/%v", m.ID, m.InPerM, m.OutPerM)
		}
	}
}

// TestCatalog_ORT12_DefaultModelRegistered checks the default model resolves
// by its stable id through the provider catalog.
func TestCatalog_ORT12_DefaultModelRegistered(t *testing.T) {
	m, ok := provider.GetModel(DefaultModel)
	if !ok {
		t.Fatalf("default model %q is not registered", DefaultModel)
	}
	if m.Provider != "openrouter" {
		t.Errorf("default model provider = %q, want openrouter", m.Provider)
	}
	if m.Slug == "" {
		t.Error("default model has no wire slug")
	}
}

// TestCatalog_ORT13_ImplicitCachingMatchesProvider checks the cache flag the
// agent relies on: Anthropic models cache only when Nova asks, so they must
// not be marked implicit, and the models that cache repeated prefixes on their
// own must be.
func TestCatalog_ORT13_ImplicitCachingMatchesProvider(t *testing.T) {
	for _, m := range Catalog {
		wantImplicit := m.Provider != "anthropic"
		if strings.Contains(m.ID, "anthropic/") {
			wantImplicit = false
		}
		if m.ImplicitCaching != wantImplicit {
			t.Errorf("%s ImplicitCaching = %v, want %v", m.ID, m.ImplicitCaching, wantImplicit)
		}
	}
}
