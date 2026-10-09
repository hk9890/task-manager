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

// L3 tests for Store.Watch on a real directory: the operating system's file
// notifications, which vfs.Mem only imitates.
package tasks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

func TestL3_Watch_WriteThroughAnotherHandle_Signals(t *testing.T) {
	s := storetest.New(t).TempDir(t)
	signals := watchStore(t, s)
	other, err := tasks.Open(s.Root())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := other.Create(tasks.CreateInput{Title: "from another handle"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantSignal(t, signals)
	wantNoSignal(t, signals)
}

func TestL3_Watch_FileWrittenOutsideTheEngine_Signals(t *testing.T) {
	s := storetest.New(t).TempDir(t)
	signals := watchStore(t, s)

	if err := os.WriteFile(filepath.Join(s.Dir(), "tst-9999.md"), []byte("by hand"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	wantSignal(t, signals)
}

// The comments directory does not exist until the first comment. The second
// comment appends to a file inside it, which only a watch on that directory
// reports.
func TestL3_Watch_SubdirectoryCreatedAfterWatch_IsWatched(t *testing.T) {
	s := storetest.New(t).Issue("tst-0001").TempDir(t)
	if _, err := os.Stat(filepath.Join(s.Dir(), "comments")); !os.IsNotExist(err) {
		t.Fatalf("comments directory: Stat error = %v, want it absent before the first comment", err)
	}
	signals := watchStore(t, s)

	if _, err := s.AddComment("tst-0001", tasks.Actor{Name: "hans"}, "first"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	wantSignal(t, signals)
	wantNoSignal(t, signals)

	if _, err := s.AddComment("tst-0001", tasks.Actor{Name: "hans"}, "second"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	wantSignal(t, signals)
}

// A write that changes nothing still takes the lock, and the first one creates
// the lock file.
func TestL3_Watch_NoOpWrite_GivesNoSignal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TASKMGR_HOME", t.TempDir())
	raw := storetest.NewRawFixture(t, root)
	raw.WriteIssue("tst-0001.md", []byte("---\nid: tst-0001\ntitle: Seeded\nstatus: open\ntype: task\npriority: 2\ncreated: 2026-01-01T00:00:00Z\nupdated: 2026-01-01T00:00:00Z\n---\n"))
	s, err := tasks.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), ".lock")); !os.IsNotExist(err) {
		t.Fatalf("lock file: Stat error = %v, want it absent before the first write", err)
	}
	signals := watchStore(t, s)

	if _, err := s.Update("tst-0001", tasks.UpdateInput{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.Dir(), ".lock")); err != nil {
		t.Fatalf("lock file after the write: %v", err)
	}
	wantNoSignal(t, signals)
}

func TestL3_Watch_StoreDirectoryRenamed_ClosesChannel(t *testing.T) {
	s := storetest.New(t).TempDir(t)
	signals := watchStore(t, s)

	if err := os.Rename(s.Dir(), s.Dir()+".moved"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	wantClosed(t, signals)
}

func TestL3_Watch_StoreDirectoryRemoved_ClosesChannel(t *testing.T) {
	s := storetest.New(t).TempDir(t)
	signals := watchStore(t, s)

	if err := os.RemoveAll(s.Dir()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	wantClosed(t, signals)
}
