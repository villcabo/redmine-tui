// Package redmine is a client for the Redmine REST API. It reads freely and writes
// only through UpdateIssue and CreateIssue; no other mutation is reachable.
package redmine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/villcabo/redmine-tui/internal/i18n"
)

const (
	requestTimeout = 30 * time.Second
	pageSize       = 100
	maxPages       = 50
	maxSearchPages = 5
	maxBodySize    = 32 << 20
)

// writableIssuePath is the only path a write may target besides POST /issues.json.
var writableIssuePath = regexp.MustCompile(`^/issues/[0-9]+\.json$`)

// APIError is a 4xx or 5xx answer from Redmine.
type APIError struct {
	Status   int
	Messages []string
}

// Error implements error with an English text.
func (e *APIError) Error() string {
	if len(e.Messages) == 0 {
		return fmt.Sprintf("Redmine answered %d %s", e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("Redmine answered %d: %s", e.Status, strings.Join(e.Messages, "; "))
}

// OutcomeUnknownError means a write was sent but its result was never seen (timeout,
// network failure, unreadable answer), so it may or may not have been applied.
// Writes are never retried automatically because of this.
type OutcomeUnknownError struct{ Err error }

// Error implements error.
func (e *OutcomeUnknownError) Error() string { return "write outcome unknown: " + e.Err.Error() }

// Unwrap returns the underlying failure.
func (e *OutcomeUnknownError) Unwrap() error { return e.Err }

// ErrRedirect is returned when Redmine answers with a redirect. Redirects are never followed:
// net/http forwards custom headers such as X-Redmine-API-Key to the new location, even on
// another host, so following one could hand the key to a third party.
var ErrRedirect = i18n.Errorf("err.redirect")

// ErrReadOnly is returned by every write on a client built with WithReadOnly.
var ErrReadOnly = i18n.Errorf("err.read_only")

// Client talks to Redmine.
type Client struct {
	baseURL  string
	header   Header
	http     *http.Client
	readOnly bool
}

// Option configures a Client.
type Option func(*Client)

// WithReadOnly makes every write fail with ErrReadOnly before any request is sent.
func WithReadOnly() Option { return func(c *Client) { c.readOnly = true } }

// NewClient returns a client for the Redmine at baseURL authenticating with header.
// A nil httpClient uses a client with a 30 second timeout. Redirects are always refused.
func NewClient(baseURL string, header Header, httpClient *http.Client, opts ...Option) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	hc := *httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect }
	c := &Client{baseURL: strings.TrimRight(baseURL, "/"), header: header, http: &hc}
	for _, o := range opts {
		o(c)
	}
	return c
}

// BaseURL returns the Redmine root URL without a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL }

// PageSize is how many issues ListIssues returns per call.
const PageSize = pageSize

// ListIssues returns one page of the issues matching q, newest update first, starting
// at offset. The caller asks for the next page when it needs it.
func (c *Client) ListIssues(ctx context.Context, q ListQuery, offset int) (IssuePage, error) {
	query := url.Values{}
	set := func(k, v string) {
		if v != "" {
			query.Set(k, v)
		}
	}
	set("assigned_to_id", q.Assigned)
	set("author_id", q.Author)
	set("watcher_id", q.Watcher)
	if q.ProjectID > 0 {
		query.Set("project_id", strconv.Itoa(q.ProjectID))
	}
	if q.TrackerID > 0 {
		query.Set("tracker_id", strconv.Itoa(q.TrackerID))
	}
	status := q.Status
	if status == "" {
		status = StatusOpen
	}
	query.Set("status_id", status)
	query.Set("sort", "updated_on:desc")
	query.Set("limit", strconv.Itoa(pageSize))
	query.Set("offset", strconv.Itoa(offset))

	var out struct {
		Issues     []Issue `json:"issues"`
		TotalCount int     `json:"total_count"`
	}
	if err := c.get(ctx, "/issues.json", query, &out); err != nil {
		return IssuePage{}, i18n.Wrap(err, "err.list_issues")
	}
	return IssuePage{Issues: out.Issues, Total: out.TotalCount}, nil
}

// ListUsers searches users by name through /users.json. Redmine serves it only to
// administrators, so other keys get a 403 (*APIError).
func (c *Client) ListUsers(ctx context.Context, name string) ([]Member, error) {
	q := url.Values{}
	if strings.TrimSpace(name) != "" {
		q.Set("name", name)
	}
	type user struct {
		ID        int    `json:"id"`
		Login     string `json:"login"`
		FirstName string `json:"firstname"`
		LastName  string `json:"lastname"`
	}
	us, err := fetchAll[user](ctx, c, "/users.json", "users", q, 2)
	if err != nil {
		return nil, i18n.Wrap(err, "err.list_users")
	}
	out := make([]Member, 0, len(us))
	for _, u := range us {
		n := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if n == "" {
			n = u.Login
		}
		out = append(out, Member{ID: u.ID, Name: n})
	}
	return out, nil
}

// GetIssue returns one issue with its journals, children, relations and the statuses
// its workflow allows next.
func (c *Client) GetIssue(ctx context.Context, id int) (*Issue, error) {
	q := url.Values{}
	q.Set("include", "journals,children,relations,allowed_statuses")
	var out struct {
		Issue Issue `json:"issue"`
	}
	if err := c.get(ctx, "/issues/"+strconv.Itoa(id)+".json", q, &out); err != nil {
		return nil, i18n.Wrap(err, "err.get_issue", id)
	}
	return &out.Issue, nil
}

// ListStatuses returns every issue status.
func (c *Client) ListStatuses(ctx context.Context) ([]Status, error) {
	var out struct {
		Statuses []Status `json:"issue_statuses"`
	}
	if err := c.get(ctx, "/issue_statuses.json", nil, &out); err != nil {
		return nil, i18n.Wrap(err, "err.list_statuses")
	}
	return out.Statuses, nil
}

// SearchIssues runs a full-text search restricted to issues.
func (c *Client) SearchIssues(ctx context.Context, text string) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", text)
	q.Set("issues", "1")
	res, err := fetchAll[SearchResult](ctx, c, "/search.json", "results", q, maxSearchPages)
	if err != nil {
		return nil, i18n.Wrap(err, "err.search")
	}
	return res, nil
}

// ListTrackers returns every tracker defined in the instance.
func (c *Client) ListTrackers(ctx context.Context) ([]IDName, error) {
	var out struct {
		Trackers []IDName `json:"trackers"`
	}
	if err := c.get(ctx, "/trackers.json", nil, &out); err != nil {
		return nil, i18n.Wrap(err, "err.list_trackers")
	}
	return out.Trackers, nil
}

// ListProjects returns every project the key can see.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	ps, err := fetchAll[Project](ctx, c, "/projects.json", "projects", nil, maxPages)
	if err != nil {
		return nil, i18n.Wrap(err, "err.list_projects")
	}
	return ps, nil
}

// GetProject returns a project with its trackers and enabled issue custom fields.
func (c *Client) GetProject(ctx context.Context, id int) (*Project, error) {
	q := url.Values{}
	q.Set("include", "trackers,issue_custom_fields")
	var out struct {
		Project Project `json:"project"`
	}
	if err := c.get(ctx, "/projects/"+strconv.Itoa(id)+".json", q, &out); err != nil {
		return nil, i18n.Wrap(err, "err.get_project")
	}
	return &out.Project, nil
}

// ListMembers returns the users (not groups) that belong to a project.
func (c *Client) ListMembers(ctx context.Context, projectID int) ([]Member, error) {
	type membership struct {
		User *IDName `json:"user"`
	}
	ms, err := fetchAll[membership](ctx, c, "/projects/"+strconv.Itoa(projectID)+"/memberships.json", "memberships", nil, maxPages)
	if err != nil {
		return nil, i18n.Wrap(err, "err.list_members")
	}
	var out []Member
	for _, m := range ms {
		if m.User != nil {
			out = append(out, Member{ID: m.User.ID, Name: m.User.Name})
		}
	}
	return out, nil
}

// CurrentUser returns the owner of the API key.
func (c *Client) CurrentUser(ctx context.Context) (Member, error) {
	var out struct {
		User struct {
			ID        int    `json:"id"`
			Login     string `json:"login"`
			FirstName string `json:"firstname"`
			LastName  string `json:"lastname"`
		} `json:"user"`
	}
	if err := c.get(ctx, "/users/current.json", nil, &out); err != nil {
		return Member{}, i18n.Wrap(err, "err.current_user")
	}
	name := strings.TrimSpace(out.User.FirstName + " " + out.User.LastName)
	if name == "" {
		name = out.User.Login
	}
	return Member{ID: out.User.ID, Name: name}, nil
}

// ListPriorities returns the active issue priorities.
func (c *Client) ListPriorities(ctx context.Context) ([]Priority, error) {
	var out struct {
		Priorities []Priority `json:"issue_priorities"`
	}
	if err := c.get(ctx, "/enumerations/issue_priorities.json", nil, &out); err != nil {
		return nil, i18n.Wrap(err, "err.list_priorities")
	}
	active := out.Priorities[:0]
	for _, p := range out.Priorities {
		if p.IsActive() {
			active = append(active, p)
		}
	}
	return active, nil
}

// ListCustomFields returns the custom field definitions. Redmine only serves them
// to administrators, so a non-admin key gets a 403 here.
func (c *Client) ListCustomFields(ctx context.Context) ([]CustomFieldDef, error) {
	var out struct {
		Fields []CustomFieldDef `json:"custom_fields"`
	}
	if err := c.get(ctx, "/custom_fields.json", nil, &out); err != nil {
		return nil, i18n.Wrap(err, "err.list_custom_fields")
	}
	return out.Fields, nil
}

// UpdateIssue applies u to an issue with PUT /issues/:id.json. A failure that is
// not an *APIError or ErrRedirect is reported as *OutcomeUnknownError and is never retried.
func (c *Client) UpdateIssue(ctx context.Context, id int, u IssueUpdate) error {
	fields := map[string]any{}
	if u.StatusID != nil {
		fields["status_id"] = *u.StatusID
	}
	if u.DoneRatio != nil {
		fields["done_ratio"] = *u.DoneRatio
	}
	if u.AssignedToID != nil {
		if *u.AssignedToID == 0 {
			fields["assigned_to_id"] = nil
		} else {
			fields["assigned_to_id"] = *u.AssignedToID
		}
	}
	if strings.TrimSpace(u.Notes) != "" {
		fields["notes"] = u.Notes
	}
	if len(fields) == 0 {
		return i18n.Errorf("err.nothing_to_update")
	}
	if err := c.write(ctx, http.MethodPut, "/issues/"+strconv.Itoa(id)+".json", map[string]any{"issue": fields}, nil); err != nil {
		return i18n.Wrap(err, "err.update_issue", id)
	}
	return nil
}

// CreateIssue creates an issue with POST /issues.json and returns it as Redmine stored it.
// Failures are classified like those of UpdateIssue.
func (c *Client) CreateIssue(ctx context.Context, n NewIssue) (*Issue, error) {
	fields := map[string]any{
		"project_id": n.ProjectID,
		"tracker_id": n.TrackerID,
		"subject":    n.Subject,
	}
	if strings.TrimSpace(n.Description) != "" {
		fields["description"] = n.Description
	}
	if n.PriorityID > 0 {
		fields["priority_id"] = n.PriorityID
	}
	if n.AssignedToID > 0 {
		fields["assigned_to_id"] = n.AssignedToID
	}
	if n.ParentID > 0 {
		fields["parent_issue_id"] = n.ParentID
	}
	if len(n.CustomFields) > 0 {
		cfs := make([]map[string]any, len(n.CustomFields))
		for i, cf := range n.CustomFields {
			cfs[i] = map[string]any{"id": cf.ID, "value": cf.Value}
		}
		fields["custom_fields"] = cfs
	}
	var out struct {
		Issue Issue `json:"issue"`
	}
	if err := c.write(ctx, http.MethodPost, "/issues.json", map[string]any{"issue": fields}, &out); err != nil {
		return nil, i18n.Wrap(err, "err.create_issue")
	}
	return &out.Issue, nil
}

// fetchAll follows pagination for a list endpoint whose items live under key.
func fetchAll[T any](ctx context.Context, c *Client, path, key string, query url.Values, pages int) ([]T, error) {
	var all []T
	for page := 0; page < pages; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(len(all)))

		var out map[string]json.RawMessage
		if err := c.get(ctx, path, q, &out); err != nil {
			return nil, err
		}
		var items []T
		if raw, ok := out[key]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, i18n.Wrap(err, "err.bad_json")
			}
		}
		var total int
		if raw, ok := out["total_count"]; ok {
			if err := json.Unmarshal(raw, &total); err != nil {
				return nil, i18n.Wrap(err, "err.bad_json")
			}
		}
		all = append(all, items...)
		if len(items) == 0 || len(all) >= total {
			break
		}
	}
	return all, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, dst any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	_, err := c.do(ctx, http.MethodGet, u, nil, dst)
	return err
}

// write sends a PUT or POST. Only PUT /issues/:id.json and POST /issues.json are accepted,
// so no other mutation can be issued through this client.
func (c *Client) write(ctx context.Context, method, path string, payload any, dst any) error {
	allowed := (method == http.MethodPut && writableIssuePath.MatchString(path)) ||
		(method == http.MethodPost && path == "/issues.json")
	if !allowed {
		return i18n.Errorf("err.write_not_allowed", method, path)
	}
	if c.readOnly {
		return ErrReadOnly
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return i18n.Wrap(err, "err.build_request")
	}
	if _, err := c.do(ctx, method, c.baseURL+path, body, dst); err != nil {
		var api *APIError
		if errors.As(err, &api) || errors.Is(err, ErrRedirect) {
			return err
		}
		return &OutcomeUnknownError{Err: err}
	}
	return nil
}

// do sends one request and decodes a JSON answer into dst when dst is not nil and the
// body is not empty. It returns the raw body.
func (c *Client) do(ctx context.Context, method, u string, body []byte, dst any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, i18n.Wrap(err, "err.build_request")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !c.header.IsZero() {
		req.Header.Set(c.header.name, c.header.value)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.transportError(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, i18n.Wrap(err, "err.read_response")
	}
	if resp.StatusCode >= 400 {
		return raw, c.apiError(resp.StatusCode, raw)
	}
	if dst != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, dst); err != nil {
			return raw, i18n.Wrap(err, "err.bad_json")
		}
	} else if dst != nil && method == http.MethodGet {
		return raw, i18n.Errorf("err.empty_response")
	}
	return raw, nil
}

func (c *Client) apiError(status int, body []byte) error {
	e := &APIError{Status: status}
	var payload struct {
		Errors []string `json:"errors"`
	}
	if json.Unmarshal(body, &payload) == nil {
		for _, m := range payload.Errors {
			e.Messages = append(e.Messages, c.header.redact(m))
		}
	}
	return e
}

// transportError drops the request URL that net/http embeds, keeping only the cause.
func (c *Client) transportError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, ErrRedirect) {
		return ErrRedirect
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return i18n.Wrap(err, "err.timeout")
	}
	return i18n.Errorf("err.connect", c.header.redact(err.Error()))
}
