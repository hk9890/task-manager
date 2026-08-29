// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
	s, _ := newMemStore(t)

	blocker, err := unwrap(s.Create(CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
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
	s, _ := newMemStore(t)

	blocker, err := unwrap(s.Create(CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
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
	s, _ := newMemStore(t)

	blocker, err := unwrap(s.Create(CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
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

// TestWriteIssue_ClosedIssueRefused calls writeIssue directly with an issue
// that lives in closed/, which is the only way to reach its defense-in-depth
// guard (TASK-STORAGE-SPEC §5). Every public mutator passes getMutable first,
// and that guard returns ErrImmutable before writeIssue is entered — so the
// second layer is unreachable from outside the package, and the test below,
// which drives AddDep/RemoveDep, never executes it.
//
// The guard exists for a future caller that skips getMutable. This test is what
// makes it a checked promise rather than a comment.
func TestWriteIssue_ClosedIssueRefused(t *testing.T) {
	s, _ := newMemStore(t)

	iss, err := unwrap(s.Create(CreateInput{Title: "to be closed"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	closed, err := unwrap(s.Close(iss.ID, ""))
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The issue now lives in closed/. Writing it back into the hot partition
	// would either duplicate it across both or silently resurrect it.
	if err := s.writeIssue(closed); !errors.Is(err, ErrImmutable) {
		t.Errorf("writeIssue on a closed issue: got %v, want ErrImmutable", err)
	}

	m := s.fs.(*vfs.Mem)
	hotPath := "/.tasks/" + iss.ID + ".md"
	if _, statErr := m.ReadFile(hotPath); statErr == nil {
		t.Errorf("writeIssue wrote %s: the closed issue was resurrected into the hot partition", hotPath)
	}

	// The refusal must not have disturbed the closed copy either.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after the refusal: %v", err)
	}
	if got.Status != StatusClosed {
		t.Errorf("status = %q after a refused writeIssue, want closed", got.Status)
	}
}

// TestWriteIssue_DefenseInDepth_ClosedIssueNotResurrected verifies the public
// half of the same rule: AddDep and RemoveDep on a closed issue are refused, and
// no hot-dir file is left behind.
//
// The refusal comes from the EARLY guard in getMutable, not from writeIssue —
// TestWriteIssue_ClosedIssueRefused above covers the second layer directly.
func TestWriteIssue_DefenseInDepth_ClosedIssueNotResurrected(t *testing.T) {
	s, _ := newMemStore(t)

	blocker, err := unwrap(s.Create(CreateInput{Title: "b"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dep, err := unwrap(s.Create(CreateInput{Title: "d"}))
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
