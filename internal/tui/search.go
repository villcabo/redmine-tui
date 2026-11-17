package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

// searchModel lists the results of a full-text search.
type searchModel struct {
	query   string
	results []redmine.SearchResult
	gen     int
	loading bool
	err     error
	cursor  int
	top     int
}

func (s *searchModel) apply(msg searchMsg) {
	if msg.gen != s.gen {
		return
	}
	s.loading = false
	if msg.err != nil {
		s.err = msg.err
		return
	}
	s.results = msg.results
	s.cursor, s.top = 0, 0
}

func (s *searchModel) selected() *redmine.SearchResult {
	if s.cursor < 0 || s.cursor >= len(s.results) {
		return nil
	}
	return &s.results[s.cursor]
}

func (s *searchModel) move(key string, e env) {
	page := max(1, s.rowsFit(e)-1)
	switch key {
	case "up", "k":
		s.cursor--
	case "down", "j":
		s.cursor++
	case "pgup":
		s.cursor -= page
	case "pgdown":
		s.cursor += page
	case "home":
		s.cursor = 0
	case "end", "G":
		s.cursor = len(s.results) - 1
	default:
		return
	}
	s.cursor = max(0, min(s.cursor, len(s.results)-1))
}

// rowsFit is how many two-line results fit under the title.
func (s *searchModel) rowsFit(e env) int { return max(1, (e.h-1-2)/2) }

func (s *searchModel) view(e env, frame string) string {
	var b strings.Builder
	b.WriteString(e.st.title.Render(" "+e.t("search.title", s.query)) + e.st.subtitle.Render(" · "+e.t("search.count", len(s.results))) + "\n\n")
	switch {
	case s.loading:
		b.WriteString(loadingView(e, frame, e.t("search.loading")))
	case s.err != nil:
		b.WriteString(errorBox(e, s.err))
	case len(s.results) == 0:
		b.WriteString("  " + e.st.dim.Render(e.t("search.empty")))
	default:
		b.WriteString(s.rows(e))
	}
	return b.String()
}

func (s *searchModel) rows(e env) string {
	fit := s.rowsFit(e)
	if s.cursor < s.top {
		s.top = s.cursor
	}
	if s.cursor >= s.top+fit {
		s.top = s.cursor - fit + 1
	}
	end := min(len(s.results), s.top+fit)
	var b strings.Builder
	for i := s.top; i < end; i++ {
		r := s.results[i]
		marker, title := "  ", e.st.text.Render(r.Title)
		if i == s.cursor {
			marker, title = e.st.cursor.Render("▸ "), e.st.selected.Render(r.Title)
		}
		desc := strings.Join(strings.Fields(r.Description), " ")
		second := "  " + e.st.dim.Render(e.tr.DateTime(r.Datetime)+" · "+desc)
		b.WriteString(truncate(marker+title, e.w-1) + "\n" + truncate(second, e.w-1) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) startSearch(query string) tea.Cmd {
	gen := 1
	if m.search != nil {
		gen = m.search.gen + 1
	}
	m.search = &searchModel{query: query, gen: gen, loading: true}
	return tea.Batch(searchCmd(m.api, gen, query), m.spin.Tick)
}
