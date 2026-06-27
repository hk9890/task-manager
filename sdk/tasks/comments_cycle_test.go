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

// L1 tests for cycle-safe comment-chain resolution (at-xp9, finding C2).
//
// Each test is guarded by go test -timeout (pass -timeout 30s at the CLI);
// if resolveComments hangs, the suite fails loudly instead of blocking forever.
//
// Test cases:
//   - 2-node cycle (A replaces B, B replaces A)
//   - self-replace (A replaces A)
//   - fork / duplicate-replaces (two docs replace the same target) — existing semantics
//   - dangling replaces (target id absent) — fail-open: doc survives as its own comment

import (
	"testing"
	"time"
)

var cycleTS = time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)

// TestResolveComments_TwoNodeCycle verifies that a 2-node cycle (A replaces B,
// B replaces A) resolves in bounded time and does not hang.
// The exact set of emitted comments is deterministic: each node is treated as
// its own chain root (graceful degradation), so both survive (unless one is a
// tombstone). At minimum, neither should cause an infinite loop.
func TestResolveComments_TwoNodeCycle(t *testing.T) {
	stream := []Comment{
		{ID: "aaaaaaaa", Created: cycleTS, Body: "comment A\n", Replaces: "bbbbbbbb"},
		{ID: "bbbbbbbb", Created: cycleTS.Add(time.Second), Body: "comment B\n", Replaces: "aaaaaaaa"},
	}

	// resolveComments must return (not hang). With -timeout 30s on the test
	// binary, a hang would cause the suite to fail rather than block silently.
	got := resolveComments(stream)

	// Degrade gracefully: each cycle node is its own root.
	// Both are non-tombstone, so both should survive.
	if len(got) == 0 {
		t.Error("2-node cycle: expected at least 1 resolved comment (graceful degradation), got 0")
	}
	// No hang — reaching here means the function returned.
}

// TestResolveComments_SelfReplace verifies that a self-replace (A replaces A)
// resolves in bounded time and does not hang.
func TestResolveComments_SelfReplace(t *testing.T) {
	stream := []Comment{
		{ID: "selfself", Created: cycleTS, Body: "self-replace comment\n", Replaces: "selfself"},
	}

	got := resolveComments(stream)

	// Graceful degradation: the self-replacing doc is treated as its own root.
	// It is not a tombstone, so it should survive.
	if len(got) != 1 {
		t.Errorf("self-replace: expected 1 resolved comment, got %d: %+v", len(got), got)
	}
	if len(got) > 0 && got[0].ID != "selfself" {
		t.Errorf("self-replace: expected selfself, got %q", got[0].ID)
	}
}

// TestResolveComments_SelfReplaceTombstone verifies that a self-replace
// tombstone (A replaces A, deleted:true) is treated as a tombstone and omitted.
func TestResolveComments_SelfReplaceTombstone(t *testing.T) {
	stream := []Comment{
		{ID: "selfself", Created: cycleTS, Deleted: true, Replaces: "selfself"},
	}

	got := resolveComments(stream)

	// A tombstone that is its own root should still be omitted from the
	// effective log (tombstone semantics are preserved).
	if len(got) != 0 {
		t.Errorf("self-replace tombstone: expected 0 resolved comments, got %d: %+v", len(got), got)
	}
}

// TestResolveComments_Fork verifies that when two docs replace the same target,
// the later one wins (existing semantics per TASK-STORAGE-SPEC §4.4 rule 3).
func TestResolveComments_Fork(t *testing.T) {
	// A fork: two docs replace the same real target "origorig".
	stream := []Comment{
		{ID: "origorig", Created: cycleTS, Body: "original\n"},
		{ID: "edit0001", Created: cycleTS.Add(time.Second), Replaces: "origorig", Body: "edit 1\n"},
		{ID: "edit0002", Created: cycleTS.Add(2 * time.Second), Replaces: "origorig", Body: "edit 2\n"},
	}

	got := resolveComments(stream)

	if len(got) != 1 {
		t.Fatalf("fork: expected 1 resolved comment, got %d: %+v", len(got), got)
	}
	// The later doc in the stream wins.
	if got[0].ID != "edit0002" {
		t.Errorf("fork: expected edit0002 (later wins), got %q", got[0].ID)
	}
}

// TestResolveComments_DanglingReplaces verifies that a doc whose replaces
// target is absent from the stream survives as its own independent comment
// (fail-open / dangling-replaces semantics).
func TestResolveComments_DanglingReplaces(t *testing.T) {
	stream := []Comment{
		// "dangdang" replaces "missing0" which does not exist in the stream.
		{ID: "dangdang", Created: cycleTS, Body: "dangling replaces\n", Replaces: "missing0"},
	}

	got := resolveComments(stream)

	// Fail-open: the doc with the dangling replaces survives as its own comment.
	if len(got) != 1 {
		t.Fatalf("dangling replaces: expected 1 resolved comment (fail-open), got %d: %+v", len(got), got)
	}
	if got[0].ID != "dangdang" {
		t.Errorf("dangling replaces: expected dangdang, got %q", got[0].ID)
	}
}

// TestResolveComments_LongChainNoCycle verifies that a legitimate long chain
// (no cycle) still resolves correctly — regression guard for the visited-set
// guard not breaking normal acyclic behaviour.
func TestResolveComments_LongChainNoCycle(t *testing.T) {
	// Build a 5-node chain: v1 → v2 → v3 → v4 → v5 (each replaces the previous).
	ids := []string{"v1aaaaaa", "v2bbbbbb", "v3cccccc", "v4dddddd", "v5eeeeee"}
	stream := make([]Comment, len(ids))
	for i, id := range ids {
		c := Comment{
			ID:      id,
			Created: cycleTS.Add(time.Duration(i) * time.Second),
			Body:    id + " body\n",
		}
		if i > 0 {
			c.Replaces = ids[i-1]
		}
		stream[i] = c
	}

	got := resolveComments(stream)

	if len(got) != 1 {
		t.Fatalf("long chain: expected 1 resolved comment, got %d: %+v", len(got), got)
	}
	if got[0].ID != "v5eeeeee" {
		t.Errorf("long chain: expected v5eeeeee (tail wins), got %q", got[0].ID)
	}
}

// TestResolveComments_TwoNodeCycleWithNonCycleComment verifies that a cycle
// between two nodes does not corrupt independent (non-cycle) comments in the
// same stream.
func TestResolveComments_TwoNodeCycleWithNonCycleComment(t *testing.T) {
	stream := []Comment{
		{ID: "goodgood", Created: cycleTS, Body: "independent comment\n"},
		{ID: "cycleaa0", Created: cycleTS.Add(time.Second), Body: "cycle A\n", Replaces: "cyclebb0"},
		{ID: "cyclebb0", Created: cycleTS.Add(2 * time.Second), Body: "cycle B\n", Replaces: "cycleaa0"},
	}

	got := resolveComments(stream)

	// "goodgood" must survive intact.
	found := false
	for _, c := range got {
		if c.ID == "goodgood" {
			found = true
		}
	}
	if !found {
		t.Errorf("cycle with independent comment: independent comment 'goodgood' missing from resolved output %+v", got)
	}
	// Total should be at least 1 (goodgood) and at most 3 (all three survive as roots).
	if len(got) == 0 {
		t.Error("cycle with independent comment: expected at least 1 resolved comment")
	}
}
