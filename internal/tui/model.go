// Package tui is the Bubble Tea interface of redmine-tui.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

const (
	minWidth  = 40
	minHeight = 8
)

// Options configures the UI.
type Options struct {
	BaseURL      string
	StartID      int // a positive id opens that issue directly
	NoColor      bool
	ReadOnly     bool
	Lang         i18n.Lang
	Editor       string
	StatusColors map[string]string // lower-cased status name -> color name
}

// env is everything a screen needs to render itself.
type env struct {
	st            styles
	tr            i18n.Translator
	dark          bool
	noColor       bool
	statuses      map[int]redmine.Status
	statusList    []redmine.Status
	defaultStatus int
	statusColors  map[string]string
	priorities    []redmine.Priority
	// fieldFormats maps custom field ids to their format ("user", "date", …) when the key
	// may read the definitions; userNames resolves the ids of user custom fields.
	fieldFormats map[int]string
	userNames    map[int]string
	now          time.Time
	w, h         int
}

func (e env) t(key string, args ...any) string { return e.tr.T(key, args...) }

type flashMsg struct {
	text string
	bad  bool
}

// Model is the root Bubble Tea model. It owns the list, a stack of issue details and
// the dialog on top of them.
type Model struct {
	api     API
	baseURL string
	host    string
	opts    Options
	tr      i18n.Translator

	st      styles
	dark    bool
	noColor bool
	w, h    int
	now     func() time.Time
	// lookPath finds the editor program; a field so tests can control it.
	lookPath func(string) (string, error)

	spin spinner.Model

	statuses      map[int]redmine.Status
	statusList    []redmine.Status
	defaultStatus int
	priorities    []redmine.Priority
	me            redmine.Member
	projects      []redmine.Project

	usersDenied bool
	usersCache  []redmine.Member

	customDefs        []redmine.CustomFieldDef
	customDefsOK      bool
	customDefsTried   bool
	customDefsPending bool
	fieldFormats      map[int]string
	userNames         map[int]string

	list listModel
	// issueCache keeps the last full copy of each opened issue, so reopening it paints at
	// once while the fresh copy loads.
	issueCache map[int]*redmine.Issue
	search     *searchModel
	stack      []*detailModel
	form       *formModel
	modal      modal
	nextUID    int

	awaitToken int
	writing    bool
	showHelp   bool
	flash      flashMsg
}

// New builds the root model.
func New(api API, opts Options) *Model {
	host := opts.BaseURL
	if u, err := url.Parse(opts.BaseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	m := &Model{
		api:        api,
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		host:       host,
		opts:       opts,
		tr:         i18n.New(opts.Lang),
		dark:       true,
		noColor:    opts.NoColor,
		w:          100,
		h:          30,
		now:        time.Now,
		lookPath:   exec.LookPath,
		spin:       spinner.New(spinner.WithSpinner(spinner.Dot)),
		statuses:   map[int]redmine.Status{},
		list:       newListModel(),
		issueCache: map[int]*redmine.Issue{},
		userNames:  map[int]string{},
	}
	m.st = newStyles(m.dark, opts.NoColor)
	if opts.StartID > 0 {
		m.stack = append(m.stack, m.newDetail(opts.StartID))
	}
	return m
}

func (m *Model) newDetail(id int) *detailModel {
	m.nextUID++
	return newDetailModel(m.nextUID, id)
}

func (m *Model) env() env {
	return env{
		st: m.st, tr: m.tr, dark: m.dark, noColor: m.noColor,
		statuses: m.statuses, statusList: m.statusList, defaultStatus: m.defaultStatus,
		statusColors: m.opts.StatusColors, priorities: m.priorities, now: m.now(),
		fieldFormats: m.fieldFormats, userNames: m.userNames,
		w: max(m.w, minWidth), h: max(m.h, minHeight),
	}
}

func (m *Model) notify(text string) { m.flash = flashMsg{text: text} }
func (m *Model) warn(text string)   { m.flash = flashMsg{text: text, bad: true} }

// Init loads the reference data and the first screen.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, fetchStatusesCmd(m.api), fetchPrioritiesCmd(m.api), fetchMeCmd(m.api)}
	if len(m.stack) > 0 {
		d := m.stack[0]
		cmds = append(cmds, fetchIssueCmd(m.api, d.uid, d.id), m.spin.Tick)
	} else {
		cmds = append(cmds, m.reloadList())
	}
	return tea.Batch(cmds...)
}

// Update handles messages for the active screen.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.refreshAll()
		return m, nil

	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.st = newStyles(m.dark, m.noColor)
		m.refreshAll()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if !m.busy() {
			return m, nil
		}
		return m, cmd

	case statusesMsg:
		if msg.err == nil {
			m.setStatuses(msg.statuses)
			m.refreshAll()
		}
		return m, nil

	case prioritiesMsg:
		if msg.err == nil {
			m.priorities = msg.priorities
			if m.form != nil && m.form.priority == nil {
				for _, p := range msg.priorities {
					if p.IsDefault {
						m.form.priority = &redmine.IDName{ID: p.ID, Name: p.Name}
					}
				}
			}
		}
		return m, nil

	case meMsg:
		if msg.err == nil {
			m.me = msg.me
		}
		return m, nil

	case issuesMsg:
		if msg.gen == m.list.gen && msg.offset == 0 && msg.err == nil {
			m.list.loadedAt = m.now()
		}
		if err := m.list.apply(msg); err != nil {
			m.warn(friendlyError(m.env(), err))
			return m, nil
		}
		return m, m.listFollowUps(msg)

	case childrenMsg:
		if err := m.list.applyChildren(msg); err != nil {
			m.warn(m.tr.T("error.children", friendlyError(m.env(), err)))
		}
		return m, nil

	case subtreeMsg:
		if err := m.list.applySubtree(msg); err != nil {
			m.warn(m.tr.T("error.children", friendlyError(m.env(), err)))
		}
		return m, nil

	case customDefsMsg:
		m.customDefsPending = false
		m.setCustomDefs(msg.defs, msg.err)
		var cmds []tea.Cmd
		for _, d := range m.stack {
			if d.issue != nil {
				cmds = append(cmds, m.resolveCustomValues(d.issue))
			}
		}
		m.refreshAll()
		return m, tea.Batch(cmds...)

	case userNamesMsg:
		for id, name := range msg.names {
			m.userNames[id] = name
		}
		m.refreshAll()
		return m, nil

	case descendantsMsg:
		for _, d := range m.stack {
			if d.uid == msg.uid {
				d.applyDescendants(msg, m.env())
			}
		}
		return m, nil

	case relatedMsg:
		for _, d := range m.stack {
			if d.uid == msg.uid {
				d.applyRelated(msg, m.env())
			}
		}
		return m, nil

	case searchMsg:
		if m.search != nil {
			m.search.apply(msg)
		}
		return m, nil

	case issueMsg:
		var cmds []tea.Cmd
		for _, d := range m.stack {
			if d.uid == msg.uid {
				d.apply(msg, m.env())
				if msg.err == nil && msg.issue != nil {
					m.issueCache[d.id] = msg.issue
					cmds = append(cmds, m.resolveCustomValues(msg.issue))
					cmds = append(cmds, d.followUps(m.api)...)
				}
			}
		}
		return m, tea.Batch(cmds...)

	case awaitMsg:
		if msg.token != m.awaitToken {
			return m, nil
		}
		if lm, ok := m.modal.(*loadingModal); ok && lm.token == msg.token {
			m.modal = nil
		}
		return m, msg.then(m, msg.res, msg.err)

	case writeDoneMsg:
		return m, m.onWriteDone(msg)

	case editorDoneMsg:
		if msg.err != nil {
			return m, m.showError(msg.err)
		}
		return m, msg.then(m, msg.text)

	case pickerQueryMsg:
		if p, ok := m.modal.(*pickerModal); ok && p.seq == msg.seq && p.remote != nil {
			return m, p.remote(msg.seq, msg.query)
		}
		return m, nil

	case pickerItemsMsg:
		if p, ok := m.modal.(*pickerModal); ok && p.seq == msg.seq {
			if msg.err != nil {
				p.note = friendlyError(m.env(), msg.err)
				return m, nil
			}
			p.items, p.note = msg.items, msg.note
			m.usersDenied, m.usersCache = msg.denied, msg.cache
		}
		return m, nil

	case openedMsg:
		if msg.err != nil {
			m.warn(m.tr.T("error.browser", msg.err.Error()))
		} else {
			m.notify(m.tr.T("notice.opened", msg.id))
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, m.forwardMsg(msg)
}

// forwardMsg gives non-key messages (cursor blinks) to whatever text input is active.
func (m *Model) forwardMsg(msg tea.Msg) tea.Cmd {
	if f, ok := m.modal.(forwarder); ok {
		return f.forward(msg)
	}
	if m.form != nil {
		return m.formForward(msg)
	}
	if m.list.filtering && m.activeScreen() == screenList {
		return m.list.forward(msg)
	}
	return nil
}

func (m *Model) setStatuses(list []redmine.Status) {
	m.statusList = list
	m.defaultStatus = 0
	for _, s := range list {
		m.statuses[s.ID] = s
		if s.IsDefault && m.defaultStatus == 0 {
			m.defaultStatus = s.ID
		}
	}
	if m.defaultStatus == 0 && len(list) > 0 {
		m.defaultStatus = list[0].ID
	}
}

type screen int

const (
	screenList screen = iota
	screenSearch
	screenDetail
	screenForm
)

func (m *Model) activeScreen() screen {
	switch {
	case m.form != nil:
		return screenForm
	case len(m.stack) > 0:
		return screenDetail
	case m.search != nil:
		return screenSearch
	default:
		return screenList
	}
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.writing {
		return m, nil
	}
	m.flash = flashMsg{}

	if m.modal != nil {
		return m, m.modal.handleKey(m, msg)
	}
	if m.showHelp {
		if key == "q" {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}
	if m.form != nil {
		return m, m.handleFormKey(msg)
	}
	if m.activeScreen() == screenList && m.list.filtering {
		return m, m.list.handleFilterKey(msg)
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "g":
		m.askIssueNumber()
		return m, nil
	case "s":
		m.askSearch()
		return m, nil
	}

	switch m.activeScreen() {
	case screenDetail:
		return m, m.handleDetailKey(m.active(), key, msg)
	case screenSearch:
		return m, m.handleSearchKey(key)
	default:
		return m, m.handleListKey(key)
	}
}

func (m *Model) handleListKey(key string) tea.Cmd {
	switch key {
	case "enter":
		if is := m.list.selected(); is != nil {
			return m.openIssue(is.ID)
		}
	case "a":
		if m.list.f.status == redmine.StatusOpen {
			m.list.f.status = redmine.StatusAny
		} else {
			m.list.f.status = redmine.StatusOpen
		}
		m.list.f.statusName = ""
		return m.reloadList()
	case "r":
		return m.reloadList()
	case "o":
		if is := m.list.selected(); is != nil {
			return m.openBrowser(is.ID)
		}
	case "y":
		if is := m.list.selected(); is != nil {
			m.openCopyMenu(is.ID, is.Subject)
		}
	case "/":
		return m.list.startFilter()
	case "esc":
		m.list.clearFilter()
	case "v":
		m.openViewMenu()
	case "f":
		m.modal = &filtersModal{draft: m.list.f}
	case "F", "ctrl+r":
		return m.applyFilters(filters{status: redmine.StatusOpen})
	case "p":
		m.list.showPreview = !m.list.showPreview
	case "O":
		f := m.list.f
		f.order = f.order.next()
		return m.applyFilters(f)
	case "N":
		return m.openForm()
	case "S", "n", "D", "A", "c":
		if is := m.list.selected(); is != nil {
			return m.actOnListIssue(is.ID, writeAction(key))
		}
	case "right", "l", "left", "h", "space":
		expand := key == "right" || key == "l"
		if key == "space" {
			is := m.list.selected()
			expand = is != nil && !m.list.expanded[is.ID]
		}
		if id := m.list.toggle(expand); id > 0 {
			return fetchSubtreeCmd(m.api, id)
		}
	default:
		m.list.move(key, m.env())
		return m.maybeLoadMore()
	}
	return nil
}

// listFollowUps asks for the children of the issues a page brought and, after a fresh
// first page, refreshes the subtrees that are still expanded.
func (m *Model) listFollowUps(msg issuesMsg) tea.Cmd {
	if msg.gen != m.list.gen || msg.err != nil {
		return nil
	}
	var cmds []tea.Cmd
	if ids := m.list.pageIDs(msg.offset); len(ids) > 0 {
		cmds = append(cmds, fetchChildrenCmd(m.api, msg.gen, ids))
	}
	if msg.offset == 0 {
		for _, id := range m.list.expandedIDs() {
			cmds = append(cmds, fetchSubtreeCmd(m.api, id))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) maybeLoadMore() tea.Cmd {
	if !m.list.nearEnd() {
		return nil
	}
	if gen, offset, ok := m.list.startMore(); ok {
		return fetchIssuesCmd(m.api, gen, m.list.f.query(), offset)
	}
	return nil
}

func (m *Model) handleSearchKey(key string) tea.Cmd {
	s := m.search
	switch key {
	case "esc", "backspace":
		m.search = nil
	case "enter":
		if r := s.selected(); r != nil {
			return m.openIssue(r.ID)
		}
	case "o":
		if r := s.selected(); r != nil {
			return m.openBrowser(r.ID)
		}
	case "y":
		if r := s.selected(); r != nil {
			m.openCopyMenu(r.ID, "")
		}
	case "r":
		return m.startSearch(s.query)
	default:
		s.move(key, m.env())
	}
	return nil
}

func (m *Model) handleDetailKey(d *detailModel, key string, msg tea.KeyPressMsg) tea.Cmd {
	switch key {
	case "esc", "backspace":
		m.stack = m.stack[:len(m.stack)-1]
		if len(m.stack) == 0 && m.search == nil && !m.list.loaded && !m.list.loading {
			return m.reloadList()
		}
		return nil
	case "o":
		return m.openBrowser(d.id)
	case "r":
		d.loading, d.err = true, nil
		return tea.Batch(fetchIssueCmd(m.api, d.uid, d.id), m.spin.Tick)
	case "y":
		subject := ""
		if d.issue != nil {
			subject = d.issue.Subject
		}
		m.openCopyMenu(d.id, subject)
		return nil
	case "enter":
		if id := d.selectedIssueID(); id > 0 {
			return m.openIssue(id)
		}
		return nil
	case "S", "n", "D", "A", "c":
		return writeAction(key)(m, d.issue)
	}
	d.handleKey(key, msg, m.env())
	return nil
}

// writeAction maps a write key to the action it starts on an issue.
func writeAction(key string) func(*Model, *redmine.Issue) tea.Cmd {
	switch key {
	case "S":
		return (*Model).startStatus
	case "n":
		return (*Model).startNote
	case "D":
		return (*Model).startDone
	case "A":
		return (*Model).startAssign
	default:
		return (*Model).startClose
	}
}

// actOnListIssue fetches the issue before acting on it from the list: a list row lacks
// the statuses the workflow allows, and its percent done or assignee may be stale.
func (m *Model) actOnListIssue(id int, act func(*Model, *redmine.Issue) tea.Cmd) tea.Cmd {
	if !m.writable() {
		return nil
	}
	api := m.api
	return m.await(m.tr.T("loading.issue", id), func(ctx context.Context) (any, error) {
		return api.GetIssue(ctx, id)
	}, func(m *Model, res any, err error) tea.Cmd {
		if err != nil {
			return m.showError(err)
		}
		return act(m, res.(*redmine.Issue))
	})
}

func (m *Model) active() *detailModel {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

func (m *Model) busy() bool {
	if m.list.loading || m.list.loadingMore || m.writing {
		return true
	}
	if _, ok := m.modal.(*loadingModal); ok {
		return true
	}
	if m.search != nil && m.search.loading {
		return true
	}
	for _, d := range m.stack {
		if d.loading {
			return true
		}
	}
	return false
}

func (m *Model) openIssue(id int) tea.Cmd {
	d := m.newDetail(id)
	d.prefill(m.knownIssue(id))
	m.stack = append(m.stack, d)
	d.relayout(m.env())
	return tea.Batch(fetchIssueCmd(m.api, d.uid, d.id), m.spin.Tick)
}

// knownIssue returns what is already known about an issue without asking Redmine: the
// last full copy, or else its list row (which lacks history, children and relations).
func (m *Model) knownIssue(id int) (is *redmine.Issue, full bool) {
	if is, ok := m.issueCache[id]; ok {
		return is, true
	}
	for _, r := range m.list.visible {
		if r.issue.ID == id {
			row := *r.issue
			return &row, false
		}
	}
	return nil, false
}

func (m *Model) reloadList() tea.Cmd {
	gen := m.list.startLoad()
	return tea.Batch(fetchIssuesCmd(m.api, gen, m.list.f.query(), 0), m.spin.Tick)
}

func (m *Model) applyFilters(f filters) tea.Cmd {
	if f == m.list.f {
		return nil
	}
	m.list.f = f
	return m.reloadList()
}

func (m *Model) openBrowser(id int) tea.Cmd {
	target := fmt.Sprintf("%s/issues/%d", m.baseURL, id)
	name, args := browserCommand(runtime.GOOS, target)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browserTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
			return openedMsg{err: err}
		}
		return openedMsg{id: id}
	}
}

// browserCommand is the program that opens a URL in the default browser on goos.
func browserCommand(goos, target string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{target}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		return "xdg-open", []string{target}
	}
}

func (m *Model) refreshAll() {
	e := m.env()
	for _, d := range m.stack {
		d.relayout(e)
	}
}

// ---- small dialogs ----------------------------------------------------

func (m *Model) askIssueNumber() {
	e := m.env()
	in := newInput("#", e.t("goto.placeholder"))
	in.Focus()
	m.modal = &inputModal{title: e.t("goto.title"), input: in, onSubmit: func(m *Model, v string) (tea.Cmd, string) {
		id, err := strconv.Atoi(strings.TrimPrefix(v, "#"))
		if err != nil || id <= 0 {
			return nil, m.tr.T("goto.invalid")
		}
		return m.openIssue(id), ""
	}}
}

func (m *Model) askSearch() {
	e := m.env()
	in := newInput("> ", e.t("search.placeholder"))
	in.Focus()
	m.modal = &inputModal{title: e.t("search.prompt"), input: in, onSubmit: func(m *Model, v string) (tea.Cmd, string) {
		if v == "" {
			return nil, m.tr.T("search.need_text")
		}
		for len(m.stack) > 0 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		return m.startSearch(v), ""
	}}
}

// openViewMenu offers the preset views; each one only sets the scope filters and keeps
// the status and tracker already chosen.
func (m *Model) openViewMenu() {
	e := m.env()
	scope := func(set func(f *filters)) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			f := m.list.f
			f.assigned, f.author, f.watched, f.project = userRef{}, userRef{}, false, nil
			set(&f)
			return m.applyFilters(f)
		}
	}
	userView := func(title string, set func(f *filters, u userRef)) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			return m.openUserPicker(title, true, false, nil, func(m *Model, u userRef) tea.Cmd {
				return scope(func(f *filters) { set(f, u) })(m)
			})
		}
	}
	projectView := func(m *Model) tea.Cmd {
		return m.pickProject(e.t("view.by_project"), nil, false, func(m *Model, p *redmine.IDName) tea.Cmd {
			return scope(func(f *filters) { f.project = p })(m)
		})
	}
	items := []pickItem{
		{label: e.t("view.assigned_me"), value: scope(func(f *filters) { f.assigned = userRef{id: "me"} })},
		{label: e.t("view.authored_me"), value: scope(func(f *filters) { f.author = userRef{id: "me"} })},
		{label: e.t("view.watched"), value: scope(func(f *filters) { f.watched = true })},
		{label: e.t("view.all"), value: scope(func(*filters) {})},
		{label: e.t("view.assigned_to_menu"), value: userView(e.t("view.assigned_to_menu"), func(f *filters, u userRef) { f.assigned = u })},
		{label: e.t("view.authored_by_menu"), value: userView(e.t("view.authored_by_menu"), func(f *filters, u userRef) { f.author = u })},
		{label: e.t("view.by_project"), value: projectView},
	}
	m.modal = newPicker(e, e.t("view.title"), items, func(m *Model, it pickItem) tea.Cmd {
		return it.value.(func(m *Model) tea.Cmd)(m)
	})
}

// openCopyMenu offers what can be copied for an issue. subject may be empty when unknown.
func (m *Model) openCopyMenu(id int, subject string) {
	e := m.env()
	link := fmt.Sprintf("%s/issues/%d", m.baseURL, id)
	items := []pickItem{
		{label: e.t("copy.link") + "  " + link, value: link},
		{label: e.t("copy.id") + "  #" + strconv.Itoa(id), value: "#" + strconv.Itoa(id)},
	}
	if subject != "" {
		items = append(items,
			pickItem{label: e.t("copy.title") + "  " + subject, value: subject},
			pickItem{label: e.t("copy.id_title") + "  #" + strconv.Itoa(id) + " " + subject, value: fmt.Sprintf("#%d %s", id, subject)},
		)
	}
	m.modal = newPicker(e, e.t("copy.title_menu"), items, func(m *Model, it pickItem) tea.Cmd {
		m.notify(m.tr.T("copy.done"))
		return tea.SetClipboard(it.value.(string))
	})
}

// await opens a loading dialog, runs fn in the background and calls then with its result.
// Closing the dialog with esc discards the result.
func (m *Model) await(label string, fn func(ctx context.Context) (any, error), then func(m *Model, res any, err error) tea.Cmd) tea.Cmd {
	m.awaitToken++
	token := m.awaitToken
	m.modal = &loadingModal{text: label, token: token}
	run := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		res, err := fn(ctx)
		return awaitMsg{token: token, res: res, err: err, then: then}
	}
	return tea.Batch(run, m.spin.Tick)
}

// ---- rendering --------------------------------------------------------

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "redmine-tui"
	return v
}

// render draws the active screen padded to the full window height.
func (m *Model) render() string {
	e := m.env()
	var body, help string
	switch {
	case m.showHelp && m.modal == nil:
		body, help = m.helpView(e), e.t("help.close_help")
	case m.form != nil:
		body, help = m.form.view(e), e.t("help.form")
	case m.active() != nil:
		body, help = m.active().view(e, m.spin.View()), e.t("help.detail")
	case m.search != nil:
		body, help = m.search.view(e, m.spin.View()), e.t("help.search")
	default:
		body, help = m.list.view(e, m.spin.View()), e.t("help.list")
	}

	room := e.h - 1
	lines := strings.Split(body, "\n")
	if m.modal != nil {
		help = m.modal.help(e)
		lines = strings.Split(lipgloss.Place(e.w, room, lipgloss.Center, lipgloss.Center, m.modal.view(e)), "\n")
	}
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n" + m.statusBar(e, help)
}

// statusBar shows the shortcuts (or the last notice) on the left and the mode, user and
// host on the right.
func (m *Model) statusBar(e env, help string) string {
	var right []string
	if m.opts.ReadOnly {
		right = append(right, e.st.danger.Bold(true).Render(e.t("status.read_only")))
	}
	if m.me.Name != "" {
		right = append(right, e.st.text.Render(m.me.Name))
	}
	right = append(right, e.st.dim.Render(m.host))
	r := strings.Join(right, e.st.dim.Render(" · "))

	var left string
	switch {
	case m.writing:
		left = e.st.accent.Render(m.spin.View() + " " + e.t("write.saving"))
	case m.flash.text != "" && m.flash.bad:
		left = e.st.danger.Render(m.flash.text)
	case m.flash.text != "":
		left = e.st.accent.Render(m.flash.text)
	default:
		left = e.st.help.Render(help)
	}
	avail := e.w - lipgloss.Width(r) - 3
	if avail < 10 {
		return truncate(left, e.w-1)
	}
	left = truncate(left, avail)
	return " " + left + strings.Repeat(" ", max(1, e.w-lipgloss.Width(left)-lipgloss.Width(r)-2)) + r
}

func (m *Model) helpView(e env) string {
	keys := [][2]string{
		{"↑ ↓ / j k", "help.row.move"},
		{"enter", "help.row.open"},
		{"g", "help.row.goto"},
		{"s", "help.row.search"},
		{"/", "help.row.filter"},
		{"v", "help.row.views"},
		{"f", "help.row.filters"},
		{"F / ctrl+r", "help.row.clear_filters"},
		{"a", "help.row.all"},
		{"p", "help.row.preview"},
		{"O", "help.row.order"},
		{"y", "help.row.copy"},
		{"→ ← / space", "help.row.expand"},
		{"tab / ← →", "help.row.tabs"},
		{"o", "help.row.browser"},
		{"r", "help.row.reload"},
		{"S", "help.row.status"},
		{"c", "help.row.close"},
		{"n", "help.row.note"},
		{"D", "help.row.done"},
		{"A", "help.row.assign"},
		{"N", "help.row.new"},
		{"esc / backspace", "help.row.back"},
		{"q / ctrl+c", "help.row.quit"},
	}
	var b strings.Builder
	b.WriteString(e.st.boxTitle.Render(e.t("help.title")) + "\n\n")
	for _, r := range keys {
		b.WriteString(e.st.accent.Render(padRight(r[0], 17)) + e.st.text.Render(e.t(r[1])) + "\n")
	}
	if m.opts.ReadOnly {
		b.WriteString("\n" + e.st.danger.Render(e.t("help.read_only_note")))
	}
	return "\n" + e.st.box.Render(strings.TrimRight(b.String(), "\n"))
}

// friendlyError turns an error into a message in the interface language.
func friendlyError(e env, err error) string {
	var unknown *redmine.OutcomeUnknownError
	if errors.As(err, &unknown) {
		return e.t("error.outcome_unknown")
	}
	var api *redmine.APIError
	if !errors.As(err, &api) {
		return e.tr.Err(err)
	}
	var head string
	switch {
	case api.Status == 401:
		head = e.t("error.401")
	case api.Status == 403:
		head = e.t("error.403")
	case api.Status == 404:
		head = e.t("error.404")
	case api.Status == 422:
		head = e.t("error.422")
	case api.Status >= 500:
		head = e.t("error.5xx", api.Status)
	default:
		head = e.t("error.other", api.Status)
	}
	if len(api.Messages) > 0 {
		head += "\n" + strings.Join(api.Messages, "\n")
	}
	return head
}

func errorBox(e env, err error) string {
	msg := e.t("error.load", friendlyError(e, err)) + "\n\n" + e.t("error.retry")
	return "\n" + e.st.errorBox.Width(max(e.w-2, 20)).Render(msg)
}

func loadingView(e env, frame, text string) string {
	return "\n  " + frame + " " + e.st.dim.Render(text)
}

func padRight(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// setCustomDefs stores the custom field definitions and the format of each field.
func (m *Model) setCustomDefs(defs []redmine.CustomFieldDef, err error) {
	m.customDefsTried = true
	m.customDefs, m.customDefsOK = defs, err == nil
	if err != nil {
		return
	}
	m.fieldFormats = make(map[int]string, len(defs))
	for _, d := range defs {
		m.fieldFormats[d.ID] = d.FieldFormat
	}
}

// resolveCustomValues fetches what the issue's custom values need to be readable: the
// field definitions once, then the names of the users that user fields point at.
func (m *Model) resolveCustomValues(is *redmine.Issue) tea.Cmd {
	if !m.customDefsTried {
		if m.customDefsPending {
			return nil
		}
		m.customDefsPending = true
		return fetchCustomDefsCmd(m.api)
	}
	var ids []int
	want := func(raw string) {
		for _, part := range strings.Split(raw, ", ") {
			id, err := strconv.Atoi(strings.TrimSpace(part))
			if _, known := m.userNames[id]; err == nil && id > 0 && !known && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	for _, cf := range is.CustomFields {
		if m.fieldFormats[cf.ID] == "user" {
			want(cf.Text())
		}
	}
	for _, j := range is.Journals {
		for _, det := range j.Details {
			if id, err := strconv.Atoi(det.Name); err == nil && det.Property == "cf" && m.fieldFormats[id] == "user" {
				want(valueOr(det.OldValue, ""))
				want(valueOr(det.NewValue, ""))
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		m.userNames[id] = "" // asked once; a failure keeps showing the id
	}
	return fetchUserNamesCmd(m.api, ids)
}
