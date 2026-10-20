package providerauth

import (
	"slices"
	"testing"
)

func TestCredentialEnv_NamesEveryCredentialNovaReads(t *testing.T) {
	got := CredentialEnv()
	for _, want := range []string{"OPENROUTER_API_KEY", "OPENCODE_GO_API_KEY", "NOVA_SERVE_TOKEN"} {
		if !slices.Contains(got, want) {
			t.Errorf("CredentialEnv() = %q, missing %s", got, want)
		}
	}
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("CredentialEnv() = %q, want sorted and without duplicates", got)
	}
	// Every registered provider's variable is covered, so a provider added later
	// is scrubbed without anyone remembering this list.
	for _, p := range All() {
		for _, name := range p.EnvVars {
			if !slices.Contains(got, name) {
				t.Errorf("provider %s variable %s is not scrubbed", p.ID, name)
			}
		}
	}
}
