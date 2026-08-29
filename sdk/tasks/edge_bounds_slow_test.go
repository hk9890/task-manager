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

//go:build integration

// The two relation-count bound tests, held out of the default suite.
//
// Each fills a relation list to its 256-item limit through the public API, which
// means 257 Create calls and 257 writes before the assertion it is here for. The
// pair costs about 17 seconds — roughly half the whole suite's wall time, against
// a few hundred milliseconds for everything else in this module.
//
// They are integration-tagged for their cost, not because the coverage is
// optional: the bound is a real rule (TASK-STORAGE-SPEC §4 + §10) and these are
// the only tests that prove the STORE enforces it rather than validateFields
// alone. `mise run test:integration` and `mise run test:all` run them, and
// `quality:full` is still the gate a change has to pass.
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
