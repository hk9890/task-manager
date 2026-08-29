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

// L3 scope-guard tests for a store whose partition and status field disagree.
//
// The engine never produces that state: Close moves the file into closed/ and
// sets the field together. A hand-edited store does, and so does a cross-partition
// move interrupted between the two writes — which is why List carries a guard
// that drops a closed-status issue found in the hot partition.
//
// Every other List test builds its fixture through the API, where the two always
// agree, so the guard's taken branch is unreachable from them. The raw fixture is
// the only way to seed the disagreement, and it writes real bytes, which puts
// these at L3.
package tasks_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// closedStatusIssueMD returns a well-formed issue carrying status: closed, for
// writing directly into the hot partition where the engine would never put it.
func closedStatusIssueMD(id string) []byte {
	return []byte("---\n" +
		"id: " + id + "\n" +
		"title: " + id + "\n" +
		"status: closed\n" +
		"type: task\n" +
		"priority: 2\n" +
		"created: 2026-06-01T00:00:00Z\n" +
		"updated: 2026-06-01T00:00:00Z\n" +
		"closed: 2026-06-02T00:00:00Z\n" +
		"---\n")
}

// openIssueMD returns a well-formed open issue for the hot partition.
func openIssueMD(id string) []byte {
	return []byte("---\n" +
		"id: " + id + "\n" +
		"title: " + id + "\n" +
		"status: open\n" +
		"type: task\n" +
		"priority: 2\n" +
		"created: 2026-06-01T00:00:00Z\n" +
		"updated: 2026-06-01T00:00:00Z\n" +
		"---\n")
}

// TestL3_List_DropsAClosedStatusIssueFoundInTheHotPartition is the scope guard's
// taken branch. Without it a hand-edited store leaks finished work into every
// default `taskmgr list`, and nothing would notice.
func TestL3_List_DropsAClosedStatusIssueFoundInTheHotPartition(t *testing.T) {
	rf := storetest.NewRawFixture(t, t.TempDir())
	rf.WriteIssue("tst-0001.md", openIssueMD("tst-0001"))
	rf.WriteIssue("tst-0002.md", closedStatusIssueMD("tst-0002"))

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	issues, err := s.List(tasks.Filter{})
	if err != nil {
		t.Fatalf("List(Filter{}): %v", err)
	}
	for _, iss := range issues {
		if iss.ID == "tst-0002" {
			t.Error("List(Filter{}) returned tst-0002: a closed-status issue in the hot partition leaked into the default scope")
		}
	}
	if len(issues) != 1 {
		t.Errorf("List(Filter{}) = %d issues, want 1 (the open one only)", len(issues))
	}
}

// TestL3_List_IncludeClosed_SeesAClosedStatusIssueInTheHotPartition is the other
// side of the same guard: opting in must not hide the issue. Without this half a
// guard that dropped the issue unconditionally would still pass the test above.
func TestL3_List_IncludeClosed_SeesAClosedStatusIssueInTheHotPartition(t *testing.T) {
	rf := storetest.NewRawFixture(t, t.TempDir())
	rf.WriteIssue("tst-0001.md", openIssueMD("tst-0001"))
	rf.WriteIssue("tst-0002.md", closedStatusIssueMD("tst-0002"))

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	issues, err := s.List(tasks.Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("List(IncludeClosed): %v", err)
	}
	var found bool
	for _, iss := range issues {
		if iss.ID == "tst-0002" {
			found = true
		}
	}
	if !found {
		t.Errorf("List(IncludeClosed) missed tst-0002; got %d issues", len(issues))
	}
}
