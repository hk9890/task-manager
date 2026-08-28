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

// list.go — the imperative shell over the pure rules in ready.go.
//
// These are the *Store methods behind Ready, Blocked, Detail, List and
// ListPage. Each one does the same two things: read what it needs through the
// s.fs seam (the hot index, the closed/ partition, a comment sidecar), then hand
// values to the pure functions in ready.go to decide the answer.
//
// Anything that can be expressed as a function over values belongs there, not
// here — see the file comment in ready.go for why the split is load-bearing.

package tasks

import (
	"errors"
	"fmt"
	"sort"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

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
//
// Documents (type doc) are never ready: they are not work, so an open one would
// otherwise sit at the top of the queue forever (TASK-STORAGE-SPEC §9). The
// exclusion lives here, in the engine, rather than in a caller's filter, so
// every consumer — the CLI, the `ready` query predicate, any future UI — agrees
// without having to remember it.
func (s *Store) Ready() ([]*Issue, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	closedStat := s.closedStatFn()
	var ready []*Issue
	for _, iss := range all {
		if isReady(iss, openBlockers(idx, closedStat, iss)) {
			ready = append(ready, iss)
		}
	}
	sortByWork(ready)
	return ready, nil
}

// Blocked returns non-closed issues that have at least one open blocker, with
// the blocking issues resolved to refs.
//
// Documents (type doc) are excluded for the same reason they are excluded from
// Ready: they are not work, so "blocked work" should never surface one
// (TASK-STORAGE-SPEC §9).
func (s *Store) Blocked() ([]BlockedIssue, error) {
	idx, all, err := s.index()
	if err != nil {
		return nil, err
	}
	closedStat := s.closedStatFn()
	var blocked []BlockedIssue
	for _, iss := range all {
		open := openBlockers(idx, closedStat, iss)
		if !isBlocked(iss, open) {
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
		// Fall through to closed/. Unresolved on purpose: Get would resolve the
		// body here and clear the flag, so BodyExternal below would read false
		// for every closed overflowed issue — the one class of issue most likely
		// to be overflowed. Detail resolves the body itself, after recording it.
		iss, err = s.getUnresolved(id)
		if err != nil {
			return nil, err
		}
	}
	d := &Detail{Issue: *iss}
	// Both sources — the index and getUnresolved — are unresolved reads, so an
	// overflowed body arrives empty. Detail is a single-issue path and resolves
	// it, like Get. BodyExternal records where the bytes actually live, which the
	// embedded Issue can no longer say once its flag is cleared — a viewer uses
	// it to warn before rendering a huge body.
	d.BodyExternal = d.bodyExternal
	if err := s.resolveBody(&d.Issue); err != nil {
		return nil, err
	}

	// resolveRef returns a Ref for id, first from the hot index and, if absent,
	// by falling through to closed/ (cheap: closed reads are lock-free).
	//
	// The fall-through is an unresolved read: a Ref carries five metadata fields
	// and no body, so going through Get would read a closed doc's entire sidecar
	// to fill them in. Only a genuinely dangling ref is dropped; an I/O failure
	// is returned, because swallowing it omitted the ref from the rendered issue
	// — a blocked issue would print with no Blocked-by line and exit 0.
	resolveRef := func(refID string) (*Ref, error) {
		if x, ok := idx[refID]; ok {
			r := ref(x)
			return &r, nil
		}
		x, err := s.getUnresolved(refID)
		if errors.Is(err, ErrNotFound) {
			// Dangling: checkRefs rejects these at write time, so this is a store
			// edited by hand. Drop the ref rather than fail the whole read.
			return nil, nil
		}
		if err != nil {
			return nil, err
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

// listMatches returns all matching issues for the filter after selection,
// sort, and reverse — but before offset/limit are applied. It is the shared
// core used by both List and ListPage. A *ParseError is returned for a
// malformed f.Expr before any disk access.
func (s *Store) listMatches(f Filter) ([]*Issue, error) {
	// Compile the expression first — return *ParseError before touching disk.
	// An empty or whitespace-only expression compiles to the always-true
	// predicate; a malformed one yields a *ParseError.
	pred, err := query.Compile(f.Expr)
	if err != nil {
		return nil, err
	}

	// Decide whether to include the closed partition. The expression's own
	// answer rides along on the compiled predicate (query.Compiled), so it
	// cannot disagree with the predicate that is about to be evaluated.
	needClosed := f.IncludeClosed || pred.NeedsClosed

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

	// The "text" virtual field spans the body (QUERY-SPEC §2), so an expression
	// that uses it must see overflowed bodies too — otherwise a large issue would
	// be silently unmatchable. Sidecars are read ONLY for such an expression:
	// structured filters never look at a body, and neither should they pay to.
	needText := pred.NeedsText

	var matches []*Issue
	for _, iss := range all {
		// Scope guard: exclude closed issues unless the caller opted in.
		if iss.Status.IsClosed() && !needClosed {
			continue
		}

		// Expression filter: evaluate using the compiled predicate and the Row
		// adapter. An overflowed body is evaluated against a resolved copy so the
		// issue that goes into the result keeps the bulk-read contract: bodies are
		// never populated by a list path (SDK-SPEC §4).
		subject := iss
		if needText && iss.bodyExternal {
			resolved, err := s.resolvedCopy(iss)
			if err != nil {
				return nil, err
			}
			subject = resolved
		}
		row := newIssueRow(subject, idx, closedStat)
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
	return window(matches, f.Offset, f.Limit), nil
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
	return Page{Issues: window(matches, f.Offset, f.Limit), Total: total}, nil
}
