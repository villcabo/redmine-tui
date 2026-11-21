package tui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

const (
	previewMinWidth = 140
	previewWidth    = 46
	minSubjectWidth = 24
	loadMoreMargin  = 3
	projectWidth    = 18
)

// listModel is the issue table: filters, paging, quick text filter and preview.
type listModel struct {
	f       filters
	issues  []redmine.Issue
	total   int
	visible []listRow

	// kids holds the direct children of each loaded issue, asked once per page.
	kids map[int][]redmine.Issue
	// expanded marks the issues whose subtree is shown below them; subtrees holds that
	// subtree once fetched, until then the direct children stand in.
	expanded map[int]bool
	subtrees map[int][]redmine.Issue

	loading     bool
	loadingMore bool
	loaded      bool
	gen         int
	err         error

	cursor int
	top    int

	input     textinput.Model
	filtering bool
	query     string

	showPreview bool
	preview     previewCache

	// loadedAt is when the first page last arrived.
	loadedAt time.Time
}

// listRow is one line of the table: a loaded issue, or a descendant shown under an
// expanded one.
type listRow struct {
	issue  *redmine.Issue
	child  bool
	root   int
	prefix string
}

type previewCache struct {
	key  string
	text string
}

func newListModel() listModel {
	in := textinput.New()
	in.Prompt = "/ "
	return listModel{
		f: defaultFilters(), input: in, showPreview: true,
		kids: map[int][]redmine.Issue{}, expanded: map[int]bool{}, subtrees: map[int][]redmine.Issue{},
	}
}

// startLoad begins a fresh load of the first page and returns its generation.
func (l *listModel) startLoad() int {
	l.gen++
	l.loading, l.loadingMore, l.err = true, false, nil
	return l.gen
}

// startMore asks for the next page when the list is not complete yet.
func (l *listModel) startMore() (gen, offset int, ok bool) {
	if !l.loaded || l.loading || l.loadingMore || len(l.issues) >= l.total {
		return 0, 0, false
	}
	l.loadingMore = true
	return l.gen, len(l.issues), true
}

// apply stores a page. It returns the error of a failed extra page, which must not
// replace the list that is already on screen.
func (l *listModel) apply(msg issuesMsg) error {
	if msg.gen != l.gen {
		return nil
	}
	if msg.offset > 0 {
		l.loadingMore = false
		if msg.err != nil {
			return msg.err
		}
		if msg.offset == len(l.issues) {
			l.issues = append(l.issues, msg.page.Issues...)
			l.total = msg.page.Total
			l.refilter()
		}
		return nil
	}
	l.loading = false
	if msg.err != nil {
		l.err = msg.err
		return nil
	}
	l.loaded = true
	l.issues = msg.page.Issues
	l.total = msg.page.Total
	l.refilter()
	return nil
}

// pageIDs returns the ids of the issues that arrived with a page starting at offset.
func (l *listModel) pageIDs(offset int) []int {
	if offset >= len(l.issues) {
		return nil
	}
	ids := make([]int, 0, len(l.issues)-offset)
	for _, is := range l.issues[offset:] {
		ids = append(ids, is.ID)
	}
	return ids
}

func (l *listModel) applyChildren(msg childrenMsg) error {
	if msg.gen != l.gen {
		return nil
	}
	if msg.err != nil {
		return msg.err
	}
	for _, p := range msg.parents {
		l.kids[p] = nil
	}
	for _, is := range msg.issues {
		if is.Parent != nil {
			l.kids[is.Parent.ID] = append(l.kids[is.Parent.ID], is)
		}
	}
	l.refilter()
	return nil
}

func (l *listModel) applySubtree(msg subtreeMsg) error {
	if msg.err != nil {
		delete(l.expanded, msg.root)
		l.refilter()
		return msg.err
	}
	l.subtrees[msg.root] = msg.issues
	l.refilter()
	return nil
}

// toggle expands or collapses the row under the cursor and returns the id whose subtree
// must be fetched, if any. On a descendant row, collapse folds its root and moves there.
func (l *listModel) toggle(expand bool) (fetch int) {
	if l.cursor < 0 || l.cursor >= len(l.visible) {
		return 0
	}
	r := l.visible[l.cursor]
	if r.child {
		if !expand {
			delete(l.expanded, r.root)
			l.rebuild()
			l.selectRow(r.root, false)
			l.top = min(l.top, l.cursor)
		}
		return 0
	}
	id := r.issue.ID
	if !expand {
		delete(l.expanded, id)
		l.refilter()
		return 0
	}
	if len(l.kids[id]) == 0 || l.expanded[id] {
		return 0
	}
	l.expanded[id] = true
	l.refilter()
	if _, ok := l.subtrees[id]; ok {
		return 0
	}
	return id
}

// expandedIDs lists the expanded issues that are still loaded, to refresh their subtrees.
func (l *listModel) expandedIDs() []int {
	var ids []int
	for _, is := range l.issues {
		if l.expanded[is.ID] {
			ids = append(ids, is.ID)
		}
	}
	return ids
}

// refilter rebuilds the visible rows and keeps the cursor on the same issue when it is
// still there, so reloads and expansions do not jump back to the top.
func (l *listModel) refilter() {
	keep, keepChild := 0, false
	if r, ok := l.row(l.cursor); ok {
		keep, keepChild = r.issue.ID, r.child
	}
	l.rebuild()
	if keep != 0 {
		l.selectRow(keep, keepChild)
	}
	l.cursor = max(0, min(l.cursor, len(l.visible)-1))
	l.top = min(l.top, l.cursor)
}

func (l *listModel) rebuild() {
	q := lower(l.query)
	l.visible = l.visible[:0]
	for i := range l.issues {
		is := &l.issues[i]
		if q != "" && !matches(*is, q) {
			continue
		}
		l.visible = append(l.visible, listRow{issue: is})
		if !l.expanded[is.ID] {
			continue
		}
		sub, ok := l.subtrees[is.ID]
		if !ok {
			sub = l.kids[is.ID]
		}
		for _, tr := range buildTree(is.ID, sub) {
			child := tr.issue
			l.visible = append(l.visible, listRow{issue: &child, child: true, root: is.ID, prefix: tr.prefix})
		}
	}
}

func (l *listModel) row(pos int) (listRow, bool) {
	if pos < 0 || pos >= len(l.visible) {
		return listRow{}, false
	}
	return l.visible[pos], true
}

// selectRow puts the cursor on the row of issue id, if it is visible.
func (l *listModel) selectRow(id int, child bool) {
	for pos, r := range l.visible {
		if r.issue.ID == id && r.child == child {
			l.cursor = pos
			return
		}
	}
}

func (l *listModel) hasChildRows() bool {
	for _, r := range l.visible {
		if r.child {
			return true
		}
	}
	return false
}

func matches(is redmine.Issue, q string) bool {
	assignee := ""
	if is.AssignedTo != nil {
		assignee = is.AssignedTo.Name
	}
	hay := lower(fmt.Sprintf("#%d %s %s %s %s %s", is.ID, is.Subject, is.Project.Name, is.Tracker.Name, is.Status.Name, assignee))
	return strings.Contains(hay, q)
}

func (l *listModel) selected() *redmine.Issue {
	if r, ok := l.row(l.cursor); ok {
		return r.issue
	}
	return nil
}

func (l *listModel) startFilter() tea.Cmd {
	l.filtering = true
	l.input.SetValue(l.query)
	return l.input.Focus()
}

func (l *listModel) clearFilter() {
	l.query = ""
	l.input.SetValue("")
	l.refilter()
}

// handleFilterKey edits the quick filter live; enter keeps it, esc discards it.
func (l *listModel) handleFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		l.filtering = false
		l.input.Blur()
	case "esc":
		l.filtering = false
		l.input.Blur()
		l.clearFilter()
	default:
		var cmd tea.Cmd
		l.input, cmd = l.input.Update(msg)
		l.query = l.input.Value()
		l.cursor, l.top = 0, 0
		l.rebuild()
		return cmd
	}
	return nil
}

// forward passes non-key messages (cursor blink) to the filter input.
func (l *listModel) forward(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.input, cmd = l.input.Update(msg)
	return cmd
}

// move handles navigation keys and reports whether the cursor is near the end of the list.
func (l *listModel) move(key string, e env) {
	page := max(1, l.rowsFit(e)-1)
	switch key {
	case "up", "k":
		l.cursor--
	case "down", "j":
		l.cursor++
	case "pgup":
		l.cursor -= page
	case "pgdown":
		l.cursor += page
	case "home":
		l.cursor = 0
	case "end", "G":
		l.cursor = len(l.visible) - 1
	default:
		return
	}
	l.cursor = max(0, min(l.cursor, len(l.visible)-1))
}

func (l *listModel) nearEnd() bool {
	return len(l.visible) > 0 && l.cursor >= len(l.visible)-loadMoreMargin
}

// headerLines is how many lines sit above the table box.
func (l *listModel) headerLines() int {
	if l.filtering || l.query != "" {
		return 3
	}
	return 2
}

func (l *listModel) boxHeight(e env) int { return max(4, e.h-1-l.headerLines()) }

// rowsFit is how many table rows fit inside the box, under its borders and column header.
func (l *listModel) rowsFit(e env) int { return max(1, l.boxHeight(e)-3) }

func (l *listModel) previewOn(e env) bool { return l.showPreview && e.w >= previewMinWidth }

func (l *listModel) tableWidth(e env) int {
	if l.previewOn(e) {
		return e.w - previewWidth
	}
	return e.w
}

func (l *listModel) view(e env, frame string) string {
	var b strings.Builder
	b.WriteString(l.titleLine(e) + "\n")
	b.WriteString(l.filterSummaryLine(e) + "\n")
	if l.headerLines() == 3 {
		b.WriteString(l.quickFilterLine(e) + "\n")
	}

	table := l.table(e, frame)
	if l.previewOn(e) {
		table = lipgloss.JoinHorizontal(lipgloss.Top, table, l.previewBox(e))
	}
	b.WriteString(table)
	return b.String()
}

func (l *listModel) countText(e env) string {
	switch {
	case lower(l.query) != "":
		return e.t("list.count_filtered", l.topLevelVisible(), len(l.issues))
	case l.loaded && len(l.issues) < l.total:
		return e.t("list.count_partial", len(l.issues), l.total)
	default:
		return fmt.Sprintf("%d", len(l.issues))
	}
}

func (l *listModel) topLevelVisible() int {
	n := 0
	for _, r := range l.visible {
		if !r.child {
			n++
		}
	}
	return n
}

func (l *listModel) titleLine(e env) string {
	left := e.st.title.Render(" "+l.f.title(e)) + e.st.subtitle.Render(" · "+l.countText(e))
	switch {
	case l.loading && l.loaded:
		left += e.st.dim.Render(" · " + e.t("list.updating"))
	case l.loadingMore:
		left += e.st.dim.Render(" · " + e.t("list.loading_more"))
	case !l.loadedAt.IsZero():
		left += e.st.dim.Render(" · " + e.t("list.loaded_at", relativeTime(e.tr, e.now, l.loadedAt)))
	}
	right := l.statusSummary(e, e.w-lipgloss.Width(left)-3)
	if right == "" {
		return left
	}
	return left + strings.Repeat(" ", max(1, e.w-lipgloss.Width(left)-lipgloss.Width(right)-1)) + right
}

// statusSummary counts the loaded issues per status as colored dots, as many as fit in width.
func (l *listModel) statusSummary(e env, width int) string {
	counts := map[int]int{}
	names := map[int]redmine.Status{}
	var order []int
	for _, is := range l.issues {
		if _, seen := counts[is.Status.ID]; !seen {
			order = append(order, is.Status.ID)
			names[is.Status.ID] = is.Status
		}
		counts[is.Status.ID]++
	}
	rank := map[int]int{}
	for i, s := range e.statusList {
		rank[s.ID] = i
	}
	sortByRank(order, rank)

	var out string
	for _, id := range order {
		s := names[id]
		part := e.st.dot(e.statusTone(s)) + e.st.text.Render(fmt.Sprintf(" %d %s", counts[id], s.Name))
		next := part
		if out != "" {
			next = out + "  " + part
		}
		if lipgloss.Width(next) > width {
			break
		}
		out = next
	}
	return out
}

func sortByRank(ids []int, rank map[int]int) {
	slices.SortStableFunc(ids, func(a, b int) int {
		ra, oka := rank[a]
		rb, okb := rank[b]
		if oka && okb {
			return ra - rb
		}
		return 0
	})
}

func (l *listModel) filterSummaryLine(e env) string {
	return " " + e.st.dim.Render(truncate(l.f.summary(e), e.w-2))
}

func (l *listModel) quickFilterLine(e env) string {
	if l.filtering {
		return " " + inputView(l.input)
	}
	return " " + e.st.dim.Render(e.t("list.quick_filter")+": ") + e.st.accent.Render(l.query) + e.st.dim.Render("  "+e.t("list.quick_filter_hint"))
}

// ---- table ------------------------------------------------------------

type column int

const (
	colID column = iota
	colStatus
	colPriority
	colTracker
	colSubject
	colAssignee
	colProject
	colDue
	colUpdated
)

// hideOrder is the order in which columns give way when the terminal is narrow.
var hideOrder = []column{colProject, colTracker, colPriority}

type tableLayout struct {
	widths map[column]int
	shown  []column
}

func (t tableLayout) has(c column) bool { _, ok := t.widths[c]; return ok }

// layout sizes every column from the visible rows and drops columns until the subject has room.
func (l *listModel) layout(e env, inner int) tableLayout {
	w := map[column]int{
		colID: 3, colStatus: 6, colPriority: 8, colTracker: 7, colAssignee: 8, colProject: 7, colDue: 5, colUpdated: 6,
	}
	for _, r := range l.visible {
		is := *r.issue
		w[colID] = max(w[colID], len(fmt.Sprintf("#%d", is.ID)))
		w[colStatus] = max(w[colStatus], min(lipgloss.Width(is.Status.Name)+2, 16))
		w[colPriority] = max(w[colPriority], min(lipgloss.Width(is.Priority.Name)+2, 11))
		w[colTracker] = max(w[colTracker], min(lipgloss.Width(is.Tracker.Name), 12))
		if is.AssignedTo != nil {
			w[colAssignee] = max(w[colAssignee], min(lipgloss.Width(is.AssignedTo.Name), 18))
		}
		w[colProject] = max(w[colProject], min(lipgloss.Width(projectLabel(is.Project.Name, projectWidth)), projectWidth))
		w[colUpdated] = max(w[colUpdated], min(lipgloss.Width(relativeTime(e.tr, e.now, is.UpdatedOn)), 12))
		w[colDue] = max(w[colDue], lipgloss.Width(e.tr.Date(is.DueDate)))
	}
	order := []column{colID, colStatus, colPriority, colTracker, colSubject, colAssignee, colProject, colUpdated}
	if l.f.order == orderDue {
		order = slices.Insert(order, len(order)-1, colDue)
	}
	if l.f.assigned.id == "me" && !l.hasChildRows() {
		order = slices.DeleteFunc(order, func(c column) bool { return c == colAssignee })
	}
	hidden := 0
	for {
		used := 2 // cursor marker
		var shown []column
		for _, c := range order {
			if c != colSubject && hiddenCol(c, hidden) {
				continue
			}
			shown = append(shown, c)
			if c != colSubject {
				used += w[c]
			}
			used++ // gap
		}
		subject := inner - used
		if subject >= minSubjectWidth || hidden == len(hideOrder) {
			out := tableLayout{widths: map[column]int{colSubject: max(subject, 8)}, shown: shown}
			for _, c := range shown {
				if c != colSubject {
					out.widths[c] = w[c]
				}
			}
			return out
		}
		hidden++
	}
}

func hiddenCol(c column, hidden int) bool {
	for _, h := range hideOrder[:hidden] {
		if h == c {
			return true
		}
	}
	return false
}

func (l *listModel) table(e env, frame string) string {
	width := l.tableWidth(e)
	inner := max(10, width-4)
	rows := l.rowsFit(e)
	var body string
	switch {
	case l.loading && !l.loaded:
		body = lipgloss.Place(inner, rows+1, lipgloss.Center, lipgloss.Center, frame+" "+e.st.dim.Render(e.t("loading.issues")))
	case l.err != nil:
		body = lipgloss.Place(inner, rows+1, lipgloss.Center, lipgloss.Center, e.st.danger.Render(truncate(e.t("error.load", friendlyError(e, l.err)), inner))+"\n"+e.st.dim.Render(e.t("error.retry")))
	case len(l.visible) == 0 && strings.TrimSpace(l.query) != "":
		body = lipgloss.Place(inner, rows+1, lipgloss.Center, lipgloss.Center, e.st.dim.Render(truncate(e.t("list.no_match", l.query), inner)))
	case len(l.visible) == 0:
		body = lipgloss.Place(inner, rows+1, lipgloss.Center, lipgloss.Center, e.st.dim.Render(e.t("list.empty")))
	default:
		body = l.rowsView(e, inner, rows)
	}
	return e.st.box.Width(width).Height(l.boxHeight(e)).Render(body)
}

func (l *listModel) rowsView(e env, inner, rows int) string {
	lay := l.layout(e, inner)
	if l.cursor < l.top {
		l.top = l.cursor
	}
	if l.cursor >= l.top+rows {
		l.top = l.cursor - rows + 1
	}
	end := min(len(l.visible), l.top+rows)

	lines := []string{l.headerRow(e, lay, inner)}
	for pos := l.top; pos < end; pos++ {
		lines = append(lines, l.renderRow(e, lay, l.visible[pos], pos == l.cursor, inner))
	}
	return strings.Join(lines, "\n")
}

func (l *listModel) headerRow(e env, lay tableLayout, inner int) string {
	labels := map[column]string{
		colID: e.t("col.id"), colStatus: e.t("col.status"), colPriority: e.t("col.priority"),
		colTracker: e.t("col.tracker"), colSubject: e.t("col.subject"), colAssignee: e.t("col.assignee"),
		colProject: e.t("col.project"), colDue: e.t("col.due"), colUpdated: e.t("col.updated"),
	}
	parts := []string{"  "}
	for _, c := range lay.shown {
		parts = append(parts, padRight(truncate(labels[c], lay.widths[c]), lay.widths[c]))
	}
	return e.st.dim.Bold(true).Render(truncate(strings.Join(parts, " "), inner))
}

// painter renders cell text over the highlight color of the selected row.
type painter struct {
	bg color.Color
}

func (p painter) style(s lipgloss.Style) lipgloss.Style {
	if p.bg != nil {
		return s.Background(p.bg)
	}
	return s
}

func (p painter) text(s lipgloss.Style, text string, width int) string {
	text = padRight(truncate(text, width), width)
	return p.style(s).Render(text)
}

func (p painter) space(n int) string {
	return p.style(lipgloss.NewStyle()).Render(strings.Repeat(" ", max(0, n)))
}

func (l *listModel) renderRow(e env, lay tableLayout, r listRow, selected bool, inner int) string {
	is := *r.issue
	p := painter{}
	if selected {
		p.bg = e.st.rowBg
	}
	marker := p.space(2)
	subjectStyle := e.st.text
	if selected {
		marker = p.style(e.st.cursor).Render("▸ ")
		subjectStyle = e.st.selected
	}
	cells := []string{marker}
	for _, c := range lay.shown {
		w := lay.widths[c]
		var cell string
		switch c {
		case colID:
			cell = p.text(e.st.accent, fmt.Sprintf("#%d", is.ID), w)
		case colStatus:
			cell = statusCell(e, p, is.Status, w)
		case colPriority:
			cell = priorityCell(e, p, is.Priority, w)
		case colTracker:
			cell = p.text(e.st.dim, is.Tracker.Name, w)
		case colSubject:
			lead, leadStyle := l.subjectLead(r)
			lw := min(lipgloss.Width(lead), w)
			cell = p.text(leadStyle(e), lead, lw) + p.text(subjectStyle, is.Subject, w-lw)
		case colAssignee:
			cell = assigneeCell(e, p, is, w)
		case colProject:
			cell = p.text(e.st.dim, projectLabel(is.Project.Name, projectWidth), w)
		case colDue:
			cell = p.text(dueStyle(e, is), e.tr.Date(is.DueDate), w)
		case colUpdated:
			cell = p.text(e.st.dim, relativeTime(e.tr, e.now, is.UpdatedOn), w)
		}
		cells = append(cells, cell)
	}
	line := cells[0]
	for _, c := range cells[1:] {
		line += p.space(1) + c
	}
	if gap := inner - lipgloss.Width(line); gap > 0 {
		line += p.space(gap)
	}
	return truncate(line, inner)
}

// subjectLead is what goes before the subject: the tree glyphs of a descendant, or
// "+n" / "−n" on an issue with n children, collapsed or expanded.
func (l *listModel) subjectLead(r listRow) (string, func(env) lipgloss.Style) {
	if r.child {
		return r.prefix, func(e env) lipgloss.Style { return e.st.dim }
	}
	n := len(l.kids[r.issue.ID])
	if n == 0 {
		return "", func(e env) lipgloss.Style { return e.st.text }
	}
	sign := "+"
	if l.expanded[r.issue.ID] {
		sign = "−"
	}
	return fmt.Sprintf("%s%d ", sign, n), func(e env) lipgloss.Style { return e.st.accent.Bold(true) }
}

// ---- preview ----------------------------------------------------------

func (l *listModel) previewBox(e env) string {
	inner := previewWidth - 4
	height := l.boxHeight(e)
	is := l.selected()
	if is == nil {
		return e.st.box.Width(previewWidth).Height(height).Render(e.st.boxTitle.Render(e.t("preview.title")))
	}
	st := e.st
	field := func(label, value string) string {
		return truncate(st.dim.Render(padRight(label, 12))+st.text.Render(value), inner)
	}
	assignee := e.t("detail.unassigned")
	if is.AssignedTo != nil {
		assignee = is.AssignedTo.Name
	}
	head := lipgloss.NewStyle().Width(inner).Render(st.title.Render(fmt.Sprintf("#%d %s", is.ID, is.Subject)))
	lines := []string{
		st.boxTitle.Render(e.t("preview.title")),
		head,
		st.badge(e.statusTone(is.Status), is.Status.Name) + " " + st.badge(e.priorityTone(is.Priority), is.Priority.Name),
		truncate(st.dim.Render(padRight(e.t("preview.done"), 12))+st.progressBar(is.DoneRatio, 16), inner),
		field(e.t("detail.assigned"), assignee),
		field(e.t("detail.author"), is.Author.Name),
		field(e.t("col.updated"), e.tr.DateTime(is.UpdatedOn)),
		truncate(st.dim.Render(padRight(e.t("detail.due"), 12))+dueStyle(e, *is).Render(e.tr.Date(is.DueDate)), inner),
	}
	if is.FixedVersion != nil {
		lines = append(lines, field(e.t("detail.version"), is.FixedVersion.Name))
	}
	if kids := l.kids[is.ID]; len(kids) > 0 {
		lines = append(lines, field(e.t("preview.children"), e.t("preview.children_value", closedCount(e, kids), len(kids))))
	}
	lines = append(lines, "")
	room := height - 2 - lipgloss.Height(strings.Join(lines, "\n"))
	desc := st.dim.Render(e.t("detail.no_description"))
	if strings.TrimSpace(is.Description) != "" {
		desc = l.previewDescription(e, is, inner)
	}
	dl := strings.Split(desc, "\n")
	if len(dl) > max(room, 1) {
		dl = dl[:max(room, 1)]
	}
	body := strings.Join(lines, "\n") + "\n" + strings.Join(dl, "\n")
	return st.box.Width(previewWidth).Height(height).Render(body)
}

func (l *listModel) previewDescription(e env, is *redmine.Issue, width int) string {
	key := fmt.Sprintf("%d|%s|%d|%t|%t", is.ID, is.UpdatedOn, width, e.dark, e.noColor)
	if l.preview.key != key {
		l.preview = previewCache{key: key, text: markdown(e, is.Description, width)}
	}
	return l.preview.text
}
