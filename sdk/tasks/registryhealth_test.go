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

// registryhealth_test.go — L2: how a registry entry's folder is classified
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

func TestStores_StatFailureIsReportedNotReadAsDangling(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/p1", filepath.Join(testCentral, storesSubdir, "p1"), "p1")
	writeRegistry(t, m, testCentral, registryEntry{Path: "/p1", Store: "p1"})

	// What an unreadable ~/.taskmgr/stores does to the Stat of a store inside it.
	m.FailOn("Stat", filepath.Join(testCentral, storesSubdir, "p1"), errors.New("permission denied"))

	got, err := storesWith(m, fakeEnv(nil))
	if err == nil {
		t.Fatalf("an unreadable store directory must be an error, got %+v", got)
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
