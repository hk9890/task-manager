//go:build integration

// L3 integration tests for Close → closed/ move and Reopen round-trip.
// Uses real TempDir (osFS) to prove durability: real fsync/flock/rename.
package tasks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// TestL3_Close_MoveAndReload verifies the full close lifecycle on a real disk:
//  1. Close moves .md to closed/.
//  2. A fresh Open + Get still finds the issue.
//  3. A second Open + Reopen moves it back.
//  4. After Reopen, Update works again.
func TestL3_Close_MoveAndReload(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "lifecycle test"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := iss.ID

	// Close it.
	closed, err := unwrap(s.Close(id, "all done"))
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Status != tasks.StatusClosed {
		t.Errorf("status = %v, want closed", closed.Status)
	}

	// Hot-dir file must not exist on real disk.
	hotPath := filepath.Join(root, ".tasks", id+".md")
	if _, err := os.Stat(hotPath); err == nil {
		t.Errorf("hot-dir file still exists at %s after Close", hotPath)
	}

	// closed/ file must exist.
	closedPath := filepath.Join(root, ".tasks", "closed", id+".md")
	if _, err := os.Stat(closedPath); err != nil {
		t.Fatalf("closed file not found at %s: %v", closedPath, err)
	}

	// Reload via a fresh Open and Get still works.
	s2, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s2.Get(id)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Status != tasks.StatusClosed {
		t.Errorf("reloaded status = %v, want closed", got.Status)
	}
	if got.CloseReason != "all done" {
		t.Errorf("reloaded close_reason = %q, want 'all done'", got.CloseReason)
	}

	// Reopen.
	reopened, err := unwrap(s2.Reopen(id))
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if reopened.Status != tasks.StatusOpen {
		t.Errorf("reopened status = %v, want open", reopened.Status)
	}
	if !reopened.Closed.IsZero() {
		t.Errorf("Closed field should be zero after Reopen")
	}

	// closed/ file must be gone.
	if _, err := os.Stat(closedPath); err == nil {
		t.Errorf("closed/ file still exists at %s after Reopen", closedPath)
	}
	// Hot-dir file must be back.
	if _, err := os.Stat(hotPath); err != nil {
		t.Fatalf("hot-dir file not found at %s after Reopen: %v", hotPath, err)
	}

	// After Reopen, Update must work.
	newTitle := "updated after reopen"
	out, err := unwrap(s2.Update(id, tasks.UpdateInput{Title: &newTitle}))
	if err != nil {
		t.Fatalf("Update after Reopen: %v", err)
	}
	if out.Title != "updated after reopen" {
		t.Errorf("title = %q", out.Title)
	}
}

// TestL3_Close_SidecarStaysInComments proves on real disk that the comment
// sidecar is NOT moved to closed/ — it stays in comments/.
func TestL3_Close_SidecarStaysInComments(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "sidecar stays"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AddComment(iss.ID, "bob", "pre-close note\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sidecarPath := filepath.Join(root, ".tasks", "comments", iss.ID+".yml")
	if _, err := os.Stat(sidecarPath); err != nil {
		t.Errorf("sidecar not found at %s after Close: %v", sidecarPath, err)
	}
}

// TestL3_Close_CommentOnClosedIssue proves on real disk that AddComment works
// on a closed issue (sidecar append is the one exception to immutability).
func TestL3_Close_CommentOnClosedIssue(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "post-close comment"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	c, err := s.AddComment(iss.ID, "alice", "post-close note\n")
	if err != nil {
		t.Fatalf("AddComment on closed issue: %v", err)
	}

	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != c.ID {
		t.Errorf("Comments() = %d, want 1", len(comments))
	}
}

// TestL3_Storetest_ClosedMaterializesIntoClosedDir verifies that the
// storetest builder's Closed() method places the file in closed/ on real disk.
func TestL3_Storetest_ClosedMaterializesIntoClosedDir(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Closed("tst-0002").
		TempDir(t)

	// tst-0002 must not be in the hot dir.
	hotPath := filepath.Join(store.Dir(), "tst-0002.md")
	if _, err := os.Stat(hotPath); err == nil {
		t.Errorf("closed issue tst-0002 found in hot dir %s — should be in closed/", hotPath)
	}

	// tst-0002 must be in closed/.
	closedPath := filepath.Join(store.Dir(), "closed", "tst-0002.md")
	if _, err := os.Stat(closedPath); err != nil {
		t.Errorf("closed issue tst-0002 not found at %s: %v", closedPath, err)
	}

	// tst-0001 must be in the hot dir.
	hotPath1 := filepath.Join(store.Dir(), "tst-0001.md")
	if _, err := os.Stat(hotPath1); err != nil {
		t.Errorf("open issue tst-0001 not found in hot dir: %v", err)
	}
}

// TestL3_ImmutableInPlace_UpdateRejected proves on real disk that Update on a
// closed issue returns an error and the closed/ file is unchanged.
func TestL3_ImmutableInPlace_UpdateRejected(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "immutable"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	newTitle := "should fail"
	_, err = s.Update(iss.ID, tasks.UpdateInput{Title: &newTitle})
	if err == nil {
		t.Fatal("Update on closed issue should fail (immutable in place)")
	}

	// File content must not have changed.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "immutable" {
		t.Errorf("title changed to %q; closed file must be immutable", got.Title)
	}
}
