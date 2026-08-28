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

package tasks

import (
	"errors"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

func TestReady_AndBlocked(t *testing.T) {
	s, _ := newMemStore(t)
	blocker := mustCreate(t, s, CreateInput{Title: "blocker"})
	dependent := mustCreate(t, s, CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}})
	free := mustCreate(t, s, CreateInput{Title: "free"})

	ready, err := s.Ready()
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(ready, blocker.ID) || !containsID(ready, free.ID) {
		t.Errorf("blocker and free should be ready: %v", ids(ready))
	}
	if containsID(ready, dependent.ID) {
		t.Errorf("dependent must not be ready while blocker is open")
	}

	blocked, err := s.Blocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].Issue.ID != dependent.ID {
		t.Fatalf("expected dependent blocked, got %+v", blocked)
	}
	if len(blocked[0].BlockedBy) != 1 || blocked[0].BlockedBy[0].ID != blocker.ID {
		t.Errorf("blocker ref wrong: %+v", blocked[0].BlockedBy)
	}

	// Closing the blocker makes the dependent ready.
	if _, err := s.Close(blocker.ID, ""); err != nil {
		t.Fatal(err)
	}
	ready, _ = s.Ready()
	if !containsID(ready, dependent.ID) {
		t.Errorf("dependent should be ready after blocker closed: %v", ids(ready))
	}
}

func TestReady_ExcludesNonOpen(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})
	ip := StatusInProgress
	if _, err := s.Update(iss.ID, UpdateInput{Status: &ip}); err != nil {
		t.Fatal(err)
	}
	ready, _ := s.Ready()
	if containsID(ready, iss.ID) {
		t.Errorf("in_progress issue should not be ready")
	}
}

func TestReady_OrderingByPriority(t *testing.T) {
	s, _ := newMemStore(t)
	p3 := 3
	p0 := 0
	low := mustCreate(t, s, CreateInput{Title: "low", Priority: &p3})
	high := mustCreate(t, s, CreateInput{Title: "high", Priority: &p0})

	ready, _ := s.Ready()
	if len(ready) < 2 || ready[0].ID != high.ID || ready[1].ID != low.ID {
		t.Errorf("expected priority order [%s,%s], got %v", high.ID, low.ID, ids(ready))
	}
}

func TestDetail_DerivesInverseEdges(t *testing.T) {
	s, _ := newMemStore(t)
	epic := mustCreate(t, s, CreateInput{Title: "epic", Type: TypeEpic})
	child := mustCreate(t, s, CreateInput{Title: "child", Parent: epic.ID})
	blocked := mustCreate(t, s, CreateInput{Title: "blocked", BlockedBy: []string{epic.ID}})
	related := mustCreate(t, s, CreateInput{Title: "related", Related: []string{epic.ID}})

	d, err := s.Detail(epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Children) != 1 || d.Children[0].ID != child.ID {
		t.Errorf("children wrong: %+v", d.Children)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].ID != blocked.ID {
		t.Errorf("blocks wrong: %+v", d.Blocks)
	}

	// The dependent's Detail should resolve its blocker ref and parent ref.
	cd, _ := s.Detail(child.ID)
	if cd.ParentRef == nil || cd.ParentRef.ID != epic.ID {
		t.Errorf("parent ref wrong: %+v", cd.ParentRef)
	}
	_ = related
}

func TestList_Filters(t *testing.T) {
	s, _ := newMemStore(t)
	p1 := 1
	bug := mustCreate(t, s, CreateInput{Title: "a bug", Type: TypeBug, Priority: &p1, Labels: []string{"x"}})
	mustCreate(t, s, CreateInput{Title: "a task", Type: TypeTask, Labels: []string{"y"}})
	done := mustCreate(t, s, CreateInput{Title: "done task"})
	if _, err := s.Close(done.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Default excludes closed.
	open, _ := s.List(Filter{})
	if containsID(open, done.ID) {
		t.Errorf("closed should be excluded by default: %v", ids(open))
	}

	// Type filter via Expr.
	bugs, _ := s.List(Filter{Expr: `type == bug`})
	if len(bugs) != 1 || bugs[0].ID != bug.ID {
		t.Errorf("type filter wrong: %v", ids(bugs))
	}

	// Label filter via Expr.
	labeled, _ := s.List(Filter{Expr: `label == "x"`})
	if len(labeled) != 1 || labeled[0].ID != bug.ID {
		t.Errorf("label filter wrong: %v", ids(labeled))
	}

	// Text filter via Expr (case-insensitive ~).
	found, _ := s.List(Filter{Expr: `text ~ "BUG"`})
	if len(found) != 1 || found[0].ID != bug.ID {
		t.Errorf("text filter wrong: %v", ids(found))
	}

	// Status filter via Expr: closed scope auto-included.
	closed, _ := s.List(Filter{Expr: `status == "closed"`})
	if len(closed) != 1 || closed[0].ID != done.ID {
		t.Errorf("status filter wrong: %v", ids(closed))
	}

	// Limit.
	limited, _ := s.List(Filter{IncludeClosed: true, Limit: 2})
	if len(limited) != 2 {
		t.Errorf("limit wrong: %v", ids(limited))
	}
}

// TestSortCreated_TieBreakByID verifies that SortCreated uses ID as a tie-break
// when two issues have identical Created timestamps, producing deterministic order.
func TestSortCreated_TieBreakByID(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	a := &Issue{ID: "agt-0001", Created: ts, Updated: ts}
	b := &Issue{ID: "agt-0002", Created: ts, Updated: ts}
	c := &Issue{ID: "agt-0003", Created: ts, Updated: ts}

	// Shuffle order to confirm sort is not relying on input order.
	issues := []*Issue{c, a, b}
	sortIssues(issues, SortCreated)

	wantIDs := []string{"agt-0001", "agt-0002", "agt-0003"}
	for i, iss := range issues {
		if iss.ID != wantIDs[i] {
			t.Errorf("SortCreated tie-break: position %d = %q, want %q; full: %v",
				i, iss.ID, wantIDs[i], ids(issues))
		}
	}
}

// TestSortUpdated_TieBreakByID verifies that SortUpdated uses ID as a tie-break
// when two issues have identical Updated timestamps.
func TestSortUpdated_TieBreakByID(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	a := &Issue{ID: "agt-0001", Updated: ts}
	b := &Issue{ID: "agt-0002", Updated: ts}
	c := &Issue{ID: "agt-0003", Updated: ts}

	issues := []*Issue{c, a, b}
	sortIssues(issues, SortUpdated)

	wantIDs := []string{"agt-0001", "agt-0002", "agt-0003"}
	for i, iss := range issues {
		if iss.ID != wantIDs[i] {
			t.Errorf("SortUpdated tie-break: position %d = %q, want %q; full: %v",
				i, iss.ID, wantIDs[i], ids(issues))
		}
	}
}

// TestSortClosed_TieBreakByID verifies that SortClosed uses ID as a tie-break
// when two issues have identical Closed timestamps.
func TestSortClosed_TieBreakByID(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	a := &Issue{ID: "agt-0001", Closed: ts}
	b := &Issue{ID: "agt-0002", Closed: ts}
	c := &Issue{ID: "agt-0003", Closed: ts}

	issues := []*Issue{c, a, b}
	sortIssues(issues, SortClosed)

	wantIDs := []string{"agt-0001", "agt-0002", "agt-0003"}
	for i, iss := range issues {
		if iss.ID != wantIDs[i] {
			t.Errorf("SortClosed tie-break: position %d = %q, want %q; full: %v",
				i, iss.ID, wantIDs[i], ids(issues))
		}
	}
}

func containsID(issues []*Issue, id string) bool {
	for _, i := range issues {
		if i.ID == id {
			return true
		}
	}
	return false
}

func ids(issues []*Issue) []string {
	out := make([]string, len(issues))
	for i, iss := range issues {
		out[i] = iss.ID
	}
	return out
}

// TestDetail_RefResolutionFailureIsNotSwallowed pins the two ends of a ref that
// is not in the hot index: a dangling one is dropped, an unreadable one is an
// error.
//
// Discarding every error there meant an I/O failure silently removed a blocker
// from the rendered issue: a blocked issue printed with no Blocked-by line and
// exit 0, which reads as "ready to work on".
func TestDetail_RefResolutionFailureIsNotSwallowed(t *testing.T) {
	newPair := func(t *testing.T) (*vfs.Mem, *Store, *Issue, *Issue) {
		t.Helper()
		m := vfs.NewMem()
		s, err := InitWithVFS("/p", "tst", m)
		if err != nil {
			t.Fatal(err)
		}
		blocker := mustCreate(t, s, CreateInput{Title: "blocker"})
		dependent := mustCreate(t, s, CreateInput{Title: "dependent"})
		if err := s.AddDep(dependent.ID, blocker.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := unwrap(s.Close(blocker.ID, "done")); err != nil {
			t.Fatal(err)
		}
		return m, s, dependent, blocker
	}

	t.Run("unreadable blocker is an error", func(t *testing.T) {
		m, s, dependent, blocker := newPair(t)
		m.FailOn("ReadFile", s.closedFilePath(blocker.ID), errors.New("disk error"))
		if _, err := s.Detail(dependent.ID); err == nil {
			t.Error("Detail must not report a blocked issue as unblocked when the blocker cannot be read")
		}
	})

	t.Run("dangling blocker is dropped", func(t *testing.T) {
		m, s, dependent, blocker := newPair(t)
		if err := m.Remove(s.closedFilePath(blocker.ID)); err != nil {
			t.Fatal(err)
		}
		d, err := s.Detail(dependent.ID)
		if err != nil {
			t.Fatalf("a hand-edited store must stay readable: %v", err)
		}
		if len(d.BlockedByRefs) != 0 {
			t.Errorf("BlockedByRefs = %v, want the dangling ref dropped", d.BlockedByRefs)
		}
	})
}
