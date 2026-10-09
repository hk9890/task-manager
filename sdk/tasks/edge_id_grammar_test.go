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
	"slices"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// An edge value is an issue ID (TASK-STORAGE-SPEC §4). The reference check
// falls back to a Stat of closed/<value>.md, so "../<open-id>" resolved to the
// open issue's own file and passed: the value was stored verbatim, and the
// issue was listed as ready while its blocker was open.

// ── the rule, as pure logic (L1) ────────────────────────────────────────────

func TestFieldViolations_EdgeOutsideTheIDGrammar(t *testing.T) {
	cases := []struct {
		field string
		set   func(*Issue, string)
	}{
		{"parent", func(i *Issue, v string) { i.Parent = v }},
		{"blocked_by", func(i *Issue, v string) { i.BlockedBy = []string{"agt-0002", v} }},
		{"related", func(i *Issue, v string) { i.Related = []string{"agt-0002", v} }},
	}
	for _, c := range cases {
		for _, value := range []string{"../agt-0002", "closed/agt-0002", "agt-0002.md", "AGT-0002", " "} {
			iss := baseIssue()
			c.set(iss, value)
			err := validateFields(iss)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != c.field {
				t.Errorf("%s = %q: got %v, want a %s validation error", c.field, value, err, c.field)
			}
		}
		iss := baseIssue()
		c.set(iss, "agt-0003")
		if err := validateFields(iss); err != nil {
			t.Errorf("%s = a valid ID: got %v, want nil", c.field, err)
		}
	}
}

// ── the rule, through the store (L2) ────────────────────────────────────────

func TestCreate_EdgeOutsideTheIDGrammar_IsRefused(t *testing.T) {
	cases := map[string]func(string) CreateInput{
		"parent":     func(v string) CreateInput { return CreateInput{Title: "x", Parent: v} },
		"blocked_by": func(v string) CreateInput { return CreateInput{Title: "x", BlockedBy: []string{v}} },
		"related":    func(v string) CreateInput { return CreateInput{Title: "x", Related: []string{v}} },
	}
	for field, input := range cases {
		t.Run(field, func(t *testing.T) {
			s, _ := newMemStore(t)
			open, err := unwrap(s.Create(CreateInput{Title: "open"}))
			if err != nil {
				t.Fatal(err)
			}

			_, err = s.Create(input("../" + open.ID))
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != field {
				t.Fatalf("got %v, want a %s validation error", err, field)
			}
			if all, err := s.All(); err != nil || len(all) != 1 {
				t.Errorf("the store holds %d issues (err %v), want the one it had", len(all), err)
			}
		})
	}
}

func TestCreateBatch_EdgeOutsideTheIDGrammar_IsRefused(t *testing.T) {
	s, _ := newMemStore(t)
	open, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateBatch([]BatchEntry{
		{Ref: "a", CreateInput: CreateInput{Title: "a"}},
		{CreateInput: CreateInput{Title: "b", Parent: "a", BlockedBy: []string{"../" + open.ID}}},
	})
	var be *BatchEntryError
	var ve *ValidationError
	if !errors.As(err, &be) || be.Index != 1 || !errors.As(err, &ve) || ve.Field != "blocked_by" {
		t.Fatalf("got %v, want a blocked_by validation error on entry 2", err)
	}
	if all, err := s.All(); err != nil || len(all) != 1 {
		t.Errorf("the store holds %d issues (err %v), want the one it had", len(all), err)
	}
}

func TestAddEdge_TargetOutsideTheIDGrammar_IsRefused(t *testing.T) {
	cases := map[string]func(*Store, string, string) error{
		"blocked_by": (*Store).AddDep,
		"related":    (*Store).AddRelated,
	}
	for field, add := range cases {
		t.Run(field, func(t *testing.T) {
			s, _ := newMemStore(t)
			open, err := unwrap(s.Create(CreateInput{Title: "open"}))
			if err != nil {
				t.Fatal(err)
			}
			iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
			if err != nil {
				t.Fatal(err)
			}

			err = add(s, iss.ID, "../"+open.ID)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != field {
				t.Fatalf("got %v, want a %s validation error", err, field)
			}
			got, err := s.Get(iss.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.BlockedBy)+len(got.Related) != 0 {
				t.Errorf("a refused edge was stored: blocked_by %v, related %v", got.BlockedBy, got.Related)
			}
		})
	}
}

func TestUpdate_ParentOutsideTheIDGrammar_IsRefused(t *testing.T) {
	s, _ := newMemStore(t)
	open, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Update(iss.ID, UpdateInput{Parent: strPtr("../" + open.ID)})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "parent" {
		t.Fatalf("got %v, want a parent validation error", err)
	}
}

// seedBlocker hand-edits an issue's frontmatter to carry value as its one
// blocker — the shape a build that did not check the grammar left behind.
func seedBlocker(t *testing.T, fs vfs.FS, s *Store, id, value string) {
	t.Helper()
	seedStored(t, fs, s, id, func(iss *Issue) { iss.BlockedBy = []string{value} })
}

func TestUpdate_StoredEdgeOutsideTheIDGrammar_DoesNotFreezeTheIssue(t *testing.T) {
	s, fs := newMemStore(t)
	open, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}
	other, err := unwrap(s.Create(CreateInput{Title: "other"}))
	if err != nil {
		t.Fatal(err)
	}
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	stored := "../" + open.ID
	seedBlocker(t, fs, s, iss.ID, stored)
	dangling, err := unwrap(s.Create(CreateInput{Title: "y"}))
	if err != nil {
		t.Fatal(err)
	}
	seedBlocker(t, fs, s, dangling.ID, "../gone")
	if _, err := s.Update(dangling.ID, UpdateInput{Title: strPtr("renamed")}); err != nil {
		t.Fatalf("a stored value that names no file must not freeze the issue either: %v", err)
	}

	// A write that leaves the value alone goes through...
	got, err := unwrap(s.Update(iss.ID, UpdateInput{Title: strPtr("renamed")}))
	if err != nil {
		t.Fatalf("update --title must not be refused by an edge it does not touch: %v", err)
	}
	if got.Title != "renamed" || len(got.BlockedBy) != 1 || got.BlockedBy[0] != stored {
		t.Errorf("got title %q, blocked_by %v; want the rename and the stored value kept", got.Title, got.BlockedBy)
	}
	// ...a write to that list is this write's own violation...
	err = s.AddDep(iss.ID, other.ID)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "blocked_by" {
		t.Errorf("dep add onto the invalid list: got %v, want a blocked_by validation error", err)
	}
	// ...and removing the value is the repair.
	if err := s.RemoveDep(iss.ID, stored); err != nil {
		t.Fatalf("dep rm of the invalid value: %v", err)
	}
	if err := s.AddDep(iss.ID, other.ID); err != nil {
		t.Errorf("dep add after the repair: %v", err)
	}
}

// ── a value already stored, on the read side (L2) ───────────────────────────

// The write path keeps a stored value, so the read path meets it. Joined onto
// closed/ it named the open issue's own file: the blocker counted as resolved
// and the issue stayed listed as ready.
func TestReady_StoredBlockerOutsideTheIDGrammar_IsNotResolved(t *testing.T) {
	s, fs := newMemStore(t)
	open, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	seedBlocker(t, fs, s, iss.ID, "../"+open.ID)

	ready, err := s.Ready()
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].ID != open.ID {
		t.Errorf("ready = %v, want only %s: a blocker that names no issue is not resolved", ids(ready), open.ID)
	}
	blocked, err := s.Blocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].Issue.ID != iss.ID {
		t.Errorf("blocked holds %d issues, want only %s", len(blocked), iss.ID)
	}
}

// seedNote writes a markdown file that is no issue beside the store, where
// "../../notes" joined onto a store directory lands.
func seedNote(t *testing.T, fs vfs.FS) {
	t.Helper()
	if err := fs.WriteAtomic("/notes.md", []byte("# notes\n"), 0o644); err != nil {
		t.Fatalf("seed note: %v", err)
	}
}

func TestDetail_StoredEdgeOutsideTheIDGrammar_IsNotResolved(t *testing.T) {
	s, fs := newMemStore(t)
	open, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	seedNote(t, fs)
	seedStored(t, fs, s, iss.ID, func(iss *Issue) {
		iss.Parent = "../" + open.ID
		iss.BlockedBy = []string{"../" + open.ID}
		iss.Related = []string{"../../notes"}
	})

	d, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatalf("an issue that stores such a value must stay readable: %v", err)
	}
	if d.ParentRef != nil || len(d.BlockedByRefs)+len(d.RelatedRefs) != 0 {
		t.Errorf("parent %v, blockers %v, related %v; want none resolved", d.ParentRef, d.BlockedByRefs, d.RelatedRefs)
	}
}

func TestRemoveRelated_TargetOutsideTheIDGrammar_HasNoInverseSide(t *testing.T) {
	s, fs := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	seedNote(t, fs)
	seedStored(t, fs, s, iss.ID, func(iss *Issue) { iss.Related = []string{"../../notes"} })

	if err := s.RemoveRelated(iss.ID, "../../notes"); err != nil {
		t.Fatalf("rel rm of the invalid value is the repair and must not read the file it names: %v", err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Related) != 0 {
		t.Errorf("related = %v, want the value removed", got.Related)
	}
}

// RemoveRelated writes both sides. The inverse side's write only removes a
// value, so a value outside the grammar that stays in its list must not refuse
// it: the first side had already been written, and the link was left half cut.
func TestRemoveRelated_InverseSideStoresAValueOutsideTheIDGrammar_CutsBothSides(t *testing.T) {
	s, fs := newMemStore(t)
	a, err := unwrap(s.Create(CreateInput{Title: "a"}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := unwrap(s.Create(CreateInput{Title: "b"}))
	if err != nil {
		t.Fatal(err)
	}
	c, err := unwrap(s.Create(CreateInput{Title: "c"}))
	if err != nil {
		t.Fatal(err)
	}
	seedStored(t, fs, s, a.ID, func(iss *Issue) { iss.Related = []string{b.ID} })
	seedStored(t, fs, s, b.ID, func(iss *Issue) { iss.Related = []string{"../x", a.ID} })

	if err := s.RemoveRelated(a.ID, b.ID); err != nil {
		t.Fatalf("rel rm: %v", err)
	}
	gotA, err := s.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := s.Get(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA.Related) != 0 || !slices.Equal(gotB.Related, []string{"../x"}) {
		t.Errorf("related: a %v, b %v; want a empty and b left with only the stored value", gotA.Related, gotB.Related)
	}

	// The tolerance is for a removal only.
	err = s.AddRelated(b.ID, c.ID)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "related" {
		t.Errorf("rel add onto the invalid list: got %v, want a related validation error", err)
	}
}

// A refused add names the value at fault. When that value is one the list
// already stored, the caller did not pass it, so the message says it is stored
// and names the command that removes it.
func TestAddEdge_StoredValueNamesNoIssue_NamesTheStoredValueAndTheRepair(t *testing.T) {
	cases := []struct {
		field, stored, reason, repair string
		seed                          func(*Issue, string)
		add                           func(*Store, string, string) error
	}{
		{"blocked_by", "../agt-1", "is not a valid issue ID", "dep rm", func(i *Issue, v string) { i.BlockedBy = []string{v} }, (*Store).AddDep},
		{"related", "../x", "is not a valid issue ID", "rel rm", func(i *Issue, v string) { i.Related = []string{v} }, (*Store).AddRelated},
		{"blocked_by", "agt-gone", "does not exist", "dep rm", func(i *Issue, v string) { i.BlockedBy = []string{v} }, (*Store).AddDep},
		{"related", "agt-gone", "does not exist", "rel rm", func(i *Issue, v string) { i.Related = []string{v} }, (*Store).AddRelated},
	}
	for _, c := range cases {
		t.Run(c.field+" "+c.stored, func(t *testing.T) {
			s, fs := newMemStore(t)
			iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
			if err != nil {
				t.Fatal(err)
			}
			other, err := unwrap(s.Create(CreateInput{Title: "other"}))
			if err != nil {
				t.Fatal(err)
			}
			seedStored(t, fs, s, iss.ID, func(iss *Issue) { c.seed(iss, c.stored) })

			err = c.add(s, iss.ID, other.ID)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != c.field {
				t.Fatalf("got %v, want a %s validation error", err, c.field)
			}
			repair := "taskmgr " + c.repair + " -- " + iss.ID + ` "` + c.stored + `"`
			for _, want := range []string{`stored value "` + c.stored + `"`, c.reason, repair} {
				if !strings.Contains(ve.Message, want) {
					t.Errorf("message %q does not contain %q", ve.Message, want)
				}
			}
		})
	}
}

func TestAddDep_PassedValueOutsideTheIDGrammar_IsNotReportedAsStored(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}

	err = s.AddDep(iss.ID, "../x")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "blocked_by" {
		t.Fatalf("got %v, want a blocked_by validation error", err)
	}
	if !strings.Contains(ve.Message, `"../x"`) || strings.Contains(ve.Message, "stored value") {
		t.Errorf("message %q; want the passed value named, and not as a stored one", ve.Message)
	}
}
