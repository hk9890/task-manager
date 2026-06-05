package tasks

import (
	"fmt"
	"sort"
	"strings"
)

// openBlockers returns the IDs of an issue's blockers that are not yet closed.
//
// A blocker present in the hot index (idx) is open if its status is not closed.
// A blocker absent from the hot index is checked against the closed/ partition
// via a cheap vfs.Stat: if found there it is resolved (closed); if found in
// neither partition it is dangling — also treated as resolved here because
// dangling refs are rejected at write time by checkRefs and should not arise
// during ordinary ready/blocked computation. This satisfies TASK-STORAGE-SPEC
// §9: "A blocker that exists in closed/ counts as resolved."
func openBlockers(idx map[string]*Issue, closedStat func(id string) bool, iss *Issue) []string {
	var open []string
	for _, b := range iss.BlockedBy {
		blk, ok := idx[b]
		if !ok {
			// Not in the hot set. If it's in closed/ it is resolved; otherwise
			// treat as resolved too (dangling refs cannot reach here in a valid
			// store — checkRefs prevents them at write time).
			if !closedStat(b) {
				// Dangling: not in hot, not in closed. Per spec this should have
				// been caught at write time; treat as unresolved to surface the
				// inconsistency rather than silently marking the issue as ready.
				open = append(open, b)
			}
			// Found in closed/ → resolved; skip.
			continue
		}
		if !blk.Status.IsClosed() {
			open = append(open, b)
		}
	}
	return open
}

// closedStatFn returns a function that checks whether an issue ID exists in the
// closed/ partition using a cheap vfs.Stat (no parse). The returned function is
// safe to call multiple times; each call performs one Stat.
func (s *Store) closedStatFn() func(id string) bool {
	return func(id string) bool {
		_, err := s.fs.Stat(s.closedFilePath(id))
		return err == nil
	}
}

// Ready returns open issues with no unresolved blockers, ordered by priority
// (most urgent first) then creation time.
func (s *Store) Ready() ([]*Issue, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	closedStat := s.closedStatFn()
	var ready []*Issue
	for _, iss := range all {
		if iss.Status != StatusOpen {
			continue
		}
		if len(openBlockers(idx, closedStat, iss)) == 0 {
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
	closedStat := s.closedStatFn()
	var blocked []BlockedIssue
	for _, iss := range all {
		if iss.Status.IsClosed() {
			continue
		}
		open := openBlockers(idx, closedStat, iss)
		if len(open) == 0 {
			continue
		}
		bi := BlockedIssue{Issue: iss}
		for _, id := range open {
			if blk, ok := idx[id]; ok {
				bi.BlockedBy = append(bi.BlockedBy, ref(blk))
			}
			// Dangling blockers are included in open (see openBlockers) but
			// cannot be resolved to a ref — they are omitted from BlockedBy
			// refs. The issue still appears in Blocked to surface the inconsistency.
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
// Detail falls through to closed/ when the issue is not in the hot set.
//
// Ref resolution falls through to closed/ (SDK-SPEC §4): if a parent, blocker,
// or related ref is not found in the hot index, Detail calls Get (which already
// handles the hot→closed fall-through) and populates the ref from the closed
// issue's metadata.
func (s *Store) Detail(id string) (*Detail, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	iss, ok := idx[id]
	if !ok {
		// Fall through to closed/.
		iss, err = s.Get(id)
		if err != nil {
			return nil, err
		}
	}
	d := &Detail{Issue: *iss}

	// resolveRef returns a Ref for id, first from the hot index and, if absent,
	// by falling through to closed/ via Get (cheap: closed reads are lock-free).
	resolveRef := func(refID string) (*Ref, error) {
		if x, ok := idx[refID]; ok {
			r := ref(x)
			return &r, nil
		}
		// Not in hot set — try closed/.
		x, err := s.Get(refID)
		if err != nil {
			// Truly dangling (should not happen post-checkRefs, but be defensive).
			return nil, nil //nolint:nilerr
		}
		r := ref(x)
		return &r, nil
	}

	if iss.Parent != "" {
		r, err := resolveRef(iss.Parent)
		if err != nil {
			return nil, fmt.Errorf("resolve parent ref %s: %w", iss.Parent, err)
		}
		d.ParentRef = r
	}
	for _, b := range iss.BlockedBy {
		r, err := resolveRef(b)
		if err != nil {
			return nil, fmt.Errorf("resolve blocker ref %s: %w", b, err)
		}
		if r != nil {
			d.BlockedBy = append(d.BlockedBy, *r)
		}
	}
	for _, relID := range iss.Related {
		r, err := resolveRef(relID)
		if err != nil {
			return nil, fmt.Errorf("resolve related ref %s: %w", relID, err)
		}
		if r != nil {
			d.Related = append(d.Related, *r)
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
//
// Scope semantics (TASK-STORAGE-SPEC §5, SDK-SPEC §4):
//   - By default only the hot (active) set is scanned. Closed issues in
//     closed/ are never read unless explicitly requested.
//   - Set IncludeClosed:true to read both hot and cold partitions.
//   - Set Expr to a filter expression (QUERY-SPEC.md); if the expression
//     references closed work (status == "closed", or a closed comparison),
//     the cold partition is included automatically — the caller does not need
//     to set IncludeClosed explicitly in that case.
//   - Expr and the structured fields (Statuses, Types, …) can be combined;
//     both must match for an issue to be returned.
type Filter struct {
	Expr          string    // filter expression (QUERY-SPEC.md); closed-scope auto-detected
	Statuses      []Status
	Types         []Type
	PriorityMin   *int
	PriorityMax   *int
	Assignee      string
	Labels        []string // issue must carry every listed label
	Text          string   // case-insensitive substring of ID, title, or body
	IncludeClosed bool     // when true, read closed/ in addition to the hot set
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

// Query selects issues using a filter expression (QUERY-SPEC.md §1).
//
// Scope semantics (QUERY-SPEC.md §5):
//   - The closed/ partition is included automatically when the expression
//     references closed work: a status == "closed" comparison, or any
//     comparison against the closed field (e.g. closed > "2026-01-01").
//   - All other expressions operate on the hot (active) set only.
//
// An empty or whitespace-only expression matches every issue in scope (the
// always-true predicate — see QUERY-SPEC.md §1).
//
// This method is equivalent to List(Filter{Expr: expr}); callers that also
// need sort/limit control should use List with an Expr set in the Filter.
func (s *Store) Query(expr string) ([]*Issue, error) {
	return s.List(Filter{Expr: expr})
}

// exprReferencesClosedWork reports whether expr contains a reference to closed
// work that requires the cold partition to be scanned. It detects:
//   - status == "closed" (or status=="closed" without spaces)
//   - any comparison against the closed timestamp field (e.g. closed > "2026-01-01")
//
// This is a lightweight textual scan — it is not a full parse. It errs on the
// side of including the cold partition (no false negatives) because the
// performance cost of a redundant closed/ scan is acceptable, and silently
// excluding the cold partition would violate correctness.
func exprReferencesClosedWork(expr string) bool {
	e := strings.ToLower(strings.TrimSpace(expr))
	if e == "" {
		return false
	}
	// status == "closed" or status=="closed"
	if strings.Contains(e, `status`) && strings.Contains(e, `closed`) {
		return true
	}
	// closed field comparisons: closed > ..., closed < ..., closed == ..., etc.
	// Look for the word "closed" followed (possibly after spaces) by a comparison
	// operator, or the field name "closed" at the start of a comparison token.
	// Heuristic: the field name "closed" followed by whitespace or an operator.
	for i, r := range e {
		_ = r
		const word = "closed"
		if i+len(word) > len(e) {
			break
		}
		if e[i:i+len(word)] != word {
			continue
		}
		// Make sure it is a whole word (not a prefix of "closedXxx").
		after := i + len(word)
		if after < len(e) {
			ch := e[after]
			if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				continue // not a word boundary
			}
		}
		// Before must be a non-word character or start of string.
		if i > 0 {
			ch := e[i-1]
			if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				continue
			}
		}
		// "closed" as a standalone word — check if it looks like a field comparison.
		rest := strings.TrimSpace(e[after:])
		if len(rest) > 0 {
			ch := rest[0]
			if ch == '=' || ch == '!' || ch == '<' || ch == '>' || ch == '~' {
				return true // closed field comparison
			}
		}
	}
	return false
}

// List returns issues matching the filter in the requested order.
//
// Scope (TASK-STORAGE-SPEC §5, SDK-SPEC §4, QUERY-SPEC.md §5):
//   - Default: hot (active) set only — closed/ is never opened.
//   - IncludeClosed:true: hot + cold partitions.
//   - f.Expr references closed work (status=="closed" or closed field comparison):
//     cold partition is auto-included.
//   - f.Statuses contains StatusClosed: cold partition is auto-included.
//
// Callers must never depend on the cold partition being scanned silently —
// always set IncludeClosed or use an expression that opts in explicitly.
func (s *Store) List(f Filter) ([]*Issue, error) {
	// Decide whether to include the closed partition.
	needClosed := f.IncludeClosed
	if !needClosed {
		for _, st := range f.Statuses {
			if st.IsClosed() {
				needClosed = true
				break
			}
		}
	}
	if !needClosed && f.Expr != "" {
		needClosed = exprReferencesClosedWork(f.Expr)
	}

	_, all, err := s.index()
	if err != nil {
		return nil, err
	}

	if needClosed {
		closed, err := s.allClosed()
		if err != nil {
			return nil, err
		}
		all = append(all, closed...)
	}

	// Rebuild idx from all (including closed if loaded).
	idx := make(map[string]*Issue, len(all))
	for _, iss := range all {
		idx[iss.ID] = iss
	}

	// closedStat is used by openBlockers to check whether a blocker not in the
	// hot index lives in the closed/ partition. When needClosed is true the idx
	// already contains closed issues (Stat would be redundant but harmless).
	closedStat := s.closedStatFn()

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
		// Scope guard: exclude closed issues unless the caller opted in (via
		// IncludeClosed, a StatusClosed filter, or a closed-referencing Expr).
		if iss.Status.IsClosed() && !needClosed {
			continue
		}

		// Structured-field filters.
		if len(statusSet) > 0 {
			if _, ok := statusSet[iss.Status]; !ok {
				continue
			}
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
		if f.OnlyReady && !(iss.Status == StatusOpen && len(openBlockers(idx, closedStat, iss)) == 0) {
			continue
		}
		if f.OnlyBlocked && len(openBlockers(idx, closedStat, iss)) == 0 {
			continue
		}

		// Expression filter (QUERY-SPEC.md): applied after all structured-field
		// guards so the expression is only evaluated for candidates that already
		// pass the cheaper structured filters.
		if f.Expr != "" && !evalExpr(f.Expr, iss, idx, closedStat) {
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
