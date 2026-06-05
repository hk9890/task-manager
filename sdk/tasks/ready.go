package tasks

import (
	"fmt"
	"sort"
	"strings"
)

// openBlockers returns the IDs of an issue's blockers that are not yet closed.
// A blocker that no longer exists is treated as resolved.
func openBlockers(idx map[string]*Issue, iss *Issue) []string {
	var open []string
	for _, b := range iss.BlockedBy {
		blk, ok := idx[b]
		if !ok {
			continue
		}
		if !blk.Status.IsClosed() {
			open = append(open, b)
		}
	}
	return open
}

// Ready returns open issues with no unresolved blockers, ordered by priority
// (most urgent first) then creation time.
func (s *Store) Ready() ([]*Issue, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	var ready []*Issue
	for _, iss := range all {
		if iss.Status != StatusOpen {
			continue
		}
		if len(openBlockers(idx, iss)) == 0 {
			ready = append(ready, iss)
		}
	}
	sortByWork(ready)
	return ready, nil
}

// BlockedIssue pairs a blocked issue with the open blockers holding it back.
type BlockedIssue struct {
	Issue     *Issue
	BlockedBy []Ref
}

// Blocked returns non-closed issues that have at least one open blocker, with
// the blocking issues resolved to refs.
func (s *Store) Blocked() ([]BlockedIssue, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	var blocked []BlockedIssue
	for _, iss := range all {
		if iss.Status.IsClosed() {
			continue
		}
		open := openBlockers(idx, iss)
		if len(open) == 0 {
			continue
		}
		bi := BlockedIssue{Issue: iss}
		for _, id := range open {
			bi.BlockedBy = append(bi.BlockedBy, ref(idx[id]))
		}
		blocked = append(blocked, bi)
	}
	sort.Slice(blocked, func(i, j int) bool {
		return less(blocked[i].Issue, blocked[j].Issue)
	})
	return blocked, nil
}

// Detail loads an issue and resolves both its outgoing references and its
// derived inverse edges (children, blocks). It also loads the comment sidecar
// lazily and populates Detail.Comments with the resolved effective log.
func (s *Store) Detail(id string) (*Detail, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	iss, ok := idx[id]
	if !ok {
		return nil, errNotFound(id)
	}
	d := &Detail{Issue: *iss}
	if iss.Parent != "" {
		if p, ok := idx[iss.Parent]; ok {
			r := ref(p)
			d.ParentRef = &r
		}
	}
	for _, b := range iss.BlockedBy {
		if x, ok := idx[b]; ok {
			d.BlockedBy = append(d.BlockedBy, ref(x))
		}
	}
	for _, r := range iss.Related {
		if x, ok := idx[r]; ok {
			d.Related = append(d.Related, ref(x))
		}
	}
	for _, other := range all {
		if other.ID == id {
			continue
		}
		if other.Parent == id {
			d.Children = append(d.Children, ref(other))
		}
		for _, b := range other.BlockedBy {
			if b == id {
				d.Blocks = append(d.Blocks, ref(other))
			}
		}
	}
	// Load comments from the sidecar (lazy; zero cost for All/Ready/List).
	stream, err := readCommentStream(s.fs, s.commentsPath(id))
	if err != nil {
		return nil, fmt.Errorf("load comments for %s: %w", id, err)
	}
	d.Comments = resolveComments(stream)
	return d, nil
}

// findCycle returns a human-readable cycle path if following BlockedBy edges
// from start leads back into the current traversal, or "" if acyclic.
func findCycle(idx map[string]*Issue, start string) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string

	var visit func(id string) []string
	visit = func(id string) []string {
		iss, ok := idx[id]
		if !ok {
			return nil
		}
		color[id] = gray
		stack = append(stack, id)
		for _, b := range iss.BlockedBy {
			switch color[b] {
			case gray:
				// Found a back-edge: slice the stack from b onward.
				for i, s := range stack {
					if s == b {
						return append(append([]string{}, stack[i:]...), b)
					}
				}
				return []string{b, b}
			case white:
				if c := visit(b); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}

	if c := visit(start); c != nil {
		return strings.Join(c, " -> ")
	}
	return ""
}

// Filter selects and orders issues for List.
type Filter struct {
	Statuses      []Status
	Types         []Type
	PriorityMin   *int
	PriorityMax   *int
	Assignee      string
	Labels        []string // issue must carry every listed label
	Text          string   // case-insensitive substring of ID, title, or body
	IncludeClosed bool     // when Statuses is empty, whether to include closed issues
	OnlyReady     bool
	OnlyBlocked   bool
	Sort          SortField
	Reverse       bool
	Limit         int
}

// SortField names the orderings List understands.
type SortField string

const (
	SortWork     SortField = "" // priority then created (default)
	SortID       SortField = "id"
	SortPriority SortField = "priority"
	SortCreated  SortField = "created"
	SortUpdated  SortField = "updated"
	SortClosed   SortField = "closed"
)

// List returns issues matching the filter in the requested order.
func (s *Store) List(f Filter) ([]*Issue, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}

	statusSet := map[Status]struct{}{}
	for _, st := range f.Statuses {
		statusSet[st] = struct{}{}
	}
	typeSet := map[Type]struct{}{}
	for _, t := range f.Types {
		typeSet[t] = struct{}{}
	}
	text := strings.ToLower(strings.TrimSpace(f.Text))

	var out []*Issue
	for _, iss := range all {
		if len(statusSet) > 0 {
			if _, ok := statusSet[iss.Status]; !ok {
				continue
			}
		} else if iss.Status.IsClosed() && !f.IncludeClosed {
			continue
		}
		if len(typeSet) > 0 {
			if _, ok := typeSet[iss.Type]; !ok {
				continue
			}
		}
		if f.PriorityMin != nil && iss.Priority < *f.PriorityMin {
			continue
		}
		if f.PriorityMax != nil && iss.Priority > *f.PriorityMax {
			continue
		}
		if f.Assignee != "" && iss.Assignee != f.Assignee {
			continue
		}
		if !hasAllLabels(iss, f.Labels) {
			continue
		}
		if text != "" && !matchesText(iss, text) {
			continue
		}
		if f.OnlyReady && !(iss.Status == StatusOpen && len(openBlockers(idx, iss)) == 0) {
			continue
		}
		if f.OnlyBlocked && len(openBlockers(idx, iss)) == 0 {
			continue
		}
		out = append(out, iss)
	}

	sortIssues(out, f.Sort)
	if f.Reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func hasAllLabels(iss *Issue, want []string) bool {
	if len(want) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(iss.Labels))
	for _, l := range iss.Labels {
		have[l] = struct{}{}
	}
	for _, w := range want {
		if _, ok := have[w]; !ok {
			return false
		}
	}
	return true
}

func matchesText(iss *Issue, lowered string) bool {
	return strings.Contains(strings.ToLower(iss.ID), lowered) ||
		strings.Contains(strings.ToLower(iss.Title), lowered) ||
		strings.Contains(strings.ToLower(iss.Description), lowered)
}

func sortIssues(issues []*Issue, field SortField) {
	switch field {
	case SortID:
		sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	case SortPriority:
		sort.Slice(issues, func(i, j int) bool { return less(issues[i], issues[j]) })
	case SortCreated:
		sort.Slice(issues, func(i, j int) bool { return issues[i].Created.After(issues[j].Created) })
	case SortUpdated:
		sort.Slice(issues, func(i, j int) bool { return issues[i].Updated.After(issues[j].Updated) })
	case SortClosed:
		sort.Slice(issues, func(i, j int) bool { return issues[i].Closed.After(issues[j].Closed) })
	default:
		sortByWork(issues)
	}
}

// sortByWork orders by priority (most urgent first), then oldest first.
func sortByWork(issues []*Issue) {
	sort.Slice(issues, func(i, j int) bool { return less(issues[i], issues[j]) })
}

func less(a, b *Issue) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if !a.Created.Equal(b.Created) {
		return a.Created.Before(b.Created)
	}
	return a.ID < b.ID
}
