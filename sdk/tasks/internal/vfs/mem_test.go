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

package vfs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// TestMem_WriteAtomicReadFile verifies a basic write+read round-trip.
func TestMem_WriteAtomicReadFile(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "/tasks/agt-0001.md"
	data := []byte("hello")

	if err := m.WriteAtomic(name, data, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := m.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

// TestMem_WriteAtomicOverwrite verifies that WriteAtomic replaces previous
// content.
func TestMem_WriteAtomicOverwrite(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "/tasks/agt-0001.md"

	if err := m.WriteAtomic(name, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteAtomic(name, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := m.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Errorf("got %q, want v2", got)
	}
}

// TestMem_AppendAccumulates verifies that Append grows the file content.
func TestMem_AppendAccumulates(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "/tasks/comments.log"

	if err := m.Append(name, []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(name, []byte("line2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := m.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestMem_ReadDirLists verifies that ReadDir returns written files.
func TestMem_ReadDirLists(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	files := []string{
		"/tasks/agt-0001.md",
		"/tasks/agt-0002.md",
	}
	for _, f := range files {
		if err := m.WriteAtomic(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := m.ReadDir("/tasks")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(files) {
		t.Errorf("got %d entries, want %d", len(entries), len(files))
	}
	// Entries should not be directories.
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("unexpected directory entry: %s", e.Name())
		}
	}
}

// TestMem_RenameMovesFile verifies Rename removes src and makes dst readable.
func TestMem_RenameMovesFile(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src := "/tasks/src.md"
	dst := "/tasks/dst.md"

	if err := m.WriteAtomic(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename(src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := m.ReadFile(src); !vfs.IsNotExist(err) {
		t.Error("src should be gone after rename")
	}
	got, err := m.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("dst content = %q, want %q", got, "content")
	}
}

// TestMem_RemoveDeletes verifies Remove removes the file.
func TestMem_RemoveDeletes(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "/tasks/agt-0001.md"

	if err := m.WriteAtomic(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := m.ReadFile(name); !vfs.IsNotExist(err) {
		t.Error("file should be gone after Remove")
	}
}

// TestMem_ReadFileNotExist verifies that reading a missing file returns a
// not-exist error.
func TestMem_ReadFileNotExist(t *testing.T) {
	m := vfs.NewMem()
	_, err := m.ReadFile("/tasks/missing.md")
	if !vfs.IsNotExist(err) {
		t.Errorf("expected not-exist error, got %v", err)
	}
}

// TestMem_StatReturnsInfo verifies Stat returns size info for existing files.
func TestMem_StatReturnsInfo(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "/tasks/agt-0001.md"
	data := []byte("hello world")

	if err := m.WriteAtomic(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := m.Stat(name)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size() != int64(len(data)) {
		t.Errorf("size = %d, want %d", fi.Size(), len(data))
	}
	if fi.IsDir() {
		t.Error("should not be a directory")
	}
}

// TestMem_StatNotExist verifies Stat returns a not-exist error for missing
// files.
func TestMem_StatNotExist(t *testing.T) {
	m := vfs.NewMem()
	_, err := m.Stat("/tasks/missing.md")
	if !vfs.IsNotExist(err) {
		t.Errorf("expected not-exist error, got %v", err)
	}
}

// TestMem_MkdirAll verifies MkdirAll is a no-op (Mem is path-flat) that does
// not error on valid paths.
func TestMem_MkdirAll(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks/sub/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// After MkdirAll, files under that path are still accessible.
	name := "/tasks/sub/dir/file.md"
	if err := m.WriteAtomic(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadFile(name); err != nil {
		t.Fatalf("ReadFile after MkdirAll: %v", err)
	}
}

// TestMem_LockInProcess verifies that Lock returns an unlock function and does
// not error. In-process: the mutex approach means sequential callers work.
func TestMem_LockInProcess(t *testing.T) {
	m := vfs.NewMem()
	path := "/tasks/.lock"

	unlock, err := m.Lock(path)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}

// TestMem_ImplementsFS verifies Mem satisfies the vfs.FS interface at compile
// time.
func TestMem_ImplementsFS(t *testing.T) {
	var _ vfs.FS = vfs.NewMem()
}

// ---- FailOn tests -----------------------------------------------------------

var errInjected = errors.New("injected fault")

// TestMem_FailOn_WriteAtomic verifies that FailOn("WriteAtomic", ...) causes
// the next matching call to fail.
func TestMem_FailOn_WriteAtomic(t *testing.T) {
	m := vfs.NewMem()
	name := "/tasks/agt-0001.md"
	m.FailOn("WriteAtomic", name, errInjected)

	err := m.WriteAtomic(name, []byte("data"), 0o644)
	if !errors.Is(err, errInjected) {
		t.Fatalf("expected injected error, got %v", err)
	}
	// After the fault fires, the file should not have been written.
	if _, readErr := m.ReadFile(name); !vfs.IsNotExist(readErr) {
		t.Error("file should not exist after failed WriteAtomic")
	}
}

// TestMem_FailOn_Rename verifies that FailOn("Rename", ...) causes the next
// Rename matching the glob to fail.
func TestMem_FailOn_Rename(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src := "/tasks/src.md"
	dst := "/tasks/dst.md"
	if err := m.WriteAtomic(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Inject fault matching the source path.
	m.FailOn("Rename", src, errInjected)

	err := m.Rename(src, dst)
	if !errors.Is(err, errInjected) {
		t.Fatalf("expected injected error, got %v", err)
	}
	// src must still exist (rename was aborted before moving).
	got, err := m.ReadFile(src)
	if err != nil {
		t.Fatalf("src should still exist: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("src content = %q, want 'content'", got)
	}
	// dst must not exist.
	if _, err := m.ReadFile(dst); !vfs.IsNotExist(err) {
		t.Error("dst should not exist after failed Rename")
	}
}

// TestMem_FailOn_FaultsAreConsumed verifies a registered fault fires once
// and then the subsequent call succeeds.
func TestMem_FailOn_FaultsAreConsumed(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "/tasks/agt-0001.md"
	m.FailOn("WriteAtomic", name, errInjected)

	// First call: fails.
	if err := m.WriteAtomic(name, []byte("v1"), 0o644); !errors.Is(err, errInjected) {
		t.Fatalf("expected injected error first call, got %v", err)
	}
	// Second call: succeeds.
	if err := m.WriteAtomic(name, []byte("v2"), 0o644); err != nil {
		t.Fatalf("second call should succeed, got %v", err)
	}
}

// TestMem_FailOn_GlobMatchesSuffix verifies that a glob pattern using "*" in
// the pathGlob works correctly.
func TestMem_FailOn_GlobMatchesSuffix(t *testing.T) {
	m := vfs.NewMem()
	m.FailOn("WriteAtomic", "/tasks/*.md", errInjected)

	if err := m.WriteAtomic("/tasks/agt-0001.md", []byte("x"), 0o644); !errors.Is(err, errInjected) {
		t.Fatalf("expected injected error for glob match, got %v", err)
	}
}

// TestMem_FailOn_GlobNoMatch verifies that a fault does not fire for a path
// that does not match the glob.
func TestMem_FailOn_GlobNoMatch(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	m.FailOn("WriteAtomic", "/other/*.md", errInjected)

	// This path does not match the glob — write should succeed.
	if err := m.WriteAtomic("/tasks/agt-0001.md", []byte("x"), 0o644); err != nil {
		t.Fatalf("non-matching path should succeed: %v", err)
	}
}

// TestMem_FailOn_ReadDir verifies FailOn works on ReadDir.
func TestMem_FailOn_ReadDir(t *testing.T) {
	m := vfs.NewMem()
	m.FailOn("ReadDir", "/tasks", errInjected)

	_, err := m.ReadDir("/tasks")
	if !errors.Is(err, errInjected) {
		t.Fatalf("expected injected error, got %v", err)
	}
}

// TestMem_FailOn_Append verifies FailOn works on Append.
func TestMem_FailOn_Append(t *testing.T) {
	m := vfs.NewMem()
	name := "/tasks/comments.log"
	m.FailOn("Append", name, errInjected)

	err := m.Append(name, []byte("x"), 0o644)
	if !errors.Is(err, errInjected) {
		t.Fatalf("expected injected error, got %v", err)
	}
}

// ---- Getwd ------------------------------------------------------------------

// TestMem_Getwd verifies that Getwd returns a non-empty working directory.
func TestMem_Getwd(t *testing.T) {
	m := vfs.NewMem()
	wd, err := m.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if wd == "" {
		t.Error("Getwd returned empty string")
	}
}

// ---- Store-on-Mem CRUD (L2) -------------------------------------------------

// These tests open a Store on vfs.Mem and verify end-to-end CRUD without
// touching the real filesystem. They live here (in the vfs package test file)
// because they need access to openWithFS which is package-internal to tasks.
// The real L2 CRUD tests that use openWithFS are in store_mem_test.go inside
// the tasks package.

// TestMem_ReadDirDir verifies ReadDir correctly reports a directory entry.
func TestMem_ReadDirDir(t *testing.T) {
	m := vfs.NewMem()
	// Directories created via MkdirAll should appear in ReadDir of their parent.
	if err := m.MkdirAll("/tasks/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := m.ReadDir("/tasks")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "sub" && e.IsDir() {
			found = true
		}
	}
	if !found {
		t.Error("expected 'sub' directory in ReadDir results")
	}
}

// TestMem_ReadDirReturnsBasenames verifies ReadDir entries have base names.
func TestMem_ReadDirReturnsBasenames(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.WriteAtomic("/tasks/agt-0001.md", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := m.ReadDir("/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Entry Name() must be base name, not full path.
	if entries[0].Name() != "agt-0001.md" {
		t.Errorf("entry name = %q, want %q", entries[0].Name(), "agt-0001.md")
	}
}

// TestMem_MultipleFilesInDifferentDirs verifies ReadDir scopes to one directory.
func TestMem_MultipleFilesInDifferentDirs(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll /tasks: %v", err)
	}
	if err := m.MkdirAll("/other", 0o755); err != nil {
		t.Fatalf("MkdirAll /other: %v", err)
	}
	if err := m.WriteAtomic("/tasks/agt-0001.md", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteAtomic("/other/foo.md", []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := m.ReadDir("/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("ReadDir(/tasks) should return 1 entry, got %d: %v", len(entries), entries)
	}
	// Entry name must match only the file in /tasks.
	if entries[0].Name() != filepath.Base("/tasks/agt-0001.md") {
		t.Errorf("unexpected entry: %s", entries[0].Name())
	}
}

// ---- Parent-dir enforcement (osFS fidelity) ----------------------------------

// TestMem_WriteAtomic_RequiresParentDir verifies that WriteAtomic returns an
// os.ErrNotExist-wrapped error when the parent directory has not been created.
// This matches osFS behaviour: os.CreateTemp fails with ENOENT when the parent
// dir is absent.
func TestMem_WriteAtomic_RequiresParentDir(t *testing.T) {
	m := vfs.NewMem()

	// No MkdirAll — parent /tasks does not exist.
	err := m.WriteAtomic("/tasks/agt-0001.md", []byte("data"), 0o644)
	if err == nil {
		t.Fatal("WriteAtomic to missing parent dir: expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("WriteAtomic to missing parent dir: want os.ErrNotExist, got %v", err)
	}

	// After MkdirAll the write must succeed.
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.WriteAtomic("/tasks/agt-0001.md", []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteAtomic after MkdirAll: %v", err)
	}
}

// TestMem_Append_RequiresParentDir verifies that Append returns an
// os.ErrNotExist-wrapped error when the parent directory has not been created.
// This matches osFS behaviour: os.OpenFile with O_APPEND fails with ENOENT
// when the parent dir is absent.
func TestMem_Append_RequiresParentDir(t *testing.T) {
	m := vfs.NewMem()

	// No MkdirAll — parent /tasks/comments does not exist.
	err := m.Append("/tasks/comments/agt-0001.yml", []byte("entry\n"), 0o644)
	if err == nil {
		t.Fatal("Append to missing parent dir: expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Append to missing parent dir: want os.ErrNotExist, got %v", err)
	}

	// After MkdirAll the append must succeed.
	if err := m.MkdirAll("/tasks/comments", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.Append("/tasks/comments/agt-0001.yml", []byte("entry\n"), 0o644); err != nil {
		t.Fatalf("Append after MkdirAll: %v", err)
	}
}

// TestMem_Rename_RequiresDstParentDir verifies that Rename returns an
// os.ErrNotExist-wrapped error when the destination parent directory does not
// exist, matching real os.Rename behaviour.
func TestMem_Rename_RequiresDstParentDir(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src := "/tasks/src.md"
	if err := m.WriteAtomic(src, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	// Destination parent /tasks/closed does not exist yet.
	err := m.Rename(src, "/tasks/closed/src.md")
	if err == nil {
		t.Fatal("Rename to missing dst parent: expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Rename to missing dst parent: want os.ErrNotExist, got %v", err)
	}
	// Source must still exist (rename was not applied).
	if _, err := m.ReadFile(src); err != nil {
		t.Errorf("src should still exist after failed rename: %v", err)
	}

	// After MkdirAll the rename must succeed.
	if err := m.MkdirAll("/tasks/closed", 0o755); err != nil {
		t.Fatalf("MkdirAll /tasks/closed: %v", err)
	}
	if err := m.Rename(src, "/tasks/closed/src.md"); err != nil {
		t.Fatalf("Rename after MkdirAll: %v", err)
	}
}

// TestMem_Rename_DirectoryUnsupported verifies that Rename returns a clear
// error when the source is a directory.  Mem.Rename supports files only;
// renaming a directory would silently orphan child-path keys.
func TestMem_Rename_DirectoryUnsupported(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/tasks/subdir", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.MkdirAll("/tasks/newdir", 0o755); err != nil {
		t.Fatalf("MkdirAll /tasks/newdir: %v", err)
	}

	err := m.Rename("/tasks/subdir", "/tasks/newdir/subdir")
	if err == nil {
		t.Fatal("Rename of directory: expected error, got nil")
	}
	// The error should be distinct (not ErrNotExist — the dir exists).
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("Rename of directory: should not be ErrNotExist, got %v", err)
	}
}

// ---- Fault/durability path tests (L2) ---------------------------------------

// TestMem_FaultInjection_RenameStep models the durability path exercised by
// closeMove: (1) MkdirAll closed/, (2) WriteAtomic to closed/<id>.md,
// (3) Rename hot/<id>.md → closed/<id>.md. A fault is injected at step 3.
// The invariant is no-torn-state: after the failed rename both the hot-dir
// file and the destination file are in a consistent state — the hot file is
// still readable (the rename did not start), and any previously written
// destination file from the WriteAtomic is intact.
func TestMem_FaultInjection_RenameStep(t *testing.T) {
	m := vfs.NewMem()

	// Set up directories: hot/ and closed/ (as closeMove would).
	hotDir := "/tasks"
	closedDir := "/tasks/closed"
	if err := m.MkdirAll(hotDir, 0o755); err != nil {
		t.Fatalf("MkdirAll hot: %v", err)
	}
	if err := m.MkdirAll(closedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll closed: %v", err)
	}

	hotPath := "/tasks/agt-0001.md"
	closedPath := "/tasks/closed/agt-0001.md"
	originalContent := []byte("original task content")
	updatedContent := []byte("updated for closing")

	// Step 1: hot file exists.
	if err := m.WriteAtomic(hotPath, originalContent, 0o644); err != nil {
		t.Fatalf("WriteAtomic hot: %v", err)
	}

	// Step 2: WriteAtomic to closed/ (mirrors what closeMove does before rename).
	if err := m.WriteAtomic(closedPath, updatedContent, 0o644); err != nil {
		t.Fatalf("WriteAtomic closed: %v", err)
	}

	// Step 3: inject fault on Rename and verify no-torn-state.
	m.FailOn("Rename", hotPath, errInjected)
	err := m.Rename(hotPath, closedPath)
	if !errors.Is(err, errInjected) {
		t.Fatalf("expected injected rename error, got %v", err)
	}

	// Hot file must still be readable and intact.
	got, err := m.ReadFile(hotPath)
	if err != nil {
		t.Fatalf("hot file must still exist after failed rename: %v", err)
	}
	if string(got) != string(originalContent) {
		t.Errorf("hot file content = %q, want %q", got, originalContent)
	}

	// Closed file (written in step 2 before the rename) must still be intact.
	got, err = m.ReadFile(closedPath)
	if err != nil {
		t.Fatalf("closed file must still be readable: %v", err)
	}
	if string(got) != string(updatedContent) {
		t.Errorf("closed file content = %q, want %q", got, updatedContent)
	}

	// After the fault is consumed, a retry of the rename must succeed.
	if err := m.Rename(hotPath, closedPath); err != nil {
		t.Fatalf("Rename retry after fault: %v", err)
	}
	if _, err := m.ReadFile(hotPath); !vfs.IsNotExist(err) {
		t.Error("hot file should be gone after successful rename")
	}
	got, err = m.ReadFile(closedPath)
	if err != nil {
		t.Fatalf("closed file after rename: %v", err)
	}
	if string(got) != string(originalContent) {
		t.Errorf("closed file after rename = %q, want original content %q", got, originalContent)
	}
}
