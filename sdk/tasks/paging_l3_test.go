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

// paging_l3_test.go — cross-partition read dedup (at-dny.1).
//
// These build the store from raw bytes with storetest.RawFixture, which writes
// directly into a real .tasks/ tree so tasks.Open can load it: the state under
// test — one issue ID present in both the hot set and closed/ — is one the Store
// API cannot produce, because it is what an interrupted close leaves behind.
package tasks_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// TestList_CrossPartitionDedup_NoDuplicateID verifies that a scan spanning both
// hot and closed/ partitions never returns the same issue ID twice, even when
// that ID appears in both partitions simultaneously (simulating a concurrent
// close/reopen in-flight). This is achieved using a RawFixture to write the
// same issue file into both partitions.
func TestList_CrossPartitionDedup_NoDuplicateID(t *testing.T) {
	// Build the raw bytes for an issue that appears in both the hot set and closed/.
	// We use two different IDs (tst-0001 hot-only, tst-0002 in BOTH partitions).
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)

	// A normal hot issue (only in hot set).
	hotOnly := []byte("---\nid: tst-0001\ntitle: hot only\nstatus: open\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("tst-0001.md", hotOnly)

	// A "ghost" issue appearing in BOTH hot and closed/ simultaneously —
	// simulates a concurrent close that wrote to closed/ but hasn't removed the
	// hot file yet, or a concurrent reopen that put it back in hot but the
	// closed/ file wasn't removed yet.
	ghost := []byte("---\nid: tst-0002\ntitle: ghost\nstatus: open\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("tst-0002.md", ghost)
	ghostClosed := []byte("---\nid: tst-0002\ntitle: ghost\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0002.md", ghostClosed)

	// A normal closed issue (only in closed/).
	closedOnly := []byte("---\nid: tst-0003\ntitle: closed only\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0003.md", closedOnly)

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Scan both partitions — IncludeClosed triggers the cross-partition merge.
	issues, err := s.List(tasks.Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("List(IncludeClosed=true): %v", err)
	}

	// Verify no duplicate IDs.
	seen := make(map[string]int)
	for _, iss := range issues {
		seen[iss.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("cross-partition dedup: ID %q appears %d times, want exactly 1", id, count)
		}
	}

	// All three logical issues should be present exactly once.
	for _, id := range []string{"tst-0001", "tst-0002", "tst-0003"} {
		if seen[id] != 1 {
			t.Errorf("cross-partition dedup: ID %q count=%d, want 1; full set=%v", id, seen[id], issueIDs(issues))
		}
	}
}

// TestListPage_CrossPartitionDedup_NoDuplicateID verifies the same dedup
// guarantee through ListPage.
func TestListPage_CrossPartitionDedup_NoDuplicateID(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)

	// Ghost issue in both partitions.
	ghost := []byte("---\nid: tst-0001\ntitle: ghost\nstatus: open\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("tst-0001.md", ghost)
	ghostClosed := []byte("---\nid: tst-0001\ntitle: ghost\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0001.md", ghostClosed)

	// Normal closed issue.
	closed := []byte("---\nid: tst-0002\ntitle: done\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0002.md", closed)

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	p, err := s.ListPage(tasks.Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}

	seen := make(map[string]int)
	for _, iss := range p.Issues {
		seen[iss.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("ListPage cross-partition dedup: ID %q appears %d times, want 1", id, count)
		}
	}

	// Total must equal the number of unique logical issues (2), not the raw file count (3).
	if p.Total != 2 {
		t.Errorf("ListPage cross-partition dedup: Total=%d, want 2 (deduplicated)", p.Total)
	}
}
