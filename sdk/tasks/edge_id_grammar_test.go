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
	"path/filepath"
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
	path := filepath.Join(s.dir, id+FileExt)
	data, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("read issue: %v", err)
	}
	raw := strings.Replace(string(data), "---\n", "---\nblocked_by:\n  - "+value+"\n", 1)
	if err := fs.WriteAtomic(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
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
