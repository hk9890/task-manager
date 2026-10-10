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

// registry_health_test.go — L2: how a registry entry's folder is classified
// (CONFIG-SPEC §3). The distinction under test is between "the folder is not
// there" and "the folder could not be read": they take different repairs, and
// collapsing them reports an intact store as dangling.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// TestStores_UnreadableEntryIsBrokenAndTheRestAreListed covers both places the
// Stat can be refused: the store directory itself (an unreadable stores/), and
// the config.yaml inside it (a store directory at mode 000).
func TestStores_UnreadableEntryIsBrokenAndTheRestAreListed(t *testing.T) {
	for name, failPath := range unreadableStorePaths("p2") {
		t.Run(name, func(t *testing.T) {
			m := vfs.NewMem()
			for _, n := range []string{"p1", "p2", "p3"} {
				makeStore(t, m, "/"+n, filepath.Join(testCentral, storesSubdir, n), n)
			}
			writeRegistry(t, m, testCentral,
				registryEntry{Path: "/p1", Store: "p1"},
				registryEntry{Path: "/p2", Store: "p2"},
				registryEntry{Path: "/p3", Store: "p3"},
			)
			m.FailOn("Stat", failPath, errors.New("permission denied"))

			got, err := storesWith(m, fakeEnv(nil))
			if err != nil {
				t.Fatalf("one unreadable store directory must not fail the listing: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("got %d entries, want all 3: %+v", len(got), got)
			}
			for _, e := range got {
				if e.Store != "p2" {
					if e.Health != StoreOK || e.Detail != "" {
						t.Errorf("entry %q = %v %q, want StoreOK with no detail", e.Store, e.Health, e.Detail)
					}
					continue
				}
				// Not StoreDangling: that label sends the reader to the repair
				// that deletes the registry entry of an intact store.
				if e.Health != StoreBroken {
					t.Errorf("entry p2 health = %v, want StoreBroken", e.Health)
				}
				if !strings.Contains(e.Detail, "permission denied") {
					t.Errorf("entry p2 detail %q does not carry the underlying failure", e.Detail)
				}
			}
		})
	}
}

func TestStores_UnreadableRegistryIsAnError(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/p1", filepath.Join(testCentral, storesSubdir, "p1"), "p1")
	writeRegistry(t, m, testCentral, registryEntry{Path: "/p1", Store: "p1"})
	m.FailOn("ReadFile", filepath.Join(testCentral, registryFileName), errors.New("permission denied"))

	got, err := storesWith(m, fakeEnv(nil))
	if err == nil {
		t.Fatalf("an unreadable registry must be an error, got %+v", got)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error %q does not carry the underlying failure", err)
	}
}

func TestResolve_StatFailureIsReportedNotSkipped(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/p1", filepath.Join(testCentral, storesSubdir, "p1"), "p1")
	writeRegistry(t, m, testCentral, registryEntry{Path: "/p1", Store: "p1"})
	m.FailOn("Stat", filepath.Join(testCentral, storesSubdir, "p1"), errors.New("permission denied"))

	_, _, err := resolveWith(ResolveOptions{WorkDir: "/p1"}, m, fakeEnv(nil), nil)
	if err == nil {
		t.Fatal("resolution must report an unreadable store directory")
	}
	if errors.Is(err, ErrNoStore) {
		t.Errorf("error = %v, want the read failure rather than ErrNoStore — the advice that comes "+
			"with ErrNoStore creates a second, empty store beside the real one", err)
	}
}

// unreadableStorePaths names both places the Stat of a store can be refused: the
// store directory itself, and the config.yaml inside it (a directory at mode 000).
func unreadableStorePaths(store string) map[string]string {
	dir := filepath.Join(testCentral, storesSubdir, store)
	return map[string]string{
		"directory": dir,
		"config":    filepath.Join(dir, ConfigFileName),
	}
}

// twoStoresOneUnreadable registers alpha for /alpha and beta for /beta, and
// refuses the next Stat of failPath. A Mem fault fires one time, so it stands
// for an unreadable store only while a resolution reads each store one time.
func twoStoresOneUnreadable(t *testing.T, failPath string) *vfs.Mem {
	t.Helper()
	m := vfs.NewMem()
	for _, n := range []string{"alpha", "beta"} {
		makeStore(t, m, "/"+n, filepath.Join(testCentral, storesSubdir, n), n)
	}
	writeRegistry(t, m, testCentral,
		registryEntry{Path: "/alpha", Store: "alpha"},
		registryEntry{Path: "/beta", Store: "beta"},
	)
	m.FailOn("Stat", failPath, errors.New("permission denied"))
	return m
}

func TestResolve_UnreadableStoreOfAnotherProject_IsErrNoStore(t *testing.T) {
	for name, failPath := range unreadableStorePaths("beta") {
		t.Run(name, func(t *testing.T) {
			m := twoStoresOneUnreadable(t, failPath)
			if err := m.MkdirAll("/elsewhere", 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}

			_, _, err := resolveWith(ResolveOptions{WorkDir: "/elsewhere"}, m, fakeEnv(nil), nil)
			if !errors.Is(err, ErrNoStore) {
				t.Errorf("error = %v, want ErrNoStore: beta owns another project", err)
			}
		})
	}
}

func TestResolve_UnreadableStoreOfAnotherProject_OpensTheOwningStore(t *testing.T) {
	for name, failPath := range unreadableStorePaths("beta") {
		t.Run(name, func(t *testing.T) {
			m := twoStoresOneUnreadable(t, failPath)

			s, info, err := resolveWith(ResolveOptions{WorkDir: "/alpha"}, m, fakeEnv(nil), nil)
			if err != nil {
				t.Fatalf("an unreadable beta must not fail the resolution of alpha: %v", err)
			}
			if s.Name() != "alpha" || info.Kind != ResolvedCentral {
				t.Errorf("resolved %q as %v, want alpha as ResolvedCentral", s.Name(), info.Kind)
			}
		})
	}
}

func TestResolve_UnreadableOwningStore_IsReported(t *testing.T) {
	for name, failPath := range unreadableStorePaths("beta") {
		t.Run(name, func(t *testing.T) {
			m := twoStoresOneUnreadable(t, failPath)

			_, _, err := resolveWith(ResolveOptions{WorkDir: "/beta"}, m, fakeEnv(nil), nil)
			if err == nil || !strings.Contains(err.Error(), "read central store") {
				t.Errorf("error = %v, want the read failure of beta", err)
			}
		})
	}
}

// nestedStoresOneUnreadable registers outer for /work and inner for /work/inner,
// and refuses the next Stat of the store directory of unreadable.
func nestedStoresOneUnreadable(t *testing.T, unreadable string) *vfs.Mem {
	t.Helper()
	m := vfs.NewMem()
	makeStore(t, m, "/work", filepath.Join(testCentral, storesSubdir, "outer"), "outer")
	makeStore(t, m, "/work/inner", filepath.Join(testCentral, storesSubdir, "inner"), "inner")
	writeRegistry(t, m, testCentral,
		registryEntry{Path: "/work", Store: "outer"},
		registryEntry{Path: "/work/inner", Store: "inner"},
	)
	m.FailOn("Stat", filepath.Join(testCentral, storesSubdir, unreadable), errors.New("permission denied"))
	return m
}

// TestResolve_UnreadableOwningStore_DoesNotFallToAShorterAncestor: the entry
// that owns the directory answers for it, also when it cannot be read.
func TestResolve_UnreadableOwningStore_DoesNotFallToAShorterAncestor(t *testing.T) {
	m := nestedStoresOneUnreadable(t, "inner")

	_, _, err := resolveWith(ResolveOptions{WorkDir: "/work/inner"}, m, fakeEnv(nil), nil)
	if err == nil || !strings.Contains(err.Error(), "read central store") {
		t.Errorf("error = %v, want the read failure of inner, not the store of /work", err)
	}
}

// TestResolve_UnreadableShorterAncestor_OpensTheLongerOne: an ancestor entry
// that loses the match does not own the directory, so its read failure has no
// effect.
func TestResolve_UnreadableShorterAncestor_OpensTheLongerOne(t *testing.T) {
	m := nestedStoresOneUnreadable(t, "outer")

	s, _, err := resolveWith(ResolveOptions{WorkDir: "/work/inner"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("an unreadable outer must not fail the resolution of /work/inner: %v", err)
	}
	if s.Name() != "inner" {
		t.Errorf("resolved %q, want inner", s.Name())
	}
}

// TestResolve_NamedUnreadableStore_IsReported: --store-name names the entry, so
// its read failure is the answer from every directory.
func TestResolve_NamedUnreadableStore_IsReported(t *testing.T) {
	for name, failPath := range unreadableStorePaths("beta") {
		t.Run(name, func(t *testing.T) {
			m := twoStoresOneUnreadable(t, failPath)

			_, _, err := resolveWith(ResolveOptions{StoreName: "beta", WorkDir: "/alpha"}, m, fakeEnv(nil), nil)
			if err == nil || !strings.Contains(err.Error(), "read central store") {
				t.Errorf("error = %v, want the read failure of beta", err)
			}
		})
	}
}

// TestResolve_NamedDanglingEntryIsNotReportedAsBroken pins the two messages
// apart. The broken-store message sends the reader to `ls` inside the store
// directory and a hand-written config.yaml; for an entry whose directory is gone
// both commands fail with ENOENT.
func TestResolve_NamedDanglingEntryIsNotReportedAsBroken(t *testing.T) {
	m := vfs.NewMem()
	writeRegistry(t, m, testCentral, registryEntry{Path: "/p1", Store: "p1"})

	_, _, err := resolveWith(ResolveOptions{StoreName: "p1"}, m, fakeEnv(nil), nil)
	if err == nil {
		t.Fatal("a named entry with no store directory must fail")
	}
	if strings.Contains(err.Error(), ConfigFileName) {
		t.Errorf("error %q reports a missing directory as a missing config.yaml", err)
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("error %q does not say the directory is gone", err)
	}
}

// TestResolve_NamedPartialStoreStillReportsTheMissingConfig keeps the other
// branch: a directory that is there but has no config.yaml is a different fault
// and keeps its own repair advice.
func TestResolve_NamedPartialStoreStillReportsTheMissingConfig(t *testing.T) {
	m := vfs.NewMem()
	dir := filepath.Join(testCentral, storesSubdir, "p1")
	if err := m.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeRegistry(t, m, testCentral, registryEntry{Path: "/p1", Store: "p1"})

	_, _, err := resolveWith(ResolveOptions{StoreName: "p1"}, m, fakeEnv(nil), nil)
	if err == nil {
		t.Fatal("a named entry whose store has no config.yaml must fail")
	}
	if !strings.Contains(err.Error(), ConfigFileName) {
		t.Errorf("error %q does not name the missing file", err)
	}
}
