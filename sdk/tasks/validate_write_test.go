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

// seedInvalidCreator hand-edits an issue's frontmatter to carry a creator past
// its length limit — a value `UpdateInput` has no field for, so no command can
// rewrite it. It is the shape a restore or an older build leaves behind.
func seedInvalidCreator(t *testing.T, fs vfs.FS, s *Store, id string) {
	t.Helper()
	path := filepath.Join(s.dir, id+FileExt)
	data, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("read issue: %v", err)
	}
	raw := strings.Replace(string(data), "---\n", "---\ncreator: "+strings.Repeat("n", maxCreatorLen+1)+"\n", 1)
	if err := fs.WriteAtomic(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
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
