//go:build !notui

package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Styles holds every lipgloss style the TUI uses. It is built once per theme
// and profile and then shared; styles are value types, so sharing is safe.
type Styles struct {
	Profile colorprofile.Profile
	Theme   Theme

	// Regions.
	Header  lipgloss.Style
	Footer  lipgloss.Style
	Sidebar lipgloss.Style

	// Text and semantics.
	Text    lipgloss.Style
	Muted   lipgloss.Style
	Dim     lipgloss.Style
	Bold    lipgloss.Style
	Spark   lipgloss.Style
	Core    lipgloss.Style
	Warm    lipgloss.Style
	Success lipgloss.Style
	Error   lipgloss.Style

	// Transcript items.
	User       lipgloss.Style
	Assistant  lipgloss.Style
	Thinking   lipgloss.Style
	Tool       lipgloss.Style
	ToolError  lipgloss.Style
	Compaction lipgloss.Style
	Notice     lipgloss.Style

	// Tool-card panels, tinted by state: running, success, and error.
	ToolPanel      lipgloss.Style
	ToolPanelOK    lipgloss.Style
	ToolPanelError lipgloss.Style
	// UserPanel is the background behind the user's own messages.
	UserPanel lipgloss.Style

	// The input area.
	Editor             lipgloss.Style
	EditorFocused      lipgloss.Style
	Placeholder        lipgloss.Style
	Completion         lipgloss.Style
	CompletionSelected lipgloss.Style

	// Diffs.
	DiffAdd     lipgloss.Style
	DiffRemove  lipgloss.Style
	DiffContext lipgloss.Style
	DiffHunk    lipgloss.Style
}

// Build derives every style from the theme's tokens and degrades the colours
// to the profile. It never emits a colour for the no-colour profiles or for
// Mono.
func Build(t Theme, p colorprofile.Profile) Styles {
	b := painter{theme: t, profile: p, mono: len(t.Colors) == 0}
	return Styles{
		Profile: p,
		Theme:   t,

		Header:  b.fg("spark").Bold(true),
		Footer:  b.fg("muted"),
		Sidebar: b.fg("muted"),

		Text:    b.fg("ink"),
		Muted:   b.fg("muted"),
		Dim:     b.fg("muted").Faint(true),
		Bold:    b.fg("ink").Bold(true),
		Spark:   b.fg("spark"),
		Core:    b.fg("core"),
		Warm:    b.fg("warm"),
		Success: b.fg("success"),
		Error:   b.fg("error"),

		User:       b.fg("core").Bold(true),
		Assistant:  b.fg("ink"),
		Thinking:   b.fg("muted").Faint(true).Italic(true),
		Tool:       b.fg("spark"),
		ToolError:  b.fg("error"),
		Compaction: b.fg("muted").Faint(true),
		Notice:     b.fg("warm"),

		ToolPanel:      b.bgstyle("panel"),
		ToolPanelOK:    b.bgstyle("panelok"),
		ToolPanelError: b.bgstyle("panelerr"),
		UserPanel:      b.bgstyle("paneluser"),

		Editor:             b.fg("ink"),
		EditorFocused:      b.fg("ink").Bold(true),
		Placeholder:        b.fg("muted").Faint(true),
		Completion:         b.fg("ink").Background(b.bg("bg")),
		CompletionSelected: b.fg("bg").Background(b.bg("spark")).Bold(true),

		DiffAdd:     b.bgfg("ink", "success"),
		DiffRemove:  b.bgfg("ink", "error"),
		DiffContext: b.fg("muted"),
		DiffHunk:    b.fg("spark").Bold(true),
	}
}

// painter applies theme tokens to a style, respecting Mono and the profile.
type painter struct {
	theme   Theme
	profile colorprofile.Profile
	mono    bool
}

// fg returns a new style with the token as its foreground, or an unstyled one
// when the token is absent, the theme is Mono, or the profile has no colour.
func (p painter) fg(token string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if c := p.color(token); c != nil {
		s = s.Foreground(c)
	}
	return s
}

// bg is fg for backgrounds.
func (p painter) bg(token string) color.Color {
	return p.color(token)
}

// bgstyle returns a style whose background is the token's colour, or an
// unstyled one when the token is absent, the theme is Mono, or the profile has
// no colour. It is the tool-card panel: only the background is set, so the
// card's own foreground styles stay in charge.
func (p painter) bgstyle(token string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if c := p.color(token); c != nil {
		s = s.Background(c)
	}
	return s
}

// bgfg returns a style with a foreground and a background, each omitted when
// the profile has no colour.
func (p painter) bgfg(fgToken, bgToken string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if c := p.color(fgToken); c != nil {
		s = s.Foreground(c)
	}
	if c := p.color(bgToken); c != nil {
		s = s.Background(c)
	}
	return s
}

// color resolves a token to a profile-safe colour, or nil for no colour.
func (p painter) color(token string) color.Color {
	if p.mono {
		return nil
	}
	hex := p.theme.Colors[token]
	if hex == "" {
		return nil
	}
	return p.profile.Convert(lipgloss.Color(hex))
}
