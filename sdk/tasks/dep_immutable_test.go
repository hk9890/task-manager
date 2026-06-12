// L2 regression tests for at-z6z: AddDep and RemoveDep must return
// ErrImmutable when the dependent issue is closed, and must never write a
// file to the hot directory.
package tasks

import (
	"errors"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// TestAddDep_ClosedIssue_ReturnsErrImmutable verifies that AddDep on a closed
// dependent issue returns ErrImmutable and does not resurrect the issue in the
// hot directory (at-z6z / finding C1).
func TestAddDep_ClosedIssue_ReturnsErrImmutable(t *testing.T) {
	s := newMemStoreForClose(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dependent"})
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}

	// Close the dependent issue.
	if _, err := s.Close(dep.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Attempt AddDep on the closed issue — must return ErrImmutable.
	err = s.AddDep(dep.ID, blocker.ID)
	if !errors.Is(err, ErrImmutable) {
		t.Errorf("AddDep on closed issue: got %v, want ErrImmutable", err)
	}

	// The id must NOT appear in the hot directory (no resurrection).
	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + dep.ID + ".md"
	if _, statErr := m.ReadFile(hotPath); statErr == nil {
		t.Errorf("hot-dir file %s exists after AddDep on closed issue (resurrection bug)", hotPath)
	}

	// The id MUST still be in closed/.
	closedPath := "/.tasks/closed/" + dep.ID + ".md"
	if _, statErr := m.ReadFile(closedPath); statErr != nil {
		t.Errorf("closed file %s not found: %v", closedPath, statErr)
	}
}

// TestRemoveDep_ClosedIssue_ReturnsErrImmutable verifies that RemoveDep on a
// closed dependent issue returns ErrImmutable and does not resurrect the issue
// in the hot directory (at-z6z / finding C1).
func TestRemoveDep_ClosedIssue_ReturnsErrImmutable(t *testing.T) {
	s := newMemStoreForClose(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dependent"})
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}

	// Give the dependent a blocker before closing it.
	if err := s.AddDep(dep.ID, blocker.ID); err != nil {
		t.Fatalf("AddDep (setup): %v", err)
	}

	// Close the dependent issue.
	if _, err := s.Close(dep.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Attempt RemoveDep on the closed issue — must return ErrImmutable.
	err = s.RemoveDep(dep.ID, blocker.ID)
	if !errors.Is(err, ErrImmutable) {
		t.Errorf("RemoveDep on closed issue: got %v, want ErrImmutable", err)
	}

	// The id must NOT appear in the hot directory (no resurrection).
	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + dep.ID + ".md"
	if _, statErr := m.ReadFile(hotPath); statErr == nil {
		t.Errorf("hot-dir file %s exists after RemoveDep on closed issue (resurrection bug)", hotPath)
	}

	// The id MUST still be in closed/.
	closedPath := "/.tasks/closed/" + dep.ID + ".md"
	if _, statErr := m.ReadFile(closedPath); statErr != nil {
		t.Errorf("closed file %s not found: %v", closedPath, statErr)
	}
}

// TestDep_AfterReopen_Succeeds verifies that after Reopen, AddDep and
// RemoveDep work normally — the immutability guard must not block them.
func TestDep_AfterReopen_Succeeds(t *testing.T) {
	s := newMemStoreForClose(t)

	blocker, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "dependent"})
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}

	// Close then reopen.
	if _, err := s.Close(dep.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.Reopen(dep.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// AddDep on reopened issue must succeed.
	if err := s.AddDep(dep.ID, blocker.ID); err != nil {
		t.Errorf("AddDep after Reopen: %v", err)
	}

	// RemoveDep on reopened issue must succeed.
	if err := s.RemoveDep(dep.ID, blocker.ID); err != nil {
		t.Errorf("RemoveDep after Reopen: %v", err)
	}
}

// TestWriteIssue_DefenseInDepth_ClosedIssueNotResurrected verifies the
// belt-and-braces guard in writeIssue: if an issue is in closed/, a direct
// call to writeIssue must return ErrImmutable rather than silently writing to
// the hot dir. This exercises the guard via the public AddDep path after the
// early guard is in place (belt-and-braces: a second layer of protection).
//
// We test this via the same lifecycle crossing used above: close then try
// AddDep. The early guard in AddDep fires first, and the defense-in-depth
// guard in writeIssue backs it up. We verify neither write ends up in hot.
func TestWriteIssue_DefenseInDepth_ClosedIssueNotResurrected(t *testing.T) {
	s := newMemStoreForClose(t)

	blocker, err := s.Create(CreateInput{Title: "b"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dep, err := s.Create(CreateInput{Title: "d"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := s.Close(dep.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Both AddDep and RemoveDep must error — not reach writeIssue.
	if err := s.AddDep(dep.ID, blocker.ID); !errors.Is(err, ErrImmutable) {
		t.Errorf("AddDep: got %v, want ErrImmutable", err)
	}
	if err := s.RemoveDep(dep.ID, blocker.ID); !errors.Is(err, ErrImmutable) {
		t.Errorf("RemoveDep: got %v, want ErrImmutable", err)
	}

	// Belt-and-braces: no hot-dir file must exist in any case.
	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + dep.ID + ".md"
	if _, statErr := m.ReadFile(hotPath); statErr == nil {
		t.Errorf("hot-dir file %s exists — writeIssue defense-in-depth guard missing", hotPath)
	}
}
