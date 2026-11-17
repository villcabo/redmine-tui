package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

func TestStatusTone(t *testing.T) {
	e := env{
		statuses:      map[int]redmine.Status{5: {ID: 5, IsClosed: true}},
		defaultStatus: 1,
		statusColors:  map[string]string{"blocked": "red"},
	}
	tests := []struct {
		name   string
		status redmine.Status
		want   tone
	}{
		{name: "closed is green", status: redmine.Status{ID: 5, Name: "Closed", IsClosed: true}, want: toneGreen},
		{name: "closed known only from the status list", status: redmine.Status{ID: 5, Name: "Closed"}, want: toneGreen},
		{name: "default is blue", status: redmine.Status{ID: 1, Name: "New"}, want: toneBlue},
		{name: "any other open status is amber", status: redmine.Status{ID: 2, Name: "In progress"}, want: toneAmber},
		{name: "configured color wins", status: redmine.Status{ID: 9, Name: "Blocked"}, want: toneRed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.statusTone(tt.status); got != tt.want {
				t.Errorf("statusTone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPriorityTone(t *testing.T) {
	withList := env{priorities: testPriorities()}
	noList := env{}
	tests := []struct {
		name string
		e    env
		in   redmine.IDName
		want tone
	}{
		{name: "default priority is amber", e: withList, in: redmine.IDName{ID: 2, Name: "Normal"}, want: toneAmber},
		{name: "above the default is red", e: withList, in: redmine.IDName{ID: 4, Name: "Urgent"}, want: toneRed},
		{name: "below the default is gray", e: withList, in: redmine.IDName{ID: 1, Name: "Low"}, want: toneGray},
		{name: "renamed priorities still work by position", e: env{priorities: []redmine.Priority{{ID: 7, Name: "Meh"}, {ID: 8, Name: "Usual", IsDefault: true}, {ID: 9, Name: "Yikes"}}}, in: redmine.IDName{ID: 9, Name: "Yikes"}, want: toneRed},
		{name: "no list, by name high", e: noList, in: redmine.IDName{Name: "High"}, want: toneRed},
		{name: "no list, by name normal", e: noList, in: redmine.IDName{Name: "Normal"}, want: toneAmber},
		{name: "no list, by name low", e: noList, in: redmine.IDName{Name: "Low"}, want: toneGray},
		{name: "no list, unknown", e: noList, in: redmine.IDName{Name: "Whatever"}, want: toneBlue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.priorityTone(tt.in); got != tt.want {
				t.Errorf("priorityTone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	statuses := map[int]redmine.Status{1: {ID: 1, Name: "New"}, 8: {ID: 8, Name: "Closed"}}
	issue := &redmine.Issue{
		Author:       redmine.IDName{ID: 25, Name: "Jane"},
		AssignedTo:   &redmine.IDName{ID: 30, Name: "John"},
		CustomFields: []redmine.CustomField{{ID: 6, Name: "Team"}},
	}
	r := newResolver(i18n.New(i18n.EN), issue, statuses)
	tests := []struct {
		name string
		in   redmine.JournalDetail
		want string
	}{
		{name: "status", in: redmine.JournalDetail{Property: "attr", Name: "status_id", OldValue: sp("1"), NewValue: sp("8")}, want: "status: New → Closed"},
		{name: "unknown status id", in: redmine.JournalDetail{Property: "attr", Name: "status_id", OldValue: sp("1"), NewValue: sp("99")}, want: "status: New → 99"},
		{name: "done ratio", in: redmine.JournalDetail{Property: "attr", Name: "done_ratio", OldValue: sp("0"), NewValue: sp("100")}, want: "% done: 0 → 100"},
		{name: "assignee known", in: redmine.JournalDetail{Property: "attr", Name: "assigned_to_id", NewValue: sp("30")}, want: "assignee: empty → John"},
		{name: "assignee unknown", in: redmine.JournalDetail{Property: "attr", Name: "assigned_to_id", OldValue: sp("77"), NewValue: sp("30")}, want: "assignee changed"},
		{name: "description hides body", in: redmine.JournalDetail{Property: "attr", Name: "description", OldValue: sp("old"), NewValue: sp("new")}, want: "description updated"},
		{name: "custom field known", in: redmine.JournalDetail{Property: "cf", Name: "6", NewValue: sp("Backend")}, want: "Team: empty → Backend"},
		{name: "custom field unknown", in: redmine.JournalDetail{Property: "cf", Name: "9", OldValue: sp("a"), NewValue: sp("b")}, want: "field #9: a → b"},
		{name: "long value is cut", in: redmine.JournalDetail{Property: "cf", Name: "6", NewValue: sp(strings.Repeat("x", 100))}, want: "Team: empty → " + strings.Repeat("x", maxValueRunes-1) + "…"},
		{name: "unknown attr", in: redmine.JournalDetail{Property: "attr", Name: "fixed_version_id"}, want: "fixed_version_id changed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.describe(tt.in); got != tt.want {
				t.Errorf("describe() = %q, want %q", got, tt.want)
			}
		})
	}

	es := newResolver(i18n.New(i18n.ES), issue, statuses)
	if got, want := es.describe(redmine.JournalDetail{Property: "attr", Name: "status_id", OldValue: sp("1"), NewValue: sp("8")}), "estado: New → Closed"; got != want {
		t.Errorf("Spanish describe() = %q, want %q", got, want)
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.Local)
	tests := []struct {
		name string
		lang i18n.Lang
		ago  time.Duration
		want string
	}{
		{name: "just now", lang: i18n.EN, ago: 20 * time.Second, want: "just now"},
		{name: "minutes", lang: i18n.EN, ago: 5 * time.Minute, want: "5 min ago"},
		{name: "hours en", lang: i18n.EN, ago: 2 * time.Hour, want: "2 h ago"},
		{name: "hours es", lang: i18n.ES, ago: 2 * time.Hour, want: "hace 2 h"},
		{name: "yesterday en", lang: i18n.EN, ago: 30 * time.Hour, want: "yesterday"},
		{name: "yesterday es", lang: i18n.ES, ago: 30 * time.Hour, want: "ayer"},
		{name: "older es", lang: i18n.ES, ago: 5 * 24 * time.Hour, want: "12 nov"},
		{name: "older en", lang: i18n.EN, ago: 5 * 24 * time.Hour, want: "Nov 12"},
		{name: "another year", lang: i18n.ES, ago: 400 * 24 * time.Hour, want: "13 oct 2025"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relativeTime(i18n.New(tt.lang), now, now.Add(-tt.ago)); got != tt.want {
				t.Errorf("relativeTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func loadedDetail(t *testing.T, width int) *Model {
	t.Helper()
	api := &fakeAPI{issue: detailIssue()}
	m := newTestModel(api, Options{StartID: 123})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m.Update(issueMsg{uid: m.stack[0].uid, issue: detailIssue()})
	return m
}

func TestDetailRender(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		t.Run(fmt.Sprintf("width %d", width), func(t *testing.T) {
			m := loadedDetail(t, width)
			out := ansi.Strip(m.render())
			containsAll(t, out, "Crash when saving", "#123", "In progress", "History", "Children (1)", "Relations (1)", "Details", "Custom fields", "Backend", "reviewed", "Jane Doe", "READ ONLY"[:0])
			for i, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("line %d is %d cells wide: %q", i, w, line)
				}
			}
			if h := strings.Count(out, "\n") + 1; h != 40 {
				t.Errorf("rendered %d lines, want 40", h)
			}
		})
	}
}

func TestDetailTabs(t *testing.T) {
	m := loadedDetail(t, 100)
	tests := []struct {
		name string
		keys []string
		want []string
	}{
		{name: "history", keys: []string{"tab"}, want: []string{"status: New → In progress", "Jane Doe", "closing"}},
		{name: "children", keys: []string{"tab", "tab"}, want: []string{"#124", "Child one"}},
		{name: "relations", keys: []string{"tab", "tab", "tab"}, want: []string{"related to", "#9"}},
		{name: "wraps to first", keys: []string{"tab", "tab", "tab", "tab"}, want: []string{"Details"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.stack[0].setTab(tabOverview, m.env())
			for _, k := range tt.keys {
				m.stack[0].handleKey(k, key(k), m.env())
			}
			containsAll(t, ansi.Strip(m.render()), tt.want...)
		})
	}
}

func TestDetailRendersInSpanish(t *testing.T) {
	m := newTestModel(&fakeAPI{}, Options{StartID: 123, Lang: i18n.ES})
	m.Update(issueMsg{uid: m.stack[0].uid, issue: detailIssue()})
	containsAll(t, ansi.Strip(m.render()), "Historial", "Hijas (1)", "Relaciones (1)", "Ficha", "Campos personalizados", "Descripción")
}

func TestEscStackNavigation(t *testing.T) {
	m := loadedDetail(t, 100)
	m.stack[0].setTab(tabChildren, m.env())
	m.Update(key("enter"))
	if len(m.stack) != 2 || m.stack[1].id != 124 {
		t.Fatalf("enter on a child should push it, stack = %d", len(m.stack))
	}
	m.Update(key("esc"))
	if len(m.stack) != 1 {
		t.Fatalf("esc should pop to the previous issue, stack = %d", len(m.stack))
	}
	m.Update(key("esc"))
	if len(m.stack) != 0 {
		t.Fatalf("esc on the first issue should go to the list, stack = %d", len(m.stack))
	}
}

func TestErrorShownTranslated(t *testing.T) {
	tests := []struct {
		name string
		lang i18n.Lang
		want []string
	}{
		{name: "english", lang: i18n.EN, want: []string{"Could not load", "does not exist", "(404)", "not found here"}},
		{name: "spanish", lang: i18n.ES, want: []string{"No se pudo cargar", "no existe", "(404)", "not found here"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(&fakeAPI{}, Options{StartID: 77, Lang: tt.lang})
			m.Update(issueMsg{uid: m.stack[0].uid, err: &redmine.APIError{Status: 404, Messages: []string{"not found here"}}})
			containsAll(t, ansi.Strip(m.render()), tt.want...)
		})
	}
}

// ---- list -------------------------------------------------------------

func listIssues() []redmine.Issue {
	a := testIssue(42, "Crash when saving a very long subject that will need to be cut somewhere", redmine.Status{ID: 1, Name: "New"})
	b := testIssue(43, "Add dark mode", redmine.Status{ID: 2, Name: "In progress"})
	b.Tracker = redmine.IDName{ID: 2, Name: "Feature"}
	b.Priority = redmine.IDName{ID: 4, Name: "Urgent"}
	b.UpdatedOn = fixedNow.Add(-30 * time.Hour)
	c := testIssue(44, "Old thing", redmine.Status{ID: 5, Name: "Closed", IsClosed: true})
	c.AssignedTo = nil
	c.Project = redmine.IDName{ID: 2, Name: "Backend"}
	c.Priority = redmine.IDName{ID: 1, Name: "Low"}
	c.UpdatedOn = fixedNow.Add(-5 * 24 * time.Hour)
	return []redmine.Issue{a, b, c}
}

func loadedList(t *testing.T, width, height int) *Model {
	t.Helper()
	m := newTestModel(&fakeAPI{}, Options{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(issuesMsg{gen: m.list.startLoad(), page: redmine.IssuePage{Issues: listIssues(), Total: 3}})
	return m
}

func TestListColumnsByWidth(t *testing.T) {
	tests := []struct {
		name    string
		width   int
		shown   []string
		hidden  []string
		preview bool
	}{
		{name: "70 hides project, tracker and priority", width: 70, shown: []string{"Status", "Subject", "Assignee", "Updated", "#42", "John Roe", "2 h ago"}, hidden: []string{"Project", "Tracker", "Priority"}},
		{name: "80 hides project and tracker", width: 80, shown: []string{"Priority", "Subject"}, hidden: []string{"Project", "Tracker"}},
		{name: "92 hides only the project", width: 92, shown: []string{"Priority", "Tracker", "Subject", "Feature"}, hidden: []string{"Project"}},
		{name: "120 shows every column", width: 120, shown: []string{"Priority", "Tracker", "Project", "Backend", "yesterday", "Nov 12"}},
		{name: "160 adds the preview", width: 160, shown: []string{"Preview", "Subject"}, preview: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := loadedList(t, tt.width, 30)
			out := ansi.Strip(m.render())
			header := strings.Split(out, "\n")[3]
			for _, s := range tt.shown {
				if !strings.Contains(out, s) {
					t.Errorf("output lacks %q\n%s", s, out)
				}
			}
			for _, s := range tt.hidden {
				if strings.Contains(header, s) {
					t.Errorf("column %q should be hidden, header: %q", s, header)
				}
			}
			if got := strings.Contains(out, "Preview"); got != tt.preview {
				t.Errorf("preview shown = %v, want %v", got, tt.preview)
			}
			for i, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > tt.width {
					t.Errorf("line %d is %d cells wide, max %d: %q", i, w, tt.width, line)
				}
			}
			if h := strings.Count(out, "\n") + 1; h != 30 {
				t.Errorf("rendered %d lines, want 30", h)
			}
		})
	}
}

func TestListPreviewThreshold(t *testing.T) {
	for _, tt := range []struct {
		width int
		want  bool
	}{{139, false}, {140, true}, {200, true}} {
		t.Run(fmt.Sprintf("width %d", tt.width), func(t *testing.T) {
			m := loadedList(t, tt.width, 30)
			if got := strings.Contains(ansi.Strip(m.render()), "Preview"); got != tt.want {
				t.Errorf("preview shown = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListPreviewShowsSelectedIssueAndToggles(t *testing.T) {
	m := loadedList(t, 160, 30)
	containsAll(t, ansi.Strip(m.render()), "Preview", "reproduce", "open the page")
	press(t, m, "down")
	out := ansi.Strip(m.render())
	containsAll(t, out, "#43 Add dark mode")
	press(t, m, "p")
	if strings.Contains(ansi.Strip(m.render()), "Preview") {
		t.Error("p should hide the preview")
	}
	press(t, m, "p")
	if !strings.Contains(ansi.Strip(m.render()), "Preview") {
		t.Error("p should bring the preview back")
	}
}

func TestListHeaderTitleFiltersAndStatusSummary(t *testing.T) {
	m := loadedList(t, 120, 30)
	lines := strings.Split(ansi.Strip(m.render()), "\n")
	containsAll(t, lines[0], "Assigned to me", "3", "● 1 New", "● 1 In progress", "● 1 Closed")
	containsAll(t, lines[1], "Status: open", "Assigned: me")

	m.list.f.project = &redmine.IDName{ID: 1, Name: "Website"}
	m.list.f.assigned = userRef{id: "31", name: "John Roe"}
	m.list.f.tracker = &redmine.IDName{ID: 2, Name: "Feature"}
	lines = strings.Split(ansi.Strip(m.render()), "\n")
	containsAll(t, lines[0], "Issues")
	containsAll(t, lines[1], "Project: Website", "Status: open", "Tracker: Feature", "Assigned: John Roe")
}

func TestListTitlePerView(t *testing.T) {
	e := newTestModel(&fakeAPI{}, Options{}).env()
	tests := []struct {
		name string
		f    filters
		want string
	}{
		{name: "mine", f: filters{assigned: userRef{id: "me"}}, want: "Assigned to me"},
		{name: "created by me", f: filters{author: userRef{id: "me"}}, want: "Created by me"},
		{name: "watching", f: filters{watched: true}, want: "Watching"},
		{name: "all", f: filters{}, want: "All issues"},
		{name: "assigned to another", f: filters{assigned: userRef{id: "3", name: "Ana"}}, want: "Assigned to Ana"},
		{name: "created by another", f: filters{author: userRef{id: "3", name: "Ana"}}, want: "Created by Ana"},
		{name: "project", f: filters{project: &redmine.IDName{ID: 1, Name: "Website"}}, want: "Project: Website"},
		{name: "tracker does not change the view", f: filters{assigned: userRef{id: "me"}, tracker: &redmine.IDName{ID: 1}}, want: "Assigned to me"},
		{name: "several scopes", f: filters{assigned: userRef{id: "me"}, project: &redmine.IDName{ID: 1, Name: "W"}}, want: "Issues"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.title(e); got != tt.want {
				t.Errorf("title() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListEmptyAndErrorStates(t *testing.T) {
	m := newTestModel(&fakeAPI{}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(issuesMsg{gen: m.list.startLoad(), page: redmine.IssuePage{}})
	out := ansi.Strip(m.render())
	containsAll(t, out, "No issues for these filters — F clears the filters")

	m.Update(issuesMsg{gen: m.list.startLoad(), err: &redmine.APIError{Status: 403}})
	containsAll(t, ansi.Strip(m.render()), "Could not load", "No permission", "Press r to retry")

	es := newTestModel(&fakeAPI{}, Options{Lang: i18n.ES})
	es.Update(issuesMsg{gen: es.list.startLoad(), page: redmine.IssuePage{}})
	containsAll(t, ansi.Strip(es.render()), "Sin issues para estos filtros — F limpia los filtros")
}

func TestListStatusBar(t *testing.T) {
	m := loadedList(t, 120, 30)
	lines := strings.Split(ansi.Strip(m.render()), "\n")
	last := lines[len(lines)-1]
	containsAll(t, last, "enter open", "Jane Doe", "redmine.example.com")
	if strings.Contains(last, "READ ONLY") {
		t.Error("READ ONLY must only show in read-only mode")
	}

	ro := newTestModel(&fakeAPI{}, Options{ReadOnly: true})
	ro.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	lines = strings.Split(ansi.Strip(ro.render()), "\n")
	containsAll(t, lines[len(lines)-1], "READ ONLY", "redmine.example.com")
}

func TestNoColorHasNoColorEscapes(t *testing.T) {
	m := newTestModel(&fakeAPI{}, Options{NoColor: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	m.Update(issuesMsg{gen: m.list.startLoad(), page: redmine.IssuePage{Issues: listIssues(), Total: 3}})
	out := m.render()
	if strings.Contains(out, "\x1b[38;") || strings.Contains(out, "\x1b[48;") {
		t.Error("NO_COLOR output still carries color escapes")
	}
}

func TestListQuickFilter(t *testing.T) {
	m := loadedList(t, 100, 24)
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{name: "by subject", query: "dark", want: 1},
		{name: "by id", query: "#42", want: 1},
		{name: "by project", query: "BACKEND", want: 1},
		{name: "by assignee", query: "john", want: 2},
		{name: "no match", query: "zzz", want: 0},
		{name: "empty", query: "", want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.list.query = tt.query
			m.list.refilter()
			if got := len(m.list.visible); got != tt.want {
				t.Errorf("visible = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStaleListResponseIgnored(t *testing.T) {
	m := newTestModel(&fakeAPI{}, Options{})
	old := m.list.startLoad()
	m.list.startLoad()
	m.Update(issuesMsg{gen: old, page: redmine.IssuePage{Issues: listIssues(), Total: 3}})
	if len(m.list.issues) != 0 {
		t.Error("a stale response replaced the list")
	}
}

// ---- paging -----------------------------------------------------------

func TestListLoadsMoreOnDemand(t *testing.T) {
	api := &fakeAPI{issues: listIssues(), total: 7}
	m := newTestModel(api, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	settle(t, m, m.reloadList())

	if len(m.list.issues) != 3 || m.list.total != 7 {
		t.Fatalf("first page: %d issues of %d", len(m.list.issues), m.list.total)
	}
	containsAll(t, ansi.Strip(m.render()), "3 of 7")
	if len(api.offsets) != 1 || api.offsets[0] != 0 {
		t.Fatalf("expected one request at offset 0, got %v", api.offsets)
	}

	press(t, m, "down")
	if len(api.offsets) != 2 || api.offsets[1] != 3 {
		t.Fatalf("reaching the end must request the next page at offset 3, got %v", api.offsets)
	}
	if len(m.list.issues) != 6 {
		t.Errorf("after the second page: %d issues, want 6", len(m.list.issues))
	}
	containsAll(t, ansi.Strip(m.render()), "6 of 7")
}

func TestListDoesNotLoadMoreWhenComplete(t *testing.T) {
	api := &fakeAPI{issues: listIssues(), total: 3}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())
	press(t, m, "down", "down", "down")
	if len(api.offsets) != 1 {
		t.Errorf("a complete list must not ask for more, requests at %v", api.offsets)
	}
}

func TestFailedExtraPageKeepsTheList(t *testing.T) {
	m := loadedList(t, 120, 30)
	m.list.total = 9
	gen, offset, ok := m.list.startMore()
	if !ok {
		t.Fatal("startMore refused")
	}
	m.Update(issuesMsg{gen: gen, offset: offset, err: errors.New("boom")})
	if len(m.list.issues) != 3 || m.list.err != nil {
		t.Errorf("a failed extra page must keep the list: %d issues, err %v", len(m.list.issues), m.list.err)
	}
	if !m.flash.bad {
		t.Error("the failure should be flagged in the status bar")
	}
}

// ---- views and filters send the right query ---------------------------

func TestViewsAndFiltersBuildTheQuery(t *testing.T) {
	api := &fakeAPI{
		issues:   listIssues(),
		projects: []redmine.Project{{ID: 1, Name: "Website"}, {ID: 2, Name: "Backend"}},
		users: func(string) ([]redmine.Member, error) {
			return []redmine.Member{{ID: 31, Name: "John Roe"}}, nil
		},
	}
	last := func() redmine.ListQuery { return api.queries[len(api.queries)-1] }
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())
	if q := last(); q.Assigned != "me" || q.Status != redmine.StatusOpen {
		t.Fatalf("the default view must be assigned to me, open: %+v", q)
	}

	steps := []struct {
		name string
		do   func()
		want redmine.ListQuery
	}{
		{name: "created by me", do: func() { press(t, m, "v", "down", "enter") }, want: redmine.ListQuery{Author: "me", Status: "open"}},
		{name: "watching", do: func() { press(t, m, "v", "down", "down", "enter") }, want: redmine.ListQuery{Watcher: "me", Status: "open"}},
		{name: "all issues", do: func() { press(t, m, "v", "down", "down", "down", "enter") }, want: redmine.ListQuery{Status: "open"}},
		{name: "toggle to every status", do: func() { press(t, m, "a") }, want: redmine.ListQuery{Status: "*"}},
		{name: "assigned to a chosen user", do: func() { press(t, m, "v", "down", "down", "down", "down", "enter", "down", "enter") }, want: redmine.ListQuery{Assigned: "31", Status: "*"}},
		{name: "by project", do: func() { press(t, m, "v", "down", "down", "down", "down", "down", "down", "enter", "down", "enter") }, want: redmine.ListQuery{ProjectID: 2, Status: "*"}},
		{name: "clear filters", do: func() { press(t, m, "F") }, want: redmine.ListQuery{Status: "open"}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			s.do()
			if got := last(); got != s.want {
				t.Errorf("query = %+v, want %+v", got, s.want)
			}
		})
	}
}

func TestFilterPanelAppliesEveryFilterOnClose(t *testing.T) {
	api := &fakeAPI{
		issues:   listIssues(),
		projects: []redmine.Project{{ID: 1, Name: "Website"}, {ID: 2, Name: "Backend"}},
		users: func(string) ([]redmine.Member, error) {
			return []redmine.Member{{ID: 31, Name: "John Roe"}, {ID: 32, Name: "Ana Pop"}}, nil
		},
	}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())
	before := len(api.queries)

	press(t, m, "f")
	// project: Backend (second real entry after "(any)")
	press(t, m, "enter", "down", "down", "enter")
	// status: Closed
	press(t, m, "down", "enter", "down", "enter")
	// tracker: Feature
	press(t, m, "down", "enter", "down", "down", "enter")
	// assigned: any (clears the default "me")
	press(t, m, "down", "enter", "enter")
	// author: John Roe (after "(any)" and "me")
	press(t, m, "down", "enter", "down", "down", "enter")
	if len(api.queries) != before {
		t.Fatal("nothing must be requested until the panel closes")
	}
	press(t, m, "esc")
	if len(api.queries) != before+1 {
		t.Fatalf("closing the panel must reload once, got %d requests", len(api.queries)-before)
	}
	want := redmine.ListQuery{ProjectID: 2, Status: redmine.StatusClosed, TrackerID: 2, Author: "31"}
	if got := api.queries[len(api.queries)-1]; got != want {
		t.Errorf("query = %+v, want %+v", got, want)
	}
	containsAll(t, ansi.Strip(m.render()), "Project: Backend", "Status: closed", "Tracker: Feature", "Author: John Roe")
}

func TestFilterPanelWithoutChangesDoesNotReload(t *testing.T) {
	api := &fakeAPI{issues: listIssues()}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())
	before := len(api.queries)
	press(t, m, "f", "esc")
	if len(api.queries) != before {
		t.Error("closing an untouched panel must not reload")
	}
}

// ---- users ------------------------------------------------------------

func TestLookupUsersUsesTheUserListWhenAllowed(t *testing.T) {
	api := &fakeAPI{users: func(name string) ([]redmine.Member, error) {
		return []redmine.Member{{ID: 1, Name: "Match for " + name}}, nil
	}}
	got := lookupUsers(context.Background(), api, "ja", false, nil)
	if got.err != nil || got.viaMembers || got.denied || len(got.users) != 1 || got.users[0].Name != "Match for ja" {
		t.Errorf("lookupUsers() = %+v", got)
	}
}

func TestLookupUsersFallsBackToMembershipsOn403(t *testing.T) {
	reads := map[string]string{
		"/users.json":                  "",
		"/projects.json":               `{"projects":[{"id":1,"name":"Website"},{"id":2,"name":"Secret"},{"id":3,"name":"Backend"}],"total_count":3}`,
		"/projects/1/memberships.json": `{"memberships":[{"user":{"id":5,"name":"Jane Doe"}},{"user":{"id":6,"name":"John Roe"}}],"total_count":2}`,
		"/projects/3/memberships.json": `{"memberships":[{"user":{"id":6,"name":"John Roe"}},{"group":{"id":9,"name":"Devs"}},{"user":{"id":7,"name":"Ana Pop"}}],"total_count":3}`,
	}
	var hits []string
	client := newForbiddenUsersClient(t, reads, &hits)

	got := lookupUsers(context.Background(), client, "", false, nil)
	if got.err != nil {
		t.Fatalf("lookupUsers() error = %v", got.err)
	}
	if !got.viaMembers || !got.denied {
		t.Errorf("the fallback must be flagged: %+v", got)
	}
	names := make([]string, len(got.users))
	for i, u := range got.users {
		names[i] = u.Name
	}
	if want := "Ana Pop,Jane Doe,John Roe"; strings.Join(names, ",") != want {
		t.Errorf("users = %v, want %s (unique, sorted, no groups, the forbidden project skipped)", names, want)
	}

	// The cache is reused: no new requests, and the name filter applies locally.
	before := len(hits)
	again := lookupUsers(context.Background(), client, "ana", got.denied, got.cache)
	if len(again.users) != 1 || again.users[0].Name != "Ana Pop" {
		t.Errorf("cached lookup = %+v", again.users)
	}
	if len(hits) != before {
		t.Errorf("a cached lookup made %d requests", len(hits)-before)
	}
}

func newForbiddenUsersClient(t *testing.T, reads map[string]string, hits *[]string) *redmine.Client {
	t.Helper()
	srvReads := map[string]string{}
	for k, v := range reads {
		if k != "/users.json" {
			srvReads[k] = v
		}
	}
	c, _ := newRedmineForbidding(t, srvReads, "/users.json", "/projects/2/memberships.json", hits)
	return c
}

func TestUserPickerWarnsWhenOnlyMembersAreListed(t *testing.T) {
	api := &fakeAPI{
		projects: []redmine.Project{{ID: 1, Name: "Website"}},
		members:  map[int][]redmine.Member{1: {{ID: 31, Name: "John Roe"}}},
	}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())
	press(t, m, "v", "down", "down", "down", "down", "enter")
	out := ansi.Strip(m.render())
	containsAll(t, out, "Only members of your projects are listed", "John Roe", "Me (Jane Doe)")
}
