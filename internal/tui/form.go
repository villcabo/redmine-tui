package tui

import (
	"context"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

// Fixed form rows; custom fields follow them.
const (
	fProject = iota
	fTracker
	fSubject
	fDesc
	fPriority
	fAssignee
	fParent
	fixedFields
)

type customInput struct {
	def   redmine.CustomFieldDef
	value string // chosen option for list fields
	input textinput.Model
	asked bool // true when the field is required
}

func (c customInput) isList() bool { return len(c.def.PossibleValues) > 0 }

func (c customInput) current() string {
	if c.isList() {
		return c.value
	}
	return strings.TrimSpace(c.input.Value())
}

// formModel is the "new issue" screen. It lives until the issue is created or the
// user discards it, so a rejected submission never loses what was typed.
type formModel struct {
	project  *redmine.IDName
	info     *redmine.Project
	tracker  *redmine.IDName
	subject  textinput.Model
	desc     string
	priority *redmine.IDName
	assignee *redmine.IDName
	parent   textinput.Model
	custom   []customInput
	focus    int
	errs     []string
	warn     string
}

func newForm(e env, priorities []redmine.Priority) *formModel {
	f := &formModel{
		subject: newInput("", e.t("form.subject_hint")),
		parent:  newInput("", e.t("form.parent_hint")),
	}
	f.subject.CharLimit = 255
	for _, p := range priorities {
		if p.IsDefault {
			f.priority = &redmine.IDName{ID: p.ID, Name: p.Name}
		}
	}
	f.setFocus(fSubject)
	return f
}

func (f *formModel) fieldCount() int { return fixedFields + len(f.custom) }

func (f *formModel) dirty() bool {
	return strings.TrimSpace(f.subject.Value()) != "" || f.desc != "" || f.project != nil
}

func (f *formModel) setFocus(n int) {
	f.subject.Blur()
	f.parent.Blur()
	for i := range f.custom {
		f.custom[i].input.Blur()
	}
	f.focus = (n + f.fieldCount()) % f.fieldCount()
	switch {
	case f.focus == fSubject:
		f.subject.Focus()
	case f.focus == fParent:
		f.parent.Focus()
	case f.focus >= fixedFields && !f.custom[f.focus-fixedFields].isList():
		f.custom[f.focus-fixedFields].input.Focus()
	}
}

// rebuildCustom recomputes the custom fields for the current project and tracker,
// keeping what was already typed. Required fields come from /custom_fields.json, which
// only administrators can read; without it every enabled field is offered as optional text.
func (f *formModel) rebuildCustom(e env, defs []redmine.CustomFieldDef, defsOK bool) {
	old := map[int]string{}
	for _, c := range f.custom {
		old[c.def.ID] = c.current()
	}
	f.custom, f.warn = nil, ""
	if f.info == nil || f.tracker == nil {
		return
	}
	enabled := map[int]bool{}
	for _, cf := range f.info.IssueCustomFields {
		enabled[cf.ID] = true
	}
	add := func(def redmine.CustomFieldDef, required bool) {
		c := customInput{def: def, asked: required, value: old[def.ID]}
		c.input = newInput("", "")
		if !c.isList() {
			c.input.SetValue(old[def.ID])
		}
		f.custom = append(f.custom, c)
	}
	if defsOK {
		for _, def := range defs {
			if def.CustomizedType == "issue" && def.IsRequired && enabled[def.ID] && def.AppliesToTracker(f.tracker.ID) {
				add(def, true)
			}
		}
		return
	}
	f.warn = e.t("form.custom_unknown")
	for _, cf := range f.info.IssueCustomFields {
		add(redmine.CustomFieldDef{ID: cf.ID, Name: cf.Name}, false)
	}
}

// validate returns what is missing before a request is worth sending.
func (f *formModel) validate(e env) []string {
	var errs []string
	if f.project == nil {
		errs = append(errs, e.t("form.need_project"))
	}
	if f.tracker == nil {
		errs = append(errs, e.t("form.need_tracker"))
	}
	if strings.TrimSpace(f.subject.Value()) == "" {
		errs = append(errs, e.t("form.need_subject"))
	}
	if p := strings.TrimSpace(f.parent.Value()); p != "" {
		if n, err := strconv.Atoi(strings.TrimPrefix(p, "#")); err != nil || n <= 0 {
			errs = append(errs, e.t("form.bad_parent"))
		}
	}
	for _, c := range f.custom {
		if c.asked && c.current() == "" {
			errs = append(errs, e.t("form.need_field", c.def.Name))
		}
	}
	return errs
}

func (f *formModel) newIssue() redmine.NewIssue {
	n := redmine.NewIssue{
		Subject:     strings.TrimSpace(f.subject.Value()),
		Description: f.desc,
	}
	if f.project != nil {
		n.ProjectID = f.project.ID
	}
	if f.tracker != nil {
		n.TrackerID = f.tracker.ID
	}
	if f.priority != nil {
		n.PriorityID = f.priority.ID
	}
	if f.assignee != nil {
		n.AssignedToID = f.assignee.ID
	}
	if p := strings.TrimPrefix(strings.TrimSpace(f.parent.Value()), "#"); p != "" {
		n.ParentID, _ = strconv.Atoi(p) // validate() already rejected anything else
	}
	for _, c := range f.custom {
		if v := c.current(); v != "" {
			var val any = v
			if c.def.Multiple {
				val = []string{v}
			}
			n.CustomFields = append(n.CustomFields, redmine.CustomValue{ID: c.def.ID, Value: val})
		}
	}
	return n
}

// ---- rendering --------------------------------------------------------

func (f *formModel) view(e env) string {
	st := e.st
	inner := max(20, e.w-6)
	row := func(idx int, label, value string) string {
		marker, lab := "  ", st.dim
		if idx == f.focus {
			marker, lab = st.cursor.Render("▸ "), st.accent
		}
		return truncate(marker+lab.Render(padRight(label, 16))+value, inner)
	}
	valueWidth := max(10, inner-20)
	f.subject.SetWidth(valueWidth)
	f.parent.SetWidth(valueWidth)
	for i := range f.custom {
		f.custom[i].input.SetWidth(valueWidth)
	}
	pickerVal := func(n *redmine.IDName, none string) string {
		if n == nil {
			return st.dim.Render(none)
		}
		return st.text.Render(n.Name)
	}

	lines := []string{
		row(fProject, e.t("form.project")+" *", pickerVal(f.project, e.t("form.choose"))),
		row(fTracker, e.t("form.tracker")+" *", pickerVal(f.tracker, e.t("form.choose"))),
		row(fSubject, e.t("form.subject")+" *", inputView(f.subject)),
		row(fDesc, e.t("form.description"), f.descView(e)),
		row(fPriority, e.t("form.priority"), pickerVal(f.priority, e.t("form.choose"))),
		row(fAssignee, e.t("form.assignee"), pickerVal(f.assignee, e.t("assign.unassigned"))),
		row(fParent, e.t("form.parent"), inputView(f.parent)),
	}
	for i, c := range f.custom {
		label := c.def.Name
		if c.asked {
			label += " *"
		}
		val := inputView(c.input)
		if c.isList() {
			val = st.text.Render(c.value)
		}
		lines = append(lines, row(fixedFields+i, label, val))
	}

	body := strings.Join(lines, "\n")
	if f.warn != "" {
		body += "\n\n" + st.dim.Render(lipgloss.NewStyle().Width(inner).Render(f.warn))
	}
	if len(f.errs) > 0 {
		body += "\n\n" + st.danger.Render(lipgloss.NewStyle().Width(inner).Render(strings.Join(f.errs, "\n")))
	}
	return "\n" + st.box.Width(e.w).Render(st.boxTitle.Render(e.t("form.title"))+"\n\n"+body)
}

func (f *formModel) descView(e env) string {
	if strings.TrimSpace(f.desc) == "" {
		return e.st.dim.Render(e.t("form.desc_empty"))
	}
	first, _, _ := strings.Cut(strings.TrimSpace(f.desc), "\n")
	return e.st.text.Render(first)
}

// ---- keys -------------------------------------------------------------

func (m *Model) openForm() tea.Cmd {
	if !m.writable() {
		return nil
	}
	m.form = newForm(m.env(), m.priorities)
	if m.priorities == nil {
		return fetchPrioritiesCmd(m.api)
	}
	return nil
}

func (m *Model) handleFormKey(msg tea.KeyPressMsg) tea.Cmd {
	f := m.form
	e := m.env()
	switch msg.String() {
	case "esc":
		if !f.dirty() {
			m.form = nil
			return nil
		}
		m.modal = &confirmModal{text: e.t("confirm.discard"), onYes: func(m *Model) tea.Cmd { m.form = nil; return nil }}
		return nil
	case "ctrl+s":
		return m.submitForm()
	case "tab", "down":
		f.setFocus(f.focus + 1)
		return nil
	case "shift+tab", "up":
		f.setFocus(f.focus - 1)
		return nil
	case "enter":
		return m.activateField()
	}
	var cmd tea.Cmd
	switch {
	case f.focus == fSubject:
		f.subject, cmd = f.subject.Update(msg)
	case f.focus == fParent:
		f.parent, cmd = f.parent.Update(msg)
	case f.focus >= fixedFields && !f.custom[f.focus-fixedFields].isList():
		c := &f.custom[f.focus-fixedFields]
		c.input, cmd = c.input.Update(msg)
	}
	return cmd
}

// formForward delivers non-key messages (cursor blink) to the focused input.
func (m *Model) formForward(msg tea.Msg) tea.Cmd {
	f := m.form
	var cmd tea.Cmd
	switch {
	case f.focus == fSubject:
		f.subject, cmd = f.subject.Update(msg)
	case f.focus == fParent:
		f.parent, cmd = f.parent.Update(msg)
	case f.focus >= fixedFields && !f.custom[f.focus-fixedFields].isList():
		c := &f.custom[f.focus-fixedFields]
		c.input, cmd = c.input.Update(msg)
	}
	return cmd
}

func (m *Model) activateField() tea.Cmd {
	f := m.form
	e := m.env()
	switch {
	case f.focus == fProject:
		return m.pickProject(e.t("form.project"), nil, false, func(m *Model, p *redmine.IDName) tea.Cmd {
			return m.chooseProject(p)
		})
	case f.focus == fTracker:
		if f.info == nil {
			m.warn(e.t("form.pick_project_first"))
			return nil
		}
		items := make([]pickItem, len(f.info.Trackers))
		for i, t := range f.info.Trackers {
			items[i] = pickItem{label: t.Name, value: t}
		}
		m.modal = newPicker(e, e.t("form.tracker"), items, func(m *Model, it pickItem) tea.Cmd {
			t := it.value.(redmine.IDName)
			f.tracker = &t
			f.rebuildCustom(m.env(), m.customDefs, m.customDefsOK)
			return nil
		})
	case f.focus == fDesc:
		return m.editText(f.desc, e.t("form.description"), func(m *Model, text string) tea.Cmd {
			f.desc = text
			return nil
		})
	case f.focus == fPriority:
		items := make([]pickItem, 0, len(m.priorities))
		for _, p := range m.priorities {
			items = append(items, pickItem{label: p.Name, value: redmine.IDName{ID: p.ID, Name: p.Name}})
		}
		m.modal = newPicker(e, e.t("form.priority"), items, func(m *Model, it pickItem) tea.Cmd {
			p := it.value.(redmine.IDName)
			f.priority = &p
			return nil
		})
	case f.focus == fAssignee:
		return m.pickAssignee()
	case f.focus >= fixedFields && f.custom[f.focus-fixedFields].isList():
		c := &f.custom[f.focus-fixedFields]
		items := make([]pickItem, len(c.def.PossibleValues))
		for i, pv := range c.def.PossibleValues {
			label := pv.Label
			if label == "" {
				label = pv.Value
			}
			items[i] = pickItem{label: label, value: pv.Value}
		}
		m.modal = newPicker(e, c.def.Name, items, func(m *Model, it pickItem) tea.Cmd {
			c.value = it.value.(string)
			return nil
		})
	default:
		f.setFocus(f.focus + 1)
	}
	return nil
}

// chooseProject loads what the form needs for p: its trackers and custom fields,
// and the global custom field definitions the first time.
func (m *Model) chooseProject(p *redmine.IDName) tea.Cmd {
	f := m.form
	if p == nil {
		return nil
	}
	api := m.api
	needDefs := !m.customDefsTried
	type result struct {
		info    *redmine.Project
		defs    []redmine.CustomFieldDef
		defsErr error
	}
	return m.await(m.tr.T("loading.project"), func(ctx context.Context) (any, error) {
		info, err := api.GetProject(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		r := result{info: info}
		if needDefs {
			r.defs, r.defsErr = api.ListCustomFields(ctx)
		}
		return r, nil
	}, func(m *Model, res any, err error) tea.Cmd {
		if err != nil {
			return m.showError(err)
		}
		r := res.(result)
		if needDefs {
			m.setCustomDefs(r.defs, r.defsErr)
		}
		f.project, f.info = p, r.info
		f.assignee = nil
		f.tracker = nil
		if len(r.info.Trackers) > 0 {
			t := r.info.Trackers[0]
			f.tracker = &t
		}
		f.rebuildCustom(m.env(), m.customDefs, m.customDefsOK)
		return nil
	})
}

func (m *Model) pickAssignee() tea.Cmd {
	f := m.form
	e := m.env()
	if f.project == nil {
		m.warn(e.t("form.pick_project_first"))
		return nil
	}
	api, projectID := m.api, f.project.ID
	return m.await(e.t("loading.members"), func(ctx context.Context) (any, error) {
		return api.ListMembers(ctx, projectID)
	}, func(m *Model, res any, err error) tea.Cmd {
		if err != nil {
			return m.showError(err)
		}
		e := m.env()
		items := []pickItem{{label: e.t("assign.unassigned"), value: (*redmine.IDName)(nil)}}
		for _, u := range res.([]redmine.Member) {
			items = append(items, pickItem{label: u.Name, value: &redmine.IDName{ID: u.ID, Name: u.Name}})
		}
		m.modal = newPicker(e, e.t("form.assignee"), items, func(m *Model, it pickItem) tea.Cmd {
			f.assignee = it.value.(*redmine.IDName)
			return nil
		}).withFilter(e)
		return nil
	})
}

func (m *Model) submitForm() tea.Cmd {
	f := m.form
	e := m.env()
	if errs := f.validate(e); len(errs) > 0 {
		f.errs = errs
		return nil
	}
	f.errs = nil
	n := f.newIssue()
	var b strings.Builder
	b.WriteString(e.t("confirm.create", f.project.Name, f.tracker.Name, n.Subject))
	if f.priority != nil {
		b.WriteString("\n" + e.t("form.priority") + ": " + f.priority.Name)
	}
	if f.assignee != nil {
		b.WriteString("\n" + e.t("form.assignee") + ": " + f.assignee.Name)
	}
	for _, c := range f.custom {
		if v := c.current(); v != "" {
			b.WriteString("\n" + c.def.Name + ": " + v)
		}
	}
	m.confirmWrite(b.String(), 0, func(ctx context.Context, a API) (*redmine.Issue, error) {
		return a.CreateIssue(ctx, n)
	})
	return nil
}
