package tasks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

// openBlockers returns the IDs of an issue's blockers that are not yet closed.
//
// A blocker present in the hot index (idx) is open if its status is not closed.
// A blocker absent from the hot index is checked against the closed/ partition
// via a cheap vfs.Stat: if found there it is resolved (closed); if found in
// neither partition it is dangling — treated as unresolved to surface the
// inconsistency (dangling refs are rejected at write time by checkRefs and
// should not arise during ordinary ready/blocked computation). This satisfies
// TASK-STORAGE-SPEC §9: "A blocker that exists in closed/ counts as resolved."
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
			d.BlockedByRefs = append(d.BlockedByRefs, *r)
		}
	}
	// related is symmetric: RelatedRefs is the union of the forward edges stored
	// on this issue and the inverse edges (issues that list this one), deduped by
	// peer ID. relatedSeen tracks peers already added so the two passes don't
	// double-count a mutually-stored link.
	relatedSeen := make(map[string]bool, len(iss.Related))
	for _, relID := range iss.Related {
		r, err := resolveRef(relID)
		if err != nil {
			return nil, fmt.Errorf("resolve related ref %s: %w", relID, err)
		}
		if r != nil {
			d.RelatedRefs = append(d.RelatedRefs, *r)
			relatedSeen[relID] = true
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
		// Inverse related edge: other lists this issue → it is a related peer.
		if !relatedSeen[other.ID] {
			for _, rel := range other.Related {
				if rel == id {
					d.RelatedRefs = append(d.RelatedRefs, ref(other))
					relatedSeen[other.ID] = true
					break
				}
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
//
// Implementation: iterative 3-color DFS using an explicit frame stack to avoid
// stack overflow on deep dependency chains. Each frame records the node being
// visited and the index of the next blocker edge to process, faithfully
// simulating the original recursive descent without goroutine-stack recursion.
func findCycle(idx map[string]*Issue, start string) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)

	// dfsFrame holds the state for one DFS call.
	type dfsFrame struct {
		id      string
		edgeIdx int // index into idx[id].BlockedBy; next edge to process
	}

	color := map[string]int{}
	// path mirrors the "stack" in the original recursive version: it holds the
	// sequence of gray nodes on the current DFS path.
	var path []string

	// Seed the worklist with the starting node.
	if _, ok := idx[start]; !ok {
		return ""
	}

	worklist := []dfsFrame{{id: start, edgeIdx: 0}}
	color[start] = gray
	path = append(path, start)

	for len(worklist) > 0 {
		top := &worklist[len(worklist)-1]
		iss := idx[top.id]

		if top.edgeIdx < len(iss.BlockedBy) {
			b := iss.BlockedBy[top.edgeIdx]
			top.edgeIdx++

			switch color[b] {
			case gray:
				// Back-edge found: reconstruct the cycle path from b onward.
				for i, s := range path {
					if s == b {
						cycle := append(append([]string{}, path[i:]...), b)
						return strings.Join(cycle, " -> ")
					}
				}
				// b is gray but not in path (should not happen in a well-formed
				// graph, but be defensive).
				return strings.Join([]string{b, b}, " -> ")
			case white:
				if _, ok := idx[b]; ok {
					// Push a new frame for b.
					color[b] = gray
					path = append(path, b)
					worklist = append(worklist, dfsFrame{id: b, edgeIdx: 0})
				}
				// If b is not in idx, skip (same as the recursive version's
				// early return nil when iss is absent).
			}
			// black: already fully explored, skip.
		} else {
			// All edges from top.id have been processed — pop the frame.
			worklist = worklist[:len(worklist)-1]
			path = path[:len(path)-1]
			color[top.id] = black
		}
	}

	return ""
}

// Filter selects and orders issues for List.
//
// Scope semantics (TASK-STORAGE-SPEC §5, SDK-SPEC §4, QUERY-SPEC.md §5):
//   - By default only the hot (active) set is scanned. Closed issues in
//     closed/ are never read unless explicitly requested.
//   - Set IncludeClosed:true to read both hot and cold partitions.
//   - Set Expr to a filter expression (QUERY-SPEC.md); if the expression
//     references closed work (status == "closed", or a closed field comparison),
//     the cold partition is included automatically — IncludeClosed need not be
//     set explicitly in that case.
type Filter struct {
	Expr          string    // filter expression (QUERY-SPEC.md); closed-scope auto-detected
	IncludeClosed bool      // when true, read closed/ in addition to the hot set
	Sort          SortField // presentation order
	Reverse       bool      // reverse the sort order
	Offset        int       // matches to skip after sort/reverse, before Limit (0 = none); negatives clamp to 0
	Limit         int       // 0 = no limit; negatives clamp to 0
}

// Page is a windowed List result plus the total number of matches in scope
// (before Offset/Limit) — the value a paging viewer needs to size a scrollbar.
//
// The window and the total come from one directory snapshot (SDK-SPEC §4).
// Paging is NOT isolated across calls: a store mutated between page fetches can
// skip or repeat an item at a window boundary.
type Page struct {
	Issues []*Issue // the window: matches[Offset : Offset+Limit] (matches[Offset:] when Limit==0)
	Total  int      // total matches in scope, before Offset/Limit
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

// listMatches returns all matching issues for the filter after selection,
// sort, and reverse — but before offset/limit are applied. It is the shared
// core used by both List and ListPage. A *ParseError is returned for a
// malformed f.Expr before any disk access.
func (s *Store) listMatches(f Filter) ([]*Issue, error) {
	// Compile the expression first — return *ParseError before touching disk.
	pred, err := compileExpr(f.Expr)
	if err != nil {
		return nil, err
	}

	// Decide whether to include the closed partition.
	needClosed := f.IncludeClosed
	if !needClosed && f.Expr != "" {
		needClosed = query.ReferencesClosedWork(f.Expr)
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

		// Cross-partition read dedup (SDK-SPEC §7, TASK-STORAGE-SPEC §7):
		// a concurrent close/reopen (a cross-partition move) can briefly make
		// an issue visible in both the hot set and closed/. Dedup by ID, letting
		// the hot entry win, so the same issue never appears twice.
		seen := make(map[string]struct{}, len(all))
		deduped := all[:0]
		for _, iss := range all {
			if _, ok := seen[iss.ID]; ok {
				continue
			}
			seen[iss.ID] = struct{}{}
			deduped = append(deduped, iss)
		}
		all = deduped
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

	var matches []*Issue
	for _, iss := range all {
		// Scope guard: exclude closed issues unless the caller opted in.
		if iss.Status.IsClosed() && !needClosed {
			continue
		}

		// Expression filter: evaluate using the compiled predicate and the Row adapter.
		row := newIssueRow(iss, idx, closedStat)
		if !pred.Match(row) {
			continue
		}

		matches = append(matches, iss)
	}

	sortIssues(matches, f.Sort)
	if f.Reverse {
		for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
			matches[i], matches[j] = matches[j], matches[i]
		}
	}
	return matches, nil
}

// List returns issues matching the filter in the requested order.
//
// Scope (TASK-STORAGE-SPEC §5, SDK-SPEC §4, QUERY-SPEC.md §5):
//   - Default: hot (active) set only — closed/ is never opened.
//   - IncludeClosed:true: hot + cold partitions.
//   - f.Expr references closed work (status=="closed" or closed field comparison):
//     cold partition is auto-included.
//
// Callers must never depend on the cold partition being scanned silently —
// always set IncludeClosed or use an expression that opts in explicitly.
//
// A malformed f.Expr returns a *ParseError and nothing is read from disk.
//
// Offset and Limit are applied after sort/reverse; negative values clamp to 0.
func (s *Store) List(f Filter) ([]*Issue, error) {
	matches, err := s.listMatches(f)
	if err != nil {
		return nil, err
	}

	// Clamp negative offset/limit to 0.
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	limit := f.Limit
	if limit < 0 {
		limit = 0
	}

	// Apply offset.
	if offset >= len(matches) {
		return nil, nil
	}
	matches = matches[offset:]

	// Apply limit.
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// ListPage runs the same selection/sort/paging as List and additionally returns
// Total — the count of all matches in scope before Offset/Limit are applied.
// The window and total come from one directory snapshot (SDK-SPEC §4).
//
// Paging is NOT isolated across calls: a store mutated between page fetches can
// skip or repeat an item at a window boundary.
//
// Negative Offset/Limit clamp to 0. When Offset >= Total, Issues is empty and
// Total still reflects the actual count of matches in scope.
func (s *Store) ListPage(f Filter) (Page, error) {
	matches, err := s.listMatches(f)
	if err != nil {
		return Page{}, err
	}
	total := len(matches)

	// Clamp negative offset/limit to 0.
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	limit := f.Limit
	if limit < 0 {
		limit = 0
	}

	// Apply offset.
	if offset >= len(matches) {
		return Page{Total: total}, nil
	}
	matches = matches[offset:]

	// Apply limit.
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return Page{Issues: matches, Total: total}, nil
}

func sortIssues(issues []*Issue, field SortField) {
	switch field {
	case SortID:
		sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	case SortPriority:
		sort.Slice(issues, func(i, j int) bool { return less(issues[i], issues[j]) })
	case SortCreated:
		sort.Slice(issues, func(i, j int) bool {
			a, b := issues[i], issues[j]
			if !a.Created.Equal(b.Created) {
				return a.Created.After(b.Created)
			}
			return a.ID < b.ID
		})
	case SortUpdated:
		sort.Slice(issues, func(i, j int) bool {
			a, b := issues[i], issues[j]
			if !a.Updated.Equal(b.Updated) {
				return a.Updated.After(b.Updated)
			}
			return a.ID < b.ID
		})
	case SortClosed:
		sort.Slice(issues, func(i, j int) bool {
			a, b := issues[i], issues[j]
			if !a.Closed.Equal(b.Closed) {
				return a.Closed.After(b.Closed)
			}
			return a.ID < b.ID
		})
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
