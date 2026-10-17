// Package image renders an image inline in a terminal that supports a graphics
// protocol. It detects the protocol from the environment, encodes a Kitty
// sequence, and falls back to a labelled text line everywhere else. It holds no
// Bubble Tea state: the transcript asks it for the lines an image occupies and
// reserves that height.
package image

import (
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"strings"
	"sync/atomic"

	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/message"
)

// Protocol is an inline image protocol a terminal may support.
type Protocol string

const (
	// Kitty is the Kitty graphics protocol, used by Kitty, Ghostty, WezTerm,
	// and Warp.
	Kitty Protocol = "kitty"
	// ITerm2 is the iTerm2 inline-image protocol. It is detected but not yet
	// rendered: the Bubble Tea repaint interaction needs a test first.
	ITerm2 Protocol = "iterm2"
)

// Capabilities is what the terminal can render.
type Capabilities struct {
	// Images is the protocol to use, or "" for the text fallback.
	Images Protocol
}

// Detect reads the environment for this terminal's capabilities.
func Detect() Capabilities { return detect(os.Getenv) }

// detect is Detect with an injectable environment, for tests.
func detect(getenv func(string) string) Capabilities {
	switch strings.ToLower(getenv("NOVA_IMAGE_PROTOCOL")) {
	case "kitty":
		return Capabilities{Images: Kitty}
	case "iterm2":
		return Capabilities{Images: ITerm2}
	case "none", "0":
		return Capabilities{}
	}
	if getenv("NO_COLOR") != "" {
		return Capabilities{}
	}
	// tmux and screen do not reliably forward a graphics protocol.
	if getenv("TMUX") != "" || strings.HasPrefix(getenv("TERM"), "screen") {
		return Capabilities{}
	}
	term := strings.ToLower(getenv("TERM"))
	program := strings.ToLower(getenv("TERM_PROGRAM"))
	switch {
	case getenv("KITTY_WINDOW_ID") != "", program == "kitty", program == "ghostty",
		strings.Contains(term, "ghostty"), getenv("GHOSTTY_RESOURCES_DIR") != "",
		getenv("WEZTERM_PANE") != "", program == "wezterm",
		program == "warpterminal", getenv("WARP_SESSION_ID") != "", program == "herdr":
		return Capabilities{Images: Kitty}
	case getenv("ITERM_SESSION_ID") != "", program == "iterm.app":
		return Capabilities{Images: ITerm2}
	}
	return Capabilities{}
}

// CellSize returns the columns and rows an image of w x h pixels occupies when
// fit inside maxWidthCells, preserving the aspect ratio. cellW and cellH are
// the terminal cell size in pixels; zero uses 9x18.
func CellSize(w, h, maxWidthCells, cellW, cellH int) (cols, rows int) {
	if cellW <= 0 {
		cellW = 9
	}
	if cellH <= 0 {
		cellH = 18
	}
	if maxWidthCells < 1 {
		maxWidthCells = 1
	}
	if w < 1 || h < 1 {
		return 1, 1
	}
	scale := float64(maxWidthCells*cellW) / float64(w)
	rows = int(math.Ceil(float64(h) * scale / float64(cellH)))
	if rows < 1 {
		rows = 1
	}
	return maxWidthCells, rows
}

// Fallback is the text line shown when the terminal cannot draw the image.
func Fallback(name, mime string, w, h int) string {
	var parts []string
	if name != "" {
		parts = append(parts, name)
	}
	if mime != "" {
		parts = append(parts, "["+mime+"]")
	}
	if w > 0 && h > 0 {
		parts = append(parts, fmt.Sprintf("%dx%d", w, h))
	}
	if len(parts) == 0 {
		return "[image]"
	}
	return "[Image: " + strings.Join(parts, " ") + "]"
}

// imageIDs hands out Kitty image ids, so two images never collide.
var imageIDs atomic.Uint32

// Render returns the lines a part occupies and whether the terminal can draw
// it. The first line carries the graphics sequence and the rest are blank, so
// the caller reserves the image's height. ok is false when the protocol is not
// Kitty (iTerm2 is deferred) or the part has no decodable image, and the caller
// shows Fallback instead.
func Render(part message.ContentPart, maxWidthCells int, caps Capabilities) (lines []string, ok bool) {
	if caps.Images != Kitty || part.Data == "" {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		return nil, false
	}
	w, h, okDim := media.Dimensions(data)
	if !okDim {
		w, h = 800, 600
	}
	cols, rows := CellSize(w, h, maxWidthCells, 0, 0)
	lines = make([]string, rows)
	lines[0] = EncodeKitty(part.Data, cols, rows, imageIDs.Add(1))
	return lines, true
}
