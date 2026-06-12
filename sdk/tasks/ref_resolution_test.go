// L2 tests for ref resolution + ready/blocked across partitions (at-zib.2.3).
//
// Covers:
//   - Create/Update with parent/blocker in closed/ succeeds (checkRefs falls through)
//   - A dangling ref (neither hot nor closed) still fails validation
//   - Ready() treats a blocker in closed/ as resolved
//   - Detail resolves parent and blocker refs that live in closed/
//   - Blocked() excludes issues whose only blocker is closed
package tasks

import (
	"errors"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// newMemStoreRef creates a mem-backed store with a deterministic clock,
// identical to newMemStoreForClose but with its own name so test isolation is clear.
func newMemStoreRef(t *testing.T) *Store {
	t.Helper()
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	return s
}

// TestCheckRefs_ClosedParentAccepted verifies that Create with a parent whose
// .md lives in closed/ succeeds — the ref is valid, not dangling.
func TestCheckRefs_ClosedParentAccepted(t *testing.T) {
	s := newMemStoreRef(t)

	// Create and close a future parent issue.
	parent, err := s.Create(CreateInput{Title: "epic parent"})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := s.Close(parent.ID, "done"); err != nil {
		t.Fatalf("Close parent: %v", err)
	}

	// Now create a child that references the closed parent.
	child, err := s.Create(CreateInput{Title: "child of closed parent", Parent: parent.ID})
	if err != nil {
		t.Errorf("Create with closed parent must succeed, got: %v", err)
		return
	}
	if child.Parent != parent.ID {
		t.Errorf("child.Parent = %q, want %q", child.Parent, parent.ID)
	}
}

// TestCheckRefs_ClosedBlockerAccepted verifies that Create with a blocker in
// closed/ succeeds.
func TestCheckRefs_ClosedBlockerAccepted(t *testing.T) {
	s := newMemStoreRef(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	dep, err := s.Create(CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}})
	if err != nil {
		t.Errorf("Create with closed blocker must succeed, got: %v", err)
		return
	}
	if len(dep.BlockedBy) != 1 || dep.BlockedBy[0] != blocker.ID {
		t.Errorf("dep.BlockedBy = %v, want [%s]", dep.BlockedBy, blocker.ID)
	}
}

// TestCheckRefs_ClosedRelatedAccepted verifies that Create with a related issue
// in closed/ succeeds.
func TestCheckRefs_ClosedRelatedAccepted(t *testing.T) {
	s := newMemStoreRef(t)

	rel, err := s.Create(CreateInput{Title: "related"})
	if err != nil {
		t.Fatalf("Create related: %v", err)
	}
	if _, err := s.Close(rel.ID, "done"); err != nil {
		t.Fatalf("Close related: %v", err)
	}

	iss, err := s.Create(CreateInput{Title: "issue with closed related", Related: []string{rel.ID}})
	if err != nil {
		t.Errorf("Create with closed related must succeed, got: %v", err)
		return
	}
	if len(iss.Related) != 1 || iss.Related[0] != rel.ID {
		t.Errorf("iss.Related = %v, want [%s]", iss.Related, rel.ID)
	}
}

// TestCheckRefs_DanglingRefStillFails verifies that a ref that exists in
// neither the hot directory nor closed/ is still a dangling reference and
// returns a ValidationError.
func TestCheckRefs_DanglingRefStillFails(t *testing.T) {
	s := newMemStoreRef(t)

	_, err := s.Create(CreateInput{Title: "orphan", Parent: "agt-9999"})
	if err == nil {
		t.Fatal("Create with dangling parent must fail")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

// TestCheckRefs_DanglingBlockerFails verifies that a blocker that does not
// exist in either partition is rejected.
func TestCheckRefs_DanglingBlockerFails(t *testing.T) {
	s := newMemStoreRef(t)

	_, err := s.Create(CreateInput{Title: "dep", BlockedBy: []string{"agt-9999"}})
	if err == nil {
		t.Fatal("Create with dangling blocker must fail")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

// TestReady_ClosedBlockerCountsAsResolved verifies that an issue whose only
// blocker is in closed/ appears in Ready().
func TestReady_ClosedBlockerCountsAsResolved(t *testing.T) {
	s := newMemStoreRef(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}})
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}

	// Before close: dep is NOT ready.
	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if containsID(ready, dep.ID) {
		t.Errorf("dep should NOT be ready while blocker is still open")
	}

	// Close the blocker.
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	// After close: dep MUST be ready (blocker is in closed/, counts as resolved).
	ready, err = s.Ready()
	if err != nil {
		t.Fatalf("Ready after Close: %v", err)
	}
	if !containsID(ready, dep.ID) {
		t.Errorf("dep must be ready when its only blocker is closed, ready=%v", ids(ready))
	}
}

// TestBlocked_ClosedBlockerNoLongerBlocks verifies that after the only blocker
// is closed, the issue does NOT appear in Blocked().
func TestBlocked_ClosedBlockerNoLongerBlocks(t *testing.T) {
	s := newMemStoreRef(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}})
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}

	// Close the blocker.
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	blocked, err := s.Blocked()
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	for _, bi := range blocked {
		if bi.Issue.ID == dep.ID {
			t.Errorf("dep must not appear in Blocked() when its only blocker is closed")
		}
	}
}

// TestDetail_ResolvesClosedParentRef verifies that Detail populates ParentRef
// even when the parent is in closed/.
func TestDetail_ResolvesClosedParentRef(t *testing.T) {
	s := newMemStoreRef(t)

	parent, err := s.Create(CreateInput{Title: "closed parent"})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := s.Close(parent.ID, "done"); err != nil {
		t.Fatalf("Close parent: %v", err)
	}

	child, err := s.Create(CreateInput{Title: "child", Parent: parent.ID})
	if err != nil {
		t.Fatalf("Create child with closed parent: %v", err)
	}

	d, err := s.Detail(child.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d.ParentRef == nil {
		t.Fatal("Detail.ParentRef must be set when parent is in closed/")
	}
	if d.ParentRef.ID != parent.ID {
		t.Errorf("ParentRef.ID = %q, want %q", d.ParentRef.ID, parent.ID)
	}
	if d.ParentRef.Status != StatusClosed {
		t.Errorf("ParentRef.Status = %q, want closed", d.ParentRef.Status)
	}
}

// TestDetail_ResolvesClosedBlockerRef verifies that Detail populates BlockedBy
// refs even when a blocker is in closed/.
func TestDetail_ResolvesClosedBlockerRef(t *testing.T) {
	s := newMemStoreRef(t)

	blocker, err := s.Create(CreateInput{Title: "closed blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	dep, err := s.Create(CreateInput{Title: "dep", BlockedBy: []string{blocker.ID}})
	if err != nil {
		t.Fatalf("Create dep with closed blocker: %v", err)
	}

	d, err := s.Detail(dep.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(d.BlockedByRefs) != 1 {
		t.Fatalf("Detail.BlockedByRefs = %v, want 1 entry", d.BlockedByRefs)
	}
	if d.BlockedByRefs[0].ID != blocker.ID {
		t.Errorf("BlockedByRefs[0].ID = %q, want %q", d.BlockedByRefs[0].ID, blocker.ID)
	}
	if d.BlockedByRefs[0].Status != StatusClosed {
		t.Errorf("BlockedByRefs[0].Status = %q, want closed", d.BlockedByRefs[0].Status)
	}
}

// TestDetail_ResolvesClosedRelatedRef verifies that Detail populates Related
// refs even when the related issue is in closed/.
func TestDetail_ResolvesClosedRelatedRef(t *testing.T) {
	s := newMemStoreRef(t)

	related, err := s.Create(CreateInput{Title: "closed related"})
	if err != nil {
		t.Fatalf("Create related: %v", err)
	}
	if _, err := s.Close(related.ID, "done"); err != nil {
		t.Fatalf("Close related: %v", err)
	}

	iss, err := s.Create(CreateInput{Title: "issue", Related: []string{related.ID}})
	if err != nil {
		t.Fatalf("Create issue with closed related: %v", err)
	}

	d, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(d.RelatedRefs) != 1 {
		t.Fatalf("Detail.RelatedRefs = %v, want 1 entry", d.RelatedRefs)
	}
	if d.RelatedRefs[0].ID != related.ID {
		t.Errorf("RelatedRefs[0].ID = %q, want %q", d.RelatedRefs[0].ID, related.ID)
	}
	if d.RelatedRefs[0].Status != StatusClosed {
		t.Errorf("RelatedRefs[0].Status = %q, want closed", d.RelatedRefs[0].Status)
	}
}

// TestUpdate_ClosedBlockerRemainsValid verifies that Update on an open issue
// that already references a closed blocker succeeds (not rejected as dangling).
func TestUpdate_ClosedBlockerRemainsValid(t *testing.T) {
	s := newMemStoreRef(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dep", BlockedBy: []string{blocker.ID}})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}

	// Close the blocker (dep still references it).
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	// Update dep — must not fail because the blocker is in closed/ (still valid).
	newTitle := "dep updated"
	_, err = s.Update(dep.ID, UpdateInput{Title: &newTitle})
	if err != nil {
		t.Errorf("Update after blocker closed must succeed, got: %v", err)
	}
}

// TestAddDep_ClosedBlockerAccepted verifies that AddDep with a closed blocker
// succeeds.
func TestAddDep_ClosedBlockerAccepted(t *testing.T) {
	s := newMemStoreRef(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dep"})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}

	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	if err := s.AddDep(dep.ID, blocker.ID); err != nil {
		t.Errorf("AddDep with closed blocker must succeed, got: %v", err)
	}
}
