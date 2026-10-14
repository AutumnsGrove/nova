//go:build !notui

package dialog

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tui/styles"
)

func TestLogin_SGN10_ProviderPicker(t *testing.T) {
	d := NewProviderPicker("Select provider to configure:", []ProviderInfo{
		{ID: "openrouter", Name: "OpenRouter", Methods: []string{"subscription", "API key"}, Ready: true, Source: "store"},
		{ID: "opencode-go", Name: "OpenCode Go", Methods: []string{"device", "API key"}},
	})
	v := d.View(80, 10, styles.Styles{})
	for _, want := range []string{"OpenRouter", "OpenCode Go", "subscription", "device", "ready", "store"} {
		if !strings.Contains(v, want) {
			t.Fatalf("provider picker missing %q:\n%s", want, v)
		}
	}
}

func TestLogin_SGN12_MethodPickerOAuth(t *testing.T) {
	d := NewMethodPicker("OpenRouter", []string{"subscription", "API key"})
	r := d.HandleKey(enterKey)
	if !r.Done || r.Value != "subscription" {
		t.Fatalf("result = %+v", r)
	}
}

func TestLogin_SGN13_MethodPickerDevice(t *testing.T) {
	d := NewMethodPicker("OpenCode Go", []string{"device", "API key"})
	d.HandleKey(downKey)
	r := d.HandleKey(enterKey)
	if !r.Done || r.Value != "API key" {
		t.Fatalf("result = %+v", r)
	}
}

func TestLogin_SGN16_EscCancels(t *testing.T) {
	d := NewProviderPicker("Select provider to configure:", []ProviderInfo{{ID: "openrouter", Name: "OpenRouter"}})
	if r := d.HandleKey(escKey); !r.Done || !r.Cancelled {
		t.Fatalf("esc result = %+v", r)
	}
}

func TestLogin_SGN17_LogoutTitle(t *testing.T) {
	d := NewProviderPicker("Select provider to logout:", []ProviderInfo{{ID: "openrouter", Name: "OpenRouter"}})
	if v := d.View(80, 10, styles.Styles{}); !strings.Contains(v, "logout") {
		t.Fatalf("logout title missing:\n%s", v)
	}
}

func TestLogin_SGN23_FlowDialogShowsURL(t *testing.T) {
	d := NewFlowDialog("Sign in with OpenRouter", "https://openrouter.ai/auth?x=1", "", true)
	v := d.View(80, 12, styles.Styles{})
	for _, want := range []string{"openrouter.ai/auth", "Approve access", "paste the URL"} {
		if !strings.Contains(v, want) {
			t.Fatalf("flow dialog missing %q:\n%s", want, v)
		}
	}
}

func TestLogin_SGN28_DeviceDialogShowsCode(t *testing.T) {
	d := NewFlowDialog("Sign in with OpenCode Go", "https://opencode.ai/device", "ABCD-1234", false)
	v := d.View(80, 12, styles.Styles{})
	if !strings.Contains(v, "opencode.ai/device") || !strings.Contains(v, "ABCD-1234") {
		t.Fatalf("device dialog missing the URL or code:\n%s", v)
	}
}

func TestLogin_SGN32_FailedFlowRetries(t *testing.T) {
	d := NewFlowDialog("Sign in", "https://x", "", true)
	f, ok := d.(interface{ Fail(string) })
	if !ok {
		t.Fatal("flow dialog has no Fail")
	}
	f.Fail("boom")
	if v := d.View(80, 12, styles.Styles{}); !strings.Contains(v, "boom") {
		t.Fatalf("error not shown:\n%s", v)
	}
	if r := d.HandleKey(enterKey); !r.Retry {
		t.Fatalf("enter result = %+v, want Retry", r)
	}
}

func TestLogin_SGN18_SecretInputIsMasked(t *testing.T) {
	d := NewSecretInput("Paste your OpenRouter key", "")
	for _, r := range "sk-secret" {
		d.HandleKey(runeKey(r))
	}
	v := d.View(80, 8, styles.Styles{})
	if strings.Contains(v, "sk-secret") {
		t.Fatalf("secret leaked into the view:\n%s", v)
	}
	if !strings.Contains(v, "•") {
		t.Fatalf("secret input shows no bullets:\n%s", v)
	}
}
