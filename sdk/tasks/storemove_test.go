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

package tasks

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// L2 tests for the three store-move operations (CONFIG-SPEC §5), hermetic on
// vfs.Mem + env.Fake: MoveToCentral (promote), RenameCentral, RelinkCentral.
// Shared fixtures (fakeEnv, makeStore, writeRegistry, testCentral) live in
// resolve_test.go.

// storeDirFor returns the central store directory for a registry name.
func storeDirFor(name string) string {
	return filepath.Join(testCentral, storesSubdir, name)
}

func TestMoveToCentral_MovesFilesAndRegisters(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/myproj", "/dev/myproj/.tasks", "myp")
	if err := m.WriteAtomic("/dev/myproj/.tasks/myp-abc123.md", []byte("---\nid: myp-abc123\n---\n"), 0o644); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	s, err := moveToCentralWith("/dev/myproj", "myproj", m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("moveToCentral: %v", err)
	}

	want := storeDirFor("myproj")
	if s.Dir() != want {
		t.Errorf("store dir = %q, want %q", s.Dir(), want)
	}
	// The project path — not the store's parent — stays the hook working dir.
	if s.Root() != "/dev/myproj" {
		t.Errorf("root = %q, want /dev/myproj", s.Root())
	}
	// The prefix survives, so existing IDs stay valid.
	if s.Prefix() != "myp" {
		t.Errorf("prefix = %q, want myp", s.Prefix())
	}
	if _, err := m.ReadFile(filepath.Join(want, "myp-abc123.md")); err != nil {
		t.Errorf("task file did not move: %v", err)
	}
	if _, err := m.Stat("/dev/myproj/.tasks"); !vfs.IsNotExist(err) {
		t.Errorf("local .tasks must be gone: %v", err)
	}

	// The project now resolves to the central store.
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/dev/myproj"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve after promote: %v", err)
	}
	if info.Kind != ResolvedCentral || info.StorePath != want {
		t.Errorf("resolve = %v %q, want central %q", info.Kind, info.StorePath, want)
	}
}

func TestMoveToCentral_Rejections(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/a", "/dev/a/.tasks", "aaa")
	makeStore(t, m, "/dev/b", "/dev/b/.tasks", "bbb")

	// No local store to promote.
	if _, err := moveToCentralWith("/dev/nothing", "nothing", m, fakeEnv(nil), nil); !errors.Is(err, ErrNoStore) {
		t.Errorf("missing local store err = %v, want ErrNoStore", err)
	}
	// Invalid store name.
	if _, err := moveToCentralWith("/dev/a", "bad/name", m, fakeEnv(nil), nil); err == nil {
		t.Error("invalid store name should be rejected")
	}

	if _, err := moveToCentralWith("/dev/a", "taken", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("first promote: %v", err)
	}
	// Duplicate store name.
	if _, err := moveToCentralWith("/dev/b", "taken", m, fakeEnv(nil), nil); !errors.Is(err, ErrStoreExists) {
		t.Errorf("duplicate name err = %v, want ErrStoreExists", err)
	}
	// Duplicate project path: /dev/a is already registered, and a second local
	// store there must not produce a second entry.
	makeStore(t, m, "/dev/a", "/dev/a/.tasks", "aaa")
	if _, err := moveToCentralWith("/dev/a", "other", m, fakeEnv(nil), nil); err == nil {
		t.Error("a project with an entry already should be rejected")
	}
	// The refused promote left the second local store alone.
	if _, err := m.Stat("/dev/a/.tasks"); err != nil {
		t.Errorf("local store must survive a refused promote: %v", err)
	}
}

// TestMoveToCentral_RollsBackEntryOnMoveFailure pins the recovery path: the
// entry is written before the move, but a move that *returns an error* undoes
// it, so the promote can simply be run again. Without the rollback the entry
// blocks every retry — under the same name and, because the project path is
// taken, under any other.
func TestMoveToCentral_RollsBackEntryOnMoveFailure(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/myproj", "/dev/myproj/.tasks", "myp")
	boom := errors.New("move failed")
	m.FailOn("MoveTree", "/dev/myproj/.tasks", boom)

	if _, err := moveToCentralWith("/dev/myproj", "myproj", m, fakeEnv(nil), nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the injected move failure", err)
	}

	entries, err := loadRegistry(m, testCentral, testHome)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want the entry rolled back", entries)
	}
	// The local store is intact and still wins resolution, so the project works.
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/dev/myproj"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve after failed promote: %v", err)
	}
	if info.Kind != ResolvedLocal || info.StorePath != "/dev/myproj/.tasks" {
		t.Errorf("resolve = %v %q, want the local store to still win", info.Kind, info.StorePath)
	}
	// The retry (the fault is consumed) succeeds under the same name.
	s, err := moveToCentralWith("/dev/myproj", "myproj", m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("retry after a rolled-back promote: %v", err)
	}
	if s.Dir() != storeDirFor("myproj") {
		t.Errorf("retry landed at %q", s.Dir())
	}
}

// TestRenameCentral_RollsBackFolderOnRegistryFailure pins the other half of the
// recovery path: the folder moves first, so a registry write that fails must put
// it back or the store is left with nothing naming it.
func TestRenameCentral_RollsBackFolderOnRegistryFailure(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/myproj", "/dev/myproj/.tasks", "myp")
	if _, err := moveToCentralWith("/dev/myproj", "old", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote: %v", err)
	}
	boom := errors.New("registry write failed")
	m.FailOn("WriteAtomic", filepath.Join(testCentral, registryFileName), boom)

	if _, err := renameCentralWith("old", "new", m, fakeEnv(nil)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the injected registry failure", err)
	}

	if _, err := m.Stat(storeDirFor("new")); !vfs.IsNotExist(err) {
		t.Errorf("the folder should have been moved back, but %s exists", storeDirFor("new"))
	}
	if _, err := m.ReadFile(filepath.Join(storeDirFor("old"), ConfigFileName)); err != nil {
		t.Errorf("store not restored to its old name: %v", err)
	}
	// The project still resolves, unchanged.
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/dev/myproj"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve after failed rename: %v", err)
	}
	if info.StorePath != storeDirFor("old") {
		t.Errorf("resolve = %q, want the store still at its old name", info.StorePath)
	}
}

// TestResolve_ReportsIncompleteStore pins that a folder without a config.yaml is
// REPORTED, not skipped.
//
// Skipping it treats a store whose config went missing as if the project had no
// store at all: `taskmgr list` fails with "no .tasks directory found — run
// 'taskmgr init'", and following that advice creates a second, empty store
// beside a folder that still holds every issue file. A folder that is entirely
// absent is a different case — a dangling entry, which is skipped (CONFIG-SPEC
// §3) and is covered below.
func TestResolve_ReportsIncompleteStore(t *testing.T) {
	m := vfs.NewMem()
	writeRegistry(t, m, testCentral, registryEntry{Path: "/dev/proj", Store: "half"})
	// A folder with issue files but no config.
	if err := m.MkdirAll(storeDirFor("half"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.WriteAtomic(filepath.Join(storeDirFor("half"), "hlf-abc123.md"), []byte("---\nid: hlf-abc123\n---\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, _, err := resolveWith(ResolveOptions{WorkDir: "/dev/proj"}, m, fakeEnv(nil), nil)
	if err == nil {
		t.Fatal("resolving into a half-built store should fail")
	}
	if errors.Is(err, ErrNoStore) {
		t.Errorf("err = %v, want a diagnostic rather than ErrNoStore", err)
	}
	if !strings.Contains(err.Error(), "not a finished store") {
		t.Errorf("err = %v, want it to name the incomplete store", err)
	}
	// Named explicitly, the error says the same thing.
	_, _, err = resolveWith(ResolveOptions{StoreName: "half"}, m, fakeEnv(nil), nil)
	if err == nil {
		t.Fatal("--store-name on a half-built store should fail")
	}
	if !strings.Contains(err.Error(), "not a finished store") {
		t.Errorf("err = %v, want it to name the incomplete store", err)
	}
}

// TestResolve_SkipsDanglingEntry pins the other half: an entry whose folder is
// gone entirely is skipped, so a hard-killed promote leaves the project able to
// resolve elsewhere instead of wedged (CONFIG-SPEC §3).
func TestResolve_SkipsDanglingEntry(t *testing.T) {
	m := vfs.NewMem()
	writeRegistry(t, m, testCentral, registryEntry{Path: "/dev/proj", Store: "gone"})

	if _, _, err := resolveWith(ResolveOptions{WorkDir: "/dev/proj"}, m, fakeEnv(nil), nil); !errors.Is(err, ErrNoStore) {
		t.Errorf("err = %v, want ErrNoStore for an entry with no folder", err)
	}
}

func TestRenameCentral_MovesFolderAndEntry(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/myproj", "/dev/myproj/.tasks", "myp")
	if _, err := moveToCentralWith("/dev/myproj", "old", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote: %v", err)
	}

	dir, err := renameCentralWith("old", "new", m, fakeEnv(nil))
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if dir != storeDirFor("new") {
		t.Errorf("dir = %q, want %q", dir, storeDirFor("new"))
	}
	if _, err := m.Stat(storeDirFor("old")); !vfs.IsNotExist(err) {
		t.Errorf("old folder must be gone: %v", err)
	}
	if _, err := m.ReadFile(filepath.Join(dir, ConfigFileName)); err != nil {
		t.Errorf("config did not move: %v", err)
	}

	entries, err := loadRegistry(m, testCentral, testHome)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(entries) != 1 || entries[0].Store != "new" || entries[0].Path != "/dev/myproj" {
		t.Errorf("entries = %+v, want the entry renamed with its path kept", entries)
	}
	// The project still resolves, now through the new name.
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/dev/myproj"}, m, fakeEnv(nil), nil)
	if err != nil || info.StorePath != dir {
		t.Errorf("resolve after rename = %v %q (err %v)", info.Kind, info.StorePath, err)
	}
}

func TestRenameCentral_Rejections(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/a", "/dev/a/.tasks", "aaa")
	makeStore(t, m, "/dev/b", "/dev/b/.tasks", "bbb")
	if _, err := moveToCentralWith("/dev/a", "a", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote a: %v", err)
	}
	if _, err := moveToCentralWith("/dev/b", "b", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote b: %v", err)
	}

	if _, err := renameCentralWith("ghost", "x", m, fakeEnv(nil)); !errors.Is(err, ErrStoreNotRegistered) {
		t.Errorf("unknown store err = %v, want ErrStoreNotRegistered", err)
	}
	if _, err := renameCentralWith("a", "b", m, fakeEnv(nil)); !errors.Is(err, ErrStoreExists) {
		t.Errorf("taken name err = %v, want ErrStoreExists", err)
	}
	if _, err := renameCentralWith("a", "bad/name", m, fakeEnv(nil)); err == nil {
		t.Error("invalid store name should be rejected")
	}
	// Every refusal left both stores where they were.
	if _, err := m.Stat(storeDirFor("a")); err != nil {
		t.Errorf("store a must survive: %v", err)
	}
	if _, err := m.Stat(storeDirFor("b")); err != nil {
		t.Errorf("store b must survive: %v", err)
	}
}

func TestRelinkCentral_RepointsEntry(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/oldhome", "/dev/oldhome/.tasks", "prj")
	if _, err := moveToCentralWith("/dev/oldhome", "prj", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The project moves on disk.
	if err := m.MkdirAll("/dev/newhome", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	project, err := relinkCentralWith("prj", "/dev/newhome", m, fakeEnv(nil))
	if err != nil {
		t.Fatalf("relink: %v", err)
	}
	if project != "/dev/newhome" {
		t.Errorf("project = %q, want /dev/newhome", project)
	}

	// The new location resolves; the old one no longer does.
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/dev/newhome"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve at new path: %v", err)
	}
	if info.Kind != ResolvedCentral || info.ProjectPath != "/dev/newhome" {
		t.Errorf("resolve = %v %q", info.Kind, info.ProjectPath)
	}
	if _, _, err := resolveWith(ResolveOptions{WorkDir: "/dev/oldhome"}, m, fakeEnv(nil), nil); !errors.Is(err, ErrNoStore) {
		t.Errorf("old path err = %v, want ErrNoStore", err)
	}
	// Relink touches no files.
	if _, err := m.ReadFile(filepath.Join(storeDirFor("prj"), ConfigFileName)); err != nil {
		t.Errorf("store files must be untouched: %v", err)
	}
}

func TestRelinkCentral_Rejections(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/a", "/dev/a/.tasks", "aaa")
	makeStore(t, m, "/dev/b", "/dev/b/.tasks", "bbb")
	if _, err := moveToCentralWith("/dev/a", "a", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote a: %v", err)
	}
	if _, err := moveToCentralWith("/dev/b", "b", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote b: %v", err)
	}

	if _, err := relinkCentralWith("ghost", "/dev/a", m, fakeEnv(nil)); !errors.Is(err, ErrStoreNotRegistered) {
		t.Errorf("unknown store err = %v, want ErrStoreNotRegistered", err)
	}
	// Pointing b's entry at a's project would give one path two stores.
	if _, err := relinkCentralWith("b", "/dev/a", m, fakeEnv(nil)); err == nil {
		t.Error("re-pointing onto an already-registered path should be rejected")
	}
	// Re-pointing an entry at the path it already has is a no-op, not a clash.
	if _, err := relinkCentralWith("a", "/dev/a", m, fakeEnv(nil)); err != nil {
		t.Errorf("self-relink should be allowed: %v", err)
	}
}

// TestRelinkCentral_RefusesMissingProject pins the guard against a typo'd path:
// canonicalize falls back to the lexical form for a path that does not exist, so
// without an explicit check a typo silently points a live entry at nothing.
func TestRelinkCentral_RefusesMissingProject(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/a", "/dev/a/.tasks", "aaa")
	if _, err := moveToCentralWith("/dev/a", "a", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote: %v", err)
	}

	if _, err := relinkCentralWith("a", "/dev/typo", m, fakeEnv(nil)); err == nil {
		t.Fatal("re-pointing at a nonexistent directory should be rejected")
	}
	// The entry still points where it did.
	entries, err := loadRegistry(m, testCentral, testHome)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "/dev/a" {
		t.Errorf("entries = %+v, want the original path untouched", entries)
	}
}

// TestCentralNameClash_ErrorNamesCentralStore pins the error wording: a central
// name collision must not report ".tasks directory already exists", which points
// the reader at the project's local store instead of the registry.
func TestCentralNameClash_ErrorNamesCentralStore(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/dev/a", "/dev/a/.tasks", "aaa")
	makeStore(t, m, "/dev/b", "/dev/b/.tasks", "bbb")
	if _, err := moveToCentralWith("/dev/a", "taken", m, fakeEnv(nil), nil); err != nil {
		t.Fatalf("promote a: %v", err)
	}

	_, err := moveToCentralWith("/dev/b", "taken", m, fakeEnv(nil), nil)
	if !errors.Is(err, ErrStoreExists) {
		t.Fatalf("err = %v, want ErrStoreExists", err)
	}
	if !strings.Contains(err.Error(), `central store "taken"`) {
		t.Errorf("err = %q, want it to name the central store", err)
	}
	if strings.Contains(err.Error(), DataDirName) {
		t.Errorf("err = %q, must not point at a local %s directory", err, DataDirName)
	}

	_, err = renameCentralWith("taken", "taken", m, fakeEnv(nil))
	if !errors.Is(err, ErrStoreExists) || !strings.Contains(err.Error(), "central store") {
		t.Errorf("rename onto a taken name err = %v, want a central-store ErrStoreExists", err)
	}
}

// TestRelinkCentral_RefusesDanglingEntry checks the guard that keeps relink from
// writing an entry resolution would ignore.
func TestRelinkCentral_RefusesDanglingEntry(t *testing.T) {
	m := vfs.NewMem()
	writeRegistry(t, m, testCentral, registryEntry{Path: "/dev/gone", Store: "ghost"})
	if err := m.MkdirAll("/dev/newhome", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if _, err := relinkCentralWith("ghost", "/dev/newhome", m, fakeEnv(nil)); !errors.Is(err, ErrNoStore) {
		t.Errorf("err = %v, want ErrNoStore for a missing store folder", err)
	}
	// The registry is unchanged.
	entries, err := loadRegistry(m, testCentral, testHome)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "/dev/gone" {
		t.Errorf("entries = %+v, want the original entry untouched", entries)
	}
}
