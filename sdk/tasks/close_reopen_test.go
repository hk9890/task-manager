// L2 tests for Close → closed/ move, immutability, and Reopen.
// These tests exercise the new behaviour from at-zib.2.2 at the vfs.Mem layer
// (fast, no real disk). Durability / real rename round-trips are tested at L3
// in close_reopen_l3_test.go.
package tasks

import (
	"errors"
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

// newMemStoreForClose creates a mem-backed store with a deterministic clock.
// The closed/ directory does NOT exist yet — Close must create it.
func newMemStoreForClose(t *testing.T) *Store {
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

// TestClose_MovesToClosedDir verifies that Close moves the task .md to
// closed/<id>.md and the hot-dir file is gone.
func TestClose_MovesToClosedDir(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "to close"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	closed, err := s.Close(iss.ID, "done")
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Status != StatusClosed {
		t.Errorf("status = %v, want closed", closed.Status)
	}
	if closed.CloseReason != "done" {
		t.Errorf("close_reason = %q, want done", closed.CloseReason)
	}
	if closed.Closed.IsZero() {
		t.Error("Closed timestamp must be set")
	}

	// Hot-dir file must be gone.
	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + iss.ID + ".md"
	if _, err := m.ReadFile(hotPath); err == nil {
		t.Errorf("hot-dir file %s still exists after Close", hotPath)
	}

	// closed/ file must exist.
	closedPath := "/.tasks/closed/" + iss.ID + ".md"
	data, err := m.ReadFile(closedPath)
	if err != nil {
		t.Fatalf("closed file %s not found: %v", closedPath, err)
	}
	if len(data) == 0 {
		t.Error("closed file is empty")
	}
}

// TestClose_SidecarStaysInComments verifies that the comment sidecar is NOT
// moved when the task .md is relocated to closed/.
func TestClose_SidecarStaysInComments(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "has comment"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Add a comment so the sidecar exists.
	if _, err := s.AddComment(iss.ID, "alice", "pre-close note\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Sidecar must still be at comments/<id>.yml (not closed/).
	m := s.fs.(*vfs.Mem)
	sidecarPath := "/.tasks/comments/" + iss.ID + ".yml"
	if _, err := m.ReadFile(sidecarPath); err != nil {
		t.Errorf("sidecar at %s not found after Close: %v", sidecarPath, err)
	}
	// Sidecar must NOT exist in closed/comments/.
	wrongPath := "/.tasks/closed/comments/" + iss.ID + ".yml"
	if _, err := m.ReadFile(wrongPath); err == nil {
		t.Errorf("sidecar should NOT be at %s", wrongPath)
	}
}

// TestClose_GetFallsThroughToClosedDir verifies that Get() still finds a
// closed issue after the .md has been moved to closed/.
func TestClose_GetFallsThroughToClosedDir(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "to find after close"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after Close: %v", err)
	}
	if got.Status != StatusClosed {
		t.Errorf("status = %v, want closed", got.Status)
	}
}

// TestClose_ImmutableInPlace_UpdateRejected verifies that Update on a closed
// issue is rejected (in-place writes to closed/ are forbidden).
func TestClose_ImmutableInPlace_UpdateRejected(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "immutable"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	newTitle := "changed"
	_, err = s.Update(iss.ID, UpdateInput{Title: &newTitle})
	if err == nil {
		t.Fatal("Update on closed issue must return an error (immutable)")
	}
	// Should wrap ErrNotFound or be a ValidationError about the closed state.
	// The spec says "reject in-place write"; we test that it errors, not the exact type.
}

// TestClose_ImmutableInPlace_CloseEditRejected verifies that calling Close
// again (which would be an in-place edit) on an already-closed issue is also
// rejected. Note: the old behaviour was idempotent; the new spec says
// in-place writes to closed/ are forbidden — re-close must error.
func TestClose_ImmutableInPlace_CloseEditRejected(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "close twice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, "first"); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close on an already-closed issue: must error.
	_, err = s.Close(iss.ID, "second")
	if err == nil {
		t.Fatal("re-closing an already-closed issue must error (immutable in place)")
	}
}

// TestClose_CommentOnClosedIssue verifies that AddComment still works on a
// closed issue (sidecar append is the one exception to immutability).
func TestClose_CommentOnClosedIssue(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "comment after close"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// AddComment on a closed issue must succeed.
	c, err := s.AddComment(iss.ID, "alice", "post-close note\n")
	if err != nil {
		t.Fatalf("AddComment on closed issue: %v", err)
	}
	if c.Body != "post-close note\n" {
		t.Errorf("comment body = %q", c.Body)
	}

	// Verify it is retrievable.
	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != c.ID {
		t.Errorf("Comments() = %+v, want 1 comment", comments)
	}
}

// TestReopen_MovesBackToHot verifies that Reopen moves the .md back to the
// hot directory, clears closed/close_reason, and sets status open.
func TestReopen_MovesBackToHot(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "reopen me"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := s.Reopen(iss.ID)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if reopened.Status != StatusOpen {
		t.Errorf("status = %v, want open", reopened.Status)
	}
	if !reopened.Closed.IsZero() {
		t.Errorf("Closed field should be zero after Reopen, got %v", reopened.Closed)
	}
	if reopened.CloseReason != "" {
		t.Errorf("CloseReason should be empty after Reopen, got %q", reopened.CloseReason)
	}

	// Hot-dir file must exist.
	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + iss.ID + ".md"
	if _, err := m.ReadFile(hotPath); err != nil {
		t.Errorf("hot-dir file %s not found after Reopen: %v", hotPath, err)
	}
	// closed/ file must be gone.
	closedPath := "/.tasks/closed/" + iss.ID + ".md"
	if _, err := m.ReadFile(closedPath); err == nil {
		t.Errorf("closed/ file %s still exists after Reopen", closedPath)
	}
}

// TestReopen_EnablesWrites verifies that after Reopen, Update works again.
func TestReopen_EnablesWrites(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "original"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.Reopen(iss.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	newTitle := "updated after reopen"
	out, err := s.Update(iss.ID, UpdateInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update after Reopen: %v", err)
	}
	if out.Title != "updated after reopen" {
		t.Errorf("title = %q", out.Title)
	}
}

// TestReopen_NotFound verifies that Reopen on an unknown ID returns ErrNotFound.
func TestReopen_NotFound(t *testing.T) {
	s := newMemStoreForClose(t)

	_, err := s.Reopen("agt-9999")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestClose_FaultInjection_WriteAtomicToClosedDir verifies that if the
// WriteAtomic to closed/ fails during Close, no torn state is left: the issue
// is still readable from the hot dir and still open.
func TestClose_FaultInjection_WriteAtomicToClosedDir(t *testing.T) {
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

	iss, err := s.Create(CreateInput{Title: "fault test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Inject a WriteAtomic fault on the closed-dir path (step 2 of closeMove).
	closedPath := "/.tasks/closed/" + iss.ID + ".md"
	m.FailOn("WriteAtomic", closedPath, errors.New("simulated disk full in closed/"))

	_, err = s.Close(iss.ID, "done")
	if err == nil {
		t.Fatal("expected Close to fail due to injected WriteAtomic fault")
	}

	// The issue must still be in the hot dir and still be open.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after failed Close: %v", err)
	}
	if got.Status != StatusOpen {
		t.Errorf("status after failed Close = %v, want open (no torn state)", got.Status)
	}
}

// TestClose_FaultInjection_RenameAfterWrite verifies that if the final
// vfs.Rename fails during Close (after WriteAtomic to both closed/ and hot/),
// the issue is still findable (in either partition) and is closed.
// This tests that there is no data loss even when the Rename step fails.
func TestClose_FaultInjection_RenameAfterWrite(t *testing.T) {
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

	iss, err := s.Create(CreateInput{Title: "rename fault test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Inject a Rename fault on the hot-dir path (step 3 of closeMove).
	hotPath := "/.tasks/" + iss.ID + ".md"
	m.FailOn("Rename", hotPath, errors.New("simulated rename failure"))

	_, err = s.Close(iss.ID, "done")
	if err == nil {
		t.Fatal("expected Close to fail due to injected Rename fault")
	}

	// After the fault: the issue should still be findable (in either partition).
	// Get falls through to closed/ if the hot dir no longer has the file.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after failed Rename: %v", err)
	}
	// Data must not be lost; the issue is findable.
	if got.ID != iss.ID {
		t.Errorf("got wrong issue id %q", got.ID)
	}
	// And it reflects the closed state: step 3 wrote the closed content to the
	// hot file before the rename failed, so Get returns the closed version.
	// Asserting only the ID would pass even if recovery silently lost the close.
	if got.Status != StatusClosed {
		t.Errorf("status after failed Rename = %v, want closed (recovery via fall-through)", got.Status)
	}
}

// TestUpdateStatus_ClosedRoutesThroughClose verifies that Update with
// Status=StatusClosed routes through Close (moves to closed/).
func TestUpdateStatus_ClosedRoutesThroughClose(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "via update"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	st := StatusClosed
	out, err := s.Update(iss.ID, UpdateInput{Status: &st})
	if err != nil {
		t.Fatalf("Update --status closed: %v", err)
	}
	if out.Status != StatusClosed {
		t.Errorf("status = %v, want closed", out.Status)
	}

	// The file must be in closed/.
	m := s.fs.(*vfs.Mem)
	closedPath := "/.tasks/closed/" + iss.ID + ".md"
	if _, err := m.ReadFile(closedPath); err != nil {
		t.Errorf("closed file not found after Update --status closed: %v", err)
	}
}

// TestUpdateStatus_OpenRoutesThroughReopen verifies that Update with
// Status=StatusOpen on a closed issue routes through Reopen (moves back).
func TestUpdateStatus_OpenRoutesThroughReopen(t *testing.T) {
	s := newMemStoreForClose(t)

	iss, err := s.Create(CreateInput{Title: "reopen via update"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st := StatusOpen
	out, err := s.Update(iss.ID, UpdateInput{Status: &st})
	if err != nil {
		t.Fatalf("Update --status open (reopen): %v", err)
	}
	if out.Status != StatusOpen {
		t.Errorf("status = %v, want open", out.Status)
	}

	// Hot-dir file must be back.
	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + iss.ID + ".md"
	if _, err := m.ReadFile(hotPath); err != nil {
		t.Errorf("hot-dir file not found after Update --status open: %v", err)
	}
}
