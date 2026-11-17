package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

const notePreviewLines = 6

// writable reports whether writes are enabled; in read-only mode it says so and returns false.
func (m *Model) writable() bool {
	if m.opts.ReadOnly {
		m.warn(m.tr.T("readonly.refused"))
		return false
	}
	return true
}

// confirmWrite shows the exact change and, only on an explicit yes, runs do.
// Cancelling, or any other key, writes nothing.
func (m *Model) confirmWrite(text string, issueID int, do func(ctx context.Context, a API) (*redmine.Issue, error)) {
	m.modal = &confirmModal{text: text, onYes: func(m *Model) tea.Cmd {
		if !m.writable() {
			return nil
		}
		m.writing = true
		api := m.api
		send := func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			defer cancel()
			is, err := do(ctx, api)
			return writeDoneMsg{issueID: issueID, created: is, err: err}
		}
		return tea.Batch(send, m.spin.Tick)
	}}
}

func (m *Model) confirmUpdate(d *detailModel, text string, u redmine.IssueUpdate) {
	id := d.id
	m.confirmWrite(text, id, func(ctx context.Context, a API) (*redmine.Issue, error) {
		return nil, a.UpdateIssue(ctx, id, u)
	})
}

// onWriteDone shows the outcome of a write. A failed write is never retried here.
func (m *Model) onWriteDone(msg writeDoneMsg) tea.Cmd {
	m.writing = false
	e := m.env()
	if msg.err != nil {
		var api *redmine.APIError
		if m.form != nil && errors.As(msg.err, &api) && api.Status == 422 {
			m.form.errs = api.Messages
		}
		return m.showError(msg.err)
	}
	cmds := []tea.Cmd{m.reloadList()}
	if msg.issueID == 0 && msg.created != nil {
		m.form = nil
		m.notify(e.t("notice.created", msg.created.ID))
		cmds = append(cmds, m.openIssue(msg.created.ID))
		return tea.Batch(cmds...)
	}
	m.notify(e.t("notice.updated", msg.issueID))
	for _, d := range m.stack {
		if d.id == msg.issueID {
			d.loading = true
			cmds = append(cmds, fetchIssueCmd(m.api, d.uid, d.id))
		}
	}
	return tea.Batch(append(cmds, m.spin.Tick)...)
}

// showError opens a dialog with a readable explanation of err.
func (m *Model) showError(err error) tea.Cmd {
	e := m.env()
	title := e.t("error.title")
	var unknown *redmine.OutcomeUnknownError
	if errors.As(err, &unknown) {
		title = e.t("error.unknown_title")
	}
	m.modal = &messageModal{title: title, text: friendlyError(e, err), danger: true}
	return nil
}

// askNote asks for an optional one-line note and then runs next with it.
func (m *Model) askNote(next func(m *Model, note string) tea.Cmd) {
	e := m.env()
	in := newInput("> ", e.t("note.placeholder"))
	in.Focus()
	m.modal = &inputModal{title: e.t("note.title"), input: in, hint: e.t("note.hint"),
		onSubmit: func(m *Model, v string) (tea.Cmd, string) { return next(m, v), "" }}
}

func withNote(e env, text, note string) string {
	if note == "" {
		return text
	}
	return text + "\n\n" + e.t("confirm.note", note)
}

// ---- status -----------------------------------------------------------

// statusChoices returns the statuses the workflow allows for the issue, or every status
// with a warning when the instance does not report them (Redmine before 5.0).
func (m *Model) statusChoices(is *redmine.Issue) (choices []redmine.Status, warning string) {
	if is.AllowedStatuses != nil {
		out := make([]redmine.Status, len(is.AllowedStatuses))
		for i, s := range is.AllowedStatuses {
			s.IsClosed = s.IsClosed || m.statuses[s.ID].IsClosed
			out[i] = s
		}
		return out, ""
	}
	return m.statusList, m.tr.T("status.no_workflow")
}

func (m *Model) startStatus(d *detailModel) tea.Cmd {
	if !m.writable() || d.issue == nil {
		return nil
	}
	e := m.env()
	choices, warning := m.statusChoices(d.issue)
	var items []pickItem
	for _, s := range choices {
		if s.ID != d.issue.Status.ID {
			items = append(items, pickItem{label: s.Name, value: s})
		}
	}
	if len(items) == 0 {
		m.warn(e.t("status.none_allowed"))
		return nil
	}
	p := newPicker(e, e.t("status.pick", d.id), items, func(m *Model, it pickItem) tea.Cmd {
		to := it.value.(redmine.Status)
		m.askNote(func(m *Model, note string) tea.Cmd {
			text := withNote(m.env(), m.tr.T("confirm.status", d.id, d.issue.Status.Name, to.Name), note)
			m.confirmUpdate(d, text, redmine.IssueUpdate{StatusID: &to.ID, Notes: note})
			return nil
		})
		return nil
	})
	p.note = warning
	m.modal = p
	return nil
}

// startClose moves the issue to a closed status among the allowed ones and sets 100%.
func (m *Model) startClose(d *detailModel) tea.Cmd {
	if !m.writable() || d.issue == nil {
		return nil
	}
	e := m.env()
	choices, warning := m.statusChoices(d.issue)
	var closed []pickItem
	for _, s := range choices {
		if s.IsClosed && s.ID != d.issue.Status.ID {
			closed = append(closed, pickItem{label: s.Name, value: s})
		}
	}
	proceed := func(m *Model, to redmine.Status) tea.Cmd {
		m.askNote(func(m *Model, note string) tea.Cmd {
			done := 100
			text := withNote(m.env(), m.tr.T("confirm.close", d.id, d.issue.Status.Name, to.Name, d.issue.DoneRatio), note)
			m.confirmUpdate(d, text, redmine.IssueUpdate{StatusID: &to.ID, DoneRatio: &done, Notes: note})
			return nil
		})
		return nil
	}
	switch len(closed) {
	case 0:
		m.warn(e.t("close.none_allowed"))
		return nil
	case 1:
		return proceed(m, closed[0].value.(redmine.Status))
	}
	p := newPicker(e, e.t("close.pick", d.id), closed, func(m *Model, it pickItem) tea.Cmd {
		return proceed(m, it.value.(redmine.Status))
	})
	p.note = warning
	m.modal = p
	return nil
}

// ---- done ratio -------------------------------------------------------

func (m *Model) startDone(d *detailModel) tea.Cmd {
	if !m.writable() || d.issue == nil {
		return nil
	}
	e := m.env()
	var items []pickItem
	cursor := 0
	for v := 0; v <= 100; v += 10 {
		items = append(items, pickItem{label: strconv.Itoa(v) + "%", value: v})
		if v <= d.issue.DoneRatio {
			cursor = len(items) - 1
		}
	}
	p := newPicker(e, e.t("done.pick", d.id), items, func(m *Model, it pickItem) tea.Cmd {
		to := it.value.(int)
		text := m.tr.T("confirm.done", d.id, d.issue.DoneRatio, to)
		m.confirmUpdate(d, text, redmine.IssueUpdate{DoneRatio: &to})
		return nil
	})
	p.cursor = cursor
	m.modal = p
	return nil
}

// ---- assignee ---------------------------------------------------------

type assignOptions struct {
	me      redmine.Member
	members []redmine.Member
}

func (m *Model) startAssign(d *detailModel) tea.Cmd {
	if !m.writable() || d.issue == nil {
		return nil
	}
	api, projectID, knownMe := m.api, d.issue.Project.ID, m.me
	return m.await(m.tr.T("loading.members"), func(ctx context.Context) (any, error) {
		opts := assignOptions{me: knownMe}
		if opts.me.ID == 0 {
			me, err := api.CurrentUser(ctx)
			if err != nil {
				return nil, err
			}
			opts.me = me
		}
		members, err := api.ListMembers(ctx, projectID)
		if err != nil {
			return nil, err
		}
		opts.members = members
		return opts, nil
	}, func(m *Model, res any, err error) tea.Cmd {
		if err != nil {
			return m.showError(err)
		}
		opts := res.(assignOptions)
		m.me = opts.me
		e := m.env()
		items := []pickItem{{label: e.t("assign.unassigned"), value: redmine.Member{}}}
		if opts.me.ID != 0 {
			items = append(items, pickItem{label: e.t("user.me_named", opts.me.Name), value: opts.me})
		}
		for _, u := range opts.members {
			if u.ID != opts.me.ID {
				items = append(items, pickItem{label: u.Name, value: u})
			}
		}
		p := newPicker(e, e.t("assign.pick", d.id), items, func(m *Model, it pickItem) tea.Cmd {
			to := it.value.(redmine.Member)
			from := e.t("assign.unassigned")
			if d.issue.AssignedTo != nil {
				from = d.issue.AssignedTo.Name
			}
			target := e.t("assign.unassigned")
			if to.ID != 0 {
				target = to.Name
			}
			id := to.ID
			m.confirmUpdate(d, m.tr.T("confirm.assign", d.id, from, target), redmine.IssueUpdate{AssignedToID: &id})
			return nil
		}).withFilter(e)
		m.modal = p
		return nil
	})
}

// ---- notes ------------------------------------------------------------

func (m *Model) startNote(d *detailModel) tea.Cmd {
	if !m.writable() || d.issue == nil {
		return nil
	}
	return m.editText("", m.tr.T("note.add_title", d.id), func(m *Model, text string) tea.Cmd {
		if text == "" {
			m.warn(m.tr.T("note.empty"))
			return nil
		}
		e := m.env()
		m.confirmUpdate(d, e.t("confirm.note_add", d.id)+"\n\n"+previewLines(text, notePreviewLines), redmine.IssueUpdate{Notes: text})
		return nil
	})
}

func previewLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

// editorArgs splits the configured editor command and reports whether its program exists.
func (m *Model) editorArgs() ([]string, bool) {
	args := strings.Fields(m.opts.Editor)
	if len(args) == 0 {
		args = []string{"vi"}
	}
	if _, err := m.lookPath(args[0]); err != nil {
		return nil, false
	}
	return args, true
}

// editText gets multi-line text from the user: through the external editor when there is
// one, otherwise through an in-app text area. then runs with the trimmed text, empty if cancelled.
func (m *Model) editText(initial, title string, then func(m *Model, text string) tea.Cmd) tea.Cmd {
	args, ok := m.editorArgs()
	if !ok {
		ta := newTextarea(m.env(), title, then)
		ta.area.SetValue(initial)
		m.modal = ta
		return nil
	}
	f, err := os.CreateTemp("", "redmine-tui-*.md") // created with mode 0600
	if err != nil {
		return m.showError(err)
	}
	path := f.Name()
	_, werr := f.WriteString(initial)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		removeQuietly(path)
		return m.showError(err)
	}
	cmd := exec.Command(args[0], append(args[1:], path)...)
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		text, readErr := readAndRemove(path)
		return editorDoneMsg{text: text, err: errors.Join(runErr, readErr), then: then}
	})
}

// readAndRemove returns the trimmed content of path and always deletes the file.
func readAndRemove(path string) (string, error) {
	data, err := os.ReadFile(path)
	removeQuietly(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// removeQuietly deletes a temporary file on a best-effort basis: a leftover empty file in
// the temp directory has nobody to be reported to.
func removeQuietly(path string) {
	os.Remove(path)
}
