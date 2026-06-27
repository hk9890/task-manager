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
	"errors"
	"testing"
	"time"
)

// ptr is a tiny helper for *int priority fields.
func ptr(i int) *int { return &i }

var (
	tCreated = time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC)
	tUpdated = time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
	tClosed  = time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
	tComment = time.Date(2025, 2, 1, 12, 0, 0, 0, time.UTC)
)

func TestImportOpenPreservesTimestamps(t *testing.T) {
	s := newTestStore(t)
	iss, err := unwrap(s.Import(ImportInput{
		Title: "an imported task", Type: TypeBug, Priority: ptr(1),
		Status: StatusOpen, Created: tCreated, Updated: tUpdated,
		Labels: []string{"ext:ext-1"},
	}))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Created.Equal(tCreated) || !got.Updated.Equal(tUpdated) {
		t.Errorf("timestamps not preserved: created=%v updated=%v", got.Created, got.Updated)
	}
	if got.Status != StatusOpen || got.Type != TypeBug || got.Priority != 1 {
		t.Errorf("fields wrong: %+v", got)
	}
	if inClosed, _ := s.isInClosed(iss.ID); inClosed {
		t.Errorf("open issue must not be in closed/")
	}
}

func TestImportClosedLandsInClosedPartition(t *testing.T) {
	s := newTestStore(t)
	iss, err := unwrap(s.Import(ImportInput{
		Title: "old closed task", Status: StatusClosed,
		Created: tCreated, Updated: tUpdated, Closed: tClosed, CloseReason: "fixed",
	}))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	inClosed, err := s.isInClosed(iss.ID)
	if err != nil || !inClosed {
		t.Fatalf("closed issue must live in closed/ (inClosed=%v err=%v)", inClosed, err)
	}
	got, _ := s.Get(iss.ID)
	if !got.Closed.Equal(tClosed) {
		t.Errorf("closed timestamp not preserved: %v", got.Closed)
	}
	if !got.Updated.Equal(tUpdated) {
		t.Errorf("updated timestamp should be preserved (not now()): %v", got.Updated)
	}
	if got.CloseReason != "fixed" {
		t.Errorf("close reason = %q", got.CloseReason)
	}
}

func TestImportClosedDefaultsClosedTimestamp(t *testing.T) {
	s := newTestStore(t)
	// No Closed provided → defaults to Updated so the closed invariant holds.
	iss, err := unwrap(s.Import(ImportInput{
		Title: "closed no ts", Status: StatusClosed, Created: tCreated, Updated: tUpdated,
	}))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, _ := s.Get(iss.ID)
	if got.Closed.IsZero() || !got.Closed.Equal(tUpdated) {
		t.Errorf("expected closed defaulted to updated, got %v", got.Closed)
	}
}

func TestImportComments(t *testing.T) {
	s := newTestStore(t)
	iss, err := unwrap(s.Import(ImportInput{
		Title: "with comments", Status: StatusOpen, Created: tCreated,
		Comments: []ImportComment{
			{Author: "alice", Created: tComment, Body: "first note"},
			{Author: "bob", Body: "second note"}, // Created zero → defaults to issue Created
		},
	}))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	cs, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("want 2 comments, got %d", len(cs))
	}
	if cs[0].Author != "alice" || cs[0].Body != "first note" || !cs[0].Created.Equal(tComment) {
		t.Errorf("comment 0 wrong: %+v", cs[0])
	}
	if cs[1].Author != "bob" || !cs[1].Created.Equal(tCreated) {
		t.Errorf("comment 1 should default created to issue created: %+v", cs[1])
	}
}

func TestImportEdgesResolveInDependencyOrder(t *testing.T) {
	s := newTestStore(t)
	parent, err := unwrap(s.Import(ImportInput{ID: "agt-epic1", Title: "epic", Type: TypeEpic, Status: StatusOpen}))
	if err != nil {
		t.Fatalf("import parent: %v", err)
	}
	blocker, err := unwrap(s.Import(ImportInput{Title: "blocker", Status: StatusOpen}))
	if err != nil {
		t.Fatalf("import blocker: %v", err)
	}
	child, err := unwrap(s.Import(ImportInput{
		Title: "child", Status: StatusInProgress,
		Parent: parent.ID, BlockedBy: []string{blocker.ID}, Related: []string{blocker.ID},
	}))
	if err != nil {
		t.Fatalf("import child: %v", err)
	}
	got, _ := s.Get(child.ID)
	if got.Parent != parent.ID || len(got.BlockedBy) != 1 || len(got.Related) != 1 {
		t.Errorf("edges not set: %+v", got)
	}
}

func TestImportMissingRefRejected(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Import(ImportInput{Title: "child", Status: StatusOpen, Parent: "agt-nope"})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "parent" {
		t.Errorf("want parent ValidationError, got %v", err)
	}
}

func TestImportCallerSuppliedID(t *testing.T) {
	s := newTestStore(t)
	iss, err := unwrap(s.Import(ImportInput{ID: "agt-keepme", Title: "x", Status: StatusOpen}))
	if err != nil || iss.ID != "agt-keepme" {
		t.Fatalf("want id agt-keepme, got %q err=%v", iss.ID, err)
	}
	// Re-importing the same ID must be rejected.
	_, err = s.Import(ImportInput{ID: "agt-keepme", Title: "dup", Status: StatusOpen})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("want ErrAlreadyExists, got %v", err)
	}
}

func TestImportControlCharCommentRejectedAtomically(t *testing.T) {
	s := newTestStore(t)
	before, _ := s.All()
	_, err := s.Import(ImportInput{
		Title: "x", Status: StatusOpen,
		Comments: []ImportComment{{Author: "a", Body: "bad \x1b[31m esc"}},
	})
	if err == nil {
		t.Fatal("expected control-char comment to be rejected")
	}
	// Atomic: a rejected comment must leave NO issue behind.
	after, _ := s.All()
	if len(after) != len(before) {
		t.Errorf("import should not have written an issue: before=%d after=%d", len(before), len(after))
	}
}

func TestImportRejectsUnknownStatus(t *testing.T) {
	s := newTestStore(t)
	// An unrecognized status is rejected; the model stays strict.
	_, err := s.Import(ImportInput{Title: "x", Status: Status("wontfix"), Created: tCreated})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "status" {
		t.Errorf("want status ValidationError, got %v", err)
	}
}
