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

// L2 tests for FS.MoveTree on the in-memory FS. The real-filesystem behaviour
// (rename, the cross-device copy fallback) is covered at L3 in os_test.go.
package vfs_test

import (
	"errors"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// TestMem_MoveTree_MovesWholeTree verifies that MoveTree re-keys every file and
// nested directory under src and leaves nothing behind at the old paths.
func TestMem_MoveTree_MovesWholeTree(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/proj/.tasks/comments", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.MkdirAll("/central/stores", 0o755); err != nil {
		t.Fatalf("MkdirAll dst parent: %v", err)
	}
	if err := m.WriteAtomic("/proj/.tasks/config.yaml", []byte("prefix: prj\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := m.WriteAtomic("/proj/.tasks/comments/prj-1.yaml", []byte("- body: hi\n"), 0o644); err != nil {
		t.Fatalf("write comment: %v", err)
	}

	if err := m.MoveTree("/proj/.tasks", "/central/stores/proj"); err != nil {
		t.Fatalf("MoveTree: %v", err)
	}

	got, err := m.ReadFile("/central/stores/proj/config.yaml")
	if err != nil {
		t.Fatalf("config at destination: %v", err)
	}
	if string(got) != "prefix: prj\n" {
		t.Errorf("config content = %q", got)
	}
	if _, err := m.ReadFile("/central/stores/proj/comments/prj-1.yaml"); err != nil {
		t.Errorf("nested file at destination: %v", err)
	}
	if _, err := m.Stat("/central/stores/proj/comments"); err != nil {
		t.Errorf("nested dir at destination: %v", err)
	}
	if _, err := m.ReadFile("/proj/.tasks/config.yaml"); !vfs.IsNotExist(err) {
		t.Errorf("source file still present: %v", err)
	}
	if _, err := m.Stat("/proj/.tasks"); !vfs.IsNotExist(err) {
		t.Errorf("source dir still present: %v", err)
	}
	// The project directory itself must survive — only .tasks moved.
	if _, err := m.Stat("/proj"); err != nil {
		t.Errorf("project dir removed: %v", err)
	}
}

// TestMem_MoveTree_Errors covers the three refusals: missing source, existing
// destination, and a destination whose parent does not exist. Each must leave
// the source where it was.
func TestMem_MoveTree_Errors(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/a/store", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.MkdirAll("/b/taken", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := m.MoveTree("/a/missing", "/b/new"); !vfs.IsNotExist(err) {
		t.Errorf("missing source err = %v, want not-exist", err)
	}
	if err := m.MoveTree("/a/store", "/b/taken"); err == nil {
		t.Error("moving onto an existing destination should fail")
	}
	if err := m.MoveTree("/a/store", "/nope/new"); !vfs.IsNotExist(err) {
		t.Errorf("missing destination parent err = %v, want not-exist", err)
	}
	if _, err := m.Stat("/a/store"); err != nil {
		t.Errorf("source must survive a refused move: %v", err)
	}
}

// TestMem_FailOn_MoveTree verifies MoveTree honours fault injection, which is
// what lets the SDK tests assert what an interrupted promote leaves behind.
func TestMem_FailOn_MoveTree(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/a/store", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := m.MkdirAll("/b", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	boom := errors.New("disk on fire")
	m.FailOn("MoveTree", "/a/store", boom)

	if err := m.MoveTree("/a/store", "/b/new"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want injected fault", err)
	}
	if _, err := m.Stat("/a/store"); err != nil {
		t.Errorf("source must survive the injected fault: %v", err)
	}
	if _, err := m.Stat("/b/new"); !vfs.IsNotExist(err) {
		t.Errorf("destination must not exist after the fault: %v", err)
	}
	// The fault is consumed, so the retry succeeds.
	if err := m.MoveTree("/a/store", "/b/new"); err != nil {
		t.Fatalf("retry after fault: %v", err)
	}
}
