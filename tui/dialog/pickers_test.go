//go:build !notui

package dialog

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/tui/styles"
)

var ctrlFKey = tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}

func TestModels_MPK01_ListAndCurrent(t *testing.T) {
	d := NewModelPicker([]ModelInfo{
		{ID: "openrouter/a", Name: "A", Provider: "openrouter", ContextWindow: 128000, InPerM: 0.27, OutPerM: 1.1, Current: true},
		{ID: "openrouter/b", Name: "B", Provider: "openrouter", ContextWindow: 200000},
	}, "openrouter/a")
	v := d.View(80, 12, styles.Styles{})
	if !strings.Contains(v, "A") || !strings.Contains(v, "B") {
		t.Fatalf("view missing catalog:\n%s", v)
	}
	if !strings.Contains(v, "*") {
		t.Fatalf("current model not marked:\n%s", v)
	}
}

func TestModels_MPK02_Filter(t *testing.T) {
	d := NewModelPicker([]ModelInfo{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, "")
	for _, r := range "beta" {
		d.HandleKey(runeKey(r))
	}
	if vis := d.(*modelPicker).visible(); len(vis) != 1 || vis[0].ID != "b" {
		t.Fatalf("visible = %+v", vis)
	}
}

func TestModels_MPK03_Choose(t *testing.T) {
	d := NewModelPicker([]ModelInfo{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, "")
	d.HandleKey(downKey)
	r := d.HandleKey(enterKey)
	if !r.Done || r.Value != "b" {
		t.Fatalf("result = %+v, want id b", r)
	}
}

func TestModels_MPK04_Formatted(t *testing.T) {
	if got := formatContext(128000); got != "128k" {
		t.Fatalf("formatContext = %q", got)
	}
	if got := formatContext(1_000_000); got != "1M" {
		t.Fatalf("formatContext = %q", got)
	}
	if got := formatPrice(0.27); got != "$0.27/M" {
		t.Fatalf("formatPrice = %q", got)
	}
	d := NewModelPicker([]ModelInfo{{ID: "a", Name: "A", ContextWindow: 128000, InPerM: 0.27, OutPerM: 1.1}}, "")
	v := d.View(80, 10, styles.Styles{})
	if !strings.Contains(v, "128k") || !strings.Contains(v, "$0.27/M") {
		t.Fatalf("formatted fields missing:\n%s", v)
	}
}

func TestModels_MPK05_Empty(t *testing.T) {
	d := NewModelPicker(nil, "")
	if v := d.View(60, 10, styles.Styles{}); !strings.Contains(v, "no models") {
		t.Fatalf("view:\n%s", v)
	}
	if r := d.HandleKey(enterKey); r.Done {
		t.Fatal("enter on an empty catalog must do nothing")
	}
}

func TestSessions_SSL01_NewestFirst(t *testing.T) {
	base := time.Unix(1000, 0)
	d := NewSessionList([]SessionInfo{
		{ID: "old", Path: "/old", Name: "old", Modified: base},
		{ID: "new", Path: "/new", Name: "new", Modified: base.Add(time.Hour)},
	})
	if got := d.(*sessionList).items[0].Path; got != "/new" {
		t.Fatalf("first = %q, want newest /new", got)
	}
}

func TestSessions_SSL02_Resume(t *testing.T) {
	d := NewSessionList([]SessionInfo{{ID: "a", Path: "/a", Name: "a", Modified: time.Unix(1000, 0)}})
	r := d.HandleKey(enterKey)
	choice, ok := r.Value.(SessionChoice)
	if !r.Done || !ok || choice.Path != "/a" || choice.Fork {
		t.Fatalf("result = %+v", r.Value)
	}
}

func TestSessions_SSL03_Fork(t *testing.T) {
	d := NewSessionList([]SessionInfo{{ID: "a", Path: "/a", Name: "a", Modified: time.Unix(1000, 0)}})
	r := d.HandleKey(ctrlFKey)
	choice, ok := r.Value.(SessionChoice)
	if !r.Done || !ok || choice.Path != "/a" || !choice.Fork {
		t.Fatalf("result = %+v", r.Value)
	}
}

func TestSessions_SSL05_Empty(t *testing.T) {
	d := NewSessionList(nil)
	if v := d.View(60, 10, styles.Styles{}); !strings.Contains(v, "no sessions") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestTree_TRV01_ActivePath(t *testing.T) {
	root := []TreeNode{
		{ID: "root", Type: "user", Children: []TreeNode{
			{ID: "mid", Type: "assistant", Children: []TreeNode{{ID: "leaf", Type: "user", Preview: "hello"}}},
			{ID: "sibling", Type: "user"},
		}},
	}
	d := NewTree(root, "leaf")
	byID := map[string]treeRow{}
	for _, r := range d.(*treeDialog).rows {
		byID[r.id] = r
	}
	for _, id := range []string{"root", "mid", "leaf"} {
		if !byID[id].active {
			t.Fatalf("%s is not on the active path", id)
		}
	}
	if byID["sibling"].active {
		t.Fatal("a sibling is marked active")
	}
	if v := d.View(60, 12, styles.Styles{}); !strings.Contains(v, "●") || !strings.Contains(v, "hello") {
		t.Fatalf("tree view missing active marker or preview:\n%s", v)
	}
}

func TestTree_TRV02_Navigate(t *testing.T) {
	root := []TreeNode{{ID: "a"}, {ID: "b", Preview: "x"}}
	d := NewTree(root, "a")
	d.HandleKey(downKey)
	r := d.HandleKey(enterKey)
	if !r.Done || r.Value != "b" {
		t.Fatalf("result = %+v, want b", r)
	}
}

func TestTree_TRV03_DeepAndWide(t *testing.T) {
	root := make([]TreeNode, 1000)
	for i := range root {
		root[i] = TreeNode{ID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Preview: "p"}
	}
	d := NewTree(root, root[500].ID)
	if got := len(d.(*treeDialog).rows); got != 1000 {
		t.Fatalf("rows = %d", got)
	}
	v := d.View(80, 20, styles.Styles{})
	if len(strings.Split(v, "\n")) != 20 {
		t.Fatalf("view is not the box height")
	}
	d.HandleKey(pgdownKey)
	if d.(*treeDialog).cursor == 0 {
		t.Fatal("pgdown did not move the cursor")
	}
}

func TestExtensions_EXV01_States(t *testing.T) {
	d := NewExtensions([]ExtInfo{
		{Name: "run", Version: "1.0.0", State: "running"},
		{Name: "fail", State: "failed", Restarts: 3, LastError: "boom"},
		{Name: "off", State: "disabled"},
	})
	v := d.View(70, 12, styles.Styles{})
	for _, want := range []string{"run", "running", "fail", "failed", "restarts:3", "boom", "off", "disabled"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
}

func TestExtensions_EXV02_Reload(t *testing.T) {
	d := NewExtensions([]ExtInfo{{Name: "a", State: "running"}})
	r := d.HandleKey(runeKey('r'))
	if !r.Done || r.Value != "reload" {
		t.Fatalf("result = %+v", r)
	}
}

func TestExtensions_EXV03_Empty(t *testing.T) {
	d := NewExtensions(nil)
	if v := d.View(60, 10, styles.Styles{}); !strings.Contains(v, "no extensions") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestHelp_HLP01_ListsAndScrolls(t *testing.T) {
	var keys []KeyHelp
	for i := 0; i < 5; i++ {
		keys = append(keys, KeyHelp{Key: "ctrl+k", Description: "do something"})
	}
	d := NewHelp(keys, []string{"help", "model", "resume"})
	v := d.View(60, 14, styles.Styles{})
	if !strings.Contains(v, "keys") || !strings.Contains(v, "commands") {
		t.Fatalf("view missing sections:\n%s", v)
	}
	if !strings.Contains(v, "/help") {
		t.Fatalf("view missing commands:\n%s", v)
	}
	d.HandleKey(pgdownKey)
	if d.(*helpDialog).scroll == 0 {
		t.Fatal("pgdown did not scroll help")
	}
}
