package providerauth

import (
	"os"
	"testing"
)

// TestProviderauth_SGN60_LooseKeyFileStillReady checks a stored key whose file
// is more permissive than 0600 still counts as signed in. auth.Resolve treats
// the permission problem as a warning (D83), and the picker must agree with the
// request path or a working key shows as "signed out".
func TestProviderauth_SGN60_LooseKeyFileStillReady(t *testing.T) {
	p, _ := Get("openrouter")
	home := t.TempDir()
	path, err := p.Save(home, Credential{Method: APIKey, Key: "loose-key"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	st := Resolve(p, "", nil, home)
	if !st.Ready || st.Source != "store" {
		t.Fatalf("status = %+v, want a ready store credential", st)
	}
	cred, ok, err := p.Load(home)
	if err != nil || !ok || cred.Key != "loose-key" {
		t.Fatalf("Load = (%+v, %v, %v), want the key with no error", cred, ok, err)
	}
}
