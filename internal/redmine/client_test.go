package redmine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const listPage = `{"issues":[{"id":1,"subject":"First","project":{"id":1,"name":"Website"},"tracker":{"id":1,"name":"Bug"},
"status":{"id":1,"name":"New","is_closed":false},"priority":{"id":2,"name":"Normal"},"author":{"id":25,"name":"Jane Doe"},
"start_date":null,"due_date":"2026-11-20","done_ratio":10,"created_on":"2026-11-17T16:59:54Z","updated_on":"2026-11-17T16:59:54Z",
"custom_fields":[{"id":4,"name":"Team","value":"Backend"},{"id":5,"name":"Empty","value":""},{"id":6,"name":"Null","value":null},{"id":7,"name":"Multi","value":["a","b"]}]}],
"total_count":%d,"offset":%d,"limit":100}`

const detailJSON = `{"issue":{"id":7,"subject":"Detail","project":{"id":1,"name":"Website"},"tracker":{"id":1,"name":"Bug"},
"status":{"id":8,"name":"Closed","is_closed":true},"priority":{"id":2,"name":"Normal"},"author":{"id":25,"name":"Jane Doe"},
"done_ratio":100,"created_on":"2026-11-17T16:59:54Z","updated_on":"2026-11-17T16:59:54Z","description":"**hello**",
"journals":[{"id":1,"user":{"id":25,"name":"Jane Doe"},"notes":"note","created_on":"2026-11-17T17:00:00Z",
"details":[{"property":"attr","name":"status_id","old_value":"1","new_value":"8"},{"property":"cf","name":"6","old_value":null,"new_value":"Backend"}]}],
"children":[{"id":15808,"tracker":{"id":1,"name":"Bug"},"subject":"Child"}],
"relations":[{"id":3,"relation_type":"relates","issue_id":7,"issue_to_id":9}]%s}}`

type request struct {
	method string
	path   string
	query  url.Values
	body   string
	apiKey string
}

type recorder struct {
	mu   sync.Mutex
	reqs []request
}

func (r *recorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body) // a failed read leaves an empty body, which the assertions then catch
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, request{
		method: req.Method, path: req.URL.Path, query: req.URL.Query(),
		body: string(body), apiKey: req.Header.Get("X-Redmine-API-Key"),
	})
}

func (r *recorder) all() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.reqs...)
}

func newTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	hdr, err := NewHeader("X-Redmine-API-Key", fakeKey)
	if err != nil {
		t.Fatalf("NewHeader: %v", err)
	}
	return NewClient(srv.URL+"/", hdr, nil, opts...), rec
}

func TestListIssuesQuery(t *testing.T) {
	tests := []struct {
		name  string
		query ListQuery
		want  map[string]string
		not   []string
	}{
		{name: "assigned to me, open", query: ListQuery{Assigned: "me"}, want: map[string]string{"assigned_to_id": "me", "status_id": "open"}, not: []string{"author_id", "watcher_id", "project_id", "tracker_id"}},
		{name: "created by me", query: ListQuery{Author: "me"}, want: map[string]string{"author_id": "me", "status_id": "open"}, not: []string{"assigned_to_id"}},
		{name: "watched", query: ListQuery{Watcher: "me"}, want: map[string]string{"watcher_id": "me"}},
		{name: "everything", query: ListQuery{Status: StatusAny}, want: map[string]string{"status_id": "*"}, not: []string{"assigned_to_id", "author_id"}},
		{name: "closed in a project", query: ListQuery{ProjectID: 12, Status: StatusClosed}, want: map[string]string{"project_id": "12", "status_id": "closed"}},
		{name: "a concrete status", query: ListQuery{Status: "5"}, want: map[string]string{"status_id": "5"}},
		{name: "assigned to another user", query: ListQuery{Assigned: "31"}, want: map[string]string{"assigned_to_id": "31"}},
		{name: "every filter together", query: ListQuery{Assigned: "31", Author: "me", Watcher: "me", ProjectID: 3, TrackerID: 2, Status: StatusOpen},
			want: map[string]string{"assigned_to_id": "31", "author_id": "me", "watcher_id": "me", "project_id": "3", "tracker_id": "2", "status_id": "open"}},
		{name: "by due date, dated only", query: ListQuery{Assigned: "me", Sort: "due_date,priority:desc", WithDueDate: true},
			want: map[string]string{"sort": "due_date,priority:desc", "due_date": "*"}},
		{name: "default order sends no due date filter", query: ListQuery{Assigned: "me"}, want: map[string]string{"sort": "updated_on:desc"}, not: []string{"due_date"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, listPage, 1, 0) })
			if _, err := c.ListIssues(context.Background(), tt.query, 0); err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			got := rec.all()[0]
			if got.method != http.MethodGet || got.path != "/issues.json" {
				t.Errorf("request = %s %s", got.method, got.path)
			}
			for k, v := range tt.want {
				if got.query.Get(k) != v {
					t.Errorf("%s = %q, want %q (query %v)", k, got.query.Get(k), v, got.query)
				}
			}
			for _, k := range tt.not {
				if got.query.Has(k) {
					t.Errorf("%s must not be sent (query %v)", k, got.query)
				}
			}
			if _, custom := tt.want["sort"]; !custom && got.query.Get("sort") != "updated_on:desc" {
				t.Errorf("default sort wrong: %v", got.query)
			}
			if got.query.Get("limit") != "100" {
				t.Errorf("limit wrong: %v", got.query)
			}
			if got.apiKey != fakeKey {
				t.Error("the key header was not sent")
			}
		})
	}
}

func TestListIssueSets(t *testing.T) {
	tests := []struct {
		name  string
		call  func(c *Client) ([]Issue, error)
		param string
		value string
	}{
		{name: "children of several parents", call: func(c *Client) ([]Issue, error) {
			return c.ListChildren(context.Background(), []int{15589, 15807})
		}, param: "parent_id", value: "15589,15807"},
		{name: "descendants at any depth", call: func(c *Client) ([]Issue, error) {
			return c.ListDescendants(context.Background(), 15589)
		}, param: "parent_id", value: "~15589"},
		{name: "issues by id", call: func(c *Client) ([]Issue, error) {
			return c.ListIssuesByID(context.Background(), []int{9, 0, 12})
		}, param: "issue_id", value: "9,12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, listPage, 1, 0) })
			issues, err := tt.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(issues) != 1 {
				t.Fatalf("issues = %d, want 1", len(issues))
			}
			got := rec.all()[0]
			if got.path != "/issues.json" || got.query.Get(tt.param) != tt.value || got.query.Get("status_id") != "*" {
				t.Errorf("request = %s %v", got.path, got.query)
			}
		})
	}
}

func TestListIssueSetsSkipEmptyInput(t *testing.T) {
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, listPage, 1, 0) })
	if got, err := c.ListChildren(context.Background(), nil); err != nil || got != nil {
		t.Fatalf("ListChildren(nil) = %v, %v", got, err)
	}
	if got, err := c.ListIssuesByID(context.Background(), []int{0}); err != nil || got != nil {
		t.Fatalf("ListIssuesByID([0]) = %v, %v", got, err)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("an empty set must not reach Redmine, got %d requests", n)
	}
}

func TestListIssuesPagesOnDemand(t *testing.T) {
	c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &offset)
		fmt.Fprintf(w, listPage, 250, offset)
	})
	first, err := c.ListIssues(context.Background(), ListQuery{}, 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if first.Total != 250 || len(first.Issues) != 1 {
		t.Fatalf("page = total %d, %d issues", first.Total, len(first.Issues))
	}
	if n := len(rec.all()); n != 1 {
		t.Fatalf("one call must fetch one page, got %d requests", n)
	}
	if _, err := c.ListIssues(context.Background(), ListQuery{}, 100); err != nil {
		t.Fatalf("ListIssues page 2: %v", err)
	}
	if got := rec.all()[1].query.Get("offset"); got != "100" {
		t.Errorf("second page offset = %q, want 100", got)
	}
}

func TestCustomFieldText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, listPage, 1, 0) })
	page, err := c.ListIssues(context.Background(), ListQuery{}, 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	want := []string{"Backend", "", "", "a, b"}
	for i, cf := range page.Issues[0].CustomFields {
		t.Run(cf.Name, func(t *testing.T) {
			if got := cf.Text(); got != want[i] {
				t.Errorf("Text() = %q, want %q", got, want[i])
			}
		})
	}
}

func TestGetIssue(t *testing.T) {
	tests := []struct {
		name        string
		extra       string
		wantAllowed int // -1 means nil (instance does not report it)
	}{
		{name: "allowed statuses reported", extra: `,"allowed_statuses":[{"id":1,"name":"New","is_closed":false},{"id":8,"name":"Closed","is_closed":true}]`, wantAllowed: 2},
		{name: "reported but empty", extra: `,"allowed_statuses":[]`, wantAllowed: 0},
		{name: "not reported", extra: ``, wantAllowed: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, detailJSON, tt.extra) })
			is, err := c.GetIssue(context.Background(), 7)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if is.ID != 7 || len(is.Journals) != 1 || len(is.Children) != 1 || len(is.Relations) != 1 {
				t.Fatalf("unexpected issue: %+v", is)
			}
			if got := is.Journals[0].Details[1]; got.OldValue != nil || got.NewValue == nil || *got.NewValue != "Backend" {
				t.Errorf("cf detail not parsed: %+v", got)
			}
			if got := is.Relations[0].Other(7); got != 9 {
				t.Errorf("Other(7) = %d, want 9", got)
			}
			if inc := rec.all()[0].query.Get("include"); inc != "journals,children,relations,allowed_statuses" {
				t.Errorf("include = %q", inc)
			}
			switch {
			case tt.wantAllowed < 0 && is.AllowedStatuses != nil:
				t.Errorf("AllowedStatuses = %v, want nil", is.AllowedStatuses)
			case tt.wantAllowed >= 0 && (is.AllowedStatuses == nil || len(is.AllowedStatuses) != tt.wantAllowed):
				t.Errorf("AllowedStatuses = %v, want %d entries", is.AllowedStatuses, tt.wantAllowed)
			}
		})
	}
}

func TestReferenceEndpoints(t *testing.T) {
	routes := map[string]string{
		"/issue_statuses.json":                `{"issue_statuses":[{"id":1,"name":"New","is_closed":false,"is_default":true},{"id":8,"name":"Closed","is_closed":true}]}`,
		"/enumerations/issue_priorities.json": `{"issue_priorities":[{"id":1,"name":"Low","is_default":false,"active":true},{"id":2,"name":"Normal","is_default":true},{"id":3,"name":"Old","active":false}]}`,
		"/trackers.json":                      `{"trackers":[{"id":1,"name":"Bug"},{"id":2,"name":"Feature"}]}`,
		"/projects.json":                      `{"projects":[{"id":1,"name":"Website","identifier":"web"}],"total_count":1}`,
		"/projects/1.json":                    `{"project":{"id":1,"name":"Website","trackers":[{"id":1,"name":"Bug"}],"issue_custom_fields":[{"id":4,"name":"Team"}]}}`,
		"/projects/1/memberships.json":        `{"memberships":[{"id":1,"user":{"id":5,"name":"Jane Doe"}},{"id":2,"group":{"id":9,"name":"Devs"}},{"id":3,"user":{"id":6,"name":"John Roe"}}],"total_count":3}`,
		"/users/current.json":                 `{"user":{"id":5,"login":"jane","firstname":"Jane","lastname":"Doe"}}`,
		"/users.json":                         `{"users":[{"id":5,"login":"jane","firstname":"Jane","lastname":"Doe"},{"id":7,"login":"nobody","firstname":"","lastname":""}],"total_count":2}`,
		"/custom_fields.json":                 `{"custom_fields":[{"id":4,"name":"Team","customized_type":"issue","field_format":"list","is_required":true,"possible_values":[{"value":"Backend","label":"Backend"}],"trackers":[{"id":1,"name":"Bug"}]}]}`,
		"/search.json":                        `{"results":[{"id":12,"title":"Bug #12 (New): Crash","type":"issue","description":"it crashes","datetime":"2026-11-02T10:00:00Z"}],"total_count":1}`,
	}
	c, rec := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	})
	ctx := context.Background()

	t.Run("statuses", func(t *testing.T) {
		st, err := c.ListStatuses(ctx)
		if err != nil || len(st) != 2 || !st[0].IsDefault || !st[1].IsClosed {
			t.Errorf("ListStatuses() = %+v, %v", st, err)
		}
	})
	t.Run("priorities skip inactive", func(t *testing.T) {
		ps, err := c.ListPriorities(ctx)
		if err != nil || len(ps) != 2 || !ps[1].IsDefault {
			t.Errorf("ListPriorities() = %+v, %v", ps, err)
		}
	})
	t.Run("trackers", func(t *testing.T) {
		ts, err := c.ListTrackers(ctx)
		if err != nil || len(ts) != 2 {
			t.Errorf("ListTrackers() = %+v, %v", ts, err)
		}
	})
	t.Run("projects", func(t *testing.T) {
		ps, err := c.ListProjects(ctx)
		if err != nil || len(ps) != 1 || ps[0].Name != "Website" {
			t.Errorf("ListProjects() = %+v, %v", ps, err)
		}
	})
	t.Run("project with trackers and custom fields", func(t *testing.T) {
		p, err := c.GetProject(ctx, 1)
		if err != nil || len(p.Trackers) != 1 || len(p.IssueCustomFields) != 1 {
			t.Fatalf("GetProject() = %+v, %v", p, err)
		}
		last := rec.all()[len(rec.all())-1]
		if last.query.Get("include") != "trackers,issue_custom_fields" {
			t.Errorf("include = %q", last.query.Get("include"))
		}
	})
	t.Run("members exclude groups", func(t *testing.T) {
		ms, err := c.ListMembers(ctx, 1)
		if err != nil || len(ms) != 2 || ms[0].Name != "Jane Doe" || ms[1].ID != 6 {
			t.Errorf("ListMembers() = %+v, %v", ms, err)
		}
	})
	t.Run("current user", func(t *testing.T) {
		u, err := c.CurrentUser(ctx)
		if err != nil || u.ID != 5 || u.Name != "Jane Doe" {
			t.Errorf("CurrentUser() = %+v, %v", u, err)
		}
	})
	t.Run("users search sends the name and falls back to the login", func(t *testing.T) {
		us, err := c.ListUsers(ctx, "ja")
		if err != nil || len(us) != 2 || us[1].Name != "nobody" {
			t.Fatalf("ListUsers() = %+v, %v", us, err)
		}
		last := rec.all()[len(rec.all())-1]
		if last.query.Get("name") != "ja" {
			t.Errorf("name = %q", last.query.Get("name"))
		}
	})
	t.Run("custom fields", func(t *testing.T) {
		defs, err := c.ListCustomFields(ctx)
		if err != nil || len(defs) != 1 || !defs[0].IsRequired || !defs[0].AppliesToTracker(1) || defs[0].AppliesToTracker(2) {
			t.Errorf("ListCustomFields() = %+v, %v", defs, err)
		}
	})
	t.Run("search", func(t *testing.T) {
		res, err := c.SearchIssues(ctx, "crash & burn")
		if err != nil || len(res) != 1 || res[0].ID != 12 {
			t.Fatalf("SearchIssues() = %+v, %v", res, err)
		}
		last := rec.all()[len(rec.all())-1]
		if last.path != "/search.json" || last.query.Get("q") != "crash & burn" || last.query.Get("issues") != "1" {
			t.Errorf("search request = %s %v", last.path, last.query)
		}
	})
	for _, r := range rec.all() {
		if r.method != http.MethodGet {
			t.Errorf("a read used %s %s", r.method, r.path)
		}
	}
}

func TestUsersForbiddenIsAnAPIError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	_, err := c.ListUsers(context.Background(), "")
	var api *APIError
	if !errors.As(err, &api) || api.Status != 403 {
		t.Fatalf("ListUsers() error = %v, want a 403 *APIError", err)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantText   string
	}{
		{name: "403 with messages", status: 403, body: `{"errors":["Not authorized"]}`, wantStatus: 403, wantText: "Not authorized"},
		{name: "404 without body", status: 404, body: ``, wantStatus: 404, wantText: "404"},
		{name: "500", status: 500, body: `{"errors":["boom"]}`, wantStatus: 500, wantText: "boom"},
		{name: "server echoes the key", status: 401, body: `{"errors":["bad key ` + fakeKey + `"]}`, wantStatus: 401, wantText: "***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			_, err := c.GetIssue(context.Background(), 1)
			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("error %v is not an *APIError", err)
			}
			if api.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", api.Status, tt.wantStatus)
			}
			if !strings.Contains(api.Error(), tt.wantText) {
				t.Errorf("error %q does not contain %q", api.Error(), tt.wantText)
			}
			if strings.Contains(api.Error(), fakeKey) {
				t.Errorf("error leaks the key: %v", api)
			}
		})
	}
}

func TestTransportErrorHasNoKeyOrURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	hdr, err := NewHeader("X-Redmine-API-Key", fakeKey)
	if err != nil {
		t.Fatalf("NewHeader: %v", err)
	}
	_, err = NewClient(url, hdr, nil).GetIssue(context.Background(), 1)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), fakeKey) || strings.Contains(err.Error(), url) {
		t.Errorf("error leaks key or URL: %v", err)
	}
}

func TestInvalidJSON(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>") })
	if _, err := c.ListStatuses(context.Background()); err == nil {
		t.Fatal("expected a JSON error")
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	thirdParty, thirdRec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, detailJSON, "")
	})
	tests := []struct {
		name   string
		status int
	}{
		{name: "302 to another host", status: http.StatusFound},
		{name: "307 to another host", status: http.StatusTemporaryRedirect},
		{name: "301 to another host", status: http.StatusMovedPermanently},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, thirdParty.BaseURL()+"/issues/7.json", tt.status)
			})
			_, err := c.GetIssue(context.Background(), 7)
			if !errors.Is(err, ErrRedirect) {
				t.Fatalf("GetIssue() error = %v, want ErrRedirect", err)
			}
			err = c.UpdateIssue(context.Background(), 7, IssueUpdate{Notes: "x"})
			if !errors.Is(err, ErrRedirect) {
				t.Fatalf("UpdateIssue() error = %v, want ErrRedirect", err)
			}
			var unknown *OutcomeUnknownError
			if errors.As(err, &unknown) {
				t.Error("a refused redirect is a definite failure, not an unknown outcome")
			}
			if n := len(thirdRec.all()); n != 0 {
				t.Errorf("the redirect target received %d requests; the key must never reach it", n)
			}
		})
	}
}

func intp(i int) *int { return &i }

func TestUpdateIssue(t *testing.T) {
	tests := []struct {
		name string
		in   IssueUpdate
		want string
	}{
		{name: "status", in: IssueUpdate{StatusID: intp(5)}, want: `{"issue":{"status_id":5}}`},
		{name: "note", in: IssueUpdate{Notes: "hello"}, want: `{"issue":{"notes":"hello"}}`},
		{name: "done ratio zero is sent", in: IssueUpdate{DoneRatio: intp(0)}, want: `{"issue":{"done_ratio":0}}`},
		{name: "assign", in: IssueUpdate{AssignedToID: intp(31)}, want: `{"issue":{"assigned_to_id":31}}`},
		{name: "unassign sends null", in: IssueUpdate{AssignedToID: intp(0)}, want: `{"issue":{"assigned_to_id":null}}`},
		{name: "quick close", in: IssueUpdate{StatusID: intp(8), DoneRatio: intp(100), Notes: "done"}, want: `{"issue":{"done_ratio":100,"notes":"done","status_id":8}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			if err := c.UpdateIssue(context.Background(), 123, tt.in); err != nil {
				t.Fatalf("UpdateIssue: %v", err)
			}
			reqs := rec.all()
			if len(reqs) != 1 {
				t.Fatalf("got %d requests, want 1", len(reqs))
			}
			got := reqs[0]
			if got.method != http.MethodPut || got.path != "/issues/123.json" {
				t.Errorf("request = %s %s", got.method, got.path)
			}
			if got.body != tt.want {
				t.Errorf("body = %s, want %s", got.body, tt.want)
			}
			if got.apiKey != fakeKey {
				t.Error("the key header was not sent")
			}
		})
	}
}

func TestUpdateIssueNothingToSendMakesNoRequest(t *testing.T) {
	c, rec := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	if err := c.UpdateIssue(context.Background(), 1, IssueUpdate{Notes: "   "}); err == nil {
		t.Fatal("expected an error")
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("sent %d requests", n)
	}
}

func TestCreateIssue(t *testing.T) {
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"issue":{"id":42,"subject":"New thing","project":{"id":1,"name":"Website"}}}`)
	})
	is, err := c.CreateIssue(context.Background(), NewIssue{
		ProjectID: 1, TrackerID: 2, Subject: "New thing", Description: "details", PriorityID: 3, AssignedToID: 5, ParentID: 40,
		CustomFields: []CustomValue{{ID: 4, Value: "Backend"}, {ID: 8, Value: []string{"x"}}},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if is.ID != 42 {
		t.Errorf("ID = %d, want 42", is.ID)
	}
	got := rec.all()[0]
	if got.method != http.MethodPost || got.path != "/issues.json" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	var body struct {
		Issue map[string]any `json:"issue"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	for k, want := range map[string]any{"project_id": 1.0, "tracker_id": 2.0, "subject": "New thing", "description": "details", "priority_id": 3.0, "assigned_to_id": 5.0, "parent_issue_id": 40.0} {
		if body.Issue[k] != want {
			t.Errorf("%s = %v, want %v", k, body.Issue[k], want)
		}
	}
	if cfs, _ := body.Issue["custom_fields"].([]any); len(cfs) != 2 {
		t.Errorf("custom_fields = %v", body.Issue["custom_fields"])
	}
}

func TestCreateIssueMinimalBodyOmitsEmptyFields(t *testing.T) {
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"issue":{"id":1}}`) })
	if _, err := c.CreateIssue(context.Background(), NewIssue{ProjectID: 1, TrackerID: 2, Subject: "s"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if got, want := rec.all()[0].body, `{"issue":{"project_id":1,"subject":"s","tracker_id":2}}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWriteValidationErrorIsShownAndDefinite(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"errors":["Subject cannot be blank","Team cannot be blank"]}`)
	})
	_, err := c.CreateIssue(context.Background(), NewIssue{ProjectID: 1, TrackerID: 2})
	var api *APIError
	if !errors.As(err, &api) || api.Status != 422 || len(api.Messages) != 2 || api.Messages[0] != "Subject cannot be blank" {
		t.Fatalf("CreateIssue() error = %v, want a 422 with both messages", err)
	}
	var unknown *OutcomeUnknownError
	if errors.As(err, &unknown) {
		t.Error("a 422 is a definite failure, not an unknown outcome")
	}
}

func TestWriteTimeoutIsUnknownAndNotRetried(t *testing.T) {
	release := make(chan struct{})
	c, rec := newTestClient(t, func(http.ResponseWriter, *http.Request) { <-release })
	t.Cleanup(func() { close(release) })
	c.http.Timeout = 50 * time.Millisecond

	err := c.UpdateIssue(context.Background(), 5, IssueUpdate{Notes: "hi"})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("UpdateIssue() error = %v, want *OutcomeUnknownError", err)
	}
	if n := len(rec.all()); n != 1 {
		t.Errorf("the write was sent %d times; it must never be retried", n)
	}
	if strings.Contains(err.Error(), fakeKey) {
		t.Errorf("error leaks the key: %v", err)
	}
}

func TestReadOnlyClientNeverWrites(t *testing.T) {
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("a read-only client sent a request")
		w.WriteHeader(http.StatusNoContent)
	}, WithReadOnly())
	if err := c.UpdateIssue(context.Background(), 1, IssueUpdate{Notes: "x"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("UpdateIssue() error = %v, want ErrReadOnly", err)
	}
	if _, err := c.CreateIssue(context.Background(), NewIssue{ProjectID: 1, TrackerID: 1, Subject: "s"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("CreateIssue() error = %v, want ErrReadOnly", err)
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("sent %d requests", n)
	}
}

func TestOnlyIssueWritesAreAllowed(t *testing.T) {
	tests := []struct {
		name         string
		method, path string
	}{
		{name: "DELETE an issue", method: http.MethodDelete, path: "/issues/1.json"},
		{name: "PUT a project", method: http.MethodPut, path: "/projects/1.json"},
		{name: "POST a user", method: http.MethodPost, path: "/users.json"},
		{name: "PUT a custom field", method: http.MethodPut, path: "/custom_fields/1.json"},
		{name: "POST to an issue path", method: http.MethodPost, path: "/issues/1.json"},
		{name: "PUT with a traversal", method: http.MethodPut, path: "/issues/1.json/../../users/1.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
			if err := c.write(context.Background(), tt.method, tt.path, map[string]any{}, nil); err == nil {
				t.Fatal("write() accepted a forbidden request")
			}
			if n := len(rec.all()); n != 0 {
				t.Errorf("sent %d requests", n)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	c, rec := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"user":{"id":72,"login":"dramos","firstname":"Diego","lastname":"Ramos"}}`)
	})
	u, err := c.GetUser(context.Background(), 72)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.ID != 72 || u.Name != "Diego Ramos" {
		t.Errorf("user = %+v", u)
	}
	if got := rec.all()[0]; got.method != http.MethodGet || got.path != "/users/72.json" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
}
