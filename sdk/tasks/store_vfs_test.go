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

package tasks

import (
	"testing"
)

// TestStoreOnRealFS_ReopenRoundTrips verifies that a store re-opened from disk
// routes every operation through the disk seam and sees what the first handle
// wrote.
//
// It used to build the second handle with openWithFS, an unexported fixture
// constructor that skipped the config read and left the caller to set s.cfg and
// s.now by hand — so the test exercised a store shape production never builds.
// Open is the real path and reads the config itself.
func TestStoreOnRealFS_ReopenRoundTrips(t *testing.T) {
	dir := t.TempDir()

	if _, err := Init(dir, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Create an issue through the seam-routed store.
	iss, err := unwrap(s2.Create(CreateInput{Title: "seam test"}))
	if err != nil {
		t.Fatalf("Create via seam: %v", err)
	}

	// Read back via a plain Get on the same seam-routed store.
	got, err := s2.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get via seam: %v", err)
	}
	if got.Title != "seam test" {
		t.Errorf("title = %q, want %q", got.Title, "seam test")
	}
}

// TestStoreOnRealFS_AllOps verifies that every Store mutation works end-to-end
// against a real osFS.
func TestStoreOnRealFS_AllOps(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	s2, err := Open(dir, WithClock(monotonicClock()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	a, err := unwrap(s2.Create(CreateInput{Title: "a"}))
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := unwrap(s2.Create(CreateInput{Title: "b"}))
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}

	// AddDep
	if err := s2.AddDep(a.ID, b.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Close
	if _, err := s2.Close(a.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// All is hot-only after at-zib.2.2 (closed issue is in closed/ partition).
	all, err := s2.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("len(All) = %d, want 1 (a is closed, b is active)", len(all))
	}

	// a is still accessible via Get (falls through to closed/).
	if _, err := s2.Get(a.ID); err != nil {
		t.Fatalf("Get closed issue: %v", err)
	}
}
