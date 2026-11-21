package redmine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// IDName is the {id, name} pair Redmine uses for most references.
type IDName struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Status is an issue status.
type Status struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsClosed  bool   `json:"is_closed"`
	IsDefault bool   `json:"is_default"`
}

// CustomField is a custom field value as returned by Redmine.
type CustomField struct {
	ID    int             `json:"id"`
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// Text flattens the value (string, null or array) into display text.
func (c CustomField) Text() string {
	if len(c.Value) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(c.Value, &v); err != nil {
		return ""
	}
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := strings.TrimSpace(fmt.Sprint(e)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(t)
	}
}

// Issue is a Redmine issue. Journals, Children, Relations and AllowedStatuses
// are only filled by GetIssue. AllowedStatuses is nil when the instance does not
// report it (Redmine before 5.0) and non-nil, possibly empty, when it does.
type Issue struct {
	ID           int        `json:"id"`
	Subject      string     `json:"subject"`
	Project      IDName     `json:"project"`
	Tracker      IDName     `json:"tracker"`
	Status       Status     `json:"status"`
	Priority     IDName     `json:"priority"`
	Author       IDName     `json:"author"`
	AssignedTo   *IDName    `json:"assigned_to"`
	Parent       *ParentRef `json:"parent"`
	StartDate    *string    `json:"start_date"`
	DueDate      *string    `json:"due_date"`
	DoneRatio    int        `json:"done_ratio"`
	FixedVersion *IDName    `json:"fixed_version"`
	Category     *IDName    `json:"category"`
	// EstimatedHours and SpentHours are nil when the instance does not track time or
	// nothing was recorded.
	EstimatedHours *float64      `json:"estimated_hours"`
	SpentHours     *float64      `json:"spent_hours"`
	CreatedOn      time.Time     `json:"created_on"`
	UpdatedOn      time.Time     `json:"updated_on"`
	ClosedOn       *time.Time    `json:"closed_on"`
	Description    string        `json:"description"`
	CustomFields   []CustomField `json:"custom_fields"`
	Journals       []Journal     `json:"journals"`
	Children       []ChildIssue  `json:"children"`
	Relations      []Relation    `json:"relations"`

	AllowedStatuses []Status `json:"allowed_statuses"`
}

// ParentRef points at the parent issue.
type ParentRef struct {
	ID int `json:"id"`
}

// ChildIssue is a child as listed inside its parent.
type ChildIssue struct {
	ID      int    `json:"id"`
	Tracker IDName `json:"tracker"`
	Subject string `json:"subject"`
}

// Relation links two issues.
type Relation struct {
	ID           int    `json:"id"`
	IssueID      int    `json:"issue_id"`
	IssueToID    int    `json:"issue_to_id"`
	RelationType string `json:"relation_type"`
}

// Other returns the issue on the far side of the relation as seen from self.
func (r Relation) Other(self int) int {
	if r.IssueID == self {
		return r.IssueToID
	}
	return r.IssueID
}

// Journal is one entry of an issue's history.
type Journal struct {
	ID        int             `json:"id"`
	User      IDName          `json:"user"`
	Notes     string          `json:"notes"`
	CreatedOn time.Time       `json:"created_on"`
	Details   []JournalDetail `json:"details"`
}

// JournalDetail is a single attribute change inside a journal.
type JournalDetail struct {
	Property string  `json:"property"`
	Name     string  `json:"name"`
	OldValue *string `json:"old_value"`
	NewValue *string `json:"new_value"`
}

// Project is a Redmine project. Trackers and IssueCustomFields are only filled
// by GetProject.
type Project struct {
	ID                int      `json:"id"`
	Name              string   `json:"name"`
	Identifier        string   `json:"identifier"`
	Trackers          []IDName `json:"trackers"`
	IssueCustomFields []IDName `json:"issue_custom_fields"`
}

// Member is a user that belongs to a project.
type Member struct {
	ID   int
	Name string
}

// Priority is an issue priority enumeration value.
type Priority struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Active    *bool  `json:"active"`
}

// IsActive reports whether the priority can be chosen; instances that do not
// report the flag are assumed to have it active.
func (p Priority) IsActive() bool { return p.Active == nil || *p.Active }

// PossibleValue is one option of a list custom field.
type PossibleValue struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// CustomFieldDef describes a custom field as returned by /custom_fields.json.
type CustomFieldDef struct {
	ID             int             `json:"id"`
	Name           string          `json:"name"`
	CustomizedType string          `json:"customized_type"`
	FieldFormat    string          `json:"field_format"`
	IsRequired     bool            `json:"is_required"`
	Multiple       bool            `json:"multiple"`
	DefaultValue   string          `json:"default_value"`
	PossibleValues []PossibleValue `json:"possible_values"`
	Trackers       []IDName        `json:"trackers"`
}

// AppliesToTracker reports whether the field is enabled for the tracker.
func (d CustomFieldDef) AppliesToTracker(trackerID int) bool {
	for _, t := range d.Trackers {
		if t.ID == trackerID {
			return true
		}
	}
	return false
}

// SearchResult is one hit of the full-text search.
type SearchResult struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Type        string    `json:"type"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Datetime    time.Time `json:"datetime"`
}

// StatusOpen, StatusClosed and StatusAny are the status_id values Redmine understands
// besides a concrete status id.
const (
	StatusOpen   = "open"
	StatusClosed = "closed"
	StatusAny    = "*"
)

// ListQuery filters a list of issues. Every field becomes a query parameter, so the
// filtering happens in Redmine. Zero values are left out, except Status, which
// defaults to open issues. Assigned, Author and Watcher take "me" or a user id.
type ListQuery struct {
	Assigned  string
	Author    string
	Watcher   string
	ProjectID int
	TrackerID int
	Status    string
	// Sort is a Redmine sort expression; empty means most recently updated first.
	Sort string
	// WithDueDate keeps only issues that have a due date. Sorting by due date needs it:
	// Redmine puts the undated issues first on an ascending sort.
	WithDueDate bool
}

// IssuePage is one page of issues and the total Redmine reports for the whole query.
type IssuePage struct {
	Issues []Issue
	Total  int
}

// IssueUpdate is a change to an existing issue. Nil and empty fields are left out of
// the request. AssignedToID points at the new assignee; a pointer to 0 unassigns.
type IssueUpdate struct {
	StatusID     *int
	DoneRatio    *int
	AssignedToID *int
	Notes        string
}

// CustomValue is the value sent for one custom field: a string, or a []string for
// multi-value fields.
type CustomValue struct {
	ID    int
	Value any
}

// NewIssue is the data to create an issue. Zero ids are left out of the request.
type NewIssue struct {
	ProjectID    int
	TrackerID    int
	Subject      string
	Description  string
	PriorityID   int
	AssignedToID int
	ParentID     int
	CustomFields []CustomValue
}
