package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func plainLines(s string) []string {
	var out []string
	for _, l := range strings.Split(ansi.Strip(s), "\n") {
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

func TestMarkdownHeadingsHaveNoHashes(t *testing.T) {
	src := "# One\n\n## Two\n\n### Three\n\n#### Four\n\n##### Five\n\n###### Six\n"
	for name, e := range map[string]env{"dark": {dark: true}, "light": {}, "no color": {noColor: true}} {
		t.Run(name, func(t *testing.T) {
			out := ansi.Strip(markdown(e, src, 60))
			if strings.Contains(out, "#") {
				t.Errorf("heading prefix left in output:\n%s", out)
			}
			for _, w := range []string{"One", "Two", "Three", "Four", "Five", "Six"} {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
		})
	}
}

func TestMarkdownLinks(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantText  []string
		hidden    []string
		wantOSC8  string
		noHyperln bool
	}{
		{name: "inline link shows only its text", src: "See [checkout runbook](https://x.example/a) now.", wantText: []string{"See checkout runbook now."}, hidden: []string{"https://x.example/a", "(#)"}, wantOSC8: "https://x.example/a"},
		{name: "bare url stays visible", src: "Docs at https://y.example/b today.", wantText: []string{"https://y.example/b"}},
		{name: "angle autolink stays visible", src: "Docs <https://y.example/b>", wantText: []string{"https://y.example/b"}},
		{name: "link text and bare url together", src: "[text](https://x.example/a) and https://y.example/b", wantText: []string{"text", "https://y.example/b"}, hidden: []string{"https://x.example/a"}},
		{name: "reference link", src: "See [the doc][d].\n\n[d]: https://x.example/ref", wantText: []string{"See the doc."}, hidden: []string{"https://x.example/ref"}, wantOSC8: "https://x.example/ref"},
		{name: "emphasis inside link text", src: "[**bold** word](https://x.example/e)", wantText: []string{"bold word"}, hidden: []string{"https://x.example/e", "*"}},
		{name: "link with title", src: `[t](https://x.example/t "title")`, wantText: []string{"t"}, hidden: []string{"https://x.example/t", "title"}},
		{name: "image is untouched", src: "![alt](https://x.example/i.png)", wantText: []string{"alt"}},
		{name: "link in a code block is untouched", src: "```\n[a](https://x.example/c)\n```", wantText: []string{"[a](https://x.example/c)"}},
		{name: "link in inline code is untouched", src: "use `[a](https://x.example/c)` here", wantText: []string{"[a](https://x.example/c)"}},
		{name: "relative link keeps text only", src: "[issue](/issues/5)", wantText: []string{"issue"}, hidden: []string{"/issues/5"}},
		{name: "link in a table", src: "| a | b |\n|---|---|\n| [x](https://x.example/tb) | 2 |", wantText: []string{"x"}, hidden: []string{"https://x.example/tb", "[1]"}},
		{name: "text with brackets that is not a link", src: "a [b] c", wantText: []string{"a [b] c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := markdown(env{dark: true}, tt.src, 60)
			out := ansi.Strip(raw)
			for _, w := range tt.wantText {
				if !strings.Contains(out, w) {
					t.Errorf("want %q in:\n%s", w, out)
				}
			}
			for _, h := range tt.hidden {
				if strings.Contains(out, h) {
					t.Errorf("%q must not be visible in:\n%s", h, out)
				}
			}
			if strings.ContainsAny(raw, "\uE000\uE001\uF8FF") {
				t.Errorf("link marker leaked: %q", raw)
			}
			if tt.wantOSC8 != "" && !strings.Contains(raw, "\x1b]8;;"+tt.wantOSC8) {
				t.Errorf("want an OSC 8 hyperlink to %s in %q", tt.wantOSC8, raw)
			}
		})
	}
}

func TestMarkdownLinkNoColorHasNoEscapes(t *testing.T) {
	out := markdown(env{noColor: true}, "[t](https://x.example/a)", 60)
	if strings.Contains(out, "\x1b") || strings.Contains(out, "https://x.example/a") {
		t.Errorf("no-color output should be plain text only: %q", out)
	}
}

func TestMarkdownLinkHyperlinkClosesOnEveryLine(t *testing.T) {
	raw := markdown(env{dark: true}, "[one two three four five six seven eight nine ten](https://x.example/w)", 24)
	for i, l := range strings.Split(raw, "\n") {
		if opens, closes := strings.Count(l, "\x1b]8;;https://"), strings.Count(l, ansi.ResetHyperlink()); opens != closes {
			t.Errorf("line %d has %d opens and %d closes: %q", i, opens, closes, l)
		}
	}
}

func TestMarkdownForgedMarkersAreDropped(t *testing.T) {
	out := ansi.Strip(markdown(env{dark: true}, "plain \uE000text\uF8FF here", 60))
	if strings.ContainsAny(out, "\uE000\uF8FF") {
		t.Errorf("user text must not carry markers: %q", out)
	}
}

func TestMarkdownKeepsSingleNewlines(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{name: "single newline stays a break", src: "line one\nline two", want: []string{"line one", "line two"}},
		{name: "blank line still separates paragraphs", src: "para one\n\npara two", want: []string{"para one", "", "para two"}},
		{name: "list items stay separate", src: "- alpha\n- beta\n- gamma", want: []string{"• alpha", "• beta", "• gamma"}},
		{name: "code block keeps its lines", src: "```\nfoo\nbar\n```", want: []string{"foo", "bar"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plainLines(markdown(env{dark: true}, tt.src, 60))
			joined := strings.Join(got, "\n")
			var at int
			for _, w := range tt.want {
				idx := -1
				for i := at; i < len(got); i++ {
					if strings.Contains(got[i], w) && (w != "" || got[i] == "") {
						idx = i
						break
					}
				}
				if idx < 0 {
					t.Fatalf("want line %q in order in:\n%s", w, joined)
				}
				at = idx + 1
			}
		})
	}
}

func TestMarkdownTableSurvives(t *testing.T) {
	out := ansi.Strip(markdown(env{dark: true}, "| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |", 60))
	for _, w := range []string{"a", "b", "1", "2", "3", "4"} {
		if !strings.Contains(out, w) {
			t.Errorf("table lost %q:\n%s", w, out)
		}
	}
	if strings.Count(out, "1") != 1 || strings.Count(out, "3") != 1 {
		t.Errorf("table rows should not be duplicated:\n%s", out)
	}
}

func TestInputViewShowsWholePlaceholder(t *testing.T) {
	tests := []struct {
		name        string
		placeholder string
		width       int
		focused     bool
	}{
		{name: "focused", placeholder: "required", focused: true},
		{name: "focused with width", placeholder: "required", width: 30, focused: true},
		{name: "blurred", placeholder: "obligatorio"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInput("", tt.placeholder)
			if tt.width > 0 {
				in.SetWidth(tt.width)
			}
			if tt.focused {
				in.Focus()
			}
			got := ansi.Strip(inputView(in))
			if !strings.HasPrefix(got, " "+tt.placeholder) {
				t.Errorf("want the cursor cell, then the whole placeholder: %q", got)
			}
			if tt.width > 0 && ansi.StringWidth(got) > tt.width {
				t.Errorf("view is %d cells wide, field is %d: %q", ansi.StringWidth(got), tt.width, got)
			}
		})
	}
}

func TestInputViewWithValueIsUntouched(t *testing.T) {
	in := newInput("> ", "hint")
	in.Focus()
	in.SetValue("typed")
	got := ansi.Strip(inputView(in))
	if !strings.Contains(got, "typed") || strings.Contains(got, "hint") {
		t.Errorf("got %q", got)
	}
}
