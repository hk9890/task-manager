package tasks

import (
	"errors"
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

// newMemStore creates a Store backed by vfs.Mem with a deterministic clock.
// It mirrors the structure of newTestStore but uses in-memory storage instead
// of real disk — this is the L2 layer.
func newMemStore(t *testing.T) *Store {
	t.Helper()
	m := vfs.NewMem()

	// Mem needs the data directory to exist before the store is used.
	// MkdirAll is a no-op on Mem but sets up the directory entry so ReadDir
	// works.
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

// TestMemStore_CreateAndGet verifies basic Create → Get round-trip on Mem.
func TestMemStore_CreateAndGet(t *testing.T) {
	s := newMemStore(t)

	iss, err := s.Create(CreateInput{Title: "hello mem"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iss.ID != "agt-0001" {
		t.Errorf("id = %q, want agt-0001", iss.ID)
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "hello mem" {
		t.Errorf("title = %q, want %q", got.Title, "hello mem")
	}
}

// TestMemStore_UpdateAndClose verifies Update and Close work on Mem.
func TestMemStore_UpdateAndClose(t *testing.T) {
	s := newMemStore(t)

	iss, err := s.Create(CreateInput{Title: "issue"})
	if err != nil {
		t.Fatal(err)
	}

	newTitle := "updated issue"
	out, err := s.Update(iss.ID, UpdateInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if out.Title != "updated issue" {
		t.Errorf("title = %q, want updated issue", out.Title)
	}

	closed, err := s.Close(iss.ID, "done")
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Status != StatusClosed {
		t.Errorf("status = %v, want closed", closed.Status)
	}
}

// TestMemStore_All verifies All returns all issues on Mem.
func TestMemStore_All(t *testing.T) {
	s := newMemStore(t)

	mustCreate(t, s, CreateInput{Title: "a"})
	mustCreate(t, s, CreateInput{Title: "b"})
	mustCreate(t, s, CreateInput{Title: "c"})

	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("All() = %d, want 3", len(all))
	}
}

// TestMemStore_FailOn_RenameOnClose_NoTornState is the key L2 fault-injection
// test from the acceptance criteria: a forced Rename failure during a
// WriteAtomic call (which uses Rename internally) leaves the issue untouched —
// no torn state.
//
// The scenario: the Store writes an issue via WriteAtomic which does a temp →
// target rename internally. On Mem, WriteAtomic is a plain map write (no real
// rename), so we inject a fault on WriteAtomic directly to simulate the
// atomic-write failure. The issue must remain in its previous state.
func TestMemStore_FailOn_WriteAtomic_NoTornState(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatal(err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	// Create an issue in the open state.
	iss, err := s.Create(CreateInput{Title: "test issue"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iss.Status != StatusOpen {
		t.Fatalf("expected open status, got %v", iss.Status)
	}

	// Inject a fault: the WriteAtomic for this issue file will fail once.
	m.FailOn("WriteAtomic", "/.tasks/"+iss.ID+".md", errors.New("simulated disk full"))

	// Try to close the issue. The close should fail because WriteAtomic fails.
	_, err = s.Close(iss.ID, "done")
	if err == nil {
		t.Fatal("expected Close to fail due to injected WriteAtomic fault")
	}

	// Verify no torn state: the issue should still be readable and still open.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after failed Close: %v", err)
	}
	if got.Status != StatusOpen {
		t.Errorf("status after failed Close = %v, want open (no torn state)", got.Status)
	}
}
