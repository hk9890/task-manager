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
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

func fixedClock() func() time.Time {
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
}

// TestOpenWithFS verifies that the openWithFS hook routes all Store
// operations through the provided FS, and that a real osFS produces
// the same behaviour as the existing Store.
func TestOpenWithFS_RealOsFS(t *testing.T) {
	dir := t.TempDir()

	// Init using the standard path (still uses os directly for bootstrap).
	s, err := Init(dir, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Re-open via the seam.
	s2 := openWithFS(dir, vfs.NewOS())
	s2.cfg = s.cfg
	s2.now = s.now

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

// TestOpenWithFS_AllOpsRouteThrough verifies that every Store mutation
// works end-to-end when routed through a real osFS.
func TestOpenWithFS_AllOps(t *testing.T) {
	dir := t.TempDir()
	s, err := Init(dir, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	s2 := openWithFS(dir, vfs.NewOS())
	s2.cfg = s.cfg
	s2.now = fixedClock()

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
