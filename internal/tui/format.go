package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/villcabo/redmine-tui/internal/i18n"
)

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// truncate shortens s to width display cells, adding an ellipsis.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// progressBar draws a bar of the given cell width for a 0-100 ratio.
func (s styles) progressBar(ratio, width int) string {
	ratio = max(0, min(100, ratio))
	filled := width * ratio / 100
	bar := s.accent.Render(strings.Repeat("█", filled)) + s.dim.Render(strings.Repeat("░", width-filled))
	return fmt.Sprintf("%s %d%%", bar, ratio)
}

// relativeTime renders t relative to now: "2 h ago", "yesterday" or a short date.
func relativeTime(tr i18n.Translator, now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return tr.T("time.now")
	case d < time.Hour:
		return tr.T("time.minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		return tr.T("time.hours", int(d.Hours()))
	}
	nl, tl := now.Local(), t.Local()
	if y := nl.AddDate(0, 0, -1); y.Year() == tl.Year() && y.YearDay() == tl.YearDay() {
		return tr.T("time.yesterday")
	}
	months := strings.Fields(tr.T("time.months"))
	if len(months) != 12 {
		return tl.Format("2006-01-02")
	}
	label := tr.T("time.day_month", tl.Day(), months[tl.Month()-1])
	if tl.Year() != nl.Year() {
		label += fmt.Sprintf(" %d", tl.Year())
	}
	return label
}
