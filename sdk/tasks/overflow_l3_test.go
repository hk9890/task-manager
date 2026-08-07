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

// L3 integration tests for body overflow against a real filesystem: the actual
// bytes on disk, the actual directory layout, and survival across a reopen of
// the store (a fresh process would see exactly this).
package tasks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

func bigL3Body(marker string) string {
	return marker + "\n" + strings.Repeat("padding line\n", tasks.MaxInlineBody/8)
}

func l3Store(t *testing.T) (*tasks.Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := tasks.Init(root, "ovf")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s, filepath.Join(root, tasks.DataDirName)
}

// TestL3_Overflow_OnDiskLayout verifies the real on-disk result: a bounded .md
// in the hot directory and the body in content/<id> with no extension.
func TestL3_Overflow_OnDiskLayout(t *testing.T) {
	s, dir := l3Store(t)
	body := bigL3Body("needle-l3")

	iss, err := s.Create(tasks.CreateInput{Title: "big", Description: body})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := iss.Issue.ID

	mdPath := filepath.Join(dir, id+tasks.FileExt)
	md, err := os.ReadFile(mdPath) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	if len(md) > tasks.MaxInlineBody {
		t.Fatalf("hot file must stay bounded, got %d bytes", len(md))
	}
	if strings.Contains(string(md), "needle-l3") {
		t.Fatal("the body must not be in the hot file")
	}
	if !strings.Contains(string(md), "body_external: true") {
		t.Fatalf("md must record the flag, got:\n%s", md)
	}

	contentPath := filepath.Join(dir, "content", id)
	got, err := os.ReadFile(contentPath) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read content sidecar: %v", err)
	}
	if string(got) != strings.TrimSpace(body) {
		t.Fatalf("sidecar bytes differ: got %d, want %d", len(got), len(strings.TrimSpace(body)))
	}

	// content/ must be a subdirectory, so the hot scan never descends into it.
	info, err := os.Stat(filepath.Join(dir, "content"))
	if err != nil || !info.IsDir() {
		t.Fatalf("content must be a directory: %v", err)
	}
}

// TestL3_Overflow_SurvivesStoreReopen: a second Open of the same directory —
// what a separate taskmgr process does — sees the same resolved body.
func TestL3_Overflow_SurvivesStoreReopen(t *testing.T) {
	s, dir := l3Store(t)
	body := bigL3Body("needle-reopen")
	iss, err := s.Create(tasks.CreateInput{Title: "big", Description: body})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := iss.Issue.ID

	s2, err := tasks.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s2.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != strings.TrimSpace(body) {
		t.Fatalf("body did not survive a reopen: got %d bytes, want %d",
			len(got.Description), len(strings.TrimSpace(body)))
	}
}

// TestL3_Overflow_CloseMovesOnlyTheMD checks the partition rule on real disk:
// the .md moves to closed/, the sidecar stays put, and the body still resolves.
func TestL3_Overflow_CloseMovesOnlyTheMD(t *testing.T) {
	s, dir := l3Store(t)
	iss, err := s.Create(tasks.CreateInput{Title: "big", Description: bigL3Body("needle-cl3")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := iss.Issue.ID

	if _, err := s.Close(id, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, id+tasks.FileExt)); !os.IsNotExist(err) {
		t.Fatal("the .md must leave the hot directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "closed", id+tasks.FileExt)); err != nil {
		t.Fatalf("the .md must land in closed/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "content", id)); err != nil {
		t.Fatalf("the sidecar must stay in content/: %v", err)
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get after close: %v", err)
	}
	if !strings.Contains(got.Description, "needle-cl3") {
		t.Fatal("a closed issue's body must still resolve")
	}
}

// TestL3_Overflow_JoinRemovesSidecarFromDisk verifies the rejoin actually
// unlinks the file rather than merely ignoring it — a leftover would be dead
// weight in git forever.
func TestL3_Overflow_JoinRemovesSidecarFromDisk(t *testing.T) {
	s, dir := l3Store(t)
	iss, err := s.Create(tasks.CreateInput{Title: "big", Description: bigL3Body("needle-join")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := iss.Issue.ID
	contentPath := filepath.Join(dir, "content", id)
	if _, err := os.Stat(contentPath); err != nil {
		t.Fatalf("setup: expected a sidecar: %v", err)
	}

	small := "small again"
	if _, err := s.Update(id, tasks.UpdateInput{Description: &small}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := os.Stat(contentPath); !os.IsNotExist(err) {
		t.Fatal("the sidecar must be unlinked once the body is back inline")
	}

	md, err := os.ReadFile(filepath.Join(dir, id+tasks.FileExt)) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	if !strings.Contains(string(md), "small again") {
		t.Fatal("the body must be back in the .md")
	}
	if strings.Contains(string(md), "body_external") {
		t.Fatal("the flag must be cleared on disk")
	}
}
