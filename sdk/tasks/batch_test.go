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
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// ---- L1: the ref rule ----

func TestResolveBatchRefs_ReplacesRefsAndKeepsIDs(t *testing.T) {
	entries := []BatchEntry{
		{Ref: "child", CreateInput: CreateInput{Parent: "epic", BlockedBy: []string{"x-exists", "other"}, Related: []string{"epic"}}},
		{Ref: "epic"},
		{Ref: "other"},
	}
	got, _, err := resolveBatchRefs("x", entries, []string{"x-1", "x-2", "x-3"})
	if err != nil {
		t.Fatal(err)
	}
	want := CreateInput{ID: "x-1", Parent: "x-2", BlockedBy: []string{"x-exists", "x-3"}, Related: []string{"x-2"}}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestResolveBatchRefs_DuplicateRef_NamesTheSecondEntry(t *testing.T) {
	_, failed, err := resolveBatchRefs("x", []BatchEntry{{Ref: "a"}, {Ref: "a"}}, []string{"x-1", "x-2"})
	var ve *ValidationError
	if failed != 1 || !errors.As(err, &ve) || ve.Field != "ref" {
		t.Errorf("want a ref validation error on entry index 1, got index %d, %v", failed, err)
	}
}

func TestResolveBatchRefs_RefWithStorePrefix_IsRefused(t *testing.T) {
	_, _, err := resolveBatchRefs("x", []BatchEntry{{Ref: "x-abc123"}}, []string{"x-1"})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "ref" {
		t.Errorf("a ref that could equal an issue ID must be refused, got %v", err)
	}
}

func TestBatchWriteOrder_WritesAnIssueAfterEveryIssueItNames(t *testing.T) {
	issues := []*Issue{
		{ID: "child", Parent: "epic", BlockedBy: []string{"schema"}},
		{ID: "schema", Parent: "epic", Related: []string{"outside-the-set"}},
		{ID: "epic"},
	}
	if got, want := batchWriteOrder(issues), []int{2, 1, 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v (epic, schema, child)", got, want)
	}
}

func TestBatchWriteOrder_MutuallyRelated_KeepsEveryIssueOnce(t *testing.T) {
	issues := []*Issue{{ID: "a", Related: []string{"b"}}, {ID: "b", Related: []string{"a"}}}
	got := batchWriteOrder(issues)
	slices.Sort(got)
	if !reflect.DeepEqual(got, []int{0, 1}) {
		t.Errorf("order = %v, want each index once", got)
	}
}

func TestFindParentCycle_ReportsTheChainBackToStart(t *testing.T) {
	idx := map[string]*Issue{
		"a": {ID: "a", Parent: "b"},
		"b": {ID: "b", Parent: "a"},
		"c": {ID: "c", Parent: "a"},
		"d": {ID: "d", Parent: "closed-or-gone"},
	}
	if got := findParentCycle(idx, "a"); got != "a -> b -> a" {
		t.Errorf("cycle from a = %q", got)
	}
	// c hangs below a cycle but is not on it; d's parent is outside the index.
	for _, id := range []string{"c", "d"} {
		if got := findParentCycle(idx, id); got != "" {
			t.Errorf("cycle from %s = %q, want none", id, got)
		}
	}
}

// ---- L2: the set, all or nothing ----

func batchStore(t *testing.T) (*Store, *vfs.Mem) {
	t.Helper()
	m := vfs.NewMem()
	s, err := InitWithVFS("/", "x", m)
	if err != nil {
		t.Fatal(err)
	}
	return s, m
}

func assertStoreEmpty(t *testing.T, s *Store) {
	t.Helper()
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a refused set wrote %d issues, want 0", len(all))
	}
}

func TestCreateBatch_ForwardRefs_BuildTheGraph(t *testing.T) {
	s, _ := batchStore(t)
	existing := mustCreate(t, s, CreateInput{Title: "already here"})

	// Children first: every edge points at an entry further down the set.
	res, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{Title: "second", Parent: "e", BlockedBy: []string{"first", existing.ID}}},
		{Ref: "first", CreateInput: CreateInput{Title: "first", Parent: "e"}},
		{Ref: "e", CreateInput: CreateInput{Title: "epic", Type: TypeEpic}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, first, epic := res[0].Issue, res[1].Issue, res[2].Issue
	stored, err := s.Get(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Parent != epic.ID || !reflect.DeepEqual(stored.BlockedBy, []string{first.ID, existing.ID}) {
		t.Errorf("stored edges parent=%q blocked_by=%v, want %q and [%s %s]", stored.Parent, stored.BlockedBy, epic.ID, first.ID, existing.ID)
	}
	if !second.Created.Equal(epic.Created) {
		t.Errorf("a set shares one creation instant; got %v and %v", second.Created, epic.Created)
	}
}

func TestCreateBatch_UnknownEdge_WritesNothing(t *testing.T) {
	s, _ := batchStore(t)
	_, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{Title: "ok"}},
		{Ref: "b", CreateInput: CreateInput{Title: "dangling", BlockedBy: []string{"nope"}}},
	})
	var be *BatchEntryError
	var ve *ValidationError
	if !errors.As(err, &be) || be.Index != 1 || be.Ref != "b" || !errors.As(err, &ve) || ve.Field != "blocked_by" {
		t.Fatalf("want a blocked_by validation error on entry index 1, got %v", err)
	}
	assertStoreEmpty(t, s)
}

func TestCreateBatch_CycleAcrossEntries_WritesNothing(t *testing.T) {
	s, _ := batchStore(t)
	_, err := s.CreateBatch([]BatchEntry{
		{Ref: "a", CreateInput: CreateInput{Title: "a", BlockedBy: []string{"b"}}},
		{Ref: "b", CreateInput: CreateInput{Title: "b", BlockedBy: []string{"a"}}},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "blocked_by" {
		t.Fatalf("want the dependency-cycle validation error, got %v", err)
	}
	assertStoreEmpty(t, s)
}

func TestCreateBatch_ParentCycleAcrossEntries_WritesNothing(t *testing.T) {
	s, _ := batchStore(t)
	_, err := s.CreateBatch([]BatchEntry{
		{Ref: "a", CreateInput: CreateInput{Title: "a", Parent: "b"}},
		{Ref: "b", CreateInput: CreateInput{Title: "b", Parent: "a"}},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "parent" {
		t.Fatalf("want the parent-cycle validation error, got %v", err)
	}
	assertStoreEmpty(t, s)
}

func TestCreateBatch_EntryRelatedToItself_WritesNothing(t *testing.T) {
	s, _ := batchStore(t)
	_, err := s.CreateBatch([]BatchEntry{{Ref: "a", CreateInput: CreateInput{Title: "a", Related: []string{"a"}}}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "related" {
		t.Fatalf("want the self-related validation error, got %v", err)
	}
	assertStoreEmpty(t, s)
}

// A set cut short between two writes must leave only issues whose edges
// resolve, so the files land referenced-first whatever the entry order.
func TestCreateBatch_WritesReferencedEntriesFirst(t *testing.T) {
	s, m := batchStore(t)
	diskFull := errors.New("simulated disk full")
	// The child comes first in the set. Were it also written first, the epic's
	// failing write would find it on disk and have to remove it — and that
	// removal is rigged to fail and show up in the error.
	m.FailOn("WriteAtomic", s.filePath("x-epic01"), diskFull)
	m.FailOn("Remove", s.filePath("x-child1"), errors.New("the child was on disk before its parent"))

	_, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{ID: "x-child1", Title: "child", Parent: "e"}},
		{Ref: "e", CreateInput: CreateInput{ID: "x-epic01", Title: "epic", Type: TypeEpic}},
	})
	var be *BatchEntryError
	if !errors.Is(err, diskFull) || !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("want the disk error on the epic (entry index 1), got %v", err)
	}
	if strings.Contains(err.Error(), "before its parent") {
		t.Errorf("the child was written before the epic it names: %v", err)
	}
	assertStoreEmpty(t, s)
}

func TestCreateBatch_Denied_CarriesTheHintsOfTheEntriesThatPassed(t *testing.T) {
	fake := &exec.Fake{Func: func(spec exec.Spec) exec.Result {
		if bytes.Contains(spec.Stdin, []byte(`"second"`)) {
			return exec.Deny(1, "no")
		}
		return exec.Allow("hint for the first")
	}}
	s, _ := hookTestStore(t, fake, []Hook{{ID: "gate", Event: "pre-create", Run: []string{"g"}}})

	_, err := s.CreateBatch([]BatchEntry{{CreateInput: CreateInput{Title: "first"}}, {CreateInput: CreateInput{Title: "second"}}})
	var de *HookDeniedError
	if !errors.As(err, &de) || !reflect.DeepEqual(de.Hints, []string{"hint for the first"}) {
		t.Fatalf("want the denial to carry the first entry's hint, got %v (%+v)", err, de)
	}
}

func TestCreateBatch_InvalidField_WritesNothing(t *testing.T) {
	s, _ := batchStore(t)
	_, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{Title: "ok"}},
		{CreateInput: CreateInput{Title: ""}},
	})
	var be *BatchEntryError
	if !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("want entry index 1 refused, got %v", err)
	}
	assertStoreEmpty(t, s)
}

func TestCreateBatch_OneEntryOverTheBound_WritesNothing(t *testing.T) {
	s, _ := batchStore(t)
	entries := make([]BatchEntry, maxBatchEntries+1)
	for i := range entries {
		entries[i].Title = "t"
	}
	var ve *ValidationError
	if _, err := s.CreateBatch(entries); !errors.As(err, &ve) || ve.Field != "entries" {
		t.Fatalf("want an entries validation error, got %v", err)
	}
	assertStoreEmpty(t, s)
	if _, err := s.CreateBatch(entries[:maxBatchEntries]); err != nil {
		t.Errorf("a set of exactly %d entries must be accepted: %v", maxBatchEntries, err)
	}
}

func TestCreateBatch_EmptySet_IsRefused(t *testing.T) {
	s, _ := batchStore(t)
	var ve *ValidationError
	if _, err := s.CreateBatch(nil); !errors.As(err, &ve) {
		t.Errorf("want a validation error for an empty set, got %v", err)
	}
}

func TestCreateBatch_PreCreateDeniesOneEntry_WritesNothingAndRunsNoPostHook(t *testing.T) {
	fake := &exec.Fake{Func: func(spec exec.Spec) exec.Result {
		if envVal(spec, "TASKMGR_HOOK_EVENT") == "pre-create" && bytes.Contains(spec.Stdin, []byte(`"third"`)) {
			return exec.Deny(1, "not this one")
		}
		return exec.Allow("")
	}}
	s, _ := hookTestStore(t, fake, []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"g"}},
		{ID: "notify", Event: "post-create", Run: []string{"n"}},
	})

	_, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{Title: "first"}},
		{CreateInput: CreateInput{Title: "second"}},
		{Ref: "t", CreateInput: CreateInput{Title: "third"}},
	})
	var be *BatchEntryError
	var de *HookDeniedError
	if !errors.As(err, &be) || be.Index != 2 || be.Ref != "t" || !errors.As(err, &de) || de.Reason != "not this one" {
		t.Fatalf("want the hook denial on entry index 2, got %v", err)
	}
	assertStoreEmpty(t, s)
	for _, call := range fake.Calls() {
		if envVal(call, "TASKMGR_HOOK_EVENT") == "post-create" {
			t.Error("a post-create hook ran for a set that was refused")
		}
	}
}

func TestCreateBatch_Allowed_RunsEveryPreHookBeforeTheFirstPostHook(t *testing.T) {
	fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Allow("hint") }}
	s, _ := hookTestStore(t, fake, []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"g"}},
		{ID: "notify", Event: "post-create", Run: []string{"n"}},
	})

	res, err := s.CreateBatch([]BatchEntry{{CreateInput: CreateInput{Title: "a"}}, {CreateInput: CreateInput{Title: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, call := range fake.Calls() {
		events = append(events, envVal(call, "TASKMGR_HOOK_EVENT"))
	}
	if want := []string{"pre-create", "pre-create", "post-create", "post-create"}; !reflect.DeepEqual(events, want) {
		t.Errorf("hook order = %v, want %v", events, want)
	}
	if got := res[1].Hints; !reflect.DeepEqual(got, []string{"hint", "hint"}) {
		t.Errorf("each result carries its own pre and post hints; got %v", got)
	}
}

func TestCreateBatch_WriteFailsMidSet_RemovesWhatWasWritten(t *testing.T) {
	s, m := batchStore(t)
	diskFull := errors.New("simulated disk full")
	m.FailOn("WriteAtomic", s.filePath("x-bbbbbb"), diskFull)

	_, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{ID: "x-aaaaaa", Title: "lands, then is removed"}},
		{CreateInput: CreateInput{ID: "x-bbbbbb", Title: "fails"}},
		{CreateInput: CreateInput{ID: "x-cccccc", Title: "never reached"}},
	})
	var be *BatchEntryError
	if !errors.Is(err, diskFull) || !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("want the disk error on entry index 1, got %v", err)
	}
	assertStoreEmpty(t, s)
}

func TestCreateBatch_SuppliedIDRepeatedInTheSet_IsRefused(t *testing.T) {
	s, _ := batchStore(t)
	_, err := s.CreateBatch([]BatchEntry{
		{CreateInput: CreateInput{ID: "x-aaaaaa", Title: "a"}},
		{CreateInput: CreateInput{ID: "x-aaaaaa", Title: "b"}},
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
	assertStoreEmpty(t, s)
}
