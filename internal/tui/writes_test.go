package tui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

// detailModelOnServer opens issue 123 on a fake Redmine that serves it with the given
// allowed statuses, then returns the model and the write recorder.
func detailModelOnServer(t *testing.T, mutate func(is *redmine.Issue), onWrite func(method, path string) (int, string), opts Options, clientOpts ...redmine.Option) (*Model, *writeRecorder) {
	t.Helper()
	is := detailIssue()
	if mutate != nil {
		mutate(is)
	}
	client, rec := newRedmine(t, map[string]string{
		"/issues/123.json":                    issueJSON(t, is),
		"/issue_statuses.json":                `{"issue_statuses":[{"id":1,"name":"New","is_default":true},{"id":2,"name":"In progress"},{"id":3,"name":"Resolved"},{"id":5,"name":"Closed","is_closed":true},{"id":6,"name":"Rejected","is_closed":true}]}`,
		"/enumerations/issue_priorities.json": `{"issue_priorities":[{"id":1,"name":"Low"},{"id":2,"name":"Normal","is_default":true},{"id":3,"name":"High"}]}`,
		"/users/current.json":                 `{"user":{"id":5,"firstname":"Jane","lastname":"Doe"}}`,
		"/projects/1/memberships.json":        `{"memberships":[{"user":{"id":5,"name":"Jane Doe"}},{"user":{"id":31,"name":"John Roe"}},{"group":{"id":9,"name":"Devs"}}],"total_count":3}`,
		"/projects.json":                      `{"projects":[{"id":1,"name":"Website"}],"total_count":1}`,
		"/projects/1.json":                    `{"project":{"id":1,"name":"Website","trackers":[{"id":1,"name":"Bug"},{"id":2,"name":"Feature"}],"issue_custom_fields":[{"id":4,"name":"Team"}]}}`,
		"/issues.json":                        `{"issues":[],"total_count":0}`,
		"/custom_fields.json":                 `{"custom_fields":[{"id":4,"name":"Team","customized_type":"issue","field_format":"list","is_required":true,"possible_values":[{"value":"Backend","label":"Backend"},{"value":"Frontend","label":"Frontend"}],"trackers":[{"id":1,"name":"Bug"}]}]}`,
	}, onWrite, clientOpts...)
	opts.StartID = 123
	m := newTestModel(client, opts)
	settle(t, m, m.Init())
	if m.stack[0].issue == nil {
		t.Fatal("the issue did not load")
	}
	return m, rec
}

func view(m *Model) string { return ansi.Strip(m.render()) }

func TestStatusChangeShowsOnlyAllowedStatusesAndSendsOnConfirm(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "S")
	out := view(m)
	containsAll(t, out, "New status for #123", "Resolved", "Closed")
	for _, forbidden := range []string{"Rejected", "New\n"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a status the workflow does not allow is offered: %q", forbidden)
		}
	}

	press(t, m, "down", "down", "enter") // Closed
	containsAll(t, view(m), "Note")      // optional note
	press(t, m, "enter")
	containsAll(t, view(m), "Change #123 from “In progress” to “Closed”? [y/N]")
	if n := len(rec.all()); n != 0 {
		t.Fatalf("nothing may be written before the confirmation, got %d writes", n)
	}

	press(t, m, "y")
	writes := rec.all()
	if len(writes) != 1 {
		t.Fatalf("got %d writes, want 1", len(writes))
	}
	if w := writes[0]; w.method != http.MethodPut || w.path != "/issues/123.json" || w.body != `{"issue":{"status_id":5}}` {
		t.Errorf("write = %+v", w)
	}
	containsAll(t, view(m), "#123 updated")
}

func TestStatusChangeWithNote(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "S", "enter")
	typeText(t, m, "fixed it")
	press(t, m, "enter")
	containsAll(t, view(m), "Change #123 from “In progress” to “Resolved”? [y/N]", "Note: fixed it")
	press(t, m, "y")
	if got := rec.all(); len(got) != 1 || got[0].body != `{"issue":{"notes":"fixed it","status_id":3}}` {
		t.Errorf("writes = %+v", got)
	}
}

func TestEveryWriteIsCancelledByDefault(t *testing.T) {
	cancels := []string{"enter", "esc", "n", "x", " "}
	for _, c := range cancels {
		t.Run("cancel with "+c, func(t *testing.T) {
			m, rec := detailModelOnServer(t, nil, nil, Options{})
			press(t, m, "D", "down", "enter") // 40% -> 50%
			containsAll(t, view(m), "[y/N]")
			press(t, m, c)
			if n := len(rec.all()); n != 0 {
				t.Errorf("%q wrote %d times; the default must cancel", c, n)
			}
			if m.modal != nil {
				t.Error("the confirmation should close")
			}
		})
	}
}

func TestStatusFallbackWhenWorkflowIsNotReported(t *testing.T) {
	m, _ := detailModelOnServer(t, func(is *redmine.Issue) { is.AllowedStatuses = nil }, nil, Options{})
	press(t, m, "S")
	containsAll(t, view(m), "does not report allowed statuses", "Rejected", "Resolved", "Closed", "New")
}

func TestQuickCloseWithASingleClosedStatus(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "c")
	containsAll(t, view(m), "Note") // straight to the note: only "Closed" is allowed and closed
	press(t, m, "enter")
	containsAll(t, view(m), "Close #123: status “In progress” to “Closed”, done 40% to 100%? [y/N]")
	press(t, m, "y")
	if got := rec.all(); len(got) != 1 || got[0].body != `{"issue":{"done_ratio":100,"status_id":5}}` {
		t.Errorf("writes = %+v", got)
	}
}

func TestQuickCloseAsksWhenSeveralClosedStatusesAreAllowed(t *testing.T) {
	m, rec := detailModelOnServer(t, func(is *redmine.Issue) {
		is.AllowedStatuses = append(is.AllowedStatuses, redmine.Status{ID: 6, Name: "Rejected", IsClosed: true})
	}, nil, Options{})
	press(t, m, "c")
	containsAll(t, view(m), "Closed status for #123", "Closed", "Rejected")
	press(t, m, "down", "enter", "enter", "y")
	if got := rec.all(); len(got) != 1 || got[0].body != `{"issue":{"done_ratio":100,"status_id":6}}` {
		t.Errorf("writes = %+v", got)
	}
}

func TestQuickCloseWithNoClosedStatusAllowed(t *testing.T) {
	m, rec := detailModelOnServer(t, func(is *redmine.Issue) {
		is.AllowedStatuses = []redmine.Status{{ID: 3, Name: "Resolved"}}
	}, nil, Options{})
	press(t, m, "c")
	if m.modal != nil || !m.flash.bad {
		t.Error("expected a warning and no dialog")
	}
	if len(rec.all()) != 0 {
		t.Error("nothing may be written")
	}
}

func TestDoneRatio(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "D")
	containsAll(t, view(m), "Percent done for #123", "0%", "100%")
	press(t, m, "down", "down", "enter") // from 40% the cursor starts on 40%, two down = 60%
	containsAll(t, view(m), "Change done ratio of #123 from 40% to 60%? [y/N]")
	press(t, m, "y")
	if got := rec.all(); len(got) != 1 || got[0].body != `{"issue":{"done_ratio":60}}` {
		t.Errorf("writes = %+v", got)
	}
}

func TestAssign(t *testing.T) {
	tests := []struct {
		name    string
		keys    []string
		confirm string
		body    string
	}{
		{name: "to a member", keys: []string{"A", "down", "down", "enter"}, confirm: "Change assignee of #123 from “John Roe” to “John Roe”? [y/N]", body: `{"issue":{"assigned_to_id":31}}`},
		{name: "to me", keys: []string{"A", "down", "enter"}, confirm: "Change assignee of #123 from “John Roe” to “Jane Doe”? [y/N]", body: `{"issue":{"assigned_to_id":5}}`},
		{name: "to nobody", keys: []string{"A", "enter"}, confirm: "Change assignee of #123 from “John Roe” to “Unassigned”? [y/N]", body: `{"issue":{"assigned_to_id":null}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, rec := detailModelOnServer(t, nil, nil, Options{})
			press(t, m, tt.keys...)
			containsAll(t, view(m), tt.confirm)
			press(t, m, "y")
			if got := rec.all(); len(got) != 1 || got[0].body != tt.body || got[0].method != http.MethodPut {
				t.Errorf("writes = %+v, want body %s", got, tt.body)
			}
		})
	}
}

func TestAssignListsUsersNotGroups(t *testing.T) {
	m, _ := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "A")
	out := view(m)
	containsAll(t, out, "Unassigned", "Me (Jane Doe)", "John Roe")
	if strings.Contains(out, "Devs") {
		t.Error("groups must not be offered")
	}
}

func TestAddNoteWithTheInAppTextArea(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "n")
	ta, ok := m.modal.(*textareaModal)
	if !ok {
		t.Fatalf("without an editor the text area must open, got %T", m.modal)
	}
	ta.area.SetValue("Looks fine.\nShipping it.")
	press(t, m, "ctrl+s")
	containsAll(t, view(m), "Add this note to #123? [y/N]", "Looks fine.", "Shipping it.")
	press(t, m, "y")
	if got := rec.all(); len(got) != 1 || got[0].body != `{"issue":{"notes":"Looks fine.\nShipping it."}}` {
		t.Errorf("writes = %+v", got)
	}
}

func TestEmptyNoteIsCancelled(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "n")
	m.modal.(*textareaModal).area.SetValue("   \n ")
	press(t, m, "ctrl+s")
	if m.modal != nil || len(rec.all()) != 0 {
		t.Error("an empty note must cancel without a confirmation or a write")
	}
	containsAll(t, view(m), "Empty note")
}

func TestEditorCommandResolution(t *testing.T) {
	tests := []struct {
		name   string
		editor string
		exists bool
		want   []string
		ok     bool
	}{
		{name: "plain editor", editor: "nano", exists: true, want: []string{"nano"}, ok: true},
		{name: "editor with arguments", editor: "code --wait", exists: true, want: []string{"code", "--wait"}, ok: true},
		{name: "empty falls back to vi", editor: "", exists: true, want: []string{"vi"}, ok: true},
		{name: "missing program", editor: "nope", exists: false, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(&fakeAPI{}, Options{Editor: tt.editor})
			m.lookPath = func(string) (string, error) {
				if tt.exists {
					return "/bin/x", nil
				}
				return "", http.ErrMissingFile
			}
			got, ok := m.editorArgs()
			if ok != tt.ok || strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("editorArgs() = %v, %v; want %v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestReadAndRemoveAlwaysDeletesTheTempFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/note.md"
	if err := writeFile(path, "  text \n"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, err := readAndRemove(path)
	if err != nil || got != "text" {
		t.Errorf("readAndRemove() = %q, %v", got, err)
	}
	if fileExists(path) {
		t.Error("the temporary file must be deleted")
	}
	if _, err := readAndRemove(path); err == nil {
		t.Error("reading a missing file must report the error")
	}
}

func TestWriteErrorsAreShownNotRetried(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, func(string, string) (int, string) {
		return http.StatusForbidden, `{"errors":["You are not allowed to do this"]}`
	}, Options{})
	press(t, m, "D", "down", "enter", "y")
	containsAll(t, view(m), "Error", "No permission", "You are not allowed to do this")
	if n := len(rec.all()); n != 1 {
		t.Errorf("sent %d writes, want exactly 1", n)
	}
}

func TestUnknownOutcomeMessage(t *testing.T) {
	tests := []struct {
		name string
		lang i18n.Lang
		want string
	}{
		{name: "english", lang: i18n.EN, want: "It is not known whether the change was applied; reload to verify"},
		{name: "spanish", lang: i18n.ES, want: "No se sabe si se aplicó; recargá para verificar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			m, rec := detailModelOnServer(t, nil, func(string, string) (int, string) {
				<-release
				return http.StatusNoContent, ""
			}, Options{Lang: tt.lang})
			// Cleanups run last-in first-out: the handler must be released before the server closes.
			t.Cleanup(func() { close(release) })
			// A client with a short timeout makes the write time out.
			m.api = withShortTimeout(t, m.api)
			ks := []string{"D", "down", "enter", "y"}
			if tt.lang == i18n.ES {
				ks[3] = "s"
			}
			press(t, m, ks...)
			containsAll(t, view(m), tt.want)
			time.Sleep(10 * time.Millisecond)
			if n := len(rec.all()); n > 1 {
				t.Errorf("the write was sent %d times; it must not be retried", n)
			}
		})
	}
}

func TestValidationErrorOfCreateIsShownAndFormKept(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, func(method, path string) (int, string) {
		return http.StatusUnprocessableEntity, `{"errors":["Subject is too short","Team cannot be blank"]}`
	}, Options{})
	press(t, m, "esc") // back to the list
	fillForm(t, m)
	fillCustom(t, m)
	press(t, m, "ctrl+s", "y")
	if m.form == nil {
		t.Fatal("the form must survive a rejected submission")
	}
	if m.form.subject.Value() != "Crash on start" || m.form.project == nil {
		t.Error("what was typed must not be lost")
	}
	containsAll(t, view(m), "Redmine rejected the data (422)", "Subject is too short", "Team cannot be blank")
	if n := len(rec.all()); n != 1 {
		t.Errorf("sent %d writes, want 1", n)
	}
	press(t, m, "enter") // dismiss the dialog
	containsAll(t, view(m), "Subject is too short", "Crash on start")
}

// fillForm opens the creation form from the list and fills the required fields.
func fillForm(t *testing.T, m *Model) {
	t.Helper()
	press(t, m, "N")
	if m.form == nil {
		t.Fatal("N should open the form")
	}
	m.form.setFocus(fProject)
	press(t, m, "enter", "enter") // project picker, first project
	if m.form.project == nil || m.form.info == nil {
		t.Fatal("the project did not load")
	}
	m.form.setFocus(fSubject)
	typeText(t, m, "Crash on start")
}

// fillCustom chooses "Frontend" for the first required custom field.
func fillCustom(t *testing.T, m *Model) {
	t.Helper()
	m.form.setFocus(fixedFields)
	press(t, m, "enter", "down", "enter")
	m.form.setFocus(fSubject)
}

func TestCreateIssueFlow(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, func(method, path string) (int, string) {
		return http.StatusCreated, `{"issue":{"id":99,"subject":"Crash on start"}}`
	}, Options{})
	press(t, m, "esc")
	fillForm(t, m)

	out := view(m)
	containsAll(t, out, "New issue", "Project *", "Tracker *", "Subject *", "Website", "Bug", "Normal", "Team *")
	if f := m.form; len(f.custom) != 1 || !f.custom[0].asked {
		t.Fatalf("the required custom field must be offered: %+v", f.custom)
	}

	press(t, m, "ctrl+s")
	containsAll(t, view(m), "Team")
	if len(rec.all()) != 0 {
		t.Fatal("a missing required field must stop the submission before any request")
	}
	fillCustom(t, m)

	press(t, m, "ctrl+s")
	containsAll(t, view(m), "Create an issue in “Website” (Bug) titled “Crash on start”? [y/N]", "Normal", "Team: Frontend")
	if len(rec.all()) != 0 {
		t.Fatal("nothing may be written before the confirmation")
	}
	press(t, m, "y")
	writes := rec.all()
	if len(writes) != 1 {
		t.Fatalf("got %d writes, want 1", len(writes))
	}
	if w := writes[0]; w.method != http.MethodPost || w.path != "/issues.json" ||
		w.body != `{"issue":{"custom_fields":[{"id":4,"value":"Frontend"}],"priority_id":2,"project_id":1,"subject":"Crash on start","tracker_id":1}}` {
		t.Errorf("write = %+v", w)
	}
	if m.form != nil {
		t.Error("the form closes after a successful creation")
	}
	if a := m.active(); a == nil || a.id != 99 {
		t.Error("the new issue must open")
	}
	containsAll(t, view(m), "#99 created")
}

func TestFormRender(t *testing.T) {
	m, _ := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "esc")
	press(t, m, "N")
	for _, width := range []int{60, 100} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		out := view(m)
		containsAll(t, out, "New issue", "Project *", "Tracker *", "Subject *", "Description", "Priority", "Assignee", "Parent issue", "(enter to choose)", "Normal")
		for i, line := range strings.Split(out, "\n") {
			if w := ansiWidth(line); w > width {
				t.Errorf("width %d: line %d is %d cells wide: %q", width, i, w, line)
			}
		}
	}
}

func TestConfirmationRender(t *testing.T) {
	m, _ := detailModelOnServer(t, nil, nil, Options{})
	press(t, m, "D", "down", "enter")
	for _, width := range []int{70, 100} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		out := view(m)
		containsAll(t, out, "Confirm", "Change done ratio of #123 from 40% to 50%? [y/N]", "y confirms · any other key cancels")
		for i, line := range strings.Split(out, "\n") {
			if w := ansiWidth(line); w > width {
				t.Errorf("width %d: line %d is %d cells wide: %q", width, i, w, line)
			}
		}
	}
}

func TestSpanishConfirmation(t *testing.T) {
	m, rec := detailModelOnServer(t, nil, nil, Options{Lang: i18n.ES})
	press(t, m, "S", "down", "down", "enter", "enter")
	containsAll(t, view(m), "¿Cambiar #123 de «In progress» a «Closed»? [s/N]")
	press(t, m, "y") // "y" is not the Spanish yes key
	if len(rec.all()) != 0 {
		t.Error("only the language's yes key may confirm")
	}
	press(t, m, "S", "down", "down", "enter", "enter", "s")
	if len(rec.all()) != 1 {
		t.Errorf("s must confirm in Spanish, got %d writes", len(rec.all()))
	}
}

// ---- read-only --------------------------------------------------------

func TestReadOnlyNeverSendsAWrite(t *testing.T) {
	var onWrite = func(method, path string) (int, string) {
		t.Errorf("a write reached the server in read-only mode: %s %s", method, path)
		return http.StatusNoContent, ""
	}
	// Both layers on: the UI refuses, and the client would refuse too.
	m, rec := detailModelOnServer(t, nil, onWrite, Options{ReadOnly: true}, redmine.WithReadOnly())
	for _, k := range []string{"S", "n", "D", "A", "c"} {
		t.Run("detail "+k, func(t *testing.T) {
			press(t, m, k)
			if m.modal != nil {
				t.Errorf("%s opened a dialog in read-only mode", k)
			}
			if !m.flash.bad || !strings.Contains(m.flash.text, "Read-only mode") {
				t.Errorf("%s must say that writes are disabled, flash = %+v", k, m.flash)
			}
		})
	}
	press(t, m, "esc", "N")
	if m.form != nil {
		t.Error("N opened the creation form in read-only mode")
	}
	containsAll(t, view(m), "READ ONLY")
	if n := len(rec.all()); n != 0 {
		t.Errorf("%d writes reached the server", n)
	}
}

func TestReadOnlyClientAloneStillBlocksWrites(t *testing.T) {
	// Even if the UI let a write through, the read-only client must not send it.
	m, rec := detailModelOnServer(t, nil, func(method, path string) (int, string) {
		t.Errorf("a write reached the server: %s %s", method, path)
		return http.StatusNoContent, ""
	}, Options{}, redmine.WithReadOnly())
	press(t, m, "D", "down", "enter", "y")
	containsAll(t, view(m), "read-only mode")
	if n := len(rec.all()); n != 0 {
		t.Errorf("%d writes reached the server", n)
	}
}
