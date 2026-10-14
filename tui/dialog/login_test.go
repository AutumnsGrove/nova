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

func TestLogin_SGN47_PickerWidensForALongRow(t *testing.T) {
	d := NewProviderPicker("Select provider to configure:", []ProviderInfo{
		{ID: "opencode-go", Name: "OpenCode Go", Methods: []string{"device", "API key"}, Ready: true, Source: "config"},
	})
	w, h := d.Size(100, 24)
	// The whole note, including where the key comes from, must fit the box.
	if v := d.View(w, h, styles.Styles{}); !strings.Contains(v, "device, API key · ready (config)") {
		t.Fatalf("the row was truncated at width %d:\n%s", w, v)
	}
}

func TestLogin_SGN48_PickerWidthIsCappedToTheScreen(t *testing.T) {
	d := NewProviderPicker("t", []ProviderInfo{
		{ID: "x", Name: strings.Repeat("N", 200), Methods: []string{"API key"}, Ready: true, Source: "env"},
	})
	if w, _ := d.Size(60, 24); w > 60 {
		t.Fatalf("width = %d, want at most the available 60", w)
	}
}

func TestLogin_SGN59_FlowDialogShowsItsStatus(t *testing.T) {
	d := NewFlowDialog("Sign in with OpenRouter", "https://example.test/auth", "", true)
	status := "Waiting for the browser callback on 127.0.0.1:5000..."
	d.(interface{ SetStatus(func() string) }).SetStatus(func() string { return status })
	w, h := d.Size(100, 24)
	if v := d.View(w, h, styles.Styles{}); !strings.Contains(v, status) {
		t.Fatalf("flow dialog missing the status:\n%s", v)
	}
	// The line is read on every render, so a change shows without a new dialog.
	status = "Callback received; finishing sign-in..."
	if v := d.View(w, h, styles.Styles{}); !strings.Contains(v, status) {
		t.Fatalf("flow dialog did not follow the status change:\n%s", v)
	}
}

func TestLogin_SGN60_FlowDialogWithoutStatusIsUnchanged(t *testing.T) {
	d := NewFlowDialog("Sign in with Device", "https://example.test", "CODE", false)
	w, h := d.Size(100, 24)
	if v := d.View(w, h, styles.Styles{}); strings.Contains(v, "Waiting") || strings.Contains(v, "Callback") {
		t.Fatalf("a dialog with no status invented one:\n%s", v)
	}
}
