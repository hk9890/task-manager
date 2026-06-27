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

// L4 CLI tests for:
//
//	taskmgr list --all       → includes closed issues
//	taskmgr search --all     → includes closed issues
//	taskmgr list (default)   → hot-only (no closed issues)
//	taskmgr search (default) → hot-only
//	taskmgr list -q <expr>   → filtered subset, closed scope auto-detected
package cmd_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// newStoreWithOpenAndClosed initialises a temp store with one open and one
// closed issue. Returns (root, openID, closedID).
func newStoreWithOpenAndClosed(t *testing.T) (root, openID, closedID string) {
	t.Helper()
	root = t.TempDir()
	s, err := tasks.Init(root, "lst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	open, err := unwrap(s.Create(tasks.CreateInput{Title: "active task", Labels: []string{"area:hot"}}))
	if err != nil {
		t.Fatalf("Create open: %v", err)
	}
	closed, err := unwrap(s.Create(tasks.CreateInput{Title: "done task", Labels: []string{"area:cold"}}))
	if err != nil {
		t.Fatalf("Create closed: %v", err)
	}
	if _, err := s.Close(closed.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return root, open.ID, closed.ID
}

// issueIDsFromJSON parses a JSON array of issueDTO and returns the ids.
func issueIDsFromJSON(t *testing.T, output string) []string {
	t.Helper()
	var dtos []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &dtos); err != nil {
		t.Fatalf("parse JSON array: %v\noutput: %s", err, output)
	}
	ids := make([]string, len(dtos))
	for i, d := range dtos {
		id, _ := d["id"].(string)
		ids[i] = id
	}
	return ids
}

// ── taskmgr list ────────────────────────────────────────────────────────────────

// TestL4_ListDefault_HotOnly verifies that `taskmgr list` without --all excludes
// closed issues (hot-only).
func TestL4_ListDefault_HotOnly(t *testing.T) {
	root, openID, closedID := newStoreWithOpenAndClosed(t)

	out, _, code := taskmgr(t, root, "--json", "list")
	if code != 0 {
		t.Fatalf("list failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	for _, id := range ids {
		if id == closedID {
			t.Errorf("list (hot-only) returned closed issue %s — expected hot-only", closedID)
		}
	}
	found := false
	for _, id := range ids {
		if id == openID {
			found = true
		}
	}
	if !found {
		t.Errorf("list (hot-only) missing open issue %s; got: %v", openID, ids)
	}
}

// TestL4_ListAll_IncludesClosed verifies that `taskmgr list --all` returns both
// hot and cold issues.
func TestL4_ListAll_IncludesClosed(t *testing.T) {
	root, openID, closedID := newStoreWithOpenAndClosed(t)

	out, _, code := taskmgr(t, root, "--json", "list", "--all")
	if code != 0 {
		t.Fatalf("list --all failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	if !have[openID] {
		t.Errorf("list --all missing open issue %s; got: %v", openID, ids)
	}
	if !have[closedID] {
		t.Errorf("list --all missing closed issue %s; got: %v", closedID, ids)
	}
}

// TestL4_ListQuery_ClosedStatus verifies that `taskmgr list -q 'status == "closed"'`
// auto-includes the closed partition so closed issues are returned.
func TestL4_ListQuery_ClosedStatus(t *testing.T) {
	root, _, closedID := newStoreWithOpenAndClosed(t)

	out, _, code := taskmgr(t, root, "--json", "list", "-q", `status == "closed"`)
	if code != 0 {
		t.Fatalf("list -q closed failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	found := false
	for _, id := range ids {
		if id == closedID {
			found = true
		}
	}
	if !found {
		t.Errorf("list -q 'status==\"closed\"' missing closed issue %s; got: %v", closedID, ids)
	}
}

// TestL4_ListQuery_OpenStatus verifies that `taskmgr list -q 'status == "open"'`
// stays hot-only (no closed-referencing expression).
func TestL4_ListQuery_OpenStatus(t *testing.T) {
	root, _, closedID := newStoreWithOpenAndClosed(t)

	out, _, code := taskmgr(t, root, "--json", "list", "-q", `status == "open"`)
	if code != 0 {
		t.Fatalf("list -q open failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	for _, id := range ids {
		if id == closedID {
			t.Errorf("list -q 'status==\"open\"' returned closed issue %s — must be hot-only", closedID)
		}
	}
}

// ── taskmgr search ──────────────────────────────────────────────────────────────

// TestL4_SearchDefault_HotOnly verifies that `taskmgr search <text>` without
// --all excludes closed issues.
func TestL4_SearchDefault_HotOnly(t *testing.T) {
	root, _, closedID := newStoreWithOpenAndClosed(t)

	// "done" is in the closed issue's title; without --all it must not appear.
	out, _, code := taskmgr(t, root, "--json", "search", "done")
	if code != 0 {
		t.Fatalf("search failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	for _, id := range ids {
		if id == closedID {
			t.Errorf("search (hot-only) returned closed issue %s — expected hot-only", closedID)
		}
	}
}

// TestL4_SearchAll_IncludesClosed verifies that `taskmgr search <text> --all`
// searches the cold partition too.
func TestL4_SearchAll_IncludesClosed(t *testing.T) {
	root, _, closedID := newStoreWithOpenAndClosed(t)

	// "done" is in the closed issue's title; with --all it must appear.
	out, _, code := taskmgr(t, root, "--json", "search", "done", "--all")
	if code != 0 {
		t.Fatalf("search --all failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	found := false
	for _, id := range ids {
		if id == closedID {
			found = true
		}
	}
	if !found {
		t.Errorf("search --all missing closed issue %s (title contains 'done'); got: %v", closedID, ids)
	}
}

// TestL4_SearchAll_OpenIssueAlsoReturned verifies that search --all returns both
// hot and cold issues that match.
func TestL4_SearchAll_OpenIssueAlsoReturned(t *testing.T) {
	root, openID, closedID := newStoreWithOpenAndClosed(t)

	// "task" is in both the open issue's title ("active task") and not in the closed
	// one; use "a" to match both. Or use "task" which matches "active task".
	out, _, code := taskmgr(t, root, "--json", "search", "task", "--all")
	if code != 0 {
		t.Fatalf("search --all failed (exit %d): %s", code, out)
	}
	ids := issueIDsFromJSON(t, out)
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	if !have[openID] {
		t.Errorf("search --all 'task' missing open issue %s; got: %v", openID, ids)
	}
	// closed issue has "done task" title — also matches "task"
	if !have[closedID] {
		t.Errorf("search --all 'task' missing closed issue %s; got: %v", closedID, ids)
	}
}
