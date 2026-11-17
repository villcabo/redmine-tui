package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

type searchAPI struct {
	fakeAPI
	queries []string
}

func (s *searchAPI) SearchIssues(_ context.Context, text string) ([]redmine.SearchResult, error) {
	s.queries = append(s.queries, text)
	return []redmine.SearchResult{
		{ID: 12, Title: "Bug #12 (New): Crash on save", Type: "issue", Description: "it  crashes\nwhen saving", Datetime: fixedNow},
		{ID: 13, Title: "Feature #13 (New): Dark mode", Type: "issue", Datetime: fixedNow},
	}, nil
}

func TestGoToIssueByNumber(t *testing.T) {
	api := &fakeAPI{issue: detailIssue()}
	m := newTestModel(api, Options{})
	press(t, m, "g")
	containsAll(t, view(m), "Go to issue")

	typeText(t, m, "abc")
	press(t, m, "enter")
	containsAll(t, view(m), "Enter a positive issue number.")
	if len(m.stack) != 0 {
		t.Fatal("an invalid number must not open anything")
	}

	for range 3 {
		press(t, m, "backspace")
	}
	m.modal.(*inputModal).input.SetValue("#123")
	press(t, m, "enter")
	if a := m.active(); a == nil || a.id != 123 {
		t.Fatalf("g + 123 should open issue 123, stack = %d", len(m.stack))
	}
	if m.modal != nil {
		t.Error("the dialog should close")
	}
}

func TestGKeyNoLongerJumpsToTheTop(t *testing.T) {
	m := loadedList(t, 100, 24)
	press(t, m, "down", "down", "g", "esc")
	if m.list.cursor != 2 {
		t.Errorf("g must open the go-to dialog, not move the cursor; cursor = %d", m.list.cursor)
	}
	press(t, m, "home")
	if m.list.cursor != 0 {
		t.Errorf("home must go to the start, cursor = %d", m.list.cursor)
	}
}

func TestSearchFlow(t *testing.T) {
	api := &searchAPI{fakeAPI: fakeAPI{issue: detailIssue()}}
	m := newTestModel(api, Options{})
	press(t, m, "s")
	press(t, m, "enter")
	containsAll(t, view(m), "Type something to search for.")

	typeText(t, m, "crash")
	press(t, m, "enter")
	if len(api.queries) != 1 || api.queries[0] != "crash" {
		t.Fatalf("search queries = %v", api.queries)
	}
	out := view(m)
	containsAll(t, out, "Search: crash", "2 results", "Bug #12 (New): Crash on save", "it crashes when saving", "Feature #13")

	press(t, m, "down", "enter")
	if a := m.active(); a == nil || a.id != 13 {
		t.Fatalf("enter should open the selected result, got %+v", a)
	}
	press(t, m, "esc")
	containsAll(t, view(m), "Search: crash")
	press(t, m, "esc")
	containsAll(t, view(m), "Assigned to me")
}

func TestCopyMenu(t *testing.T) {
	m := loadedList(t, 100, 24)
	press(t, m, "y")
	containsAll(t, view(m), "Copy", "Link  https://redmine.example.com/issues/42", "Number  #42", "Title  Crash when saving", "Number and title  #42 Crash when saving")

	// The picker hands the chosen text to the OSC 52 clipboard command.
	items := m.modal.(*pickerModal).items
	wants := []string{"https://redmine.example.com/issues/42", "#42", items[2].value.(string), "#42 " + items[2].value.(string)}
	for i, it := range items {
		if it.value.(string) != wants[i] {
			t.Errorf("item %d copies %q, want %q", i, it.value, wants[i])
		}
	}
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("choosing an item must return the clipboard command")
	}
	if m.flash.text != "Copied" {
		t.Errorf("flash = %q, want Copied", m.flash.text)
	}
	if strings.Contains(view(m), "Link  https") {
		t.Error("the menu should close")
	}
}
