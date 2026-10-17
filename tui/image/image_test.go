package image

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

// env builds a getenv from a map.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestImage_IMG01_Detect(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Protocol
	}{
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, Kitty},
		{"kitty", map[string]string{"KITTY_WINDOW_ID": "1"}, Kitty},
		{"wezterm", map[string]string{"WEZTERM_PANE": "1"}, Kitty},
		{"iterm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, ITerm2},
		{"tmux wins over ghostty", map[string]string{"TERM_PROGRAM": "ghostty", "TMUX": "/tmp/x"}, ""},
		{"no color", map[string]string{"TERM_PROGRAM": "ghostty", "NO_COLOR": "1"}, ""},
		{"forced kitty", map[string]string{"NOVA_IMAGE_PROTOCOL": "kitty"}, Kitty},
		{"forced none", map[string]string{"TERM_PROGRAM": "ghostty", "NOVA_IMAGE_PROTOCOL": "none"}, ""},
		{"unknown", map[string]string{"TERM": "xterm-256color"}, ""},
	}
	for _, tc := range cases {
		if got := detect(env(tc.env)); got.Images != tc.want {
			t.Errorf("%s: Images = %q, want %q", tc.name, got.Images, tc.want)
		}
	}
}

func TestImage_IMG02_CellSize(t *testing.T) {
	cols, rows := CellSize(1000, 500, 80, 0, 0)
	if cols != 80 || rows != 20 {
		t.Fatalf("CellSize = %d,%d, want 80,20", cols, rows)
	}
	if cols, rows := CellSize(0, 0, 80, 0, 0); cols != 1 || rows != 1 {
		t.Fatalf("degenerate CellSize = %d,%d, want 1,1", cols, rows)
	}
}

func TestImage_IMG03_Fallback(t *testing.T) {
	got := Fallback("shot.png", "image/png", 1280, 800)
	if got != "[Image: shot.png [image/png] 1280x800]" {
		t.Fatalf("Fallback = %q", got)
	}
	if got := Fallback("", "", 0, 0); got != "[image]" {
		t.Fatalf("empty Fallback = %q", got)
	}
}

func TestImage_IMG04_EncodeKittyChunks(t *testing.T) {
	small := EncodeKitty("aGk=", 4, 2, 7)
	if !strings.HasPrefix(small, "\x1b_G") || !strings.Contains(small, "c=4,r=2,i=7,C=1") || !strings.Contains(small, ";aGk=") {
		t.Fatalf("small sequence = %q", small)
	}

	big := strings.Repeat("A", kittyChunk+10)
	seq := EncodeKitty(big, 4, 2, 7)
	if !strings.Contains(seq, ",m=1;") || !strings.Contains(seq, "m=0;") {
		t.Fatalf("chunked sequence missing continuations: %q", seq[:80])
	}
	if strings.Count(seq, "\x1b_G") != 2 {
		t.Fatalf("chunks = %d, want 2", strings.Count(seq, "\x1b_G"))
	}
}

func TestImage_IMG05_Render(t *testing.T) {
	part := message.ContentPart{Type: message.PartImage, MimeType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not really a png"))}
	lines, ok := Render(part, 40, Capabilities{Images: Kitty})
	if !ok || len(lines) == 0 || !strings.HasPrefix(lines[0], "\x1b_G") {
		t.Fatalf("Render = %v, %v", lines, ok)
	}
	if _, ok := Render(part, 40, Capabilities{Images: ITerm2}); ok {
		t.Fatal("Render claimed iTerm2 support, which is deferred")
	}
	if _, ok := Render(message.ContentPart{Type: message.PartImage}, 40, Capabilities{Images: Kitty}); ok {
		t.Fatal("Render accepted a part with no data")
	}
}
