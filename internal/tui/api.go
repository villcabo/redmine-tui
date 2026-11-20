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
	ListChildren(ctx context.Context, parentIDs []int) ([]redmine.Issue, error)
	ListDescendants(ctx context.Context, rootID int) ([]redmine.Issue, error)
	ListIssuesByID(ctx context.Context, ids []int) ([]redmine.Issue, error)
	ListStatuses(ctx context.Context) ([]redmine.Status, error)
	ListPriorities(ctx context.Context) ([]redmine.Priority, error)
	ListTrackers(ctx context.Context) ([]redmine.IDName, error)
	SearchIssues(ctx context.Context, text string) ([]redmine.SearchResult, error)
	ListProjects(ctx context.Context) ([]redmine.Project, error)
	GetProject(ctx context.Context, id int) (*redmine.Project, error)
	ListMembers(ctx context.Context, projectID int) ([]redmine.Member, error)
	ListUsers(ctx context.Context, name string) ([]redmine.Member, error)
	CurrentUser(ctx context.Context) (redmine.Member, error)
	GetUser(ctx context.Context, id int) (redmine.Member, error)
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
	// childrenMsg carries the direct children of the list issues in parents.
	childrenMsg struct {
		gen     int
		parents []int
		issues  []redmine.Issue
		err     error
	}
	// subtreeMsg carries every issue below root, for a row expanded in the list.
	subtreeMsg struct {
		root   int
		issues []redmine.Issue
		err    error
	}
	// descendantsMsg carries the tree under the issue of detail uid.
	descendantsMsg struct {
		uid    int
		issues []redmine.Issue
		err    error
	}
	// relatedMsg carries the issues on the far side of the relations of detail uid.
	relatedMsg struct {
		uid    int
		issues []redmine.Issue
		err    error
	}
	customDefsMsg struct {
		defs []redmine.CustomFieldDef
		err  error
	}
	// userNamesMsg maps user ids to names; a user that could not be read maps to "".
	userNamesMsg struct{ names map[int]string }
	searchMsg    struct {
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
	openedMsg struct {
		id  int
		err error
	}
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

func fetchChildrenCmd(a API, gen int, parents []int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		issues, err := a.ListChildren(ctx, parents)
		return childrenMsg{gen: gen, parents: parents, issues: issues, err: err}
	}
}

func fetchSubtreeCmd(a API, root int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		issues, err := a.ListDescendants(ctx, root)
		return subtreeMsg{root: root, issues: issues, err: err}
	}
}

func fetchDescendantsCmd(a API, uid, id int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		issues, err := a.ListDescendants(ctx, id)
		return descendantsMsg{uid: uid, issues: issues, err: err}
	}
}

func fetchRelatedCmd(a API, uid int, ids []int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		issues, err := a.ListIssuesByID(ctx, ids)
		return relatedMsg{uid: uid, issues: issues, err: err}
	}
}

func fetchCustomDefsCmd(a API) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		defs, err := a.ListCustomFields(ctx)
		return customDefsMsg{defs: defs, err: err}
	}
}

func fetchUserNamesCmd(a API, ids []int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		names := make(map[int]string, len(ids))
		for _, id := range ids {
			u, err := a.GetUser(ctx, id)
			if err != nil {
				names[id] = ""
				continue
			}
			names[id] = u.Name
		}
		return userNamesMsg{names: names}
	}
}
