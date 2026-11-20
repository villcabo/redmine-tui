package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

type tab int

const (
	tabOverview tab = iota
	tabHistory
	tabChildren
	tabRelations
	tabCount
)

// wideLayout is the terminal width from which the overview boxes sit side by side.
const wideLayout = 100

// detailModel is the screen for one issue.
type detailModel struct {
	uid, id int
	issue   *redmine.Issue
	loading bool
	err     error

	tab       tab
	vp        viewport.Model
	header    string
	childCur  int
	relCur    int
	laidOutAt env

	// tree replaces issue.Children once the descendants arrive with their full fields.
	tree       []treeRow
	treeLoaded bool
	treeErr    error
	// related holds the far side of each relation, by id, once it arrives.
	related map[int]redmine.Issue
}

func newDetailModel(uid, id int) *detailModel {
	return &detailModel{uid: uid, id: id, loading: true, vp: viewport.New()}
}

func (d *detailModel) apply(msg issueMsg, e env) {
	d.loading = false
	if msg.err != nil {
		d.err = msg.err
		return
	}
	d.err = nil
	d.issue = msg.issue
	d.childCur = min(d.childCur, max(0, d.childCount()-1))
	d.relCur = min(d.relCur, max(0, len(d.issue.Relations)-1))
	d.relayout(e)
}

// followUps fetches what the issue alone does not carry: the status, priority and
// assignee of its descendants and of its related issues.
func (d *detailModel) followUps(a API) []tea.Cmd {
	var cmds []tea.Cmd
	if len(d.issue.Children) > 0 {
		cmds = append(cmds, fetchDescendantsCmd(a, d.uid, d.issue.ID))
	}
	if len(d.issue.Relations) > 0 {
		ids := make([]int, len(d.issue.Relations))
		for i, r := range d.issue.Relations {
			ids[i] = r.Other(d.issue.ID)
		}
		cmds = append(cmds, fetchRelatedCmd(a, d.uid, ids))
	}
	return cmds
}

func (d *detailModel) applyDescendants(msg descendantsMsg, e env) {
	if d.issue == nil {
		return
	}
	d.treeErr = msg.err
	if msg.err == nil {
		d.tree, d.treeLoaded = buildTree(d.issue.ID, msg.issues), true
		d.childCur = min(d.childCur, max(0, d.childCount()-1))
	}
	d.relayout(e)
}

func (d *detailModel) applyRelated(msg relatedMsg, e env) {
	if msg.err != nil {
		return
	}
	d.related = make(map[int]redmine.Issue, len(msg.issues))
	for _, is := range msg.issues {
		d.related[is.ID] = is
	}
	d.relayout(e)
}

// childCount is the number of rows on the children tab.
func (d *detailModel) childCount() int {
	if d.treeLoaded {
		return len(d.tree)
	}
	return len(d.issue.Children)
}

// selectedIssueID returns the issue under the cursor on the children and relations tabs.
func (d *detailModel) selectedIssueID() int {
	if d.issue == nil {
		return 0
	}
	switch d.tab {
	case tabChildren:
		if d.treeLoaded && d.childCur < len(d.tree) {
			return d.tree[d.childCur].issue.ID
		}
		if !d.treeLoaded && d.childCur < len(d.issue.Children) {
			return d.issue.Children[d.childCur].ID
		}
	case tabRelations:
		if d.relCur < len(d.issue.Relations) {
			return d.issue.Relations[d.relCur].Other(d.issue.ID)
		}
	}
	return 0
}

func (d *detailModel) handleKey(key string, msg tea.KeyPressMsg, e env) {
	if d.issue == nil {
		return
	}
	switch key {
	case "tab", "right":
		d.setTab((d.tab+1)%tabCount, e)
	case "shift+tab", "left":
		d.setTab((d.tab+tabCount-1)%tabCount, e)
	case "up", "k", "down", "j":
		if d.moveCursor(key == "up" || key == "k") {
			d.relayout(e)
			d.vp.EnsureVisible(d.cursorLine(), 0, 0)
			return
		}
		d.vp = d.update(msg)
	case "home":
		d.vp.GotoTop()
	case "G", "end":
		d.vp.GotoBottom()
	default:
		d.vp = d.update(msg)
	}
}

// update scrolls the viewport; its Cmd is always nil because the viewport has no async work.
func (d *detailModel) update(msg tea.Msg) viewport.Model {
	vp, _ := d.vp.Update(msg)
	return vp
}

func (d *detailModel) setTab(t tab, e env) {
	d.tab = t
	d.relayout(e)
	d.vp.GotoTop()
}

// moveCursor moves the selection on list tabs and reports whether this tab has one.
func (d *detailModel) moveCursor(up bool) bool {
	delta := 1
	if up {
		delta = -1
	}
	switch d.tab {
	case tabChildren:
		d.childCur = max(0, min(d.childCur+delta, d.childCount()-1))
		return true
	case tabRelations:
		d.relCur = max(0, min(d.relCur+delta, len(d.issue.Relations)-1))
		return true
	}
	return false
}

func (d *detailModel) cursorLine() int {
	if d.tab == tabChildren {
		return d.childCur + d.childHeaderLines()
	}
	return d.relCur
}

// relayout rebuilds the header, tabs and body for the current size and data.
func (d *detailModel) relayout(e env) {
	d.laidOutAt = e
	if d.issue == nil {
		return
	}
	d.header = d.renderHeader(e)
	d.vp.SetWidth(e.w)
	d.vp.SetHeight(max(1, e.h-1-lipgloss.Height(d.header)-2))
	d.vp.SetContent(d.renderBody(e))
}

func (d *detailModel) view(e env, frame string) string {
	switch {
	case d.err != nil:
		return errorBox(e, d.err)
	case d.issue == nil:
		return loadingView(e, frame, e.t("loading.issue", d.id))
	}
	rule := e.st.rule.Render(strings.Repeat("─", e.w))
	return d.header + "\n" + d.renderTabs(e) + "\n" + rule + "\n" + d.vp.View()
}

func (d *detailModel) renderHeader(e env) string {
	is := d.issue
	st := e.st
	inner := max(10, e.w-4)

	title := st.title.Render(fmt.Sprintf("#%d  ", is.ID)) + st.title.Render(is.Subject)
	title = lipgloss.NewStyle().Width(inner).Render(title)

	line := strings.Join([]string{
		st.badge(e.statusTone(is.Status), is.Status.Name),
		st.badge(toneGray, is.Tracker.Name),
		st.badge(e.priorityTone(is.Priority), is.Priority.Name),
		st.progressBar(is.DoneRatio, 16),
	}, " ")
	return st.card.Width(e.w).Render(title + "\n" + line)
}

func (d *detailModel) renderTabs(e env) string {
	labels := [tabCount]string{
		e.t("tab.overview"),
		e.t("tab.history"),
		e.t("tab.children", len(d.issue.Children)),
		e.t("tab.relations", len(d.issue.Relations)),
	}
	parts := make([]string, 0, tabCount)
	for i, l := range labels {
		if tab(i) == d.tab {
			parts = append(parts, e.st.tabActive.Render(l))
		} else {
			parts = append(parts, e.st.tabIdle.Render(l))
		}
	}
	return " " + strings.Join(parts, e.st.dim.Render("  ·  "))
}

func (d *detailModel) renderBody(e env) string {
	switch d.tab {
	case tabHistory:
		return d.renderHistory(e)
	case tabChildren:
		return d.renderChildren(e)
	case tabRelations:
		return d.renderRelations(e)
	default:
		return d.renderOverview(e)
	}
}

func (d *detailModel) renderOverview(e env) string {
	is := d.issue
	st := e.st
	field := func(label, value string) string {
		return st.dim.Render(padRight(label, 12)) + st.text.Render(value)
	}

	assignee := e.t("detail.unassigned")
	if is.AssignedTo != nil {
		assignee = is.AssignedTo.Name
	}
	parent := "-"
	if is.Parent != nil {
		parent = fmt.Sprintf("#%d", is.Parent.ID)
	}
	period := st.dim.Render(padRight(e.t("detail.period"), 12)) + st.text.Render(e.tr.Date(is.StartDate)+" → ") +
		dueStyle(e, *is).Render(e.tr.Date(is.DueDate))
	ficha := []string{
		field(e.t("detail.project"), is.Project.Name),
		field(e.t("detail.assigned"), assignee),
		field(e.t("detail.author"), is.Author.Name+" · "+e.tr.DateTime(is.CreatedOn)),
		period,
		field(e.t("detail.parent"), parent),
	}
	var clasif []string
	for _, cf := range is.CustomFields {
		v := cf.Text()
		if v == "" {
			continue
		}
		clasif = append(clasif, st.dim.Render(cf.Name+": ")+st.text.Render(v))
	}
	if len(clasif) == 0 {
		clasif = []string{st.dim.Render(e.t("detail.no_custom"))}
	}

	var boxes string
	if e.w >= wideLayout {
		left := (e.w) / 2
		right := e.w - left
		n := max(len(ficha), len(clasif))
		boxes = lipgloss.JoinHorizontal(lipgloss.Top,
			d.box(e, e.t("detail.box_info"), pad(ficha, n), left),
			d.box(e, e.t("detail.box_custom"), pad(clasif, n), right))
	} else {
		boxes = d.box(e, e.t("detail.box_info"), ficha, e.w) + "\n" + d.box(e, e.t("detail.box_custom"), clasif, e.w)
	}

	desc := st.dim.Render(e.t("detail.no_description"))
	if strings.TrimSpace(is.Description) != "" {
		desc = markdown(e, is.Description, e.w-2)
	}
	return boxes + "\n\n " + st.boxTitle.Render(e.t("detail.description")) + "\n" + desc
}

// box draws a titled bordered box whose total width is width cells.
func (d *detailModel) box(e env, title string, lines []string, width int) string {
	inner := max(4, width-4)
	for i, l := range lines {
		lines[i] = truncate(l, inner)
	}
	body := e.st.boxTitle.Render(title) + "\n" + strings.Join(lines, "\n")
	return e.st.box.Width(width).Render(body)
}

func pad(lines []string, n int) []string {
	out := append([]string(nil), lines...)
	for len(out) < n {
		out = append(out, "")
	}
	return out
}

func (d *detailModel) renderHistory(e env) string {
	res := newResolver(e.tr, d.issue, e.statuses)
	var entries []string
	for _, j := range d.issue.Journals {
		var lines []string
		for _, det := range j.Details {
			lines = append(lines, "  "+e.st.accent.Render("▸ ")+e.st.text.Render(res.describe(det)))
		}
		if strings.TrimSpace(j.Notes) != "" {
			lines = append(lines, indent(markdown(e, j.Notes, e.w-6), "    "))
		}
		if len(lines) == 0 {
			continue
		}
		head := e.st.accent.Render("● ") + e.st.dim.Render(e.tr.DateTime(j.CreatedOn)) + "  " + e.st.userStyle(j.User.ID).Render(j.User.Name)
		entries = append(entries, head+"\n"+strings.Join(lines, "\n"))
	}
	if len(entries) == 0 {
		return " " + e.st.dim.Render(e.t("history.empty"))
	}
	return strings.Join(entries, "\n\n")
}

func (d *detailModel) renderChildren(e env) string {
	if len(d.issue.Children) == 0 {
		return " " + e.st.dim.Render(e.t("children.empty"))
	}
	var head []string
	if d.treeErr != nil {
		head = append(head, " "+e.st.danger.Render(truncate(e.t("children.tree_error", friendlyError(e, d.treeErr)), e.w-2)))
	}
	if !d.treeLoaded {
		rows := make([]string, len(d.issue.Children))
		for i, c := range d.issue.Children {
			label := e.st.accent.Render(fmt.Sprintf("#%d", c.ID)) + " " + e.st.dim.Render(c.Tracker.Name) + " " + c.Subject
			rows[i] = d.row(e, i == d.childCur, label)
		}
		return strings.Join(append(head, rows...), "\n")
	}
	issues := make([]redmine.Issue, len(d.tree))
	for i, r := range d.tree {
		issues[i] = r.issue
	}
	head = append(head, " "+e.st.dim.Render(e.t("children.summary", len(d.issue.Children), len(d.tree), closedCount(e, issues))))
	t := measureIssues(e, issues, 0)
	rows := make([]string, len(d.tree))
	for i, r := range d.tree {
		rows[i] = t.row(e, r.issue, "", r.prefix, i == d.childCur, e.w-1)
	}
	return strings.Join(append(head, rows...), "\n")
}

// childHeaderLines is how many lines sit above the first child row.
func (d *detailModel) childHeaderLines() int {
	n := 0
	if d.treeErr != nil {
		n++
	}
	if d.treeLoaded && len(d.issue.Children) > 0 {
		n++
	}
	return n
}

func (d *detailModel) renderRelations(e env) string {
	if len(d.issue.Relations) == 0 {
		return " " + e.st.dim.Render(e.t("relations.empty"))
	}
	kinds := make([]string, len(d.issue.Relations))
	lead := 0
	var known []redmine.Issue
	for i, r := range d.issue.Relations {
		kind := e.t("rel." + r.RelationType)
		if kind == "rel."+r.RelationType {
			kind = r.RelationType
		}
		kinds[i] = kind
		lead = max(lead, lipgloss.Width(kind))
		if is, ok := d.related[r.Other(d.issue.ID)]; ok {
			known = append(known, is)
		}
	}
	t := measureIssues(e, known, lead)
	rows := make([]string, len(d.issue.Relations))
	for i, r := range d.issue.Relations {
		if is, ok := d.related[r.Other(d.issue.ID)]; ok {
			rows[i] = t.row(e, is, kinds[i], "", i == d.relCur, e.w-1)
			continue
		}
		label := e.st.dim.Render(padRight(kinds[i], lead)) + " " + e.st.accent.Render(fmt.Sprintf("#%d", r.Other(d.issue.ID)))
		rows[i] = d.row(e, i == d.relCur, label)
	}
	return strings.Join(rows, "\n")
}

func (d *detailModel) row(e env, selected bool, label string) string {
	marker := "  "
	if selected {
		marker = e.st.cursor.Render("▸ ")
	}
	return truncate(" "+marker+label, e.w-1)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
