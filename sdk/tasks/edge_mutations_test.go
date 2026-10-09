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

// L2 tests for the four edge mutations: the no-op contract shared by RemoveDep
// and RemoveRelated.
//
// The bounds half of this subject — that the STORE, not only validateFields,
// refuses a 257th blocker or related edge — lives in edge_bounds_slow_test.go
// behind the integration tag: filling a list to its limit through the public API
// costs about 17 seconds for the pair, which is half the default suite.
package tasks

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestRemoveDep_Absent_WritesNothing verifies the no-op contract: removing a
// blocker that is not present leaves Updated untouched and writes no file.
// RemoveDep used to bump Updated and rewrite unconditionally, which reordered
// `--sort updated` results and produced a spurious git diff for a command that
// changed nothing. Its sibling RemoveRelated always had the contract; the pair
// now shares one implementation (SDK-SPEC §6).
//
// The injected fault is the assertion: it makes any write to the issue file
// fail, so an attempted write surfaces as an error instead of having to be
// inferred from timestamps. It is never consumed, because nothing may write.
func TestRemoveDep_Absent_WritesNothing(t *testing.T) {
	s, m := newMemStore(t)

	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}
	other, err := unwrap(s.Create(CreateInput{Title: "other"}))
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}

	before, err := s.Get(dep.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	path, err := s.issueFilePath(dep.ID)
	if err != nil {
		t.Fatalf("issueFilePath: %v", err)
	}
	m.FailOn("WriteAtomic", path, errors.New("no write expected"))

	if err := s.RemoveDep(dep.ID, other.ID); err != nil {
		t.Fatalf("RemoveDep on an absent blocker: %v (it must not write)", err)
	}

	after, err := s.Get(dep.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Errorf("Updated moved from %v to %v; a no-op removal must not bump it", before.Updated, after.Updated)
	}
}

// TestRemoveRelated_Absent_WritesNothing is the related-edge peer of
// TestRemoveDep_Absent_WritesNothing, pinning the contract on both sides so
// they cannot drift apart again.
func TestRemoveRelated_Absent_WritesNothing(t *testing.T) {
	s, m := newMemStore(t)

	subject, err := unwrap(s.Create(CreateInput{Title: "subject"}))
	if err != nil {
		t.Fatalf("Create subject: %v", err)
	}
	other, err := unwrap(s.Create(CreateInput{Title: "other"}))
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}

	before, err := s.Get(subject.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	path, err := s.issueFilePath(subject.ID)
	if err != nil {
		t.Fatalf("issueFilePath: %v", err)
	}
	m.FailOn("WriteAtomic", path, errors.New("no write expected"))

	if err := s.RemoveRelated(subject.ID, other.ID); err != nil {
		t.Fatalf("RemoveRelated on an absent reference: %v (it must not write)", err)
	}

	after, err := s.Get(subject.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Errorf("Updated moved from %v to %v; a no-op removal must not bump it", before.Updated, after.Updated)
	}
}

// A removal introduces no violation of the list it shortens (TASK-STORAGE-SPEC
// §10). Refused, a list over its bound — two branches merged, say — could not
// be brought back under it through the API: every removal left it over.
func TestRemoveDep_ListOverItsBound_RemovesTheEntry(t *testing.T) {
	s, fs := newMemStore(t)
	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatal(err)
	}
	blockers := make([]string, maxBlockedBy+2)
	for i := range blockers {
		blockers[i] = fmt.Sprintf("agt-b%03d", i)
	}
	seedStored(t, fs, s, dep.ID, func(iss *Issue) { iss.BlockedBy = blockers })

	if err := s.RemoveDep(dep.ID, blockers[0]); err != nil {
		t.Fatalf("RemoveDep on a list over its bound: %v", err)
	}
	got, err := s.Get(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.BlockedBy, blockers[1:]) {
		t.Errorf("blocked_by has %d entries, want the %d that were not removed", len(got.BlockedBy), len(blockers)-1)
	}
}

func TestRemoveDep_StoredDuplicates_RemovesTheValue(t *testing.T) {
	s, fs := newMemStore(t)
	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatal(err)
	}
	seedStored(t, fs, s, dep.ID, func(iss *Issue) { iss.BlockedBy = []string{"agt-x", "agt-x", "agt-y", "agt-y"} })

	if err := s.RemoveDep(dep.ID, "agt-x"); err != nil {
		t.Fatalf("RemoveDep while another value stays duplicated: %v", err)
	}
	got, err := s.Get(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.BlockedBy, []string{"agt-y", "agt-y"}) {
		t.Errorf("blocked_by = %v, want only the other value left", got.BlockedBy)
	}
}

// The blockers exist, so the bound is the only rule the add can break.
func TestAddDep_ListOverItsBound_IsRefused(t *testing.T) {
	s, fs := newMemStore(t)
	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatal(err)
	}
	var blockers []string
	for len(blockers) < maxBlockedBy+2 {
		set := make([]BatchEntry, (maxBlockedBy+2)/2)
		for i := range set {
			set[i].Title = "blocker"
		}
		results, err := s.CreateBatch(set)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			blockers = append(blockers, r.Issue.ID)
		}
	}
	stored := blockers[:maxBlockedBy+1]
	seedStored(t, fs, s, dep.ID, func(iss *Issue) { iss.BlockedBy = stored })

	err = s.AddDep(dep.ID, blockers[maxBlockedBy+1])
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "blocked_by" || !strings.Contains(ve.Message, "too many blockers") {
		t.Fatalf("AddDep onto a list over its bound: got %v, want the blocked_by bound to refuse it", err)
	}
	got, err := s.Get(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.BlockedBy) != len(stored) {
		t.Errorf("blocked_by has %d entries, want the %d stored", len(got.BlockedBy), len(stored))
	}
}

// labels follows the same rule, through the one write that removes a label.
func TestUpdate_RemoveLabelFromListOverItsBound_RemovesIt(t *testing.T) {
	s, fs := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	labels := make([]string, maxLabels+2)
	for i := range labels {
		labels[i] = fmt.Sprintf("l%02d", i)
	}
	seedStored(t, fs, s, iss.ID, func(iss *Issue) { iss.Labels = labels })

	got, err := unwrap(s.Update(iss.ID, UpdateInput{RemoveLabels: labels[:1]}))
	if err != nil {
		t.Fatalf("removing a label from a list over its bound: %v", err)
	}
	if len(got.Labels) != len(labels)-1 {
		t.Errorf("labels has %d entries, want %d", len(got.Labels), len(labels)-1)
	}
	if _, err := s.Update(iss.ID, UpdateInput{AddLabels: []string{"new"}}); err == nil {
		t.Error("adding a label to a list over its bound was accepted")
	}
}
