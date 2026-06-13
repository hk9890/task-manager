//go:build integration

// L3 integration tests using the storetest fixture builder (real TempDir backend).
// These are the reference usage pattern for the storetest package at L3.
package tasks_test

import (
	"os"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// TestStoretest_L3_AllAndReady is the reference L3 test that builds a fixture
// with the storetest builder (real TempDir backend) and asserts All() / Ready().
// This proves durability: the fixture is written to a real temp directory via
// the osFS seam (real fsync/flock/rename).
//
// After at-zib.2.2: All() returns only the hot (active) set; tst-0004 lives
// in closed/ and is accessible via Get().
func TestStoretest_L3_AllAndReady(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0003")).
		Issue("tst-0003", storetest.Open).
		Closed("tst-0004").
		TempDir(t)

	all, err := store.All()
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	// All() is hot-only; tst-0004 (closed) is in closed/, not in All().
	if len(all) != 3 {
		t.Errorf("All() = %d issues, want 3 (closed issue is in closed/ partition)", len(all))
	}

	// Get() falls through to closed/.
	if _, err := store.Get("tst-0004"); err != nil {
		t.Errorf("Get(tst-0004) from closed/ failed: %v", err)
	}

	ready, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready(): %v", err)
	}
	if len(ready) != 2 {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("Ready() = %v, want 2 ready issues", ids)
	}
}

// TestStoretest_L3_EquivalentToMem is the cross-backend equivalence test:
// the same fixture built into Mem and TempDir must yield the same All()/Ready().
// After at-zib.2.2: All() is hot-only, and Get() falls through to closed/.
func TestStoretest_L3_EquivalentToMem(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001", storetest.Open, storetest.Priority(1)).
		Issue("tst-0002", storetest.InProgress).
		Issue("tst-0003", storetest.Open, storetest.BlockedBy("tst-0001")).
		Closed("tst-0004")

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

	// Both backends: All() is hot-only (3 issues; tst-0004 is in closed/).
	if len(memAll) != len(diskAll) {
		t.Fatalf("All() count: mem=%d disk=%d", len(memAll), len(diskAll))
	}
	for i := range memAll {
		if memAll[i].ID != diskAll[i].ID {
			t.Errorf("[%d] ID mismatch: mem=%q disk=%q", i, memAll[i].ID, diskAll[i].ID)
		}
		if memAll[i].Status != diskAll[i].Status {
			t.Errorf("[%d] %s status: mem=%q disk=%q", i, memAll[i].ID, memAll[i].Status, diskAll[i].Status)
		}
		if memAll[i].Priority != diskAll[i].Priority {
			t.Errorf("[%d] %s priority: mem=%d disk=%d", i, memAll[i].ID, memAll[i].Priority, diskAll[i].Priority)
		}
		if memAll[i].Type != diskAll[i].Type {
			t.Errorf("[%d] %s type: mem=%q disk=%q", i, memAll[i].ID, memAll[i].Type, diskAll[i].Type)
		}
	}

	// tst-0004 must be findable via Get in both backends (falls through to closed/).
	for _, st := range []*tasks.Store{memStore, diskStore} {
		iss, err := st.Get("tst-0004")
		if err != nil {
			t.Fatalf("Get(tst-0004): %v", err)
		}
		if iss.Status != tasks.StatusClosed {
			t.Errorf("tst-0004 status = %v, want closed", iss.Status)
		}
	}

	memReady, err := memStore.Ready()
	if err != nil {
		t.Fatalf("Mem Ready(): %v", err)
	}
	diskReady, err := diskStore.Ready()
	if err != nil {
		t.Fatalf("TempDir Ready(): %v", err)
	}
	if len(memReady) != len(diskReady) {
		t.Fatalf("Ready() count: mem=%d disk=%d", len(memReady), len(diskReady))
	}
	for i := range memReady {
		if memReady[i].ID != diskReady[i].ID {
			t.Errorf("[%d] Ready ID: mem=%q disk=%q", i, memReady[i].ID, diskReady[i].ID)
		}
	}
}

// TestNextID_L3_ScansClosedPartition is the L3 regression test for
// at-zib.2.1 / at-2fb: a file placed directly in the closed/ subdirectory (real
// temp dir, osFS) must be folded into nextID's dedup set so the next Create
// reads closed/ without error and never re-issues that closed ID.
func TestNextID_L3_ScansClosedPartition(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Manually create a closed/ dir and place a high-numbered file there.
	// (At-zib.2.2 will wire the real Close→move flow; here we seed it directly
	// so the test is self-contained and does not depend on at-zib.2.2.)
	closedDir := root + "/.tasks/closed"
	if err := os.MkdirAll(closedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll closed: %v", err)
	}
	highFile := closedDir + "/tst-0099.md"
	if err := os.WriteFile(highFile, []byte("---\nid: tst-0099\ntitle: old\nstatus: closed\ntype: task\npriority: 2\ncreated: 2026-01-01T00:00:00Z\nupdated: 2026-01-01T00:00:00Z\nclosed: 2026-01-01T00:00:00Z\n---\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The hot directory has no active issues; closed/ has tst-0099. Create must
	// read closed/ without error and allocate a fresh ID, never the closed one.
	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "new issue after closed"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(iss.ID, "tst-") || len(iss.ID) <= len("tst-") {
		t.Errorf("Create().ID = %q, want a tst- prefixed ID", iss.ID)
	}
	if iss.ID == "tst-0099" {
		t.Errorf("Create re-issued the closed ID %q", iss.ID)
	}
}

// TestStoretest_L3_Reload proves that the fixture written by TempDir can be
// reopened by a fresh tasks.Open call and still yields the expected issues.
// This is the key durability check: L3 only.
func TestStoretest_L3_Reload(t *testing.T) {
	root := t.TempDir()
	// We need to build into an explicit root so we can reopen it.
	// Use Init to get a store in root, then populate via storetest.
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Populate via storetest helper that takes an already-initialised store.
	// Since TempDir creates its own temp dir, we demonstrate reload manually.
	_, err = s.Create(tasks.CreateInput{Title: "tst-0001"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Now reopen from the same root.
	s2, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	all, err := s2.All()
	if err != nil {
		t.Fatalf("All after reload: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("All after reload = %d, want 1", len(all))
	}
}
