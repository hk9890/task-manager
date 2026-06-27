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

package vfs_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

func TestOsFS_WriteAtomic_NoTornFile(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	name := filepath.Join(dir, "issue.md")
	data := []byte("hello world")

	if err := fs.WriteAtomic(name, data, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := fs.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}

	// Verify no temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "issue.md" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

func TestOsFS_WriteAtomic_Overwrite(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	name := filepath.Join(dir, "file.md")

	if err := fs.WriteAtomic(name, []byte("v1"), 0o644); err != nil {
		t.Fatalf("first WriteAtomic: %v", err)
	}
	if err := fs.WriteAtomic(name, []byte("v2"), 0o644); err != nil {
		t.Fatalf("second WriteAtomic: %v", err)
	}

	got, err := fs.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("got %q, want %q", got, "v2")
	}
}

func TestOsFS_Append_Grows(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	name := filepath.Join(dir, "log.txt")

	if err := fs.Append(name, []byte("line1\n"), 0o644); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := fs.Append(name, []byte("line2\n"), 0o644); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	got, err := fs.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "line1\nline2\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOsFS_Lock_Serializes(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	lockPath := filepath.Join(dir, ".lock")

	// Overlap detector: each goroutine bumps `inside` on entry to the critical
	// section. If Lock truly serializes writers, `inside` is always exactly 1
	// in there, so inside.Add(1) returns 1. A no-op lock (zero mutual
	// exclusion) lets goroutines overlap, so a later entrant sees inside > 1
	// and flips `raced` — which is exactly what this test must catch.
	//
	// The detector uses atomics on purpose: the kernel flock's happens-before
	// is invisible to Go's race detector, so an *unguarded plain* counter here
	// would false-positive under `go test -race` even when the lock is correct.
	var inside atomic.Int32
	var raced atomic.Bool
	var done atomic.Int32

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := fs.Lock(lockPath)
			if err != nil {
				errs <- err
				return
			}
			if inside.Add(1) != 1 {
				raced.Store(true)
			}
			// Widen the critical section so a missing lock reliably overlaps.
			time.Sleep(time.Millisecond)
			inside.Add(-1)
			done.Add(1)
			errs <- unlock()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("Lock/unlock: %v", err)
		}
	}
	if raced.Load() {
		t.Error("two goroutines were inside the lock at once — Lock did not serialize")
	}
	if got := done.Load(); got != goroutines {
		t.Errorf("done = %d, want %d", got, goroutines)
	}
}

func TestOsFS_ReadDir(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	if err := fs.WriteAtomic(filepath.Join(dir, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteAtomic(filepath.Join(dir, "b.md"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := fs.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestOsFS_Stat(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	name := filepath.Join(dir, "file.md")
	if err := fs.WriteAtomic(name, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	fi, err := fs.Stat(name)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size() != 4 {
		t.Errorf("size = %d, want 4", fi.Size())
	}
}

func TestOsFS_MkdirAll_Remove(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	sub := filepath.Join(dir, "a", "b", "c")
	if err := fs.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fi, err := os.Stat(sub)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected directory at %s", sub)
	}

	name := filepath.Join(dir, "f.md")
	if err := fs.WriteAtomic(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Errorf("file should be gone")
	}
}

func TestOsFS_Rename(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	src := filepath.Join(dir, "src.md")
	dst := filepath.Join(dir, "dst.md")

	if err := fs.WriteAtomic(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rename(src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be gone")
	}
	got, err := fs.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "content" {
		t.Errorf("got %q", got)
	}
}

// TestOsFS_WriteAtomic_DirSynced verifies that WriteAtomic fsyncs the parent
// directory after renaming: the call must succeed and the file must be readable,
// confirming the dir-fsync code path runs without error.
func TestOsFS_WriteAtomic_DirSynced(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	name := filepath.Join(dir, "synced.md")
	data := []byte("crash-durable")

	if err := fs.WriteAtomic(name, data, 0o644); err != nil {
		t.Fatalf("WriteAtomic (with dir fsync): %v", err)
	}

	got, err := fs.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile after WriteAtomic: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}

	// Overwrite triggers another rename → dir fsync.
	data2 := []byte("updated")
	if err := fs.WriteAtomic(name, data2, 0o644); err != nil {
		t.Fatalf("WriteAtomic overwrite (with dir fsync): %v", err)
	}
	got2, err := fs.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile after overwrite: %v", err)
	}
	if !bytes.Equal(got2, data2) {
		t.Errorf("got %q, want %q", got2, data2)
	}
}

// TestOsFS_Append_CreateFsyncDir verifies that the first Append (which creates
// a new dir entry) succeeds including the parent-dir fsync, and that a
// subsequent Append (file already exists) also succeeds without dir fsync.
func TestOsFS_Append_CreateFsyncDir(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	name := filepath.Join(dir, "new.log")

	// First call: file does not exist → O_CREATE adds a dir entry → dir fsync.
	if err := fs.Append(name, []byte("entry1\n"), 0o644); err != nil {
		t.Fatalf("Append create (with dir fsync): %v", err)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Second call: file exists → no new dir entry → no dir fsync (but must succeed).
	if err := fs.Append(name, []byte("entry2\n"), 0o644); err != nil {
		t.Fatalf("Append existing (no dir fsync): %v", err)
	}

	got, err := fs.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "entry1\nentry2\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestOsFS_Rename_DirSynced verifies that the bare Rename fsyncs the
// destination directory after the rename succeeds.
func TestOsFS_Rename_DirSynced(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()

	src := filepath.Join(dir, "before.md")
	dst := filepath.Join(dir, "after.md")

	if err := fs.WriteAtomic(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rename(src, dst); err != nil {
		t.Fatalf("Rename (with dir fsync): %v", err)
	}

	// src must be gone, dst must have the content.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be gone after rename")
	}
	got, err := fs.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("got %q, want %q", got, "payload")
	}
}
