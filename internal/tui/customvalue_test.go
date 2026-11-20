package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

// issueWithUserField has a user custom field (72), a date field and a journal that
// changed the user field from 72 to 80.
func issueWithUserField() *redmine.Issue {
	is := detailIssue()
	is.CustomFields = []redmine.CustomField{
		{ID: 19, Name: "Operator", Value: []byte(`"72"`)},
		{ID: 12, Name: "Installed on", Value: []byte(`"2026-10-02"`)},
	}
	is.Journals = append(is.Journals, redmine.Journal{
		ID: 2, User: redmine.IDName{ID: 25, Name: "Jane Doe"}, CreatedOn: fixedNow,
		Details: []redmine.JournalDetail{{Property: "cf", Name: "19", OldValue: sp("72"), NewValue: sp("80")}},
	})
	return is
}

func TestUserCustomFieldShowsTheName(t *testing.T) {
	api := &fakeAPI{
		issue:      issueWithUserField(),
		customDefs: []redmine.CustomFieldDef{{ID: 19, Name: "Operator", FieldFormat: "user"}, {ID: 12, Name: "Installed on", FieldFormat: "date"}},
		userNames:  map[int]string{72: "Diego Ramos", 80: "Ana Paz"},
	}
	m := newTestModel(api, Options{})
	settle(t, m, m.openIssue(123))

	out := ansi.Strip(m.render())
	containsAll(t, out, "Operator: Diego Ramos", "Installed on: 10/02/2026")
	if strings.Contains(out, "Operator: 72") {
		t.Errorf("the user id must give way to the name:\n%s", out)
	}
	m.active().setTab(tabHistory, m.env())
	containsAll(t, ansi.Strip(m.render()), "Operator: Diego Ramos → Ana Paz")
}

func TestUserCustomFieldWithoutDefinitionsStaysRaw(t *testing.T) {
	api := &fakeAPI{issue: issueWithUserField(), userNames: map[int]string{72: "Diego Ramos"}}
	m := newTestModel(api, Options{})
	settle(t, m, m.openIssue(123))
	containsAll(t, ansi.Strip(m.render()), "Operator: 72")
	if len(m.userNames) != 0 {
		t.Errorf("without definitions no user may be looked up, asked %v", m.userNames)
	}
}

func TestUnreadableUserKeepsTheID(t *testing.T) {
	api := &fakeAPI{
		issue:      issueWithUserField(),
		customDefs: []redmine.CustomFieldDef{{ID: 19, Name: "Operator", FieldFormat: "user"}},
	}
	m := newTestModel(api, Options{})
	settle(t, m, m.openIssue(123))
	containsAll(t, ansi.Strip(m.render()), "Operator: 72")
}
