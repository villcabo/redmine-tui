package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

func TestListWriteActionFetchesTheIssueFirst(t *testing.T) {
	api := &fakeAPI{issues: listIssues(), issue: detailIssue()}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())

	press(t, m, "S")
	p, ok := m.modal.(*pickerModal)
	if !ok {
		t.Fatalf("S in the list should open the status picker, modal = %T", m.modal)
	}
	var labels []string
	for _, it := range p.items {
		labels = append(labels, it.label)
	}
	// The workflow statuses come from GetIssue; the list row has none.
	if !slices.Equal(labels, []string{"Resolved", "Closed"}) {
		t.Errorf("status choices = %q, want the workflow ones without the current status", labels)
	}
	if len(m.stack) != 0 {
		t.Error("acting from the list must not open the detail")
	}
}

func TestListWriteActionRefusedReadOnly(t *testing.T) {
	api := &fakeAPI{issues: listIssues(), issue: detailIssue()}
	m := newTestModel(api, Options{ReadOnly: true})
	settle(t, m, m.reloadList())
	press(t, m, "n")
	if m.modal != nil {
		t.Fatalf("read-only must refuse before fetching, modal = %T", m.modal)
	}
	if !m.flash.bad {
		t.Error("read-only refusal should be shown")
	}
}

func TestListOrderCycles(t *testing.T) {
	api := &fakeAPI{issues: listIssues()}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())

	press(t, m, "O")
	q := api.queries[len(api.queries)-1]
	if q.Sort != "due_date,priority:desc" || !q.WithDueDate {
		t.Fatalf("first O should sort by due date over dated issues, query = %+v", q)
	}
	out := ansi.Strip(m.render())
	containsAll(t, out, "Order: due date (dated only)", "11/20/2026")
	if header := strings.Split(out, "\n")[3]; !strings.Contains(header, "Due") {
		t.Errorf("ordering by due date shows the Due column, header %q", header)
	}

	press(t, m, "O", "O", "O")
	q = api.queries[len(api.queries)-1]
	if q.Sort != "" || q.WithDueDate {
		t.Fatalf("O should cycle back to the default order, query = %+v", q)
	}
	if header := strings.Split(ansi.Strip(m.render()), "\n")[3]; strings.Contains(header, "Due") {
		t.Errorf("the Due column belongs to the due-date order only, header %q", header)
	}
	if strings.Contains(ansi.Strip(m.render()), "Order:") {
		t.Error("the default order is not worth a mention in the filter line")
	}
}

func TestDetailOpensAtOnceFromTheListRow(t *testing.T) {
	api := &fakeAPI{issues: listIssues(), issue: detailIssue()}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())

	_, cmd := m.Update(key("enter"))
	out := ansi.Strip(m.render())
	containsAll(t, out, "#42", "Crash when saving", "Children (…)", "updating…")

	settle(t, m, cmd)
	out = ansi.Strip(m.render())
	containsAll(t, out, "Children (1)")
	if strings.Contains(out, "updating…") {
		t.Error("the mark must go once the full issue arrives")
	}

	press(t, m, "esc")
	m.Update(key("enter"))
	containsAll(t, ansi.Strip(m.render()), "Children (1)", "updating…")
}

func TestListEmptyStates(t *testing.T) {
	m := loadedList(t, 120, 30)
	containsAll(t, ansi.Strip(m.render()), "loaded just now")

	m.list.query = "zzz"
	m.list.rebuild()
	containsAll(t, ansi.Strip(m.render()), "Nothing matches “zzz” — esc clears the filter")
}

func TestBrowserCommand(t *testing.T) {
	tests := []struct {
		goos, name string
		args       []string
	}{
		{"linux", "xdg-open", []string{"u"}},
		{"freebsd", "xdg-open", []string{"u"}},
		{"darwin", "open", []string{"u"}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", "u"}},
	}
	for _, tt := range tests {
		name, args := browserCommand(tt.goos, "u")
		if name != tt.name || !slices.Equal(args, tt.args) {
			t.Errorf("%s: %s %q, want %s %q", tt.goos, name, args, tt.name, tt.args)
		}
	}
}

func TestDetailShowsVersionCategoryAndHoursOnlyWhenSet(t *testing.T) {
	m := loadedDetail(t, 120)
	out := ansi.Strip(m.render())
	for _, s := range []string{"Version", "Category", "Hours"} {
		if strings.Contains(out, s) {
			t.Errorf("%q must not show when the instance reports nothing", s)
		}
	}

	is := detailIssue()
	est, spent := 8.0, 3.5
	is.FixedVersion = &redmine.IDName{ID: 1, Name: "Sprint 4"}
	is.Category = &redmine.IDName{ID: 2, Name: "Backend"}
	is.EstimatedHours, is.SpentHours = &est, &spent
	m.Update(issueMsg{uid: m.stack[0].uid, issue: is})
	containsAll(t, ansi.Strip(m.render()), "Sprint 4", "Category", "8 estimated · 3.5 spent")
}
