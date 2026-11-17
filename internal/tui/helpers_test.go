package tui

import (
	"charm.land/lipgloss/v2"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

var fixedNow = time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)

func sp(s string) *string { return &s }

// fakeAPI answers from memory and records the list queries it receives.
type fakeAPI struct {
	mu          sync.Mutex
	queries     []redmine.ListQuery
	offsets     []int
	issues      []redmine.Issue
	total       int
	issue       *redmine.Issue
	users       func(name string) ([]redmine.Member, error)
	projects    []redmine.Project
	members     map[int][]redmine.Member
	updateCalls int
	createCalls int
}

func (f *fakeAPI) ListIssues(_ context.Context, q redmine.ListQuery, offset int) (redmine.IssuePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.offsets = append(f.offsets, offset)
	total := f.total
	if total == 0 {
		total = len(f.issues)
	}
	return redmine.IssuePage{Issues: f.issues, Total: total}, nil
}
func (f *fakeAPI) GetIssue(context.Context, int) (*redmine.Issue, error)  { return f.issue, nil }
func (f *fakeAPI) ListStatuses(context.Context) ([]redmine.Status, error) { return testStatuses(), nil }
func (f *fakeAPI) ListPriorities(context.Context) ([]redmine.Priority, error) {
	return testPriorities(), nil
}
func (f *fakeAPI) ListTrackers(context.Context) ([]redmine.IDName, error) {
	return []redmine.IDName{{ID: 1, Name: "Bug"}, {ID: 2, Name: "Feature"}}, nil
}
func (f *fakeAPI) SearchIssues(context.Context, string) ([]redmine.SearchResult, error) {
	return nil, nil
}
func (f *fakeAPI) ListProjects(context.Context) ([]redmine.Project, error) { return f.projects, nil }
func (f *fakeAPI) GetProject(context.Context, int) (*redmine.Project, error) {
	return &redmine.Project{ID: 1, Name: "Website", Trackers: []redmine.IDName{{ID: 1, Name: "Bug"}}}, nil
}
func (f *fakeAPI) ListMembers(_ context.Context, id int) ([]redmine.Member, error) {
	return f.members[id], nil
}
func (f *fakeAPI) ListUsers(_ context.Context, name string) ([]redmine.Member, error) {
	if f.users != nil {
		return f.users(name)
	}
	return nil, &redmine.APIError{Status: 403}
}
func (f *fakeAPI) CurrentUser(context.Context) (redmine.Member, error) {
	return redmine.Member{ID: 5, Name: "Jane Doe"}, nil
}
func (f *fakeAPI) ListCustomFields(context.Context) ([]redmine.CustomFieldDef, error) {
	return nil, &redmine.APIError{Status: 403}
}
func (f *fakeAPI) UpdateIssue(context.Context, int, redmine.IssueUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	return nil
}
func (f *fakeAPI) CreateIssue(context.Context, redmine.NewIssue) (*redmine.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	return &redmine.Issue{ID: 99}, nil
}

func testStatuses() []redmine.Status {
	return []redmine.Status{
		{ID: 1, Name: "New", IsDefault: true},
		{ID: 2, Name: "In progress"},
		{ID: 3, Name: "Resolved"},
		{ID: 5, Name: "Closed", IsClosed: true},
		{ID: 6, Name: "Rejected", IsClosed: true},
	}
}

func testPriorities() []redmine.Priority {
	return []redmine.Priority{{ID: 1, Name: "Low"}, {ID: 2, Name: "Normal", IsDefault: true}, {ID: 3, Name: "High"}, {ID: 4, Name: "Urgent"}}
}

func testIssue(id int, subject string, status redmine.Status) redmine.Issue {
	return redmine.Issue{
		ID: id, Subject: subject,
		Project:    redmine.IDName{ID: 1, Name: "Website"},
		Tracker:    redmine.IDName{ID: 1, Name: "Bug"},
		Status:     status,
		Priority:   redmine.IDName{ID: 2, Name: "Normal"},
		Author:     redmine.IDName{ID: 25, Name: "Jane Doe"},
		AssignedTo: &redmine.IDName{ID: 31, Name: "John Roe"},
		StartDate:  sp("2026-11-01"), DueDate: sp("2026-11-20"),
		DoneRatio: 40,
		CreatedOn: fixedNow.Add(-72 * time.Hour), UpdatedOn: fixedNow.Add(-2 * time.Hour),
		Description: "Steps to **reproduce** the crash:\n\n- open the page\n- click save",
	}
}

func detailIssue() *redmine.Issue {
	is := testIssue(123, "Crash when saving", redmine.Status{ID: 2, Name: "In progress"})
	is.Description = "Needs to be **reviewed**:\n\n- one\n- two\n\n[link](https://example.com)"
	is.CustomFields = []redmine.CustomField{
		{ID: 4, Name: "Team", Value: []byte(`"Backend"`)},
		{ID: 5, Name: "Module", Value: []byte(`""`)},
	}
	is.Journals = []redmine.Journal{{
		ID: 1, User: redmine.IDName{ID: 25, Name: "Jane Doe"}, Notes: "Done, **closing**.", CreatedOn: fixedNow,
		Details: []redmine.JournalDetail{{Property: "attr", Name: "status_id", OldValue: sp("1"), NewValue: sp("2")}},
	}}
	is.Children = []redmine.ChildIssue{{ID: 124, Tracker: redmine.IDName{Name: "Bug"}, Subject: "Child one"}}
	is.Relations = []redmine.Relation{{ID: 1, IssueID: 123, IssueToID: 9, RelationType: "relates"}}
	is.AllowedStatuses = []redmine.Status{{ID: 2, Name: "In progress"}, {ID: 3, Name: "Resolved"}, {ID: 5, Name: "Closed", IsClosed: true}}
	return &is
}

// key builds the key press the terminal would deliver for a key name.
func key(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	r := []rune(name)
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

func newTestModel(api API, opts Options) *Model {
	if opts.Lang == "" {
		opts.Lang = i18n.EN
	}
	if opts.BaseURL == "" {
		opts.BaseURL = "https://redmine.example.com"
	}
	m := New(api, opts)
	m.now = func() time.Time { return fixedNow }
	m.lookPath = func(string) (string, error) { return "", io.EOF } // no editor: the in-app text area is used
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(statusesMsg{statuses: testStatuses()})
	m.Update(prioritiesMsg{priorities: testPriorities()})
	m.Update(meMsg{me: redmine.Member{ID: 5, Name: "Jane Doe"}})
	return m
}

// settle runs a command and every command it spawns, feeding the messages back into the
// model. The spinner tick is skipped: it only schedules more ticks.
func settle(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for steps := 0; len(pending) > 0 && steps < 50; steps++ {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		msg := runCmd(c)
		switch msg := msg.(type) {
		case nil, spinner.TickMsg:
		case tea.BatchMsg:
			pending = append(pending, msg...)
		default:
			_, next := m.Update(msg)
			pending = append(pending, next)
		}
	}
}

// runCmd runs a command with a deadline, so a command that only waits (a tick) is dropped.
func runCmd(c tea.Cmd) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- c() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(150 * time.Millisecond):
		return nil
	}
}

// press sends keys to the model and settles every command they produce.
func press(t *testing.T, m *Model, names ...string) {
	t.Helper()
	for _, n := range names {
		_, cmd := m.Update(key(n))
		settle(t, m, cmd)
	}
}

func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(key(string(r))) // the blink commands it returns are of no interest
	}
}

// writeRecorder is an httptest Redmine that serves reads and records every write.
type writeRecorder struct {
	mu     sync.Mutex
	writes []recordedWrite
}

type recordedWrite struct {
	method, path, body string
}

func (w *writeRecorder) all() []recordedWrite {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recordedWrite(nil), w.writes...)
}

// newRedmine starts a fake Redmine. reads maps a path to its JSON answer; onWrite answers
// PUT and POST (status, body) and may be nil for 204.
func newRedmine(t *testing.T, reads map[string]string, onWrite func(method, path string) (int, string), opts ...redmine.Option) (*redmine.Client, *writeRecorder) {
	t.Helper()
	rec := &writeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body) // an unreadable body only makes the assertion fail
			rec.mu.Lock()
			rec.writes = append(rec.writes, recordedWrite{method: r.Method, path: r.URL.Path, body: string(body)})
			rec.mu.Unlock()
			status, resp := http.StatusNoContent, ""
			if onWrite != nil {
				status, resp = onWrite(r.Method, r.URL.Path)
			}
			w.WriteHeader(status)
			io.WriteString(w, resp) //nolint:errcheck // a failed test response surfaces in the client under test
			return
		}
		body, ok := reads[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body) //nolint:errcheck // same as above
	}))
	t.Cleanup(srv.Close)
	h, err := redmine.NewHeader("X-Redmine-API-Key", "fake-key-0123456789abcdef")
	if err != nil {
		t.Fatalf("NewHeader: %v", err)
	}
	return redmine.NewClient(srv.URL, h, nil, opts...), rec
}

func issueJSON(t *testing.T, is *redmine.Issue) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"issue": is})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func containsAll(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q\n%s", w, out)
		}
	}
}

// newRedmineForbidding is a fake Redmine that answers 403 for the given paths.
func newRedmineForbidding(t *testing.T, reads map[string]string, forbidden1, forbidden2 string, hits *[]string) (*redmine.Client, *writeRecorder) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.URL.Path)
		if r.URL.Path == forbidden1 || r.URL.Path == forbidden2 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, ok := reads[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body) //nolint:errcheck // a failed test response surfaces in the client under test
	}))
	t.Cleanup(srv.Close)
	h, err := redmine.NewHeader("X-Redmine-API-Key", "fake-key-0123456789abcdef")
	if err != nil {
		t.Fatalf("NewHeader: %v", err)
	}
	return redmine.NewClient(srv.URL, h, nil), &writeRecorder{}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ansiWidth(s string) int { return lipgloss.Width(s) }

// withShortTimeout rebuilds the client of a test model with a 60 ms timeout, sharing nothing else.
func withShortTimeout(t *testing.T, api API) API {
	t.Helper()
	c, ok := api.(*redmine.Client)
	if !ok {
		t.Fatalf("not a redmine client: %T", api)
	}
	h, err := redmine.NewHeader("X-Redmine-API-Key", "fake-key-0123456789abcdef")
	if err != nil {
		t.Fatalf("NewHeader: %v", err)
	}
	return redmine.NewClient(c.BaseURL(), h, &http.Client{Timeout: 60 * time.Millisecond})
}
