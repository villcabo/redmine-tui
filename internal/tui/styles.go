package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

// tone is a semantic color family shared by badges and progress.
type tone int

const (
	toneBlue tone = iota
	toneGreen
	toneAmber
	toneMagenta
	toneRed
	toneGray
)

// colorTones maps the color names accepted in [status_colors] to tones.
var colorTones = map[string]tone{
	"green": toneGreen, "blue": toneBlue, "amber": toneAmber,
	"magenta": toneMagenta, "red": toneRed, "gray": toneGray,
}

// statusTone colors a status without knowing anything about the instance's ids:
// a configured color for its name wins, closed is green, the default status is
// blue and every other open status is amber.
func (e env) statusTone(s redmine.Status) tone {
	if c, ok := e.statusColors[lower(s.Name)]; ok {
		if t, ok := colorTones[c]; ok {
			return t
		}
	}
	if s.IsClosed || e.statuses[s.ID].IsClosed {
		return toneGreen
	}
	if s.ID == e.defaultStatus {
		return toneBlue
	}
	return toneAmber
}

// priorityTone colors a priority by its place in the instance's priority list:
// the default priority is amber, anything above it red, anything below it gray.
// Without the list it falls back to common names.
func (e env) priorityTone(p redmine.IDName) tone {
	pos, def := -1, -1
	for i, pr := range e.priorities {
		if pr.ID == p.ID {
			pos = i
		}
		if pr.IsDefault {
			def = i
		}
	}
	if pos >= 0 && def >= 0 {
		switch {
		case pos == def:
			return toneAmber
		case pos > def:
			return toneRed
		default:
			return toneGray
		}
	}
	switch lower(p.Name) {
	case "high", "urgent", "immediate", "alta", "urgente", "inmediata":
		return toneRed
	case "normal", "medium", "media":
		return toneAmber
	case "low", "baja":
		return toneGray
	}
	return toneBlue
}

// styles holds every Lip Gloss style the UI uses for one color setup.
type styles struct {
	noColor bool

	text, dim, accent, danger lipgloss.Style
	title, subtitle           lipgloss.Style
	card, box, boxTitle       lipgloss.Style
	tabActive, tabIdle        lipgloss.Style
	help, rule                lipgloss.Style
	cursor, selected          lipgloss.Style
	rowBg                     color.Color
	errorBox                  lipgloss.Style

	badges   map[tone]lipgloss.Style
	dots     map[tone]lipgloss.Style
	userTint []lipgloss.Style
}

// newStyles builds the palette for a dark or light background. With noColor
// set the styles keep only attributes (bold, reverse) so NO_COLOR is honored.
func newStyles(dark, noColor bool) styles {
	pick := lipgloss.LightDark(dark)
	c := func(light, darkHex string) color.Color {
		if noColor {
			return lipgloss.NoColor{}
		}
		return pick(lipgloss.Color(light), lipgloss.Color(darkHex))
	}

	text := c("#1f2328", "#e6edf3")
	dim := c("#6e7781", "#7d8590")
	border := c("#afb8c1", "#3d444d")
	accent := c("#0969da", "#58a6ff")
	danger := c("#cf222e", "#ff7b72")

	badge := func(bg, fg color.Color) lipgloss.Style {
		st := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		if noColor {
			return st.Reverse(true)
		}
		return st.Background(bg).Foreground(fg)
	}
	white := lipgloss.Color("#ffffff")
	ink := lipgloss.Color("#1f2328")

	s := styles{
		noColor:   noColor,
		text:      lipgloss.NewStyle().Foreground(text),
		dim:       lipgloss.NewStyle().Foreground(dim),
		accent:    lipgloss.NewStyle().Foreground(accent),
		danger:    lipgloss.NewStyle().Foreground(danger),
		title:     lipgloss.NewStyle().Bold(true).Foreground(text),
		subtitle:  lipgloss.NewStyle().Foreground(dim),
		card:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1),
		box:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1),
		boxTitle:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		tabActive: lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true),
		tabIdle:   lipgloss.NewStyle().Foreground(dim),
		help:      lipgloss.NewStyle().Foreground(dim),
		rule:      lipgloss.NewStyle().Foreground(border),
		cursor:    lipgloss.NewStyle().Bold(true).Foreground(accent),
		selected:  lipgloss.NewStyle().Bold(true),
		errorBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(danger).Foreground(danger).Padding(0, 1),
		badges: map[tone]lipgloss.Style{
			toneBlue:    badge(c("#0969da", "#1f6feb"), white),
			toneGreen:   badge(c("#1a7f37", "#2ea043"), white),
			toneAmber:   badge(c("#bf8700", "#d29922"), ink),
			toneMagenta: badge(c("#bf3989", "#db61a2"), white),
			toneRed:     badge(c("#cf222e", "#da3633"), white),
			toneGray:    badge(c("#6e7781", "#484f58"), white),
		},
	}
	s.dots = map[tone]lipgloss.Style{
		toneBlue:    lipgloss.NewStyle().Foreground(c("#0969da", "#58a6ff")),
		toneGreen:   lipgloss.NewStyle().Foreground(c("#1a7f37", "#3fb950")),
		toneAmber:   lipgloss.NewStyle().Foreground(c("#bf8700", "#d29922")),
		toneMagenta: lipgloss.NewStyle().Foreground(c("#bf3989", "#db61a2")),
		toneRed:     lipgloss.NewStyle().Foreground(c("#cf222e", "#ff7b72")),
		toneGray:    lipgloss.NewStyle().Foreground(c("#6e7781", "#7d8590")),
	}
	if !noColor {
		s.rowBg = c("#ddf4ff", "#1c2b3f")
	}
	for _, hex := range [][2]string{
		{"#0550ae", "#79c0ff"}, {"#116329", "#7ee787"}, {"#8250df", "#d2a8ff"},
		{"#9a6700", "#e3b341"}, {"#bf3989", "#ff7eb6"}, {"#1b7c83", "#56d4dd"},
	} {
		s.userTint = append(s.userTint, lipgloss.NewStyle().Bold(true).Foreground(c(hex[0], hex[1])))
	}
	return s
}

// badge renders text as a colored badge.
func (s styles) badge(t tone, text string) string {
	return s.badges[t].Render(text)
}

// dot renders a colored bullet for a tone.
func (s styles) dot(t tone) string { return s.dots[t].Render("●") }

// userStyle gives each user a stable color.
func (s styles) userStyle(id int) lipgloss.Style {
	if id < 0 {
		id = -id
	}
	return s.userTint[id%len(s.userTint)]
}
