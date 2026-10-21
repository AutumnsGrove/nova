//go:build !notui

package trust

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
)

// project builds a folder with every kind of loadable resource.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nova", "extensions", "hello"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".nova", "config.yaml"), "hooks:\n  - event: tool_call\n  - event: agent_end\n")
	write(t, filepath.Join(dir, ".nova", "extensions", "hello", "extension.yaml"), "name: hello\n")
	write(t, filepath.Join(dir, "AGENTS.md"), "rules\n")
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// storeAt returns a store and a home whose trust file lives in a temp dir.
func storeAt(t *testing.T) (config.TrustStore, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return config.NewTrustStore(filepath.Join(home, ".config", "nova", "trust.json")), home
}

func TestTrust_TRP01_Found(t *testing.T) {
	dir := project(t)
	f := Discover(dir)
	if !f.Config || f.Hooks != 2 || len(f.Extensions) != 1 || f.Extensions[0] != "hello" || len(f.ContextFiles) != 1 {
		t.Fatalf("Found = %+v", f)
	}
	var out bytes.Buffer
	if _, err := Prompt(dir, f, strings.NewReader("2\n"), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"config", "hello", "hooks", "2", "AGENTS.md", "Trust this folder", "Quit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("prompt missing %q:\n%s", want, text)
		}
	}
}

func TestTrust_TRP02_TrustUpdatesStore(t *testing.T) {
	dir := project(t)
	store, _ := storeAt(t)
	var out bytes.Buffer
	d, err := Ensure(dir, store, Discover(dir), true, strings.NewReader("1\n"), &out)
	if err != nil || d != Trust {
		t.Fatalf("Ensure = %v, %v", d, err)
	}
	if !store.IsTrusted(dir) {
		t.Fatal("trust answer did not update the store")
	}
}

func TestTrust_TRP03_ContinueWithout(t *testing.T) {
	dir := project(t)
	store, _ := storeAt(t)
	var out bytes.Buffer
	d, err := Ensure(dir, store, Discover(dir), true, strings.NewReader("2\n"), &out)
	if err != nil || d != Once {
		t.Fatalf("Ensure = %v, %v", d, err)
	}
	if store.IsTrusted(dir) {
		t.Fatal("continue-without must not trust the folder")
	}
}

func TestTrust_TRP04_Quit(t *testing.T) {
	dir := project(t)
	store, _ := storeAt(t)
	var out bytes.Buffer
	d, err := Ensure(dir, store, Discover(dir), true, strings.NewReader("3\n"), &out)
	if err != nil || d != Quit {
		t.Fatalf("Ensure = %v, %v", d, err)
	}
	if store.IsTrusted(dir) {
		t.Fatal("quit must not trust the folder")
	}
}

func TestTrust_TRP05_TooBroad(t *testing.T) {
	store, home := storeAt(t)
	if err := ApplyTrust(home, store); !errors.Is(err, ErrTooBroad) {
		t.Fatalf("ApplyTrust(home) = %v, want ErrTooBroad", err)
	}
	var out bytes.Buffer
	d, err := Ensure(home, store, Found{Config: true}, true, strings.NewReader("1\n"), &out)
	if err != nil || d != Once {
		t.Fatalf("Ensure = %v, %v", d, err)
	}
	if !strings.Contains(out.String(), "too broad to trust") {
		t.Fatalf("refusal message missing:\n%s", out.String())
	}
	if store.IsTrusted(home) {
		t.Fatal("a too-broad folder must not be trusted")
	}
}

func TestTrust_TRP06_NonTerminal(t *testing.T) {
	dir := project(t)
	store, _ := storeAt(t)
	var out bytes.Buffer
	d, err := Ensure(dir, store, Discover(dir), false, strings.NewReader("1\n"), &out)
	if err != nil || d != Once {
		t.Fatalf("Ensure = %v, %v", d, err)
	}
	if out.Len() != 0 {
		t.Fatalf("non-terminal run prompted:\n%s", out.String())
	}
	if store.IsTrusted(dir) {
		t.Fatal("a non-terminal run must stay untrusted")
	}
}

func TestTrust_TRP07_NothingRunBeforeAnswer(t *testing.T) {
	dir := project(t)
	marker := filepath.Join(dir, "ran")
	// A hook and an extension that would touch a marker if they ever ran.
	write(t, filepath.Join(dir, ".nova", "extensions", "hello", "extension.yaml"),
		"name: hello\nrun: [\"sh\", \"-c\", \"touch "+marker+"\"]\n")
	store, _ := storeAt(t)
	var out bytes.Buffer
	if _, err := Ensure(dir, store, Discover(dir), true, strings.NewReader("3\n"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the extension ran before the trust answer")
	}
	if store.IsTrusted(dir) {
		t.Fatal("quit must leave the project untrusted")
	}
}

func TestTrust_TRP08_NothingToDo(t *testing.T) {
	empty := t.TempDir()
	store, _ := storeAt(t)
	var out bytes.Buffer
	d, err := Ensure(empty, store, Discover(empty), true, strings.NewReader("1\n"), &out)
	if err != nil || d != Trust {
		t.Fatalf("empty project = %v, %v", d, err)
	}
	if out.Len() != 0 {
		t.Fatalf("empty project prompted:\n%s", out.String())
	}

	dir := project(t)
	if err := store.Trust(dir); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	d, err = Ensure(dir, store, Discover(dir), true, strings.NewReader("1\n"), &out)
	if err != nil || d != Trust {
		t.Fatalf("trusted project = %v, %v", d, err)
	}
	if out.Len() != 0 {
		t.Fatalf("trusted project prompted:\n%s", out.String())
	}
}

// The prompt shows names read from a folder the user has not trusted, which is
// exactly where hostile bytes arrive. None of them may reach the terminal as a
// control sequence, or the prompt could be made to read as something else.
func TestTrust_TRP09_PromptShowsNamesWithoutControlBytes(t *testing.T) {
	hostile := "\x1b[2J\x1b[Hall clear‮"
	f := Found{
		Config:       true,
		Extensions:   []string{"ext" + hostile},
		ContextFiles: []string{"/p/AGENTS.md" + hostile},
	}
	var out bytes.Buffer
	if _, err := Prompt("/work"+hostile, f, strings.NewReader("3\n"), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "ext") || !strings.Contains(text, "/work") {
		t.Fatalf("the names were dropped instead of cleaned:\n%s", text)
	}
	if strings.ContainsRune(text, '\x1b') || strings.ContainsRune(text, '‮') {
		t.Fatalf("the prompt passed control bytes through: %q", text)
	}
}
