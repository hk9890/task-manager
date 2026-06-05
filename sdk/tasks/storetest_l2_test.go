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
func TestStoretest_L2_AllAndReady(t *testing.T) {
	// tst-0001: open, no blockers → ready
	// tst-0002: open, blocked by tst-0003 (open) → not ready
	// tst-0003: open, no blockers → ready
	// tst-0004: closed → not in ready list
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
	if len(all) != 4 {
		t.Errorf("All() = %d issues, want 4", len(all))
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
// StatusClosed and that they appear in All() but not in Ready().
func TestStoretest_L2_ClosedIssueStatus(t *testing.T) {
	store := storetest.New(t).
		Closed("tst-0001").
		Mem()

	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Status != tasks.StatusClosed {
		t.Errorf("status = %q, want closed", iss.Status)
	}

	ready, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready(): %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("Ready() = %d, want 0 (closed issues are not ready)", len(ready))
	}
}
