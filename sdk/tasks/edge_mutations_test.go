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
