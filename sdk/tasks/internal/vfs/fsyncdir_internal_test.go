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

// Internal L3 tests for the parent-directory fsync — the half of the disk seam
// that makes a newly published directory entry survive a crash.
//
// The sibling tests in os_test.go assert that these calls succeed and that the
// bytes read back. Neither observation changes when the fsync is deleted, so
// they cannot fail for its absence; these can. They swap the fsyncDirFn seam for
// a double that records which directories were synced, and for one that fails,
// so both halves of the contract are checked: that the sync happens where it is
// required, and that its failure is reported rather than swallowed.
//
// Never call t.Parallel here: fsyncDirFn is package state.
package vfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// recordFsync installs a recorder in place of the real directory fsync for the
// duration of the test and returns a pointer to the directories it saw, in call
// order. The real fsync still runs, so the operation under test stays honest.
func recordFsync(t *testing.T) *[]string {
	t.Helper()
	var seen []string
	prev := fsyncDirFn
	fsyncDirFn = func(dir string) error {
		seen = append(seen, dir)
		return prev(dir)
	}
	t.Cleanup(func() { fsyncDirFn = prev })
	return &seen
}

// failFsync installs a double that fails every directory fsync.
func failFsync(t *testing.T, err error) {
	t.Helper()
	prev := fsyncDirFn
	fsyncDirFn = func(string) error { return err }
	t.Cleanup(func() { fsyncDirFn = prev })
}

// contains reports whether dir is among the recorded syncs.
func contains(seen []string, dir string) bool {
	for _, s := range seen {
		if s == dir {
			return true
		}
	}
	return false
}

// A first Append creates a directory entry, which is durable only once the
// PARENT is synced. Syncing the file alone leaves a crash able to lose the whole
// sidecar — the entry naming it was never written.
func TestOsFS_Append_SyncsTheParentDirOnlyWhenTheEntryIsNew(t *testing.T) {
	dir := t.TempDir()
	fs := NewOS()
	name := filepath.Join(dir, "new.log")

	seen := recordFsync(t)

	if err := fs.Append(name, []byte("entry1\n"), 0o644); err != nil {
		t.Fatalf("Append create: %v", err)
	}
	if !contains(*seen, dir) {
		t.Errorf("creating %s synced %v, want the parent %s — a new dir entry is not durable without it",
			name, *seen, dir)
	}

	// An append to a file that already exists adds no directory entry, so the
	// parent sync is pure cost. Asserting its absence is what stops the branch
	// collapsing to an unconditional sync and still passing the case above.
	after := len(*seen)
	if err := fs.Append(name, []byte("entry2\n"), 0o644); err != nil {
		t.Fatalf("Append existing: %v", err)
	}
	if extra := (*seen)[after:]; len(extra) != 0 {
		t.Errorf("appending to an existing file synced %v, want no parent sync", extra)
	}
}

// WriteAtomic publishes its temp file with a rename, which is durable only once
// the containing directory is synced.
func TestOsFS_WriteAtomic_SyncsTheParentDir(t *testing.T) {
	dir := t.TempDir()
	fs := NewOS()
	seen := recordFsync(t)

	if err := fs.WriteAtomic(filepath.Join(dir, "issue.md"), []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if !contains(*seen, dir) {
		t.Errorf("WriteAtomic synced %v, want the parent %s", *seen, dir)
	}
}

// Rename publishes the destination name; the destination directory carries it.
func TestOsFS_Rename_SyncsTheDestinationDir(t *testing.T) {
	dir := t.TempDir()
	fs := NewOS()
	src := filepath.Join(dir, "before.md")
	dst := filepath.Join(dir, "after.md")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	seen := recordFsync(t)

	if err := fs.Rename(src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if !contains(*seen, filepath.Dir(dst)) {
		t.Errorf("Rename synced %v, want the destination dir %s", *seen, filepath.Dir(dst))
	}
}

// A directory fsync that fails means the write is not durable. Reporting success
// there would be the worst outcome available: the caller believes a body landed
// that a crash can still take away.
func TestOsFS_ReportsAFailedDirSync(t *testing.T) {
	boom := errors.New("injected fsync failure")

	cases := []struct {
		name string
		call func(fs FS, dir string) error
	}{
		{
			name: "WriteAtomic",
			call: func(fs FS, dir string) error {
				return fs.WriteAtomic(filepath.Join(dir, "issue.md"), []byte("body"), 0o644)
			},
		},
		{
			name: "Append creating the file",
			call: func(fs FS, dir string) error {
				return fs.Append(filepath.Join(dir, "new.log"), []byte("entry\n"), 0o644)
			},
		},
		{
			name: "Rename",
			call: func(fs FS, dir string) error {
				src := filepath.Join(dir, "before.md")
				if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
					return err
				}
				return fs.Rename(src, filepath.Join(dir, "after.md"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fs := NewOS()
			failFsync(t, boom)

			err := tc.call(fs, dir)
			if !errors.Is(err, boom) {
				t.Errorf("got %v, want the injected fsync failure — a non-durable write was reported as success", err)
			}
		})
	}
}
