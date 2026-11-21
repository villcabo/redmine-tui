package tui

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

// maxProjectsForMembers bounds the memberships requests of the user fallback.
const maxProjectsForMembers = 200

// userRef is a user filter value: "me" or a numeric id.
type userRef struct {
	id   string
	name string
}

func (u userRef) set() bool { return u.id != "" }

// filters is the state of the issue list. Every field becomes a query parameter.
type filters struct {
	assigned   userRef
	author     userRef
	watched    bool
	project    *redmine.IDName
	tracker    *redmine.IDName
	status     string // open, closed, * or a status id
	statusName string // label of a concrete status
	order      listOrder
}

// listOrder is how Redmine sorts the list; the zero value is most recently updated first.
type listOrder int

const (
	orderUpdated listOrder = iota
	orderDue
	orderPriority
	orderCreated
	orderCount
)

func (o listOrder) next() listOrder { return (o + 1) % orderCount }

func (o listOrder) label(e env) string {
	return e.t([orderCount]string{"order.updated", "order.due", "order.priority", "order.created"}[o])
}

func defaultFilters() filters {
	return filters{assigned: userRef{id: "me"}, status: redmine.StatusOpen}
}

func (f filters) query() redmine.ListQuery {
	q := redmine.ListQuery{Assigned: f.assigned.id, Author: f.author.id, Status: f.status}
	if f.watched {
		q.Watcher = "me"
	}
	if f.project != nil {
		q.ProjectID = f.project.ID
	}
	if f.tracker != nil {
		q.TrackerID = f.tracker.ID
	}
	switch f.order {
	case orderDue:
		q.Sort, q.WithDueDate = "due_date,priority:desc", true
	case orderPriority:
		q.Sort = "priority:desc,updated_on:desc"
	case orderCreated:
		q.Sort = "id:desc"
	}
	return q
}

func (f filters) userLabel(e env, u userRef) string {
	if u.id == "me" {
		return e.t("user.me")
	}
	return u.name
}

func (f filters) statusLabel(e env) string {
	switch f.status {
	case redmine.StatusClosed:
		return e.t("status.closed")
	case redmine.StatusAny:
		return e.t("status.all")
	case redmine.StatusOpen, "":
		return e.t("status.open")
	}
	return f.statusName
}

// title names the view after the one scope filter that defines it.
func (f filters) title(e env) string {
	scopes := 0
	title := e.t("view.all")
	if f.assigned.set() {
		scopes++
		if f.assigned.id == "me" {
			title = e.t("view.assigned_me")
		} else {
			title = e.t("view.assigned_to", f.assigned.name)
		}
	}
	if f.author.set() {
		scopes++
		if f.author.id == "me" {
			title = e.t("view.authored_me")
		} else {
			title = e.t("view.authored_by", f.author.name)
		}
	}
	if f.watched {
		scopes++
		title = e.t("view.watched")
	}
	if f.project != nil {
		scopes++
		title = e.t("view.project", f.project.Name)
	}
	if scopes > 1 {
		return e.t("view.issues")
	}
	return title
}

// summary lists the active filters, for the line under the title.
func (f filters) summary(e env) string {
	var parts []string
	if f.project != nil {
		parts = append(parts, e.t("filter.project")+": "+f.project.Name)
	}
	parts = append(parts, e.t("filter.status")+": "+f.statusLabel(e))
	if f.tracker != nil {
		parts = append(parts, e.t("filter.tracker")+": "+f.tracker.Name)
	}
	if f.assigned.set() {
		parts = append(parts, e.t("filter.assigned")+": "+f.userLabel(e, f.assigned))
	}
	if f.author.set() {
		parts = append(parts, e.t("filter.author")+": "+f.userLabel(e, f.author))
	}
	if f.watched {
		parts = append(parts, e.t("filter.watched"))
	}
	if f.order != orderUpdated {
		parts = append(parts, e.t("filter.order")+": "+f.order.label(e))
	}
	return strings.Join(parts, " · ")
}

// ---- users ------------------------------------------------------------

type userLookup struct {
	users      []redmine.Member
	denied     bool // /users.json answered 403
	viaMembers bool
	cache      []redmine.Member // memberships of the visible projects, once loaded
	err        error
}

// lookupUsers finds users by name. It uses /users.json, which only administrators can
// read; on a 403 it falls back to the members of the visible projects, ignoring the
// projects whose memberships are forbidden. denied and cache carry what earlier calls learned.
func lookupUsers(ctx context.Context, a API, name string, denied bool, cache []redmine.Member) userLookup {
	if !denied {
		users, err := a.ListUsers(ctx, name)
		var api *redmine.APIError
		switch {
		case err == nil:
			return userLookup{users: users, cache: cache}
		case errors.As(err, &api) && api.Status == 403:
			denied = true
		default:
			return userLookup{err: err, cache: cache}
		}
	}
	if cache == nil {
		var err error
		if cache, err = collectMembers(ctx, a); err != nil {
			return userLookup{denied: true, err: err}
		}
	}
	var out []redmine.Member
	q := lower(name)
	for _, u := range cache {
		if q == "" || strings.Contains(lower(u.Name), q) {
			out = append(out, u)
		}
	}
	return userLookup{users: out, denied: true, viaMembers: true, cache: cache}
}

func collectMembers(ctx context.Context, a API) ([]redmine.Member, error) {
	projects, err := a.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	all := []redmine.Member{}
	for _, p := range projects[:min(len(projects), maxProjectsForMembers)] {
		ms, err := a.ListMembers(ctx, p.ID)
		var api *redmine.APIError
		if errors.As(err, &api) {
			continue // a project the key cannot list does not stop the view
		}
		if err != nil {
			return nil, err
		}
		for _, u := range ms {
			if !seen[u.ID] {
				seen[u.ID] = true
				all = append(all, u)
			}
		}
	}
	slices.SortFunc(all, func(x, y redmine.Member) int { return strings.Compare(lower(x.Name), lower(y.Name)) })
	return all, nil
}

func userItems(e env, me redmine.Member, users []redmine.Member, includeMe, includeClear bool) []pickItem {
	var items []pickItem
	if includeClear {
		items = append(items, pickItem{label: e.t("user.any"), value: userRef{}})
	}
	if includeMe {
		label, name := e.t("user.me"), e.t("user.me")
		if me.Name != "" {
			label, name = e.t("user.me_named", me.Name), me.Name
		}
		items = append(items, pickItem{label: label, value: userRef{id: "me", name: name}})
	}
	for _, u := range users {
		items = append(items, pickItem{label: u.Name, value: userRef{id: strconv.Itoa(u.ID), name: u.Name}})
	}
	return items
}

// openUserPicker loads the users and opens a searchable picker. back is shown on cancel.
func (m *Model) openUserPicker(title string, includeMe, includeClear bool, back modal, onPick func(m *Model, u userRef) tea.Cmd) tea.Cmd {
	denied, cache, api := m.usersDenied, m.usersCache, m.api
	e := m.env()
	return m.await(e.t("loading.users"), func(ctx context.Context) (any, error) {
		return lookupUsers(ctx, api, "", denied, cache), nil
	}, func(m *Model, res any, _ error) tea.Cmd {
		r := res.(userLookup)
		if r.err != nil {
			return m.showError(r.err)
		}
		m.usersDenied, m.usersCache = r.denied, r.cache
		e := m.env()
		pick := func(m *Model, it pickItem) tea.Cmd { return onPick(m, it.value.(userRef)) }
		p := newPicker(e, title, userItems(e, m.me, r.users, includeMe, includeClear), pick).withFilter(e)
		p.back = back
		if r.viaMembers {
			p.note = e.t("users.members_only")
		} else {
			p.remote = func(seq int, q string) tea.Cmd {
				return m.searchUsersCmd(seq, q, includeMe, includeClear)
			}
		}
		m.modal = p
		return nil
	})
}

func (m *Model) searchUsersCmd(seq int, q string, includeMe, includeClear bool) tea.Cmd {
	api, denied, cache := m.api, m.usersDenied, m.usersCache
	e, me := m.env(), m.me
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		r := lookupUsers(ctx, api, q, denied, cache)
		msg := pickerItemsMsg{seq: seq, err: r.err, denied: r.denied, cache: r.cache}
		if r.err == nil {
			msg.items = userItems(e, me, r.users, includeMe && q == "", includeClear && q == "")
			if r.viaMembers {
				msg.note = e.t("users.members_only")
			}
		}
		return msg
	}
}

// ---- filter panel -----------------------------------------------------

const (
	rowProject = iota
	rowStatus
	rowTracker
	rowAssigned
	rowAuthor
	filterRows
)

// filtersModal edits a copy of the filters and applies it when closed.
type filtersModal struct {
	draft  filters
	cursor int
}

func (fm *filtersModal) value(e env, row int) string {
	d := fm.draft
	switch row {
	case rowProject:
		if d.project != nil {
			return d.project.Name
		}
	case rowStatus:
		return d.statusLabel(e)
	case rowTracker:
		if d.tracker != nil {
			return d.tracker.Name
		}
	case rowAssigned:
		if d.assigned.set() {
			return d.userLabel(e, d.assigned)
		}
	case rowAuthor:
		if d.author.set() {
			return d.userLabel(e, d.author)
		}
	}
	return e.t("filter.any")
}

func (fm *filtersModal) view(e env) string {
	labels := []string{"filter.project", "filter.status", "filter.tracker", "filter.assigned", "filter.author"}
	var b strings.Builder
	for i, key := range labels {
		marker, st := "  ", e.st.text
		if i == fm.cursor {
			marker, st = e.st.cursor.Render("▸ "), e.st.selected
		}
		b.WriteString(marker + e.st.dim.Render(padRight(e.t(key), 12)) + st.Render(fm.value(e, i)) + "\n")
	}
	return boxed(e, e.t("filters.title"), strings.TrimRight(b.String(), "\n"), dialogWidth(e, 56))
}

func (fm *filtersModal) help(e env) string { return e.t("help.filters") }

func (fm *filtersModal) clearRow(row int) {
	switch row {
	case rowProject:
		fm.draft.project = nil
	case rowStatus:
		fm.draft.status, fm.draft.statusName = redmine.StatusOpen, ""
	case rowTracker:
		fm.draft.tracker = nil
	case rowAssigned:
		fm.draft.assigned = userRef{}
	case rowAuthor:
		fm.draft.author = userRef{}
	}
}

func (fm *filtersModal) handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "f":
		m.modal = nil
		return m.applyFilters(fm.draft)
	case "up", "k":
		fm.cursor = max(0, fm.cursor-1)
	case "down", "j":
		fm.cursor = min(filterRows-1, fm.cursor+1)
	case "backspace", "delete", "x":
		fm.clearRow(fm.cursor)
	case "F":
		fm.draft = filters{status: redmine.StatusOpen}
	case "enter":
		return m.editFilterRow(fm)
	}
	return nil
}

func (m *Model) editFilterRow(fm *filtersModal) tea.Cmd {
	e := m.env()
	switch fm.cursor {
	case rowProject:
		return m.pickProject(e.t("filter.project"), fm, true, func(m *Model, p *redmine.IDName) tea.Cmd {
			fm.draft.project = p
			m.modal = fm
			return nil
		})
	case rowStatus:
		items := []pickItem{
			{label: e.t("status.open"), value: redmine.StatusOpen},
			{label: e.t("status.closed"), value: redmine.StatusClosed},
			{label: e.t("status.all"), value: redmine.StatusAny},
		}
		for _, s := range m.statusList {
			items = append(items, pickItem{label: s.Name, value: strconv.Itoa(s.ID)})
		}
		p := newPicker(e, e.t("filter.status"), items, func(m *Model, it pickItem) tea.Cmd {
			fm.draft.status = it.value.(string)
			fm.draft.statusName = it.label
			m.modal = fm
			return nil
		})
		p.back = fm
		m.modal = p
	case rowTracker:
		return m.await(e.t("loading.trackers"), func(ctx context.Context) (any, error) {
			return m.api.ListTrackers(ctx)
		}, func(m *Model, res any, err error) tea.Cmd {
			if err != nil {
				return m.showError(err)
			}
			e := m.env()
			items := []pickItem{{label: e.t("filter.any"), value: (*redmine.IDName)(nil)}}
			for _, t := range res.([]redmine.IDName) {
				items = append(items, pickItem{label: t.Name, value: &redmine.IDName{ID: t.ID, Name: t.Name}})
			}
			p := newPicker(e, e.t("filter.tracker"), items, func(m *Model, it pickItem) tea.Cmd {
				fm.draft.tracker = it.value.(*redmine.IDName)
				m.modal = fm
				return nil
			})
			p.back = fm
			m.modal = p
			return nil
		})
	case rowAssigned:
		return m.openUserPicker(e.t("filter.assigned"), true, true, fm, func(m *Model, u userRef) tea.Cmd {
			fm.draft.assigned = u
			m.modal = fm
			return nil
		})
	case rowAuthor:
		return m.openUserPicker(e.t("filter.author"), true, true, fm, func(m *Model, u userRef) tea.Cmd {
			fm.draft.author = u
			m.modal = fm
			return nil
		})
	}
	return nil
}

// pickProject loads the projects (once per session) and opens a searchable picker.
func (m *Model) pickProject(title string, back modal, allowNone bool, onPick func(m *Model, p *redmine.IDName) tea.Cmd) tea.Cmd {
	open := func(m *Model) {
		e := m.env()
		var items []pickItem
		if allowNone {
			items = append(items, pickItem{label: e.t("filter.any"), value: (*redmine.IDName)(nil)})
		}
		for _, p := range m.projects {
			items = append(items, pickItem{label: p.Name, value: &redmine.IDName{ID: p.ID, Name: p.Name}})
		}
		p := newPicker(e, title, items, func(m *Model, it pickItem) tea.Cmd { return onPick(m, it.value.(*redmine.IDName)) }).withFilter(e)
		p.back = back
		m.modal = p
	}
	if m.projects != nil {
		open(m)
		return nil
	}
	return m.await(m.env().t("loading.projects"), func(ctx context.Context) (any, error) {
		return m.api.ListProjects(ctx)
	}, func(m *Model, res any, err error) tea.Cmd {
		if err != nil {
			return m.showError(err)
		}
		m.projects = res.([]redmine.Project)
		if m.projects == nil {
			m.projects = []redmine.Project{}
		}
		open(m)
		return nil
	})
}
