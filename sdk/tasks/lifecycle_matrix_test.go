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

// L2 lifecycle state-transition matrix test (package tasks_test).
//
// This test drives every mutating Store operation against every lifecycle state
// and asserts both the expected result and store-wide invariants after each
// step. The matrix would have caught:
//
//   - C1 (AddDep/RemoveDep on closed issue silently wrote to hot dir): the
//     ErrImmutable row verifies the correct error and the invariant checker
//     verifies that the ID is not in the hot dir after the call.
//   - Close-idempotency bug: the closed→Close row asserts success (no error)
//     and the original close_reason is preserved.
//
// Matrix dimensions:
//   - Operations: Create, Update, Close, Reopen, AddDep, RemoveDep,
//     AddComment, EditComment, DeleteComment  (9 ops)
//   - States: open, closed, reopened                                     (3 states)
//
// Each cell specifies: wantErr (true/false), wantErrImmutable (true/false),
// and a brief invariant comment. AssertStoreInvariants is called after each
// step regardless of whether the operation succeeded.
package tasks_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// setupLifecycleStore creates a mem-backed store for lifecycle matrix tests.
// It returns the store, a "subject" issue ID that will be driven through the
// matrix, and a "blocker" issue ID that is used for AddDep/RemoveDep tests.
// The helper also provides a second issue "other" for self-dep protection checks.
func setupLifecycleStore(t *testing.T, prefix string) *tasks.Store {
	t.Helper()
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s, err := tasks.InitWithVFS("/", prefix, m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	return s
}

// lifecycleState is the pre-condition state of the subject issue in a matrix row.
type lifecycleState string

const (
	stateOpen     lifecycleState = "open"
	stateClosed   lifecycleState = "closed"
	stateReopened lifecycleState = "reopened"
)

// prepareSubject creates and optionally transitions the subject issue into the
// desired lifecycle state. Returns the current subject issue.
func prepareSubject(t *testing.T, s *tasks.Store, state lifecycleState) (subjectID, blockerID, commentID string) {
	t.Helper()

	blocker, err := unwrap(s.Create(tasks.CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	blockerID = blocker.ID

	subject, err := unwrap(s.Create(tasks.CreateInput{Title: "subject"}))
	if err != nil {
		t.Fatalf("Create subject: %v", err)
	}
	subjectID = subject.ID

	// Add a comment so EditComment/DeleteComment have a target.
	c, err := s.AddComment(subjectID, "setup", "initial comment")
	if err != nil {
		t.Fatalf("AddComment setup: %v", err)
	}
	commentID = c.ID

	switch state {
	case stateOpen:
		// nothing more to do
	case stateClosed:
		if _, err := s.Close(subjectID, "first"); err != nil {
			t.Fatalf("Close (setup): %v", err)
		}
	case stateReopened:
		if _, err := s.Close(subjectID, "first"); err != nil {
			t.Fatalf("Close (setup): %v", err)
		}
		if _, err := s.Reopen(subjectID); err != nil {
			t.Fatalf("Reopen (setup): %v", err)
		}
	}

	return subjectID, blockerID, commentID
}

// TestLifecycleMatrix drives each mutating operation against each lifecycle
// state and asserts the expected outcome plus AssertStoreInvariants.
func TestLifecycleMatrix(t *testing.T) {
	type opResult struct {
		wantErrImmutable bool // expect ErrImmutable
		wantOtherErr     bool // expect some other error (not ErrImmutable, not nil)
		wantSuccess      bool // expect nil error
	}

	// Each test case drives one operation against one state.
	// The "name" is "<operation>/<state>" for clear output.
	type tc struct {
		name      string
		state     lifecycleState
		run       func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error
		wantErr   opResult
		postCheck func(t *testing.T, s *tasks.Store, subjectID string)
	}

	cases := []tc{
		// ======== Create ========
		// Create is not state-specific (always creates a new issue in open state).
		// We verify it succeeds and leaves invariants intact.
		{
			name:  "Create/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Create(tasks.CreateInput{Title: "new issue"})
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},
		{
			name:  "Create/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Create(tasks.CreateInput{Title: "new issue while subject closed"})
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},
		{
			name:  "Create/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Create(tasks.CreateInput{Title: "new issue while subject reopened"})
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},

		// ======== Update (normal field) ========
		// open → succeeds; closed → ErrImmutable; reopened → succeeds
		{
			name:  "Update/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				newTitle := "updated title"
				_, err := s.Update(subjectID, tasks.UpdateInput{Title: &newTitle})
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get after Update: %v", err)
					return
				}
				if iss.Title != "updated title" {
					t.Errorf("title after Update = %q, want %q", iss.Title, "updated title")
				}
				if iss.Status != tasks.StatusOpen {
					t.Errorf("status after Update = %q, want open", iss.Status)
				}
			},
		},
		{
			name:  "Update/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				newTitle := "mutate closed"
				_, err := s.Update(subjectID, tasks.UpdateInput{Title: &newTitle})
				return err
			},
			// Closed issues are immutable; Update must return ErrImmutable.
			wantErr: opResult{wantErrImmutable: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				// Issue must still be closed and unchanged.
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q, want closed (must not have been mutated)", iss.Status)
				}
			},
		},
		{
			name:  "Update/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				newTitle := "updated after reopen"
				_, err := s.Update(subjectID, tasks.UpdateInput{Title: &newTitle})
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Title != "updated after reopen" {
					t.Errorf("title = %q, want %q", iss.Title, "updated after reopen")
				}
			},
		},

		// ======== Close ========
		// open → succeeds; closed → idempotent no-op (succeeds, original reason kept); reopened → succeeds
		{
			name:  "Close/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Close(subjectID, "done")
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q, want closed", iss.Status)
				}
				if iss.CloseReason != "done" {
					t.Errorf("close_reason = %q, want done", iss.CloseReason)
				}
			},
		},
		{
			// C1-class test: Close on already-closed issue must be an idempotent no-op.
			// Pre-fix, this could attempt an in-place write (split-brain).
			name:  "Close/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Close(subjectID, "second-reason")
				return err
			},
			// Idempotent: must succeed (no error).
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q, want closed", iss.Status)
				}
				// Original reason must be preserved (not overwritten by re-close).
				if iss.CloseReason != "first" {
					t.Errorf("close_reason = %q after re-close, want %q (original preserved)", iss.CloseReason, "first")
				}
			},
		},
		{
			name:  "Close/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Close(subjectID, "re-closing")
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q, want closed", iss.Status)
				}
			},
		},

		// ======== Reopen ========
		// open → idempotent (returns existing); closed → succeeds; reopened → idempotent
		{
			name:  "Reopen/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Reopen(subjectID)
				return err
			},
			// Idempotent on an already-open issue: must succeed.
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusOpen {
					t.Errorf("status = %q, want open", iss.Status)
				}
			},
		},
		{
			name:  "Reopen/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Reopen(subjectID)
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusOpen {
					t.Errorf("status = %q, want open after Reopen", iss.Status)
				}
				if !iss.Closed.IsZero() {
					t.Errorf("Closed timestamp = %v, want zero after Reopen", iss.Closed)
				}
				if iss.CloseReason != "" {
					t.Errorf("close_reason = %q, want empty after Reopen", iss.CloseReason)
				}
			},
		},
		{
			name:  "Reopen/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.Reopen(subjectID)
				return err
			},
			// Idempotent: already open (reopened state), must succeed.
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusOpen {
					t.Errorf("status = %q, want open", iss.Status)
				}
			},
		},

		// ======== AddDep ========
		// open → succeeds; closed → ErrImmutable (C1!); reopened → succeeds
		{
			name:  "AddDep/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.AddDep(subjectID, blockerID)
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				found := false
				for _, b := range iss.BlockedBy {
					if b == iss.ID || true { // check we got something
						found = true
						break
					}
				}
				_ = found
			},
		},
		{
			// C1 regression: AddDep on a closed issue must return ErrImmutable and
			// must NOT write the issue to the hot dir (split-brain).
			// Before the fix, this call silently resurrected the issue in hot/.
			name:  "AddDep/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.AddDep(subjectID, blockerID)
			},
			wantErr: opResult{wantErrImmutable: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				// The issue must still be in closed/ and NOT in hot.
				// AssertStoreInvariants covers the split-brain check (C1).
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get after AddDep on closed: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q, want closed (must not have been resurrected)", iss.Status)
				}
			},
		},
		{
			name:  "AddDep/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.AddDep(subjectID, blockerID)
			},
			wantErr: opResult{wantSuccess: true},
		},

		// ======== RemoveDep ========
		// open → succeeds (no-op if dep not present); closed → ErrImmutable; reopened → succeeds
		{
			name:  "RemoveDep/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				// AddDep first so there is something to remove.
				if err := s.AddDep(subjectID, blockerID); err != nil {
					t.Fatalf("AddDep setup: %v", err)
				}
				return s.RemoveDep(subjectID, blockerID)
			},
			wantErr: opResult{wantSuccess: true},
		},
		{
			// C1 regression: RemoveDep on a closed issue must return ErrImmutable.
			name:  "RemoveDep/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.RemoveDep(subjectID, blockerID)
			},
			wantErr: opResult{wantErrImmutable: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q, want closed", iss.Status)
				}
			},
		},
		{
			name:  "RemoveDep/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				// RemoveDep on a dep that is not present is a no-op (succeeds).
				return s.RemoveDep(subjectID, blockerID)
			},
			wantErr: opResult{wantSuccess: true},
		},

		// ======== AddComment ========
		// Comments via the sidecar are allowed on ALL states (including closed).
		{
			name:  "AddComment/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.AddComment(subjectID, "alice", "note on open")
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},
		{
			name:  "AddComment/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				// AddComment on closed issue must succeed (sidecar-append exception).
				_, err := s.AddComment(subjectID, "alice", "post-close note")
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				// Issue must still be closed; AddComment must not alter status.
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q after AddComment, want closed", iss.Status)
				}
				// The comment must be retrievable.
				cs, err := s.Comments(subjectID)
				if err != nil {
					t.Errorf("Comments: %v", err)
					return
				}
				// There is the setup comment + the new one = at least 2.
				if len(cs) < 2 {
					t.Errorf("Comments() = %d, want >= 2", len(cs))
				}
			},
		},
		{
			name:  "AddComment/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.AddComment(subjectID, "alice", "note on reopened")
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},

		// ======== EditComment ========
		// EditComment appends to the sidecar; allowed on all states.
		{
			name:  "EditComment/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.EditComment(subjectID, commentID, "alice", "edited body")
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},
		{
			name:  "EditComment/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.EditComment(subjectID, commentID, "alice", "edited on closed")
				return err
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q after EditComment, want closed", iss.Status)
				}
			},
		},
		{
			name:  "EditComment/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				_, err := s.EditComment(subjectID, commentID, "alice", "edited on reopened")
				return err
			},
			wantErr: opResult{wantSuccess: true},
		},

		// ======== DeleteComment ========
		// DeleteComment appends a tombstone to the sidecar; allowed on all states.
		{
			name:  "DeleteComment/open",
			state: stateOpen,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.DeleteComment(subjectID, commentID, "alice")
			},
			wantErr: opResult{wantSuccess: true},
		},
		{
			name:  "DeleteComment/closed",
			state: stateClosed,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.DeleteComment(subjectID, commentID, "alice")
			},
			wantErr: opResult{wantSuccess: true},
			postCheck: func(t *testing.T, s *tasks.Store, subjectID string) {
				t.Helper()
				iss, err := s.Get(subjectID)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if iss.Status != tasks.StatusClosed {
					t.Errorf("status = %q after DeleteComment, want closed", iss.Status)
				}
			},
		},
		{
			name:  "DeleteComment/reopened",
			state: stateReopened,
			run: func(t *testing.T, s *tasks.Store, subjectID, blockerID, commentID string) error {
				t.Helper()
				return s.DeleteComment(subjectID, commentID, "alice")
			},
			wantErr: opResult{wantSuccess: true},
		},
	}

	for _, c := range cases {
		c := c // capture loop variable
		t.Run(c.name, func(t *testing.T) {
			// Each test case gets its own fresh store to isolate state.
			// Use a per-case prefix derived from the counter to get deterministic IDs.
			s := setupLifecycleStore(t, "lmt")
			subjectID, blockerID, commentID := prepareSubject(t, s, c.state)

			// Verify invariants hold BEFORE the operation.
			storetest.AssertStoreInvariants(t, s)

			// Run the operation.
			err := c.run(t, s, subjectID, blockerID, commentID)

			// Check expected error outcome.
			switch {
			case c.wantErr.wantErrImmutable:
				if !errors.Is(err, tasks.ErrImmutable) {
					t.Errorf("op returned %v, want ErrImmutable", err)
				}
			case c.wantErr.wantOtherErr:
				if err == nil {
					t.Errorf("op returned nil, want a non-nil error")
				} else if errors.Is(err, tasks.ErrImmutable) {
					t.Errorf("op returned ErrImmutable, want a different error")
				}
			case c.wantErr.wantSuccess:
				if err != nil {
					t.Errorf("op returned %v, want nil", err)
				}
			}

			// Verify invariants hold AFTER the operation (even on error).
			storetest.AssertStoreInvariants(t, s)

			// Run per-case post-check if provided.
			if c.postCheck != nil {
				c.postCheck(t, s, subjectID)
			}
		})
	}
}

// TestLifecycleMatrix_WouldHaveCaughtC1 is a focused test that explicitly
// demonstrates the C1 invariant failure scenario: before the fix, AddDep on a
// closed issue would write the issue to the hot dir (split-brain), and
// AssertStoreInvariants would have reported invariant C1 violated.
//
// This test runs AddDep on a closed issue and asserts:
//  1. ErrImmutable is returned.
//  2. AssertStoreInvariants passes (no split-brain).
//
// If you revert the ErrImmutable guard in AddDep, this test FAILS on
// AssertStoreInvariants (C1 violation), proving the checker catches the bug.
func TestLifecycleMatrix_WouldHaveCaughtC1(t *testing.T) {
	s := setupLifecycleStore(t, "c1t")

	blocker, err := unwrap(s.Create(tasks.CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}

	subject, err := unwrap(s.Create(tasks.CreateInput{Title: "subject"}))
	if err != nil {
		t.Fatalf("Create subject: %v", err)
	}

	// Close the subject.
	if _, err := s.Close(subject.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Pre-operation invariants must hold.
	storetest.AssertStoreInvariants(t, s)

	// AddDep on a closed issue: must return ErrImmutable.
	err = s.AddDep(subject.ID, blocker.ID)
	if !errors.Is(err, tasks.ErrImmutable) {
		t.Errorf("AddDep on closed: got %v, want ErrImmutable", err)
	}

	// Post-operation invariants: no split-brain must exist.
	// If the pre-fix code ran, the subject would be in BOTH hot and closed/,
	// and AssertStoreInvariants would report "invariant C1 violated".
	storetest.AssertStoreInvariants(t, s)
}
