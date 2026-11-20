package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

func childOf(id, parent int, subject string, status redmine.Status) redmine.Issue {
	is := testIssue(id, subject, status)
	is.Parent = &redmine.ParentRef{ID: parent}
	is.AssignedTo = &redmine.IDName{ID: 40, Name: "Ana Paz"}
	return is
}

// familyTree hangs two children under list issue #42, one of them closed and with a
// child of its own.
func familyTree() []redmine.Issue {
	return []redmine.Issue{
		childOf(101, 42, "Certify", redmine.Status{ID: 5, Name: "Closed", IsClosed: true}),
		childOf(102, 42, "Deploy", redmine.Status{ID: 1, Name: "New"}),
		childOf(103, 101, "Sign off", redmine.Status{ID: 2, Name: "In progress"}),
	}
}

func TestBuildTree(t *testing.T) {
	issues := append(familyTree(), childOf(900, 77, "Orphan", redmine.Status{ID: 1, Name: "New"}))
	rows := buildTree(42, issues)
	var got []string
	for _, r := range rows {
		got = append(got, r.prefix+r.issue.Subject)
	}
	want := []string{"├─ Certify", "│  └─ Sign off", "└─ Deploy"}
	if !slices.Equal(got, want) {
		t.Fatalf("tree = %q, want %q", got, want)
	}
}

func treeList(t *testing.T) (*Model, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{issues: listIssues(), tree: familyTree(), issue: detailIssue()}
	m := newTestModel(api, Options{})
	settle(t, m, m.reloadList())
	return m, api
}

func TestListMarksIssuesWithChildren(t *testing.T) {
	m, _ := treeList(t)
	m.Update(windowSize(160, 30))
	out := ansi.Strip(m.render())
	containsAll(t, out, "+2 Crash when saving", "Children    1 of 2 closed")
	if strings.Contains(out, "+0") || strings.Contains(out, "Add dark mode +") {
		t.Errorf("issues without children must not be marked:\n%s", out)
	}
}

func TestListExpandsAndCollapsesChildren(t *testing.T) {
	m, api := treeList(t)
	press(t, m, "right")
	if !slices.Equal(api.subtreeCalls, []int{42}) {
		t.Fatalf("expanding should fetch the subtree of #42 once, calls = %v", api.subtreeCalls)
	}
	out := ansi.Strip(m.render())
	containsAll(t, out, "−2 Crash when saving", "├─ Certify", "│  └─ Sign off", "└─ Deploy", "Ana Paz")
	if is := m.list.selected(); is == nil || is.ID != 42 {
		t.Fatalf("expanding must keep the cursor on #42, got %v", is)
	}

	press(t, m, "down", "down")
	if is := m.list.selected(); is == nil || is.ID != 103 {
		t.Fatalf("cursor should reach the grandchild #103, got %v", is)
	}
	press(t, m, "left")
	if is := m.list.selected(); is == nil || is.ID != 42 {
		t.Fatalf("collapsing from a child should go back to its root, got %v", is)
	}
	if strings.Contains(ansi.Strip(m.render()), "Certify") {
		t.Error("collapsed children must disappear")
	}

	press(t, m, "space", "down", "enter")
	if len(m.stack) != 1 || m.stack[0].id != 101 {
		t.Fatalf("enter on a child row should open it, stack = %d", len(m.stack))
	}
}

func TestListHidesAssigneeWhenItIsMe(t *testing.T) {
	m := loadedList(t, 120, 30)
	header := strings.Split(ansi.Strip(m.render()), "\n")[3]
	if strings.Contains(header, "Assignee") {
		t.Errorf("assigned to me: the assignee column repeats one name, header %q", header)
	}
	m.list.f = filters{status: redmine.StatusOpen}
	header = strings.Split(ansi.Strip(m.render()), "\n")[3]
	if !strings.Contains(header, "Assignee") {
		t.Errorf("other views need the assignee column, header %q", header)
	}
}

func TestListKeepsCursorOnReload(t *testing.T) {
	m, api := treeList(t)
	press(t, m, "down")
	is := listIssues()
	api.mu.Lock()
	api.issues = []redmine.Issue{is[1], is[2], is[0]} // the update moved #43 to the top
	api.mu.Unlock()
	press(t, m, "r")
	if is := m.list.selected(); is == nil || is.ID != 43 {
		t.Fatalf("reload must keep the cursor on #43, got %v", is)
	}
}

func TestProjectLabelKeepsTheDistinctivePart(t *testing.T) {
	tests := []struct{ name, want string }{
		{"Desarrollo - CORE", "Desarrollo - CORE"},
		{"Desarrollo - Recaudo Digital", "Recaudo Digital"},
		{"A very long project name with no separator", "A very long project name with no separator"},
	}
	for _, tt := range tests {
		if got := projectLabel(tt.name, 18); got != tt.want {
			t.Errorf("projectLabel(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDetailChildrenTreeAndRelations(t *testing.T) {
	parent := detailIssue()
	tree := []redmine.Issue{
		childOf(124, 123, "Child one", redmine.Status{ID: 3, Name: "Resolved"}),
		childOf(125, 124, "Grandchild", redmine.Status{ID: 1, Name: "New"}),
		testIssue(9, "Related thing", redmine.Status{ID: 2, Name: "In progress"}),
	}
	api := &fakeAPI{issue: parent, tree: tree}
	m := newTestModel(api, Options{})
	m.Update(windowSize(120, 30))
	settle(t, m, m.openIssue(123))

	d := m.active()
	d.setTab(tabChildren, m.env())
	out := ansi.Strip(m.render())
	containsAll(t, out, "1 direct children · 2 in the tree · 0 closed", "└─ Child one", "   └─ Grandchild", "Resolved", "Ana Paz")

	press(t, m, "down", "enter")
	if m.active().id != 125 {
		t.Fatalf("enter on the grandchild should open #125, got #%d", m.active().id)
	}
	press(t, m, "esc")

	m.active().setTab(tabRelations, m.env())
	containsAll(t, ansi.Strip(m.render()), "related to", "#9", "In progress", "Related thing")
}

func windowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }
