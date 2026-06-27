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

// findcycle_test.go — unit tests for the iterative findCycle implementation.
//
// These tests verify:
//   - deep linear chains do not overflow the stack (no crash)
//   - cycle detection still works after the iterative rewrite
//   - edge cases: empty graph, missing start node, self-cycle, two-node cycle,
//     three-node cycle (original regression test), acyclic chain

import (
	"strings"
	"testing"
)

// buildLinearChain builds a dependency chain of length n in a map:
// node[0] is blocked by node[1], node[1] by node[2], ..., node[n-2] by node[n-1].
// The last node has no blockers. Returns the ID of the first node (start).
func buildLinearChain(n int) (map[string]*Issue, string) {
	idx := make(map[string]*Issue, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fakeID(i)
	}
	for i := 0; i < n-1; i++ {
		idx[ids[i]] = &Issue{ID: ids[i], BlockedBy: []string{ids[i+1]}}
	}
	idx[ids[n-1]] = &Issue{ID: ids[n-1], BlockedBy: nil}
	return idx, ids[0]
}

// fakeID produces a short but unique ID for test purposes.
func fakeID(i int) string {
	return "x-" + itoa(i)
}

// itoa is a simple int-to-string (avoid importing strconv for readability).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestFindCycle_Acyclic_ShallowChain verifies a simple 3-node linear chain
// has no cycle.
func TestFindCycle_Acyclic_ShallowChain(t *testing.T) {
	idx, start := buildLinearChain(3)
	if got := findCycle(idx, start); got != "" {
		t.Errorf("expected no cycle, got %q", got)
	}
}

// TestFindCycle_SelfCycle detects a node blocked by itself.
func TestFindCycle_SelfCycle(t *testing.T) {
	idx := map[string]*Issue{
		"a": {ID: "a", BlockedBy: []string{"a"}},
	}
	got := findCycle(idx, "a")
	if got == "" {
		t.Error("expected cycle for self-dependency, got empty string")
	}
	if !strings.Contains(got, "a") {
		t.Errorf("cycle path should contain 'a', got %q", got)
	}
}

// TestFindCycle_TwoNodeCycle detects a -> b -> a.
func TestFindCycle_TwoNodeCycle(t *testing.T) {
	idx := map[string]*Issue{
		"a": {ID: "a", BlockedBy: []string{"b"}},
		"b": {ID: "b", BlockedBy: []string{"a"}},
	}
	got := findCycle(idx, "a")
	if got == "" {
		t.Error("expected cycle a->b->a, got empty string")
	}
}

// TestFindCycle_ThreeNodeCycle detects a -> b -> c -> a (the original regression case).
func TestFindCycle_ThreeNodeCycle(t *testing.T) {
	idx := map[string]*Issue{
		"a": {ID: "a", BlockedBy: []string{"b"}},
		"b": {ID: "b", BlockedBy: []string{"c"}},
		"c": {ID: "c", BlockedBy: []string{"a"}},
	}
	got := findCycle(idx, "a")
	if got == "" {
		t.Error("expected cycle a->b->c->a, got empty string")
	}
}

// TestFindCycle_Acyclic_NoNode verifies that starting from an ID not in the
// index returns no cycle.
func TestFindCycle_Acyclic_NoNode(t *testing.T) {
	idx := map[string]*Issue{}
	if got := findCycle(idx, "missing"); got != "" {
		t.Errorf("expected no cycle for missing node, got %q", got)
	}
}

// TestFindCycle_Acyclic_EmptyGraph verifies an empty graph returns no cycle.
func TestFindCycle_Acyclic_EmptyGraph(t *testing.T) {
	idx := map[string]*Issue{
		"a": {ID: "a", BlockedBy: nil},
	}
	if got := findCycle(idx, "a"); got != "" {
		t.Errorf("expected no cycle for isolated node, got %q", got)
	}
}

// TestFindCycle_DeepLinearChain verifies that a very deep (100 000-node) linear
// chain — which would overflow the goroutine stack in the old recursive version —
// returns no cycle without crashing.
func TestFindCycle_DeepLinearChain(t *testing.T) {
	const n = 100_000
	idx, start := buildLinearChain(n)
	got := findCycle(idx, start)
	if got != "" {
		t.Errorf("expected no cycle in linear chain of %d, got %q", n, got)
	}
}

// TestFindCycle_DeepLinearChain_WithCycleAtEnd verifies that a deep chain with a
// cycle at the end is still detected.
func TestFindCycle_DeepLinearChain_WithCycleAtEnd(t *testing.T) {
	const n = 1_000
	idx, start := buildLinearChain(n)
	// Close the cycle: last node points back to first.
	lastID := fakeID(n - 1)
	firstID := fakeID(0)
	idx[lastID].BlockedBy = []string{firstID}
	got := findCycle(idx, start)
	if got == "" {
		t.Errorf("expected cycle in chain-with-loop of %d, got empty string", n)
	}
}
