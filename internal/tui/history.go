package tui

import (
	"strconv"
	"strings"

	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

const maxValueRunes = 60

// resolver translates the raw ids found in journal details into readable names.
type resolver struct {
	tr       i18n.Translator
	statuses map[int]redmine.Status
	users    map[int]string
	fields   map[int]string
}

// newResolver collects every name the issue and the status list can provide.
func newResolver(tr i18n.Translator, issue *redmine.Issue, statuses map[int]redmine.Status) resolver {
	r := resolver{tr: tr, statuses: statuses, users: map[int]string{}, fields: map[int]string{}}
	if issue == nil {
		return r
	}
	r.users[issue.Author.ID] = issue.Author.Name
	if issue.AssignedTo != nil {
		r.users[issue.AssignedTo.ID] = issue.AssignedTo.Name
	}
	for _, j := range issue.Journals {
		r.users[j.User.ID] = j.User.Name
	}
	for _, cf := range issue.CustomFields {
		r.fields[cf.ID] = cf.Name
	}
	return r
}

// describe turns one journal detail into a short phrase in the interface language.
func (r resolver) describe(d redmine.JournalDetail) string {
	switch d.Property {
	case "attr":
		return r.describeAttr(d)
	case "cf":
		return r.describeCustomField(d)
	case "attachment":
		return r.tr.T("hist.attachment", valueOr(d.NewValue, valueOr(d.OldValue, "-")))
	case "relation":
		return r.tr.T("hist.relation")
	default:
		return r.tr.T("hist.attr_changed", d.Name)
	}
}

func (r resolver) describeAttr(d redmine.JournalDetail) string {
	switch d.Name {
	case "status_id":
		return r.tr.T("hist.status", r.change(d, r.statusName))
	case "done_ratio":
		return r.tr.T("hist.done", r.change(d, nil))
	case "assigned_to_id":
		if !r.resolvable(d.OldValue, r.userName) || !r.resolvable(d.NewValue, r.userName) {
			return r.tr.T("hist.assigned_changed")
		}
		return r.tr.T("hist.assigned", r.change(d, r.userName))
	case "priority_id":
		return r.tr.T("hist.priority_changed")
	case "subject":
		return r.tr.T("hist.subject_changed")
	case "description":
		return r.tr.T("hist.description_updated")
	case "start_date":
		return r.tr.T("hist.start", r.change(d, nil))
	case "due_date":
		return r.tr.T("hist.due", r.change(d, nil))
	case "parent_id":
		return r.tr.T("hist.parent", r.change(d, hashID))
	case "tracker_id":
		return r.tr.T("hist.tracker_changed")
	case "project_id":
		return r.tr.T("hist.project_changed")
	default:
		return r.tr.T("hist.attr_changed", d.Name)
	}
}

func (r resolver) describeCustomField(d redmine.JournalDetail) string {
	name := r.tr.T("hist.field_unknown", d.Name)
	if id, err := strconv.Atoi(d.Name); err == nil {
		if n, ok := r.fields[id]; ok {
			name = n
		}
	}
	return name + ": " + r.change(d, nil)
}

// change renders "old → new"; a nil mapper keeps values as they are.
func (r resolver) change(d redmine.JournalDetail, mapper func(string) (string, bool)) string {
	return r.show(d.OldValue, mapper) + " → " + r.show(d.NewValue, mapper)
}

func (r resolver) show(v *string, mapper func(string) (string, bool)) string {
	if v == nil || *v == "" {
		return r.tr.T("hist.empty_value")
	}
	out := *v
	if mapper != nil {
		if name, ok := mapper(*v); ok {
			out = name
		}
	}
	runes := []rune(out)
	if len(runes) > maxValueRunes {
		out = string(runes[:maxValueRunes-1]) + "…"
	}
	return out
}

// resolvable reports whether v is empty or maps to a known name.
func (r resolver) resolvable(v *string, mapper func(string) (string, bool)) bool {
	if v == nil || *v == "" {
		return true
	}
	_, ok := mapper(*v)
	return ok
}

func (r resolver) statusName(raw string) (string, bool) {
	id, err := strconv.Atoi(raw)
	if err != nil {
		return "", false
	}
	s, ok := r.statuses[id]
	return s.Name, ok
}

func (r resolver) userName(raw string) (string, bool) {
	id, err := strconv.Atoi(raw)
	if err != nil {
		return "", false
	}
	n, ok := r.users[id]
	return n, ok
}

func hashID(raw string) (string, bool) { return "#" + raw, true }

func valueOr(v *string, fallback string) string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return fallback
	}
	return *v
}
