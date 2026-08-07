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

// L3 tests for osFS.MoveTree against a real filesystem: the same-device rename
// and — where the machine offers a second filesystem — the cross-device copy
// fallback. The copy walker itself is unit-tested in copytree_internal_test.go.
package vfs_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// buildTree writes a small store-shaped tree at root: a config file, a task
// file, and a nested comments/ directory.
func buildTree(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "comments"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("prefix: prj\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "prj-abc123.md"), []byte("---\nid: prj-abc123\n---\n"), 0o600); err != nil {
		t.Fatalf("write task: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "comments", "prj-abc123.yaml"), []byte("- body: hi\n"), 0o644); err != nil {
		t.Fatalf("write comment: %v", err)
	}
}

// assertTree checks that root holds exactly what buildTree wrote, with the
// permissions preserved.
func assertTree(t *testing.T, root string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if string(got) != "prefix: prj\n" {
		t.Errorf("config = %q", got)
	}
	if _, err := os.ReadFile(filepath.Join(root, "comments", "prj-abc123.yaml")); err != nil {
		t.Errorf("nested comment: %v", err)
	}
	fi, err := os.Stat(filepath.Join(root, "prj-abc123.md"))
	if err != nil {
		t.Fatalf("task file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("task file perm = %o, want 600 (permissions must survive the move)", perm)
	}
}

func TestOsFS_MoveTree_SameDevice(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "proj", ".tasks")
	buildTree(t, src)
	if err := os.MkdirAll(filepath.Join(base, "central", "stores"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dst := filepath.Join(base, "central", "stores", "proj")

	if err := vfs.NewOS().MoveTree(src, dst); err != nil {
		t.Fatalf("MoveTree: %v", err)
	}
	assertTree(t, dst)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "proj")); err != nil {
		t.Errorf("project dir removed: %v", err)
	}
}

func TestOsFS_MoveTree_RefusesExistingDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	buildTree(t, src)
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := vfs.NewOS().MoveTree(src, dst); err == nil {
		t.Fatal("MoveTree onto an existing destination should fail")
	}
	assertTree(t, src) // source untouched
}

// TestOsFS_MoveTree_CrossDevice exercises the copy+remove fallback for real. It
// needs two filesystems; /dev/shm is a tmpfs on Linux, so the test runs there
// and skips anywhere the two paths turn out to share a device.
func TestOsFS_MoveTree_CrossDevice(t *testing.T) {
	other := "/dev/shm"
	if fi, err := os.Stat(other); err != nil || !fi.IsDir() {
		t.Skip("no /dev/shm: cannot construct a cross-device move")
	}
	otherBase, err := os.MkdirTemp(other, "taskmgr-movetree-*")
	if err != nil {
		t.Skipf("cannot write to %s: %v", other, err)
	}
	defer func() { _ = os.RemoveAll(otherBase) }()

	src := filepath.Join(t.TempDir(), ".tasks")
	buildTree(t, src)
	dst := filepath.Join(otherBase, "proj")

	// Confirm the two really are on different filesystems; otherwise this test
	// would silently re-test the rename path.
	if err := os.Rename(src, dst); !errors.Is(err, syscall.EXDEV) {
		if err == nil {
			_ = os.Rename(dst, src) // put it back
		}
		t.Skipf("%s and the temp dir share a filesystem (rename err: %v)", other, err)
	}

	if err := vfs.NewOS().MoveTree(src, dst); err != nil {
		t.Fatalf("MoveTree across devices: %v", err)
	}
	assertTree(t, dst)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source must be gone after a cross-device move: %v", err)
	}
}
