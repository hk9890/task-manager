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

package tasks

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// A write refuses a field violation it introduces and passes one it found
// (TASK-STORAGE-SPEC §10). Validating the proposed issue outright froze an issue
// that was already invalid: the constraints cover fields no input struct
// exposes, so the only refusal on offer was a permanent one.

// ── the rule, as pure logic (L1) ────────────────────────────────────────────

func TestFieldViolations_ReportsEveryFieldNotOnlyTheFirst(t *testing.T) {
	iss := &Issue{
		ID: "tst-1", Title: "", Status: StatusOpen, Type: TypeTask,
		Creator: strings.Repeat("c", maxCreatorLen+1),
		Labels:  []string{"NOPE"},
	}
	got := make(map[string]bool)
	for _, v := range fieldViolations(iss) {
		got[v.Field] = true
	}
	for _, want := range []string{"title", "creator", "labels"} {
		if !got[want] {
			t.Errorf("violations %v do not include %q; grandfathering the first would hide the rest", got, want)
		}
	}
	if err := validateFields(iss); err == nil || !strings.Contains(err.Error(), "title") {
		t.Errorf("validateFields must still report the first violation, got %v", err)
	}
}

func TestFieldUnchanged_ComparesEveryInputTheConstraintReads(t *testing.T) {
	base := &Issue{ID: "tst-1", Title: "t", Status: StatusClosed, Type: TypeTask, Labels: []string{"a"}, BlockedBy: []string{"tst-2"}}
	cases := []struct {
		name  string
		field string
		next  func(*Issue)
		want  bool
	}{
		{"same creator", "creator", func(i *Issue) {}, true},
		{"changed title", "title", func(i *Issue) { i.Title = "other" }, false},
		{"closed reads status too", "closed", func(i *Issue) { i.Status = StatusOpen }, false},
		{"labels compare by value", "labels", func(i *Issue) { i.Labels = []string{"b"} }, false},
		{"blocked_by reads the id too", "blocked_by", func(i *Issue) { i.ID = "tst-9" }, false},
		{"a field this build does not model", "future_field", func(i *Issue) {}, false},
	}
	for _, c := range cases {
		next := cloneIssue(base)
		c.next(next)
		if got := fieldUnchanged(c.field, base, next); got != c.want {
			t.Errorf("%s: fieldUnchanged(%q) = %v, want %v", c.name, c.field, got, c.want)
		}
	}
}

// ── the rule, through the store (L2) ────────────────────────────────────────

// seedStored hand-edits a stored issue, the way a restore, a merge or an older
// build leaves a value the engine would refuse to write.
func seedStored(t *testing.T, fs vfs.FS, s *Store, id string, edit func(*Issue)) {
	t.Helper()
	path := filepath.Join(s.dir, id+FileExt)
	data, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("read issue: %v", err)
	}
	iss, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("parse issue: %v", err)
	}
	edit(iss)
	if data, err = Marshal(iss); err != nil {
		t.Fatalf("marshal issue: %v", err)
	}
	if err := fs.WriteAtomic(path, data, 0o644); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
}

// seedInvalidCreator stores a creator past its length limit — a value
// `UpdateInput` has no field for, so no command can rewrite it.
func seedInvalidCreator(t *testing.T, fs vfs.FS, s *Store, id string) {
	t.Helper()
	seedStored(t, fs, s, id, func(iss *Issue) { iss.Creator = strings.Repeat("n", maxCreatorLen+1) })
}

func TestClose_AnInvalidStoredFieldDoesNotFreezeTheIssue(t *testing.T) {
	s, fs := chainStore(t)
	first, err := s.Create(CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Create(CreateInput{Title: "stuck"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Issue.ID
	seedInvalidCreator(t, fs, s, id)

	if _, err := s.Get(id); err != nil {
		t.Fatalf("the issue must still read: %v", err)
	}
	if err := s.AddDep(id, first.Issue.ID); err != nil {
		t.Errorf("dep add must not be refused by a field it does not touch: %v", err)
	}
	if _, err := s.Close(id, "done"); err != nil {
		t.Fatalf("close must not be refused by a field it does not touch: %v", err)
	}
	if _, err := s.Reopen(id); err != nil {
		t.Fatalf("reopen must not be refused either: %v", err)
	}
}

func TestUpdate_RefusesAViolationThisWriteIntroduces(t *testing.T) {
	s, fs := chainStore(t)
	res, err := s.Create(CreateInput{Title: "stuck"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Issue.ID
	seedInvalidCreator(t, fs, s, id)

	// An unrelated field is writable...
	if _, err := s.Update(id, UpdateInput{Title: strPtr("renamed")}); err != nil {
		t.Fatalf("an ordinary edit must still work: %v", err)
	}
	// ...and a violation of the caller's own making is still refused.
	_, err = s.Update(id, UpdateInput{Title: strPtr(strings.Repeat("t", maxTitleLen+1))})
	if err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("a violation this write introduces must be refused, got %v", err)
	}
}

// MONITORING.md defines io_error as a failed store *write*. A validation refusal
// touches no file, so recording it there fired the alert on rejected input.
func TestLogIOError_ValidationRefusalIsNotAnIOError(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, err := InitWithVFS("/p", "tst", vfs.NewMem(), WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()

	if _, err := s.Update(res.Issue.ID, UpdateInput{Title: strPtr("  ")}); err == nil {
		t.Fatal("an empty title must be refused")
	}
	if rec := find(records(t, &buf), "io_error"); rec != nil {
		t.Errorf("a validation refusal logged io_error: %v", rec)
	}
}

func strPtr(s string) *string { return &s }

// ── the same rule for the graph checks (L2) ─────────────────────────────────

func TestUpdate_AStoredGraphViolationDoesNotFreezeTheIssue(t *testing.T) {
	cases := map[string]func(a, b *Issue){
		"own parent":       func(a, b *Issue) { a.Parent = a.ID },
		"parent cycle":     func(a, b *Issue) { a.Parent = b.ID },
		"dangling parent":  func(a, b *Issue) { a.Parent = "tst-gone00" },
		"dangling blocker": func(a, b *Issue) { a.BlockedBy = []string{"tst-gone00"} },
		"dangling related": func(a, b *Issue) { a.Related = []string{"tst-gone00"} },
		"dependency cycle": func(a, b *Issue) { a.BlockedBy = []string{b.ID} },
	}
	for name, violate := range cases {
		t.Run(name, func(t *testing.T) {
			s, fs := chainStore(t)
			a := mustCreate(t, s, CreateInput{Title: "a"})
			b := mustCreate(t, s, CreateInput{Title: "b", Parent: a.ID, BlockedBy: []string{a.ID}})
			seedStored(t, fs, s, a.ID, func(stored *Issue) { violate(stored, b) })

			if _, err := s.Update(a.ID, UpdateInput{Title: strPtr("renamed")}); err != nil {
				t.Fatalf("a title edit must not be refused by an edge it does not touch: %v", err)
			}
			closed := StatusClosed
			if _, err := s.Update(a.ID, UpdateInput{Status: &closed}); err != nil {
				t.Errorf("a close through Update must not be refused either: %v", err)
			}
		})
	}
}

func TestUpdate_AStoredParentCycle_IsRepairedThroughTheParent(t *testing.T) {
	s, fs := chainStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b", Parent: a.ID})
	seedStored(t, fs, s, a.ID, func(stored *Issue) { stored.Parent = b.ID })

	if _, err := s.Update(a.ID, UpdateInput{Parent: strPtr("")}); err != nil {
		t.Fatalf("clearing the parent must break the cycle: %v", err)
	}
	_, err := s.Update(a.ID, UpdateInput{Parent: &b.ID})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "parent" || !strings.Contains(ve.Message, "parent cycle") {
		t.Errorf("writing the cycle back must be refused, got %v", err)
	}
}

func TestAddDep_AStoredDanglingBlocker_IsRefusedUntilItIsRemoved(t *testing.T) {
	s, fs := chainStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	seedStored(t, fs, s, a.ID, func(stored *Issue) { stored.BlockedBy = []string{"tst-gone00"} })

	err := s.AddDep(a.ID, b.ID)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "blocked_by" {
		t.Fatalf("a write to blocked_by must still check the whole list, got %v", err)
	}
	if err := s.RemoveDep(a.ID, "tst-gone00"); err != nil {
		t.Fatalf("removing the dangling blocker is the repair and must work: %v", err)
	}
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Errorf("the list is valid again, got %v", err)
	}
}

func TestRemoveDep_AnotherStoredDanglingBlocker_DoesNotRefuseTheRemoval(t *testing.T) {
	s, fs := chainStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	gone := []string{"tst-gone00", "tst-gone01"}
	seedStored(t, fs, s, a.ID, func(stored *Issue) { stored.BlockedBy = gone })

	for _, id := range gone {
		if err := s.RemoveDep(a.ID, id); err != nil {
			t.Fatalf("each removal is a repair, also while another bad edge stays: %v", err)
		}
	}
}

// The cycle walks end at closed/, so a cycle through a closed issue is on disk
// without ever being refused. The reopen is the write that brings it into the
// graph, although it changes no edge.
func TestUpdate_ReopenIntoADependencyCycle_IsRefused(t *testing.T) {
	s, _ := chainStore(t)
	b := mustCreate(t, s, CreateInput{Title: "b"})
	a := mustCreate(t, s, CreateInput{Title: "a", BlockedBy: []string{b.ID}})
	if _, err := s.Close(a.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDep(b.ID, a.ID); err != nil {
		t.Fatalf("an edge to a closed issue closes no cycle yet: %v", err)
	}

	open := StatusOpen
	_, err := s.Update(a.ID, UpdateInput{Status: &open})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "blocked_by" || !strings.Contains(ve.Message, "dependency cycle") {
		t.Fatalf("want the dependency-cycle validation error, got %v", err)
	}
	if got, err := s.Get(a.ID); err != nil || !got.Status.IsClosed() {
		t.Errorf("a must stay closed, got %+v (err %v)", got, err)
	}
}
