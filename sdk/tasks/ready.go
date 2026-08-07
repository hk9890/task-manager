// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// ready.go — the pure graph rules behind ready/blocked/list.
//
// Everything here is a plain function over values: an in-memory index of issues
// and a caller-supplied predicate for "is this ID closed?". Nothing in this file
// touches the filesystem, directly or through a *Store, which is what lets the
// rules be unit-tested at L1 with no store at all. The methods that fetch the
// index and answer that predicate live in list.go, the imperative shell.
//
// The boundary is enforced by TestImportBoundary_PureCoreNoVfs, which fails a
// pure-core file that imports the vfs seam OR declares a method on *Store —
// a method reaches the seam through s.fs, which no import list reveals.

package tasks

import (
	"sort"
	"strings"
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

// BlockedIssue pairs a blocked issue with the open blockers holding it back.
type BlockedIssue struct {
	Issue     *Issue
	BlockedBy []Ref
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

// window applies the offset/limit paging rules shared by List and ListPage:
// negative offset/limit clamp to 0, an offset past the end yields nil, and a
// positive limit caps the returned slice.
func window(matches []*Issue, offset, limit int) []*Issue {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if offset >= len(matches) {
		return nil
	}
	matches = matches[offset:]
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
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
