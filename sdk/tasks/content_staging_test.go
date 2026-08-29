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

// L2 tests for the staged half of the overflow write path: when an issue
// ALREADY has an external body, the replacement is written beside the old one
// and renamed over it only after the .md lands.
//
// The order is the whole contract. Writing the new body straight to the final
// path would leave a failed .md write with a committed body the frontmatter
// never named — the one outcome the staging exists to prevent. Nothing reached
// that branch before this file: every other overflow test creates an issue,
// where there is no previous body to protect and the bytes go direct.
package tasks

import (
	"errors"
	"strings"
	"testing"
)

// newOverflowedIssue creates an issue whose body is already external, which is
// the precondition for the staged path.
func newOverflowedIssue(t *testing.T, s *Store, marker string) *Issue {
	t.Helper()
	iss, err := unwrap(s.Create(CreateInput{Title: "doc", Description: bigBody(marker)}))
	if err != nil {
		t.Fatalf("Create with an overflowed body: %v", err)
	}
	if _, err := s.fs.Stat(s.contentPath(iss.ID)); err != nil {
		t.Fatalf("test setup: no content sidecar at %s: %v", s.contentPath(iss.ID), err)
	}
	return iss
}

// TestWriteFiles_ReplacedBody_StagesBesideTheOldOne proves the staging is real:
// with the .md write failing, the previous body must still be what a read
// returns, and the staged file must not have been committed over it.
func TestWriteFiles_ReplacedBody_StagesBesideTheOldOne(t *testing.T) {
	s, m := newMemStore(t)
	iss := newOverflowedIssue(t, s, "first-generation")

	// Fail the .md write only. The glob cannot match the sidecar paths: the
	// staged body is under content/ and carries no .md suffix.
	boom := errors.New("injected .md write failure")
	m.FailOn("WriteAtomic", "/.tasks/*.md", boom)

	replacement := bigBody("second-generation")
	if _, err := s.Update(iss.ID, UpdateInput{Description: &replacement}); !errors.Is(err, boom) {
		t.Fatalf("Update with a failing .md write: got %v, want the injected error", err)
	}

	// The committed body is still the first one: nothing was published.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after the failed update: %v", err)
	}
	if !strings.HasPrefix(got.Description, "first-generation") {
		t.Errorf("body starts %.20q, want the first generation — a failed .md write committed a new body",
			got.Description)
	}

	// The staged file is garbage once the .md write fails, and leaving it would
	// be a body nothing points at.
	if _, err := m.Stat(s.stagedContentPath(iss.ID)); err == nil {
		t.Errorf("staged body %s survived a failed .md write", s.stagedContentPath(iss.ID))
	}
}

// TestWriteFiles_ReplacedBody_CommitsAfterTheMD is the success half: the staged
// file is renamed over the live sidecar and does not linger.
func TestWriteFiles_ReplacedBody_CommitsAfterTheMD(t *testing.T) {
	s, m := newMemStore(t)
	iss := newOverflowedIssue(t, s, "first-generation")

	replacement := bigBody("second-generation")
	if _, err := s.Update(iss.ID, UpdateInput{Description: &replacement}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(got.Description, "second-generation") {
		t.Errorf("body starts %.20q, want the second generation", got.Description)
	}
	if _, err := m.Stat(s.stagedContentPath(iss.ID)); err == nil {
		t.Errorf("staged body %s was left behind after a successful commit", s.stagedContentPath(iss.ID))
	}
}

// TestWriteFiles_ReplacedBody_FailedCommitDoesNotLoseTheOldOne covers the rename
// itself failing: the .md is written but the body is not published. The write
// must be reported as failed rather than reporting success over a body that is
// not there.
func TestWriteFiles_ReplacedBody_FailedCommitDoesNotLoseTheOldOne(t *testing.T) {
	s, m := newMemStore(t)
	iss := newOverflowedIssue(t, s, "first-generation")

	// The commit is a rename of the staged body over the live sidecar; the glob
	// is that exact path, so no other rename in the write can absorb the fault.
	boom := errors.New("injected rename failure")
	m.FailOn("Rename", s.stagedContentPath(iss.ID), boom)

	replacement := bigBody("second-generation")
	if _, err := s.Update(iss.ID, UpdateInput{Description: &replacement}); !errors.Is(err, boom) {
		t.Fatalf("Update with a failing commit rename: got %v, want the injected error", err)
	}

	// Whichever generation is on disk, a body must still be there: the failure
	// may not leave the issue pointing at nothing.
	if _, err := m.Stat(s.contentPath(iss.ID)); err != nil {
		t.Errorf("the live content sidecar is gone after a failed commit: %v", err)
	}
}

// TestWriteFiles_NewOverflow_WritesDirect pins the other side of the branch: a
// body that overflows for the FIRST time has no previous body to protect, so it
// goes straight to the final path with no staged file in between.
func TestWriteFiles_NewOverflow_WritesDirect(t *testing.T) {
	s, m := newMemStore(t)

	iss, err := unwrap(s.Create(CreateInput{Title: "doc", Description: bigBody("only-generation")}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Stat(s.contentPath(iss.ID)); err != nil {
		t.Errorf("no content sidecar at the final path: %v", err)
	}
	if _, err := m.Stat(s.stagedContentPath(iss.ID)); err == nil {
		t.Errorf("a first overflow staged at %s: there is no previous body to protect",
			s.stagedContentPath(iss.ID))
	}
}
