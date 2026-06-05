package storetest_test

import (
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/storetest"
)

// TestBuilder_MemAndTempDir_EquivalentAll verifies that a fixture built into
// vfs.Mem (L2) and into a real t.TempDir() (L3) yields equivalent All() results.
func TestBuilder_MemAndTempDir_EquivalentAll(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001", storetest.Open, storetest.Priority(1)).
		Issue("tst-0002", storetest.InProgress).
		Closed("tst-0003")

	memStore := b.Mem()
	diskStore := b.TempDir(t)

	memAll, err := memStore.All()
	if err != nil {
		t.Fatalf("Mem All(): %v", err)
	}
	diskAll, err := diskStore.All()
	if err != nil {
		t.Fatalf("TempDir All(): %v", err)
	}

	if len(memAll) != len(diskAll) {
		t.Fatalf("All() length: mem=%d disk=%d", len(memAll), len(diskAll))
	}
	for i := range memAll {
		if memAll[i].ID != diskAll[i].ID {
			t.Errorf("[%d] ID: mem=%q disk=%q", i, memAll[i].ID, diskAll[i].ID)
		}
		if memAll[i].Status != diskAll[i].Status {
			t.Errorf("[%d] %s status: mem=%q disk=%q", i, memAll[i].ID, memAll[i].Status, diskAll[i].Status)
		}
		if memAll[i].Priority != diskAll[i].Priority {
			t.Errorf("[%d] %s priority: mem=%d disk=%d", i, memAll[i].ID, memAll[i].Priority, diskAll[i].Priority)
		}
	}
}

// TestBuilder_MemAndTempDir_EquivalentReady verifies that Ready() returns the
// same issues from both backends.
func TestBuilder_MemAndTempDir_EquivalentReady(t *testing.T) {
	// tst-0002 is blocked by tst-0003 (open); tst-0001 is open with no blockers.
	b := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0003")).
		Issue("tst-0003", storetest.Open)

	memStore := b.Mem()
	diskStore := b.TempDir(t)

	memReady, err := memStore.Ready()
	if err != nil {
		t.Fatalf("Mem Ready(): %v", err)
	}
	diskReady, err := diskStore.Ready()
	if err != nil {
		t.Fatalf("TempDir Ready(): %v", err)
	}

	if len(memReady) != len(diskReady) {
		t.Fatalf("Ready() length: mem=%d disk=%d", len(memReady), len(diskReady))
	}
	for i := range memReady {
		if memReady[i].ID != diskReady[i].ID {
			t.Errorf("[%d] ID: mem=%q disk=%q", i, memReady[i].ID, diskReady[i].ID)
		}
	}
}

// TestBuilder_Comment verifies that Comment() adds a comment to an issue.
func TestBuilder_Comment(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001").
		Comment("tst-0001", "hans", "first note")

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(iss.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(iss.Comments))
	}
	if iss.Comments[0].Body != "first note" {
		t.Errorf("comment body = %q, want %q", iss.Comments[0].Body, "first note")
	}
	if iss.Comments[0].Author != "hans" {
		t.Errorf("comment author = %q, want %q", iss.Comments[0].Author, "hans")
	}
}

// TestBuilder_Parent verifies that Parent() sets the parent relationship.
func TestBuilder_Parent(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001").
		Issue("tst-0002", storetest.Parent("tst-0001"))

	store := b.Mem()
	child, err := store.Get("tst-0002")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if child.Parent != "tst-0001" {
		t.Errorf("parent = %q, want tst-0001", child.Parent)
	}
}

// TestBuilder_Labels verifies that Label() opts are applied.
func TestBuilder_Labels(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001", storetest.Label("urgent", "backend"))

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(iss.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %v", iss.Labels)
	}
}

// TestBuilder_TypeOpt verifies that IssueType() sets the issue type.
func TestBuilder_TypeOpt(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug))

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Type != tasks.TypeBug {
		t.Errorf("type = %q, want bug", iss.Type)
	}
}

// TestBuilder_Closed verifies that Closed() creates a closed issue.
func TestBuilder_Closed(t *testing.T) {
	b := storetest.New(t).
		Closed("tst-0001")

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Status != tasks.StatusClosed {
		t.Errorf("status = %q, want closed", iss.Status)
	}
}
