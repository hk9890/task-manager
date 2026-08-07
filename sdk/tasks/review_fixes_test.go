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

// L2 regression tests for defects found reviewing the v0.6.1..v0.6.2+ range:
// the content sidecar's path and write ordering, the cost and failure modes of
// resolving a body inside the shared read primitive, and the registry's handling
// of stores that are present but unusable.
package tasks

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// TestUnmarshal_RejectsIDThatEscapesTheStore pins that an issue ID is checked
// against the grammar at the parse boundary.
//
// The sidecar path is built by joining the frontmatter id onto the store's
// content dir, so an id of "../../../../etc/passwd" reads and writes that path.
// The .tasks directory is git-tracked, which makes the frontmatter reachable by
// a pull request, not just by the store's owner.
func TestUnmarshal_RejectsIDThatEscapesTheStore(t *testing.T) {
	for _, id := range []string{
		"../../../../etc/passwd",
		"../sibling",
		"a/b",
		"tst-0001/../../x",
		"",
		".",
	} {
		if _, err := Unmarshal([]byte("---\nid: " + id + "\ntitle: t\n---\n")); err == nil {
			t.Errorf("Unmarshal accepted id %q", id)
		}
	}

	// And the read paths inherit it: a hand-planted file cannot make the store
	// key anything on such an id.
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	evil := "---\nid: ../../../../etc/passwd\ntitle: pwn\nbody_external: true\n---\n"
	if err := m.WriteAtomic(filepath.Join(s.dir, "notes.md"), []byte(evil), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.All(); err == nil {
		t.Error("All() accepted a file whose frontmatter id escapes the store")
	}
	if _, err := s.Get("../../../../etc/passwd"); err == nil {
		t.Error("Get() resolved an id that escapes the store")
	}
}

// TestMetadataReadsDoNotNeedTheSidecar pins that operations which only need an
// issue's metadata or its existence do not read its body.
//
// Resolving inside the shared read primitive made every such caller pay a full
// body read — and turned a missing sidecar into a hard failure for operations
// that never touch the body, so a comment could not be added to a doc whose
// sidecar had gone missing. A removed sidecar is the observable stand-in for
// "did it read the body?".
func TestMetadataReadsDoNotNeedTheSidecar(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "doc", Type: TypeDoc, Description: bigBody("body")})
	if err := m.Remove(s.contentPath(iss.ID)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Comments(iss.ID); err != nil {
		t.Errorf("Comments: %v", err)
	}
	if _, err := s.AddComment(iss.ID, "hans", "a note"); err != nil {
		t.Errorf("AddComment: %v", err)
	}
	// A duplicate-ID check reports "taken" without materializing the body.
	_, err = s.Create(CreateInput{ID: iss.ID, Title: "clash"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create with a taken id: err = %v, want ErrAlreadyExists", err)
	}

	// Get still resolves — that is its contract — and still reports the loss.
	if _, err := s.Get(iss.ID); err == nil {
		t.Error("Get must still surface a missing sidecar")
	}
}

// TestDetail_BodyExternalSurvivesClose pins that Detail reports where a body
// actually lives for a CLOSED issue too.
//
// A closed issue is never in the hot index, so Detail reaches it through the
// single-issue read. Resolving the body there clears the mirror flag before
// Detail copies it, and the flag reads false for exactly the issues most likely
// to be overflowed — a viewer stops warning about a huge body the moment the
// issue is closed.
func TestDetail_BodyExternalSurvivesClose(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "doc", Type: TypeDoc, Description: bigBody("body")})

	hot, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hot.BodyExternal {
		t.Fatal("open overflowed issue: BodyExternal = false, want true")
	}
	if _, err := unwrap(s.Close(iss.ID, "done")); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.BodyExternal {
		t.Error("closed overflowed issue: BodyExternal = false, want true")
	}
	if !strings.Contains(closed.Description, "body") {
		t.Error("the body must still be resolved for the caller")
	}
}

// TestDetail_RefResolutionFailureIsNotSwallowed pins the two ends of a ref that
// is not in the hot index: a dangling one is dropped, an unreadable one is an
// error.
//
// Discarding every error there meant an I/O failure silently removed a blocker
// from the rendered issue: a blocked issue printed with no Blocked-by line and
// exit 0, which reads as "ready to work on".
func TestDetail_RefResolutionFailureIsNotSwallowed(t *testing.T) {
	newPair := func(t *testing.T) (*vfs.Mem, *Store, *Issue, *Issue) {
		t.Helper()
		m := vfs.NewMem()
		s, err := InitWithVFS("/p", "tst", m)
		if err != nil {
			t.Fatal(err)
		}
		blocker := mustCreate(t, s, CreateInput{Title: "blocker"})
		dependent := mustCreate(t, s, CreateInput{Title: "dependent"})
		if err := s.AddDep(dependent.ID, blocker.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := unwrap(s.Close(blocker.ID, "done")); err != nil {
			t.Fatal(err)
		}
		return m, s, dependent, blocker
	}

	t.Run("unreadable blocker is an error", func(t *testing.T) {
		m, s, dependent, blocker := newPair(t)
		m.FailOn("ReadFile", s.closedFilePath(blocker.ID), errors.New("disk error"))
		if _, err := s.Detail(dependent.ID); err == nil {
			t.Error("Detail must not report a blocked issue as unblocked when the blocker cannot be read")
		}
	})

	t.Run("dangling blocker is dropped", func(t *testing.T) {
		m, s, dependent, blocker := newPair(t)
		if err := m.Remove(s.closedFilePath(blocker.ID)); err != nil {
			t.Fatal(err)
		}
		d, err := s.Detail(dependent.ID)
		if err != nil {
			t.Fatalf("a hand-edited store must stay readable: %v", err)
		}
		if len(d.BlockedByRefs) != 0 {
			t.Errorf("BlockedByRefs = %v, want the dangling ref dropped", d.BlockedByRefs)
		}
	})
}

// TestOverflow_StaleSidecarRemovalDoesNotFailTheWrite pins that a failure while
// deleting a now-stale sidecar does not report a committed write as failed.
//
// The removal happens AFTER the .md has landed, so by then the mutation is real:
// returning an error makes the CLI exit non-zero and skips the post-hooks for a
// change the next read will show. The leftover file is inert — the .md says the
// body is inline, so nothing points at it.
func TestOverflow_StaleSidecarRemovalDoesNotFailTheWrite(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "doc", Description: bigBody("big")})

	small := "now small"
	m.FailOn("Remove", s.contentPath(iss.ID), errors.New("read-only content dir"))
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Description: &small})); err != nil {
		t.Fatalf("the mutation was committed; it must not be reported as failed: %v", err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != small {
		t.Errorf("Description = %q, want the committed body", got.Description)
	}
}

// TestMoveStoreDir_NeverPublishesAHalfBuiltStore pins that the destination is
// created by one atomic rename.
//
// The registry entry is live before the files move, so anything visible at the
// destination is resolvable. Copying into place publishes it a file at a time,
// and the moment config.yaml lands another process opens the half-copied folder
// as a finished store and writes into it — under a lock file that is not the one
// the promote holds.
func TestMoveStoreDir_NeverPublishesAHalfBuiltStore(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/proj", "/dev/proj/.tasks", "prj")
	dst := storeDirFor("demo")
	staging := filepath.Join(filepath.Dir(dst), stagingPrefix+"demo")

	// Fail the publishing rename: the tree is fully staged, nothing is at dst.
	m.FailOn("MoveTree", staging, errors.New("simulated crash"))
	if _, err := moveToCentralWith("/dev/proj", "demo", m, fakeEnv(nil), nil); err == nil {
		t.Fatal("expected the injected fault to fail the promote")
	}
	if _, err := m.Stat(filepath.Join(dst, ConfigFileName)); !vfs.IsNotExist(err) {
		t.Errorf("a half-built store was published at %s", dst)
	}

	// The entry is rolled back, so the project does not resolve to a store that
	// is not there.
	if _, _, err := resolveWith(ResolveOptions{WorkDir: "/dev/proj"}, m, fakeEnv(nil), nil); !errors.Is(err, ErrNoStore) {
		t.Errorf("err = %v, want the entry rolled back", err)
	}
}

// TestMoveToCentral_RetryAfterAPartialCopy pins that a leftover from a failed
// promote does not wedge the command.
//
// A partial copy left at the destination fails every retry with ErrStoreExists —
// under the same name (taken) and under any other (the project path is taken) —
// until the user works out that they have to delete it by hand. Staging keeps
// the leftover out of the destination's way, and the next attempt clears it.
func TestMoveToCentral_RetryAfterAPartialCopy(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/proj", "/dev/proj/.tasks", "prj")
	staging := filepath.Join(testCentral, storesSubdir, stagingPrefix+"demo")

	// Seed the debris a died-part-way copy leaves behind.
	if err := m.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteAtomic(filepath.Join(staging, "prj-old111.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := moveToCentralWith("/dev/proj", "demo", m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("a leftover staging directory must not block a retry: %v", err)
	}
	if s.Dir() != storeDirFor("demo") {
		t.Errorf("store dir = %q, want %q", s.Dir(), storeDirFor("demo"))
	}
	if _, err := m.Stat(filepath.Join(staging, "prj-old111.md")); !vfs.IsNotExist(err) {
		t.Error("the stale staging tree should have been cleared, not carried into the new store")
	}
	if _, err := m.Stat(filepath.Join(storeDirFor("demo"), "prj-old111.md")); !vfs.IsNotExist(err) {
		t.Error("stale debris was published into the promoted store")
	}
}

// TestRelinkCentral_RefusesAnUnfinishedStore pins that relink applies the same
// completeness test resolution does.
//
// Checking only that the folder is a directory lets relink report success on an
// entry the very next command skips: CLI-SPEC promises relink "will not create a
// dangling entry", and that is exactly what it created.
func TestRelinkCentral_RefusesAnUnfinishedStore(t *testing.T) {
	m := vfs.NewMem()
	writeRegistry(t, m, testCentral, registryEntry{Path: "/dev/old", Store: "half"})
	if err := m.MkdirAll(storeDirFor("half"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.MkdirAll("/dev/new", 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := relinkCentralWith("half", "/dev/new", m, fakeEnv(nil))
	if err == nil {
		t.Fatal("relink onto a folder with no config.yaml should fail")
	}
	if !errors.Is(err, ErrNoStore) {
		t.Errorf("err = %v, want ErrNoStore", err)
	}
	if !strings.Contains(err.Error(), "not a finished store") {
		t.Errorf("err = %v, want it to say the store is unfinished", err)
	}
}
