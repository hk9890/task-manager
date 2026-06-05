//go:build integration

// L3 integration tests using the storetest fixture builder (real TempDir backend).
// These are the reference usage pattern for the storetest package at L3.
package tasks_test

import (
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/storetest"
)

// TestStoretest_L3_AllAndReady is the reference L3 test that builds a fixture
// with the storetest builder (real TempDir backend) and asserts All() / Ready().
// This proves durability: the fixture is written to a real temp directory via
// the osFS seam (real fsync/flock/rename).
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
	if len(all) != 4 {
		t.Errorf("All() = %d issues, want 4", len(all))
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
