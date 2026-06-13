package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
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

	iss, err := unwrap(s.Create(CreateInput{Title: "hello mem"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(iss.ID, "agt-") || !idRe.MatchString(iss.ID) {
		t.Errorf("id = %q, want a valid agt- prefixed ID", iss.ID)
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

	iss, err := unwrap(s.Create(CreateInput{Title: "issue"}))
	if err != nil {
		t.Fatal(err)
	}

	newTitle := "updated issue"
	out, err := unwrap(s.Update(iss.ID, UpdateInput{Title: &newTitle}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if out.Title != "updated issue" {
		t.Errorf("title = %q, want updated issue", out.Title)
	}

	closed, err := unwrap(s.Close(iss.ID, "done"))
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

// TestNextID_ScansClosedPartition is the regression test for the nextID bug
// (at-zib.2.1), updated for collision-resistant IDs (at-2fb): closed/ is still
// scanned and folded into the dedup set, so an ID already living in closed/ is
// never re-issued. (Random tokens make the original high-water collision class
// structurally impossible; this guards the closed/ read path and dedup wiring.)
//
// This is an L2 test: it uses vfs.Mem so no real disk is touched.
func TestNextID_ScansClosedPartition(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll hot: %v", err)
	}
	// Simulate a high-numbered file already living in closed/ so that the hot
	// dir has no files (nextID would return agt-0001 without the fix).
	closedDir := "/.tasks/closed"
	if err := m.MkdirAll(closedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll closed: %v", err)
	}
	// Write a fake closed issue file with a high number.
	if err := m.WriteAtomic(closedDir+"/agt-0042.md", []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteAtomic closed file: %v", err)
	}

	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	// The hot directory has no issues; closed/ has agt-0042. nextID must read
	// closed/ without error and return a valid ID that does not re-use agt-0042.
	id, err := s.nextID()
	if err != nil {
		t.Fatalf("nextID: %v", err)
	}
	if !strings.HasPrefix(id, "agt-") || !idRe.MatchString(id) {
		t.Errorf("nextID = %q, want a valid agt- prefixed ID", id)
	}
	if id == "agt-0042" {
		t.Errorf("nextID re-issued the closed ID %q", id)
	}
}

// TestNextID_ClosedDirAbsent verifies that nextID works correctly when the
// closed/ directory does not exist yet (treat absent as empty) and never
// re-issues an ID already present in the hot dir.
//
// This is an L2 test: uses vfs.Mem.
func TestNextID_ClosedDirAbsent(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Write one issue in the hot dir; closed/ does not exist.
	if err := m.WriteAtomic("/.tasks/agt-0005.md", []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	id, err := s.nextID()
	if err != nil {
		t.Fatalf("nextID: %v", err)
	}
	if !strings.HasPrefix(id, "agt-") || !idRe.MatchString(id) {
		t.Errorf("nextID = %q, want a valid agt- prefixed ID", id)
	}
	if id == "agt-0005" {
		t.Errorf("nextID re-issued the existing ID %q", id)
	}
}

// TestNextID_BothPartitionsPopulated verifies that nextID reads both the hot
// dir and closed/ without error and returns a valid ID that collides with
// neither existing entry.
//
// This is an L2 test: uses vfs.Mem.
func TestNextID_BothPartitionsPopulated(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.MkdirAll("/.tasks/closed", 0o755); err != nil {
		t.Fatalf("MkdirAll closed: %v", err)
	}
	// Hot dir has agt-0100; closed/ has agt-0042.
	if err := m.WriteAtomic("/.tasks/agt-0100.md", []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteAtomic hot: %v", err)
	}
	if err := m.WriteAtomic("/.tasks/closed/agt-0042.md", []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteAtomic closed: %v", err)
	}

	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	id, err := s.nextID()
	if err != nil {
		t.Fatalf("nextID: %v", err)
	}
	if !strings.HasPrefix(id, "agt-") || !idRe.MatchString(id) {
		t.Errorf("nextID = %q, want a valid agt- prefixed ID", id)
	}
	if id == "agt-0100" || id == "agt-0042" {
		t.Errorf("nextID re-issued an existing ID %q", id)
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
	iss, err := unwrap(s.Create(CreateInput{Title: "test issue"}))
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
