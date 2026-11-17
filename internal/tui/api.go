package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/villcabo/redmine-tui/internal/redmine"
)

const (
	fetchTimeout   = 30 * time.Second
	writeTimeout   = 30 * time.Second
	browserTimeout = 10 * time.Second
	debounceDelay  = 250 * time.Millisecond
)

// API is the Redmine access the UI needs. *redmine.Client satisfies it.
type API interface {
	ListIssues(ctx context.Context, q redmine.ListQuery, offset int) (redmine.IssuePage, error)
	GetIssue(ctx context.Context, id int) (*redmine.Issue, error)
	ListStatuses(ctx context.Context) ([]redmine.Status, error)
	ListPriorities(ctx context.Context) ([]redmine.Priority, error)
	ListTrackers(ctx context.Context) ([]redmine.IDName, error)
	SearchIssues(ctx context.Context, text string) ([]redmine.SearchResult, error)
	ListProjects(ctx context.Context) ([]redmine.Project, error)
	GetProject(ctx context.Context, id int) (*redmine.Project, error)
	ListMembers(ctx context.Context, projectID int) ([]redmine.Member, error)
	ListUsers(ctx context.Context, name string) ([]redmine.Member, error)
	CurrentUser(ctx context.Context) (redmine.Member, error)
	ListCustomFields(ctx context.Context) ([]redmine.CustomFieldDef, error)
	UpdateIssue(ctx context.Context, id int, u redmine.IssueUpdate) error
	CreateIssue(ctx context.Context, n redmine.NewIssue) (*redmine.Issue, error)
}

var _ API = (*redmine.Client)(nil)

// Messages produced by background commands.
type (
	statusesMsg struct {
		statuses []redmine.Status
		err      error
	}
	prioritiesMsg struct {
		priorities []redmine.Priority
		err        error
	}
	meMsg struct {
		me  redmine.Member
		err error
	}
	issuesMsg struct {
		gen    int
		offset int
		page   redmine.IssuePage
		err    error
	}
	issueMsg struct {
		uid   int
		issue *redmine.Issue
		err   error
	}
	searchMsg struct {
		gen     int
		results []redmine.SearchResult
		err     error
	}
	// awaitMsg carries the result of a fetch started by Model.await.
	awaitMsg struct {
		token int
		res   any
		err   error
		then  func(m *Model, res any, err error) tea.Cmd
	}
	writeDoneMsg struct {
		issueID int
		created *redmine.Issue
		err     error
	}
	editorDoneMsg struct {
		text string
		err  error
		then func(m *Model, text string) tea.Cmd
	}
	pickerQueryMsg struct {
		seq   int
		query string
	}
	pickerItemsMsg struct {
		seq    int
		items  []pickItem
		note   string
		err    error
		denied bool
		cache  []redmine.Member
	}
	openedMsg struct{ err error }
)

func fetchStatusesCmd(a API) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		s, err := a.ListStatuses(ctx)
		return statusesMsg{statuses: s, err: err}
	}
}

func fetchPrioritiesCmd(a API) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		p, err := a.ListPriorities(ctx)
		return prioritiesMsg{priorities: p, err: err}
	}
}

func fetchMeCmd(a API) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		me, err := a.CurrentUser(ctx)
		return meMsg{me: me, err: err}
	}
}

func fetchIssuesCmd(a API, gen int, q redmine.ListQuery, offset int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		page, err := a.ListIssues(ctx, q, offset)
		return issuesMsg{gen: gen, offset: offset, page: page, err: err}
	}
}

func fetchIssueCmd(a API, uid, id int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		is, err := a.GetIssue(ctx, id)
		return issueMsg{uid: uid, issue: is, err: err}
	}
}

func searchCmd(a API, gen int, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		res, err := a.SearchIssues(ctx, text)
		return searchMsg{gen: gen, results: res, err: err}
	}
}
