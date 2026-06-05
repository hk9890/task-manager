// L2 store tests using the storetest fixture builder (vfs.Mem backend).
// These are the reference usage pattern for the storetest package at L2.
package tasks_test

import (
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/storetest"
)

// TestStoretest_L2_AllAndReady is the reference L2 test that builds a fixture
// with the storetest builder (Mem backend) and asserts All() / Ready().
//
// After at-zib.2.2: All() returns only the hot (active) set; closed issues
// live in closed/ and are excluded from All(). Get() still finds them by
// falling through to closed/.
func TestStoretest_L2_AllAndReady(t *testing.T) {
	// tst-0001: open, no blockers → ready
	// tst-0002: open, blocked by tst-0003 (open) → not ready
	// tst-0003: open, no blockers → ready
	// tst-0004: closed → in closed/, not in All()
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0003")).
		Issue("tst-0003", storetest.Open).
		Closed("tst-0004").
		Mem()

	all, err := store.All()
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	// All() is hot-only after at-zib.2.2 (closed/ is a separate partition).
	if len(all) != 3 {
		t.Errorf("All() = %d issues, want 3 (closed issue is in closed/, not hot)", len(all))
	}

	// Get() still falls through to closed/.
	closed, err := store.Get("tst-0004")
	if err != nil {
		t.Fatalf("Get(tst-0004) from closed/: %v", err)
	}
	if closed.Status != tasks.StatusClosed {
		t.Errorf("tst-0004 status = %v, want closed", closed.Status)
	}

	ready, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready(): %v", err)
	}
	// tst-0001 and tst-0003 are ready; tst-0002 is blocked; tst-0004 is closed.
	if len(ready) != 2 {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("Ready() = %v, want [tst-0001, tst-0003]", ids)
	}
}

// TestStoretest_L2_Comment is the reference L2 test for Comment() on the builder.
func TestStoretest_L2_Comment(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Comment("tst-0001", "alice", "looks good").
		Mem()

	// Comments are now in the sidecar, not on the Issue.
	// Use store.Comments() to retrieve them.
	comments, err := store.Comments("tst-0001")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Author != "alice" || comments[0].Body != "looks good" {
		t.Errorf("comment = %+v", comments[0])
	}
}

// TestStoretest_L2_ClosedIssueStatus verifies that Closed() creates issues with
// StatusClosed, that Get() finds them in closed/, and that they do not appear
// in All() or Ready().
func TestStoretest_L2_ClosedIssueStatus(t *testing.T) {
	store := storetest.New(t).
		Closed("tst-0001").
		Mem()

	// Get falls through to closed/.
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Status != tasks.StatusClosed {
		t.Errorf("status = %q, want closed", iss.Status)
	}

	// All() is hot-only; closed issue must not appear.
	all, err := store.All()
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	for _, iss := range all {
		if iss.ID == "tst-0001" {
			t.Errorf("All() contains closed issue tst-0001 — should be in closed/ only")
		}
	}

	ready, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready(): %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("Ready() = %d, want 0 (closed issues are not ready)", len(ready))
	}
}
