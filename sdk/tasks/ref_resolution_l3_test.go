//go:build integration

// L3 integration tests for ref resolution + ready/blocked across partitions
// (at-zib.2.3). Uses real TempDir (osFS) to prove durability: real
// fsync/flock/rename.
package tasks_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// TestL3_CheckRefs_ClosedParentAccepted verifies on a real disk that Create
// with a parent in closed/ succeeds (checkRefs falls through to closed/).
func TestL3_CheckRefs_ClosedParentAccepted(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	parent, err := unwrap(s.Create(tasks.CreateInput{Title: "parent epic"}))
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := s.Close(parent.ID, "done"); err != nil {
		t.Fatalf("Close parent: %v", err)
	}

	// Reopen via a fresh store to prove the closed/ file is persisted.
	s2, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	child, err := unwrap(s2.Create(tasks.CreateInput{Title: "child of closed parent", Parent: parent.ID}))
	if err != nil {
		t.Errorf("Create with closed parent must succeed on real disk, got: %v", err)
		return
	}
	if child.Parent != parent.ID {
		t.Errorf("child.Parent = %q, want %q", child.Parent, parent.ID)
	}
}

// TestL3_CheckRefs_ClosedBlockerAccepted verifies on a real disk that Create
// with a blocker in closed/ succeeds.
func TestL3_CheckRefs_ClosedBlockerAccepted(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	blocker, err := unwrap(s.Create(tasks.CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	s2, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dep, err := unwrap(s2.Create(tasks.CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}}))
	if err != nil {
		t.Errorf("Create with closed blocker must succeed on real disk, got: %v", err)
		return
	}
	if len(dep.BlockedBy) != 1 || dep.BlockedBy[0] != blocker.ID {
		t.Errorf("dep.BlockedBy = %v, want [%s]", dep.BlockedBy, blocker.ID)
	}
}

// TestL3_Ready_ClosedBlockerCountsAsResolved verifies on a real disk that
// Ready() treats a blocker in closed/ as resolved.
func TestL3_Ready_ClosedBlockerCountsAsResolved(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	blocker, err := unwrap(s.Create(tasks.CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := unwrap(s.Create(tasks.CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}}))
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}
	if _, err := s.Close(blocker.ID, "done"); err != nil {
		t.Fatalf("Close blocker: %v", err)
	}

	// Reload via fresh Open.
	s2, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ready, err := s2.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	found := false
	for _, r := range ready {
		if r.ID == dep.ID {
			found = true
			break
		}
	}
	if !found {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("dep must be ready when its only blocker is in closed/, ready=%v", ids)
	}
}

// TestL3_Detail_ResolvesClosedParentRef verifies on a real disk that Detail
// populates ParentRef from the closed/ partition.
func TestL3_Detail_ResolvesClosedParentRef(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	parent, err := unwrap(s.Create(tasks.CreateInput{Title: "closed parent"}))
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := s.Close(parent.ID, "done"); err != nil {
		t.Fatalf("Close parent: %v", err)
	}
	child, err := unwrap(s.Create(tasks.CreateInput{Title: "child", Parent: parent.ID}))
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// Reload.
	s2, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d, err := s2.Detail(child.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d.ParentRef == nil {
		t.Fatal("Detail.ParentRef must be set when parent is in closed/")
	}
	if d.ParentRef.ID != parent.ID {
		t.Errorf("ParentRef.ID = %q, want %q", d.ParentRef.ID, parent.ID)
	}
	if d.ParentRef.Status != tasks.StatusClosed {
		t.Errorf("ParentRef.Status = %q, want closed", d.ParentRef.Status)
	}
}

// TestL3_Storetest_ClosedBlockerRefResolution verifies via the storetest builder
// (TempDir backend) that an issue with a closed blocker validates OK and is
// ready after the blocker is closed.
func TestL3_Storetest_ClosedBlockerRefResolution(t *testing.T) {
	// tst-0001: closed (blocker)
	// tst-0002: open, blocked_by tst-0001 (closed → resolved)
	store := storetest.New(t).
		Closed("tst-0001").
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0001")).
		TempDir(t)

	ready, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	found := false
	for _, r := range ready {
		if r.ID == "tst-0002" {
			found = true
			break
		}
	}
	if !found {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("tst-0002 must be ready (its only blocker tst-0001 is closed), ready=%v", ids)
	}
}

// TestL3_Storetest_ClosedBlockerRefResolution_Mem verifies the same behaviour
// on the Mem backend (cross-backend consistency).
func TestL3_Storetest_ClosedBlockerRefResolution_Mem(t *testing.T) {
	store := storetest.New(t).
		Closed("tst-0001").
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0001")).
		Mem()

	ready, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	found := false
	for _, r := range ready {
		if r.ID == "tst-0002" {
			found = true
			break
		}
	}
	if !found {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("tst-0002 must be ready (its only blocker tst-0001 is closed), ready=%v", ids)
	}
}
