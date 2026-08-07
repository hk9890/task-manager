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

// L3 tests for the OS-backed entry points: Init and Open only exist against a
// real filesystem, so these cannot move to vfs.Mem. Everything in store_test.go
// that does not need real disk runs at L2 on the in-memory seam instead.
package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInitRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "agt"); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, "agt"); !errors.Is(err, ErrStoreExists) {
		t.Errorf("expected ErrStoreExists, got %v", err)
	}
}

func TestInitRejectsBadPrefix(t *testing.T) {
	for _, p := range []string{"", "A", "1x", "has-dash", "has space"} {
		if _, err := Init(t.TempDir(), p); err == nil {
			t.Errorf("prefix %q: expected error", p)
		}
	}
}

func TestOpenWalksUp(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "agt"); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(nested)
	if err != nil {
		t.Fatalf("Open from nested: %v", err)
	}
	if s.Prefix() != "agt" {
		t.Errorf("prefix = %q", s.Prefix())
	}
}

func TestOpenNoStore(t *testing.T) {
	if _, err := Open(t.TempDir()); !errors.Is(err, ErrNoStore) {
		t.Errorf("expected ErrNoStore, got %v", err)
	}
}

// TestAtomicWriteLeavesNoTemp proves the temp-file half of WriteAtomic: the
// staging file must not survive the rename. vfs.Mem cannot show this — its
// WriteAtomic is a single map update with no temp file to leak.
func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	s, err := Init(t.TempDir(), "agt")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	mustCreate(t, s, CreateInput{Title: "x"})
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == "" && e.Name() != ConfigFileName && e.Name() != lockFileName {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}
