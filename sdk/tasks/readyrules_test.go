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

// readyrules_test.go — L1 tests for the graph rules in ready.go.
//
// No store, no filesystem, no fixture: an index literal and a closure standing
// in for "is this ID closed?". This is what the layer model always promised for
// ready/blocked and what the file split makes possible — every one of these
// cases previously needed a Store to reach.

import (
	"testing"
	"time"
)

// closedSet returns a closedStat predicate over a fixed set of IDs.
func closedSet(ids ...string) func(string) bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

func TestOpenBlockers_ClassifiesEachBlocker(t *testing.T) {
	// agt-0002 is open in the hot index, agt-0003 is closed in the hot index,
	// agt-0004 is absent from the index but present in closed/, and agt-0009 is
	// absent from both — a dangling ref.
	idx := map[string]*Issue{
		"agt-0002": {ID: "agt-0002", Status: StatusOpen},
		"agt-0003": {ID: "agt-0003", Status: StatusClosed},
	}
	subject := &Issue{ID: "agt-0001", BlockedBy: []string{"agt-0002", "agt-0003", "agt-0004", "agt-0009"}}

	got := openBlockers(idx, closedSet("agt-0004"), subject)

	// TASK-STORAGE-SPEC §9: a blocker in closed/ counts as resolved. A dangling
	// ref stays unresolved so the inconsistency surfaces rather than silently
	// unblocking work.
	want := []string{"agt-0002", "agt-0009"}
	if len(got) != len(want) {
		t.Fatalf("openBlockers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("openBlockers[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOpenBlockers_NoBlockersIsEmpty(t *testing.T) {
	if got := openBlockers(map[string]*Issue{}, closedSet(), &Issue{ID: "agt-0001"}); len(got) != 0 {
		t.Errorf("openBlockers = %v, want empty", got)
	}
}

func TestWindow_ClampsOffsetAndLimit(t *testing.T) {
	all := []*Issue{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}

	cases := []struct {
		name   string
		offset int
		limit  int
		want   []string
	}{
		{"no window", 0, 0, []string{"a", "b", "c", "d"}},
		{"offset only", 2, 0, []string{"c", "d"}},
		{"offset and limit", 1, 2, []string{"b", "c"}},
		{"limit past the end", 3, 10, []string{"d"}},
		{"offset past the end", 9, 0, nil},
		{"negative offset clamps to zero", -5, 2, []string{"a", "b"}},
		{"negative limit clamps to none", 0, -5, []string{"a", "b", "c", "d"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := window(all, c.offset, c.limit)
			if len(got) != len(c.want) {
				t.Fatalf("window(%d, %d) = %v, want %v", c.offset, c.limit, ids(got), c.want)
			}
			for i := range c.want {
				if got[i].ID != c.want[i] {
					t.Errorf("window[%d] = %q, want %q", i, got[i].ID, c.want[i])
				}
			}
		})
	}
}

func TestSortIssues_WorkOrdersByPriorityThenCreated(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	issues := []*Issue{
		{ID: "later-p0", Priority: 0, Created: base.Add(time.Hour)},
		{ID: "p2", Priority: 2, Created: base},
		{ID: "earlier-p0", Priority: 0, Created: base},
	}

	sortIssues(issues, SortWork)

	want := []string{"earlier-p0", "later-p0", "p2"}
	for i, id := range want {
		if issues[i].ID != id {
			t.Errorf("sorted[%d] = %q, want %q (full order %v)", i, issues[i].ID, id, ids(issues))
		}
	}
}
