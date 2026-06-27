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

// Package storetest: raw-bytes fixture helpers.
//
// RawFixture lets tests materialise a store from arbitrary byte content written
// directly into the .tasks/ tree — bypassing the Store API entirely. This is the
// mechanism for exercising hand-edited, git-merged, or otherwise externally
// produced on-disk states: malformed frontmatter, sidecar cycles, truncated
// docs, BOM-prefixed files, etc.
//
// Usage:
//
//	dir := t.TempDir()
//	rf := storetest.NewRawFixture(t, dir)
//	rf.WriteIssue("tst-0001.md", []byte("---\nid: tst-0001\n---\n"))
//	rf.WriteSidecar("tst-0001.yml", []byte("---\nid: abcd1234\ncreated: ...\nbody: |\n  note\n"))
//	s, err := tasks.Open(dir)
package storetest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// RawFixture holds a temp directory that has been initialised as a .tasks/ root
// so that tasks.Open can load it after raw files have been written.
type RawFixture struct {
	t       *testing.T
	taskDir string // path to the .tasks/ subdirectory
}

// NewRawFixture creates a .tasks/ skeleton (the directories expected by the
// store) inside root and returns a RawFixture that writes into it.
// root must already exist (e.g. t.TempDir()).
func NewRawFixture(t *testing.T, root string) *RawFixture {
	t.Helper()
	taskDir := filepath.Join(root, tasks.DataDirName)
	for _, sub := range []string{
		taskDir,
		filepath.Join(taskDir, "comments"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("RawFixture: MkdirAll(%q): %v", sub, err)
		}
	}
	// Write a minimal .tasks/config.yaml so tasks.Open succeeds.
	cfgPath := filepath.Join(taskDir, tasks.ConfigFileName)
	cfgData := []byte("prefix: tst\n")
	if err := os.WriteFile(cfgPath, cfgData, 0o644); err != nil {
		t.Fatalf("RawFixture: write config: %v", err)
	}
	return &RawFixture{t: t, taskDir: taskDir}
}

// WriteIssue writes raw bytes to .tasks/<name>. name must be a bare filename
// (e.g. "tst-0001.md" or "closed/tst-0002.md"); no path traversal.
func (rf *RawFixture) WriteIssue(name string, data []byte) {
	rf.t.Helper()
	path := filepath.Join(rf.taskDir, name)
	// Ensure the parent directory exists (handles "closed/" prefix).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		rf.t.Fatalf("RawFixture.WriteIssue: MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		rf.t.Fatalf("RawFixture.WriteIssue(%q): %v", name, err)
	}
}

// WriteSidecar writes raw bytes to .tasks/comments/<name>.  name must be a
// bare filename (e.g. "tst-0001.yml").
func (rf *RawFixture) WriteSidecar(name string, data []byte) {
	rf.t.Helper()
	path := filepath.Join(rf.taskDir, "comments", name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		rf.t.Fatalf("RawFixture.WriteSidecar(%q): %v", name, err)
	}
}

// Dir returns the root directory (parent of .tasks/) so callers can pass it to
// tasks.Open.
func (rf *RawFixture) Dir() string {
	return filepath.Dir(rf.taskDir)
}
