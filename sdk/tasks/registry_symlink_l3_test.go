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

// L3 registry tests that need a real symlink. The in-memory FS resolves
// EvalSymlinks to the identity, so the gap between a path as RECORDED and the
// same path as RESOLVED — the gap the registry's two-key matching exists to
// close — only exists against a real filesystem.
package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// TestRelinkCentral_RefusesADuplicateAcrossASymlink pins that relink matches a
// project path on both keys, as every other registry writer does.
//
// Entry "a" was recorded before its path became a symlink, so it holds the
// pre-symlink spelling. Relinking "b" onto that same path canonicalizes to the
// symlink TARGET, which does not match "a"'s recorded spelling lexically — and
// lexical is the only way registry entries are ever compared. Matching on the
// resolved form alone therefore writes a second entry for one project, which
// loadRegistry's dedup cannot see either, leaving resolution to pick whichever
// of the two it scans into first.
func TestRelinkCentral_RefusesADuplicateAcrossASymlink(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "proj") // -> real
	other := filepath.Join(base, "other")

	for _, d := range []string{home, real, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs := vfs.NewOS()
	e := env.Fake{Vars: map[string]string{"TASKMGR_HOME": home}}

	// Entry "a" records the pre-symlink spelling; entry "b" is a real store
	// registered elsewhere. Both must be finished stores.
	if _, err := initCentralWith(link, "a", "aaa", fs, e, nil); err != nil {
		t.Fatalf("init a: %v", err)
	}
	if _, err := initCentralWith(other, "b", "bbb", fs, e, nil); err != nil {
		t.Fatalf("init b: %v", err)
	}
	// Rewrite a's entry to the un-resolved spelling, which is what an entry
	// recorded before the path became a symlink looks like.
	entries, err := loadRegistry(fs, home, home)
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		if entries[i].Store == "a" {
			entries[i].Path = link
		}
	}
	if err := saveRegistry(fs, home, entries); err != nil {
		t.Fatal(err)
	}

	// Relinking b onto the same project must be refused.
	_, err = relinkCentralWith("b", link, fs, e)
	if err == nil {
		t.Fatal("relink created a second entry for a project that already has one")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %v, want it to report the existing registration", err)
	}

	// And the registry still holds exactly one entry per project.
	entries, err = loadRegistry(fs, home, home)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, en := range entries {
		key := canonicalize(fs, en.Path, home, home)
		if prev, dup := seen[key]; dup {
			t.Errorf("two entries resolve to %s: %q and %q", key, prev, en.Store)
		}
		seen[key] = en.Store
	}
}
