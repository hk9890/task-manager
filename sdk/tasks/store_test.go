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
	"strings"
	"testing"
)

func mustCreate(t *testing.T, s *Store, in CreateInput) *Issue {
	t.Helper()
	iss, err := unwrap(s.Create(in))
	if err != nil {
		t.Fatalf("Create(%q): %v", in.Title, err)
	}
	return iss
}

func TestCreate_AllocatesUniqueIDs(t *testing.T) {
	s, _ := newMemStore(t)
	a := mustCreate(t, s, CreateInput{Title: "first"})
	b := mustCreate(t, s, CreateInput{Title: "second"})
	for _, id := range []string{a.ID, b.ID} {
		if !strings.HasPrefix(id, "agt-") || !idRe.MatchString(id) {
			t.Errorf("id = %q, want a valid agt- prefixed ID", id)
		}
	}
	if a.ID == b.ID {
		t.Fatalf("allocated IDs must be unique, both = %q", a.ID)
	}
	// Defaults applied.
	if a.Type != TypeTask || a.Priority != PriorityDefault || a.Status != StatusOpen {
		t.Errorf("defaults wrong: %+v", a)
	}
}

// TestCreate_ExplicitID covers the CreateInput.ID escape hatch (at-2fb): a
// caller-supplied ID is honoured when valid, and rejected when malformed,
// carrying the wrong prefix, or already in use.
func TestCreate_ExplicitID(t *testing.T) {
	s, _ := newMemStore(t)

	// Valid explicit ID (incl. legacy numeric form) is used verbatim.
	got := mustCreate(t, s, CreateInput{ID: "agt-0042", Title: "pinned"})
	if got.ID != "agt-0042" {
		t.Fatalf("explicit ID not honoured: got %q, want agt-0042", got.ID)
	}
	if _, err := s.Get("agt-0042"); err != nil {
		t.Fatalf("Get(agt-0042): %v", err)
	}

	// Duplicate is rejected.
	if _, err := s.Create(CreateInput{ID: "agt-0042", Title: "dup"}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("duplicate explicit ID: err = %v, want ErrAlreadyExists", err)
	}

	// Wrong prefix and malformed IDs are rejected.
	for _, bad := range []string{"xyz-0001", "agt_0001", "AGT-0001", "agt-", "nodash"} {
		if _, err := s.Create(CreateInput{ID: bad, Title: "bad"}); err == nil {
			t.Errorf("explicit ID %q should have been rejected", bad)
		}
	}
}

func TestCreate_Validates(t *testing.T) {
	s, _ := newMemStore(t)
	if _, err := s.Create(CreateInput{Title: "  "}); err == nil {
		t.Error("empty title should fail")
	}
	p := 9
	if _, err := s.Create(CreateInput{Title: "x", Priority: &p}); err == nil {
		t.Error("out-of-range priority should fail")
	}
	if _, err := s.Create(CreateInput{Title: "x", Type: Type("nonsense")}); err == nil {
		t.Error("unknown type should fail")
	}
}

func TestCreate_RejectsMissingRefs(t *testing.T) {
	s, _ := newMemStore(t)
	if _, err := s.Create(CreateInput{Title: "x", BlockedBy: []string{"agt-9999"}}); err == nil {
		t.Error("missing blocker should fail")
	}
	if _, err := s.Create(CreateInput{Title: "x", Parent: "agt-9999"}); err == nil {
		t.Error("missing parent should fail")
	}
}

func TestGet_NotFound(t *testing.T) {
	s, _ := newMemStore(t)
	if _, err := s.Get("agt-0001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdate_Partial(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "orig"})

	newTitle := "changed"
	pr := 0
	st := StatusInProgress
	out, err := unwrap(s.Update(iss.ID, UpdateInput{Title: &newTitle, Priority: &pr, Status: &st}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "changed" || out.Priority != 0 || out.Status != StatusInProgress {
		t.Errorf("update not applied: %+v", out)
	}
	if !out.Updated.After(iss.Updated) {
		t.Errorf("Updated should advance: %v -> %v", iss.Updated, out.Updated)
	}
	// Untouched fields remain.
	reloaded, _ := s.Get(iss.ID)
	if reloaded.Type != TypeTask {
		t.Errorf("type should be unchanged, got %v", reloaded.Type)
	}
}

func TestUpdate_Labels(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x", Labels: []string{"a", "b"}})

	out, _ := unwrap(s.Update(iss.ID, UpdateInput{AddLabels: []string{"c"}, RemoveLabels: []string{"a"}}))
	if got := labelSet(out.Labels); !got["b"] || !got["c"] || got["a"] {
		t.Errorf("add/remove labels wrong: %v", out.Labels)
	}

	out, _ = unwrap(s.Update(iss.ID, UpdateInput{SetLabels: []string{"x", "y"}}))
	if len(out.Labels) != 2 || out.Labels[0] != "x" {
		t.Errorf("set labels wrong: %v", out.Labels)
	}

	out, _ = unwrap(s.Update(iss.ID, UpdateInput{ClearLabels: true}))
	if len(out.Labels) != 0 {
		t.Errorf("clear labels wrong: %v", out.Labels)
	}
}

func TestStatusClosed_StampsAndClears(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})

	closedStatus := StatusClosed
	out, _ := unwrap(s.Update(iss.ID, UpdateInput{Status: &closedStatus}))
	if out.Closed.IsZero() {
		t.Error("closing via update should stamp Closed")
	}
	openStatus := StatusOpen
	out, _ = unwrap(s.Update(iss.ID, UpdateInput{Status: &openStatus}))
	if !out.Closed.IsZero() || out.CloseReason != "" {
		t.Errorf("reopening should clear Closed/reason: %v / %q", out.Closed, out.CloseReason)
	}
}

func TestAddComment_SanitizesBody(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})
	// sanitizeCommentBody strips trailing whitespace per line, not leading.
	// "a note\n" is a clean body; use it directly.
	c, err := s.AddComment(iss.ID, "hans", "a note\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Body != "a note\n" || c.Author != "hans" {
		t.Errorf("comment wrong: %+v", c)
	}
	if len(c.ID) != 8 {
		t.Errorf("comment ID length = %d, want 8", len(c.ID))
	}
	// Verify via Comments() that it's stored in the sidecar.
	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != c.ID {
		t.Errorf("Comments() = %+v, want 1 comment with id %q", comments, c.ID)
	}
}

func TestAddDep_RejectsCycle(t *testing.T) {
	s, _ := newMemStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})

	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatalf("re-add dep: %v", err)
	}
	reloaded, _ := s.Get(a.ID)
	if len(reloaded.BlockedBy) != 1 {
		t.Errorf("expected one blocker, got %v", reloaded.BlockedBy)
	}
	// Adding the reverse edge would close a cycle and must fail.
	if err := s.AddDep(b.ID, a.ID); err == nil {
		t.Error("expected cycle rejection")
	}
	if err := s.AddDep(a.ID, a.ID); err == nil {
		t.Error("self-dependency should fail")
	}
}

// TestAddDep_TransitiveCycle covers the multi-hop cycle that the direct 2-node
// case in TestAddDep_RejectsCycle never reaches: a -> b -> c -> a. Closing the loop
// must exercise findCycle's gray back-edge / stack-slicing branch.
func TestAddDep_TransitiveCycle(t *testing.T) {
	s, _ := newMemStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	c := mustCreate(t, s, CreateInput{Title: "c"})

	// A valid deep chain a -> b -> c (a blocked by b, b blocked by c) is fine.
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDep(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	// Closing the loop transitively (c blocked by a) forms a 3-node cycle and
	// must be rejected — this is the boundary the direct 2-node case misses.
	if err := s.AddDep(c.ID, a.ID); err == nil {
		t.Error("expected transitive cycle a -> b -> c -> a to be rejected")
	}
	// The rejected edge must not have been persisted.
	if reloaded, _ := s.Get(c.ID); len(reloaded.BlockedBy) != 0 {
		t.Errorf("rejected cycle edge leaked: c.BlockedBy = %v, want empty", reloaded.BlockedBy)
	}
}

func TestRemoveDep_DropsTheEdge(t *testing.T) {
	s, _ := newMemStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := s.Get(a.ID)
	if len(reloaded.BlockedBy) != 0 {
		t.Errorf("blocker not removed: %v", reloaded.BlockedBy)
	}
}

func labelSet(ls []string) map[string]bool {
	m := map[string]bool{}
	for _, l := range ls {
		m[l] = true
	}
	return m
}
