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

// L2 tests for the four edge mutations: the blocked_by / related count bounds,
// and the no-op contract shared by RemoveDep and RemoveRelated.
//
// The bounds half:
// validate_fields_test.go proves validateFields rejects a 257th item; these
// prove the STORE refuses it too. The edge mutations reach the FS through
// writeFiles without passing validateAndIndex, so before validation moved to
// that funnel the 257th AddDep was written to disk and only failed the next
// time the issue was re-validated (TASK-STORAGE-SPEC §4 + §10).
package tasks

import (
	"errors"
	"testing"
)

// seedBlockers creates n issues and returns their IDs. Each is a valid target
// for AddDep/AddRelated, which reject a reference to an issue that is absent.
func seedBlockers(t *testing.T, s *Store, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		iss, err := unwrap(s.Create(CreateInput{Title: "blocker"}))
		if err != nil {
			t.Fatalf("Create blocker %d: %v", i, err)
		}
		ids[i] = iss.ID
	}
	return ids
}

// TestAddDep_AtBound_AcceptsThenRefuses verifies that AddDep fills blocked_by to
// maxBlockedBy and then refuses the next one with a *ValidationError, leaving
// the stored issue at the bound rather than one past it.
func TestAddDep_AtBound_AcceptsThenRefuses(t *testing.T) {
	s, _ := newMemStore(t)

	dep, err := unwrap(s.Create(CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}
	ids := seedBlockers(t, s, maxBlockedBy+1)

	for i := 0; i < maxBlockedBy; i++ {
		if err := s.AddDep(dep.ID, ids[i]); err != nil {
			t.Fatalf("AddDep %d of %d: %v", i+1, maxBlockedBy, err)
		}
	}

	err = s.AddDep(dep.ID, ids[maxBlockedBy])
	if err == nil {
		t.Fatalf("AddDep past the bound: expected a validation error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("AddDep past the bound: got %T (%v); want *ValidationError", err, err)
	}
	if ve.Field != "blocked_by" {
		t.Errorf("error Field = %q; want %q", ve.Field, "blocked_by")
	}

	// The refusal must not have been written: re-read from the store.
	got, err := s.Get(dep.ID)
	if err != nil {
		t.Fatalf("Get after refusal: %v", err)
	}
	if len(got.BlockedBy) != maxBlockedBy {
		t.Errorf("stored blocked_by = %d; want %d (the refused edge was written)", len(got.BlockedBy), maxBlockedBy)
	}
}

// TestAddRelated_AtBound_AcceptsThenRefuses is the related-edge peer of
// TestAddDep_AtBound_AcceptsThenRefuses.
func TestAddRelated_AtBound_AcceptsThenRefuses(t *testing.T) {
	s, _ := newMemStore(t)

	subject, err := unwrap(s.Create(CreateInput{Title: "subject"}))
	if err != nil {
		t.Fatalf("Create subject: %v", err)
	}
	ids := seedBlockers(t, s, maxRelated+1)

	for i := 0; i < maxRelated; i++ {
		if err := s.AddRelated(subject.ID, ids[i]); err != nil {
			t.Fatalf("AddRelated %d of %d: %v", i+1, maxRelated, err)
		}
	}

	err = s.AddRelated(subject.ID, ids[maxRelated])
	if err == nil {
		t.Fatalf("AddRelated past the bound: expected a validation error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("AddRelated past the bound: got %T (%v); want *ValidationError", err, err)
	}
	if ve.Field != "related" {
		t.Errorf("error Field = %q; want %q", ve.Field, "related")
	}

	got, err := s.Get(subject.ID)
	if err != nil {
		t.Fatalf("Get after refusal: %v", err)
	}
	if len(got.Related) != maxRelated {
		t.Errorf("stored related = %d; want %d (the refused edge was written)", len(got.Related), maxRelated)
	}
}

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
