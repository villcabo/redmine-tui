package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

// treeRow is one issue of a flattened tree. prefix holds the branch glyphs that draw
// its place under the root ("├─ ", "│  └─ ").
type treeRow struct {
	issue  redmine.Issue
	prefix string
}

// buildTree orders the issues under rootID depth first, siblings by id. Issues whose
// parent is not reachable from rootID are left out.
func buildTree(rootID int, issues []redmine.Issue) []treeRow {
	kids := map[int][]redmine.Issue{}
	for _, is := range issues {
		if is.Parent != nil {
			kids[is.Parent.ID] = append(kids[is.Parent.ID], is)
		}
	}
	for _, k := range kids {
		slices.SortFunc(k, func(a, b redmine.Issue) int { return a.ID - b.ID })
	}
	var out []treeRow
	seen := map[int]bool{rootID: true}
	var walk func(parent int, lead string)
	walk = func(parent int, lead string) {
		list := kids[parent]
		for i, is := range list {
			if seen[is.ID] {
				continue
			}
			seen[is.ID] = true
			branch, next := "├─ ", "│  "
			if i == len(list)-1 {
				branch, next = "└─ ", "   "
			}
			out = append(out, treeRow{issue: is, prefix: lead + branch})
			walk(is.ID, lead+next)
		}
	}
	walk(rootID, "")
	return out
}

// closedCount counts the issues in a closed status.
func closedCount(e env, issues []redmine.Issue) int {
	n := 0
	for _, is := range issues {
		if is.Status.IsClosed || e.statuses[is.Status.ID].IsClosed {
			n++
		}
	}
	return n
}

// issueTable lays out issue rows outside the main list (children, relations) with the
// same cells the list uses. Zero widths are hidden columns.
type issueTable struct {
	lead, id, status, priority, tracker, assignee int
}

// measureIssues sizes the columns for rows and drops the minor ones on narrow screens.
func measureIssues(e env, issues []redmine.Issue, lead int) issueTable {
	t := issueTable{lead: lead, id: 3, status: 6, priority: 8, tracker: 7, assignee: 8}
	for _, is := range issues {
		t.id = max(t.id, len(fmt.Sprintf("#%d", is.ID)))
		t.status = max(t.status, min(lipgloss.Width(is.Status.Name)+2, 16))
		t.priority = max(t.priority, min(lipgloss.Width(is.Priority.Name)+2, 11))
		t.tracker = max(t.tracker, min(lipgloss.Width(is.Tracker.Name), 14))
		t.assignee = max(t.assignee, min(lipgloss.Width(assigneeName(e, is)), 18))
	}
	if e.w < 110 {
		t.tracker = 0
	}
	if e.w < 90 {
		t.priority = 0
	}
	if e.w < 70 {
		t.assignee = 0
	}
	return t
}

// row renders one issue. lead fills the optional first column (a relation kind) and
// prefix goes before the subject (tree glyphs).
func (t issueTable) row(e env, is redmine.Issue, lead, prefix string, selected bool, width int) string {
	p := painter{}
	marker := p.space(2)
	subjectStyle := e.st.text
	if selected {
		p.bg = e.st.rowBg
		marker = p.style(e.st.cursor).Render("▸ ")
		subjectStyle = e.st.selected
	}
	cells := []string{p.space(1) + marker}
	if t.lead > 0 {
		cells = append(cells, p.text(e.st.dim, lead, t.lead))
	}
	cells = append(cells, p.text(e.st.accent, fmt.Sprintf("#%d", is.ID), t.id), statusCell(e, p, is.Status, t.status))
	if t.priority > 0 {
		cells = append(cells, priorityCell(e, p, is.Priority, t.priority))
	}
	if t.tracker > 0 {
		cells = append(cells, p.text(e.st.dim, is.Tracker.Name, t.tracker))
	}
	used := 0
	for _, c := range cells {
		used += lipgloss.Width(c) + 1
	}
	if t.assignee > 0 {
		used += t.assignee + 1
	}
	subject := max(8, width-used)
	cells = append(cells, p.text(e.st.dim, prefix, min(lipgloss.Width(prefix), subject))+
		p.text(subjectStyle, is.Subject, max(0, subject-lipgloss.Width(prefix))))
	if t.assignee > 0 {
		cells = append(cells, assigneeCell(e, p, is, t.assignee))
	}
	line := cells[0]
	for _, c := range cells[1:] {
		line += p.space(1) + c
	}
	if gap := width - lipgloss.Width(line); gap > 0 {
		line += p.space(gap)
	}
	return truncate(line, width)
}

func statusCell(e env, p painter, s redmine.Status, w int) string {
	badge := e.st.badge(e.statusTone(s), truncate(s.Name, w-2))
	return badge + p.space(w-lipgloss.Width(badge))
}

func priorityCell(e env, p painter, pr redmine.IDName, w int) string {
	return p.style(e.st.dots[e.priorityTone(pr)]).Render("●") + p.text(e.st.text, " "+pr.Name, w-1)
}

func assigneeCell(e env, p painter, is redmine.Issue, w int) string {
	if is.AssignedTo == nil {
		return p.text(e.st.dim, "-", w)
	}
	return p.text(e.st.text, is.AssignedTo.Name, w)
}

func assigneeName(e env, is redmine.Issue) string {
	if is.AssignedTo == nil {
		return e.t("detail.unassigned")
	}
	return is.AssignedTo.Name
}

// projectLabel keeps a project name that fits in width; a longer "Parent - Child" name
// shows its last part, which is what tells sibling projects apart.
func projectLabel(name string, width int) string {
	if lipgloss.Width(name) <= width {
		return name
	}
	if i := strings.LastIndex(name, " - "); i >= 0 {
		return name[i+3:]
	}
	return name
}

// dueStyle colors a due date: red when an open issue is overdue, amber when it is due
// within two days.
func dueStyle(e env, is redmine.Issue) lipgloss.Style {
	if is.DueDate == nil || is.Status.IsClosed || e.statuses[is.Status.ID].IsClosed {
		return e.st.text
	}
	due, err := time.ParseInLocation("2006-01-02", *is.DueDate, time.Local)
	if err != nil {
		return e.st.text
	}
	now := e.now.Local()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch {
	case due.Before(today):
		return e.st.danger.Bold(true)
	case due.Before(today.AddDate(0, 0, 3)):
		return e.st.dots[toneAmber].Bold(true)
	}
	return e.st.text
}
